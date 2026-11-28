package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// GET /v1/sub2api/account: the wallet balance, the subscription allowance and today's usage of the ACCOUNT behind
// an API key. These tests pin what it reads (the key's owner, never the key), how "today" is decided, that a block
// that could not be read is left out instead of reported as zero, and that nothing that identifies the owner or
// anyone else ever appears in the answer.

// ---------- fixtures ----------

// The site's current day is answered by the dashboard read once the day is an hour old and by the range read before
// that (see UsageService.GetUserDayUsageSummary), so the two fakes return the same account figures: what the endpoint
// answers must not depend on the time of day the test happens to run. Which read answered is recorded separately.
type keyAccountUsageRepo struct {
	service.UsageLogRepository
	dashboard      usagestats.UserDashboardStats
	dashboardErr   error
	dashboardUsers []int64
	aggregate      usagestats.UsageStats
	aggregateErr   error
	aggregateCalls []keyAccountAggregateCall
	readUsers      []int64         // the user of every usage read, whichever read it was
	readSaw        []time.Duration // time left on the context when each usage read started; -1: no deadline
	keyScoped      int             // reads of a single key's usage: the account endpoint must never make one
}

func (r *keyAccountUsageRepo) failReads(err error) { r.dashboardErr, r.aggregateErr = err, err }

func (r *keyAccountUsageRepo) noteRead(ctx context.Context, userID int64) {
	r.readUsers = append(r.readUsers, userID)
	if deadline, ok := ctx.Deadline(); ok {
		r.readSaw = append(r.readSaw, time.Until(deadline))
	} else {
		r.readSaw = append(r.readSaw, -1)
	}
}

type keyAccountAggregateCall struct {
	userID     int64
	start, end time.Time
}

func (r *keyAccountUsageRepo) GetUserDashboardStats(ctx context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	r.dashboardUsers = append(r.dashboardUsers, userID)
	r.noteRead(ctx, userID)
	if r.dashboardErr != nil {
		return nil, r.dashboardErr
	}
	out := r.dashboard
	return &out, nil
}

func (r *keyAccountUsageRepo) GetUserStatsAggregated(ctx context.Context, userID int64, start, end time.Time) (*usagestats.UsageStats, error) {
	r.aggregateCalls = append(r.aggregateCalls, keyAccountAggregateCall{userID: userID, start: start, end: end})
	r.noteRead(ctx, userID)
	if r.aggregateErr != nil {
		return nil, r.aggregateErr
	}
	out := r.aggregate
	return &out, nil
}

func (r *keyAccountUsageRepo) GetAPIKeyStatsAggregated(context.Context, int64, time.Time, time.Time) (*usagestats.UsageStats, error) {
	r.keyScoped++
	return &usagestats.UsageStats{TotalRequests: 7, TotalTokens: 150, TotalCost: 0.5, TotalActualCost: 0.25}, nil
}

type keyAccountUserRepo struct {
	service.UserRepository
	users map[int64]*service.User
	asked []int64
	saw   []time.Duration
	err   error
}

func (r *keyAccountUserRepo) GetByID(ctx context.Context, id int64) (*service.User, error) {
	r.asked = append(r.asked, id)
	if deadline, ok := ctx.Deadline(); ok {
		r.saw = append(r.saw, time.Until(deadline))
	} else {
		r.saw = append(r.saw, -1)
	}
	if r.err != nil {
		return nil, r.err
	}
	user, ok := r.users[id]
	if !ok {
		return nil, service.ErrUserNotFound
	}
	clone := *user
	return &clone, nil
}

type keyAccountSubRepo struct {
	service.UserSubscriptionRepository
	subs  []service.UserSubscription
	err   error
	asked []int64
}

func (r *keyAccountSubRepo) ListActiveByUserID(_ context.Context, userID int64) ([]service.UserSubscription, error) {
	r.asked = append(r.asked, userID)
	if r.err != nil {
		return nil, r.err
	}
	return append([]service.UserSubscription(nil), r.subs...), nil
}

type keyAccountFixture struct {
	*keyInfoFixture
	users *keyAccountUserRepo
	subs  *keyAccountSubRepo
	usage *keyAccountUsageRepo
}

// The key (id 100) belongs to user 7. Two other users exist so that reading the wrong one is visible: user 100 (the
// key's id used as a user id) and user 8 (somebody else). The auth snapshot carries a stale balance on purpose.
func newKeyAccountFixture(t *testing.T) *keyAccountFixture {
	t.Helper()
	f := newKeyInfoFixture(t)
	totp := "owner-totp-secret-sentinel"
	users := &keyAccountUserRepo{users: map[int64]*service.User{
		7: {
			ID: 7, Email: "owner-email-sentinel@example.test", Username: "owner-username-sentinel", Notes: "owner-admin-note-sentinel",
			PasswordHash: "owner-password-hash-sentinel", TotpSecretEncrypted: &totp, AvatarURL: "https://avatar.example.test/owner-avatar-sentinel",
			Role: service.RoleUser, Status: service.StatusActive, Balance: 1234.56789012, FrozenBalance: 2.5, TotalRecharged: 98765.4321,
			APIKeys: []service.APIKey{{ID: 555, Name: "other-key-name-sentinel", Key: "other-key-plaintext-sentinel"}},
		},
		100: {ID: 100, Balance: 1.25, Status: service.StatusActive},
		8:   {ID: 8, Balance: 8.5, Status: service.StatusActive},
	}}
	subs := &keyAccountSubRepo{}
	usage := &keyAccountUsageRepo{
		dashboard: usagestats.UserDashboardStats{
			TodayRequests: 321, TodayInputTokens: 1000, TodayOutputTokens: 200, TodayCacheCreationTokens: 30, TodayCacheReadTokens: 4000,
			TodayTokens: 5230, TodayCost: 1.75, TodayActualCost: 0.875,
			// What the wide dashboard struct carries and the account endpoint must not copy.
			TotalAPIKeys: 31415926, TotalRequests: 918273645, TotalActualCost: 55555.5, Rpm: 27182818, Tpm: 16180339,
			ByPlatform: []usagestats.PlatformDashboardStats{{Platform: "platform-breakdown-sentinel", TotalRequests: 1}},
		},
		aggregate: usagestats.UsageStats{
			TotalRequests: 321, TotalInputTokens: 1000, TotalOutputTokens: 200, TotalCacheCreationTokens: 30, TotalCacheReadTokens: 4000,
			TotalTokens: 5230, TotalCost: 1.75, TotalActualCost: 0.875, TotalAccountCost: ptrFloat(777.7),
		},
	}
	f.handler.apiKeyService = service.NewAPIKeyService(&keyInfoAPIKeyRepo{}, users, f.groups, subs, &keyInfoRateRepo{}, nil, f.handler.cfg)
	f.handler.usageService = service.NewUsageService(usage, nil, nil, nil)
	f.apiKey.User = &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 5, Email: "snapshot-email-sentinel@example.test", Username: "snapshot-username-sentinel"}
	return &keyAccountFixture{keyInfoFixture: f, users: users, subs: subs, usage: usage}
}

func (f *keyAccountFixture) account(t *testing.T, query string) (int, map[string]any, string) {
	t.Helper()
	return f.call(t, "/v1/sub2api/account"+query, f.handler.KeyAccount)
}

func keyAccountKeys(t *testing.T, v any) []string {
	t.Helper()
	m := keyInfoObj(t, v)
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// keyAccountOtherZone picks a time zone whose midnight is not the site's, so "today" there is a different day.
func keyAccountOtherZone(t *testing.T) string {
	t.Helper()
	site := timezone.StartOfDay(timezone.Now())
	for _, name := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago", "America/Los_Angeles", "Asia/Kolkata"} {
		if !timezone.StartOfDayInUserLocation(timezone.NowInUserLocation(name), name).Equal(site) {
			return name
		}
	}
	t.Skip("no candidate zone has a different midnight than the site's")
	return ""
}

// ---------- what it reports ----------

func TestKeyAccountReportsBalanceSubscriptionsAndTodayUsage(t *testing.T) {
	f := newKeyAccountFixture(t)
	now := time.Now()
	daily, weekly := 10.0, 20.0
	f.subs.subs = []service.UserSubscription{{
		ID: 1, UserID: 7, GroupID: 5, Status: service.SubscriptionStatusActive, StartsAt: now.Add(-30 * 24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour),
		DailyUsageUSD: 3, WeeklyUsageUSD: 18, DailyWindowStart: timePtr(timezone.StartOfDay(now)), WeeklyWindowStart: timePtr(now.Add(-2 * 24 * time.Hour)),
		Group: &service.Group{ID: 5, Name: "Claude Pro", DailyLimitUSD: &daily, WeeklyLimitUSD: &weekly},
	}}

	code, body, raw := f.account(t, "")

	require.Equal(t, http.StatusOK, code, raw)
	require.Equal(t, "sub2api.account", body["object"])
	require.EqualValues(t, 1, body["schema_version"])
	require.NotEmpty(t, body["timezone"])
	require.NotEmpty(t, body["observed_at"])
	require.NotContains(t, body, "partial")

	balance := keyInfoObj(t, body["balance"])
	require.EqualValues(t, 1234.56789012, balance["wallet"], "the owner's wallet, with the full precision of the user row")
	require.EqualValues(t, 2.5, balance["frozen"])
	require.Equal(t, "USD", balance["unit"])

	subs := keyInfoList(t, body["subscriptions"])
	require.Len(t, subs, 1)
	sub := keyInfoObj(t, subs[0])
	require.EqualValues(t, 5, sub["group_id"])
	require.Equal(t, "Claude Pro", sub["group_name"])
	require.NotEmpty(t, sub["expires_at"])
	require.EqualValues(t, 2, sub["remaining"], "the tightest limit: weekly 20 - 18, not daily 10 - 3")
	require.Equal(t, false, sub["unlimited"])

	today := keyInfoObj(t, body["today"])
	require.Equal(t, "today", today["label"])
	require.Equal(t, today["start"], today["end"], "one day")
	require.EqualValues(t, 321, today["requests"])
	require.EqualValues(t, 1000, today["input_tokens"])
	require.EqualValues(t, 200, today["output_tokens"])
	require.EqualValues(t, 4000, today["cache_read_tokens"])
	require.EqualValues(t, 30, today["cache_write_tokens"])
	require.EqualValues(t, 5230, today["total_tokens"])
	require.EqualValues(t, 1.75, today["cost"], "list price")
	require.EqualValues(t, 0.875, today["actual_cost"], "what was charged")
}

func TestKeyAccountWithoutSubscriptionsSaysSoWithAnEmptyList(t *testing.T) {
	f := newKeyAccountFixture(t)
	_, body, raw := f.account(t, "")
	require.Contains(t, body, "subscriptions", "an empty list is the answer 'no active subscription', not a missing block")
	require.Empty(t, keyInfoList(t, body["subscriptions"]))
	require.Contains(t, raw, `"subscriptions":[]`)
}

// The usage is the account's, all the owner's keys together, and the account is the key's OWNER: never the key's own
// numbers, never the user whose id happens to equal the key's id.
func TestKeyAccountReadsTheOwnerNotTheKey(t *testing.T) {
	f := newKeyAccountFixture(t)

	_, body, _ := f.account(t, "")

	require.Equal(t, []int64{7}, f.users.asked, "the wallet is read for the key's owner")
	require.Equal(t, []int64{7}, f.subs.asked)
	require.Equal(t, []int64{7}, f.usage.readUsers, "the account's usage is read once, for the owner")
	require.Zero(t, f.usage.keyScoped, "one key's usage is never read")
	require.EqualValues(t, 321, keyInfoObj(t, body["today"])["requests"], "the whole account, not the 7 requests of this key")
	require.EqualValues(t, 1234.56789012, keyInfoObj(t, body["balance"])["wallet"], "not user 100's 1.25 and not user 8's 8.5")
}

func TestKeyAccountReadsTheWalletFreshAndNeverFromTheAuthSnapshot(t *testing.T) {
	f := newKeyAccountFixture(t)
	require.EqualValues(t, 5, f.apiKey.User.Balance, "fixture: the snapshot is stale")

	_, body, _ := f.account(t, "")

	require.EqualValues(t, 1234.56789012, keyInfoObj(t, body["balance"])["wallet"])
}

func TestKeyAccountReadsAreBoundedByABudget(t *testing.T) {
	f := newKeyAccountFixture(t)
	f.account(t, "")
	require.Len(t, f.users.saw, 1)
	require.Len(t, f.usage.readSaw, 1)
	for name, left := range map[string]time.Duration{"wallet": f.users.saw[0], "usage": f.usage.readSaw[0]} {
		require.Greater(t, left, time.Duration(0), "%s read has no deadline", name)
		require.LessOrEqual(t, left, 5*time.Second, "%s read runs on a bounded budget", name)
	}
}

// ---------- when "today" starts ----------

func TestKeyAccountTodayFollowsTheTimezoneParameter(t *testing.T) {
	site := timezone.Location().String()
	other := keyAccountOtherZone(t)

	t.Run("no parameter, the site's own zone and an unusable value all mean the site's day", func(t *testing.T) {
		for name, query := range map[string]string{
			"none":      "",
			"site zone": "?timezone=" + site,
			"unknown":   "?timezone=Not/AZone",
			"too long":  "?timezone=" + strings.Repeat("x", 200),
			"blank":     "?timezone=%20%20",
		} {
			f := newKeyAccountFixture(t)
			before := timezone.Today()
			_, body, raw := f.account(t, query)
			after := timezone.Today()
			require.Equal(t, site, body["timezone"], name)
			require.EqualValues(t, 321, keyInfoObj(t, body["today"])["requests"], "%s: %s", name, raw)
			// One usage read answered it: the dashboard's cached read, or (in the first hour of the site's day) a range
			// read of exactly the site's whole day. Which one depends on the time of day; the day does not.
			require.Equal(t, 1, len(f.usage.dashboardUsers)+len(f.usage.aggregateCalls), name)
			for _, call := range f.usage.aggregateCalls {
				require.True(t, call.start.Equal(before) || call.start.Equal(after), "%s: the site's day starts at its own midnight: %s", name, call.start)
				require.True(t, call.end.Equal(call.start.AddDate(0, 0, 1)), name)
			}
		}
	})

	t.Run("a zone with another midnight is a different day, read as that day's range", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		before := timezone.StartOfDayInUserLocation(timezone.NowInUserLocation(other), other)

		_, body, raw := f.account(t, "?timezone="+other)

		after := timezone.StartOfDayInUserLocation(timezone.NowInUserLocation(other), other)
		require.Equal(t, other, body["timezone"])
		today := keyInfoObj(t, body["today"])
		require.EqualValues(t, 321, today["requests"], raw)
		require.EqualValues(t, 5230, today["total_tokens"])
		require.EqualValues(t, 0.875, today["actual_cost"])
		require.Empty(t, f.usage.dashboardUsers, "the dashboard only knows the site's day")
		require.Len(t, f.usage.aggregateCalls, 1)
		call := f.usage.aggregateCalls[0]
		require.EqualValues(t, 7, call.userID)
		require.True(t, call.start.Equal(before) || call.start.Equal(after), "the day starts at that zone's midnight: %s", call.start)
		require.True(t, call.end.Equal(call.start.AddDate(0, 0, 1)), "and lasts one day")
		loc, err := time.LoadLocation(other)
		require.NoError(t, err)
		require.Equal(t, call.start.In(loc).Format("2006-01-02"), today["start"])
		require.Equal(t, today["start"], today["end"])
	})
}

// Both endpoints that take a timezone resolve it through one helper: same zone, same fallback, same day label.
func TestKeyAccountAndKeyUsageResolveTheTimezoneTheSameWay(t *testing.T) {
	other := keyAccountOtherZone(t)
	for name, query := range map[string]string{
		"none":     "",
		"valid":    "?timezone=Asia/Shanghai",
		"other":    "?timezone=" + other,
		"unknown":  "?timezone=Not/AZone",
		"too long": "?timezone=" + strings.Repeat("x", 65),
		"padded":   "?timezone=%20UTC%20",
	} {
		f := newKeyAccountFixture(t)
		_, account, _ := f.account(t, query)
		_, usage, _ := f.call(t, "/v1/sub2api/usage"+query, f.handler.KeyUsage)

		require.Equal(t, usage["timezone"], account["timezone"], name)
		accountToday, usageToday := keyInfoObj(t, account["today"]), keyInfoObj(t, usage["today"])
		require.Equal(t, usageToday["start"], accountToday["start"], name)
		require.Equal(t, usageToday["end"], accountToday["end"], name)
		require.Equal(t, usageToday["label"], accountToday["label"], name)
	}
}

// ---------- a block that cannot be read is left out ----------

func TestKeyAccountLeavesOutWhatItCouldNotReadInsteadOfReportingZero(t *testing.T) {
	busy := errors.New("usage statistics are busy")

	t.Run("today", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		f.usage.failReads(busy)
		code, body, raw := f.account(t, "")
		require.Equal(t, http.StatusOK, code, "a slow aggregate does not fail the endpoint")
		require.Equal(t, []any{"today"}, body["partial"])
		require.NotContains(t, body, "today")
		require.NotContains(t, raw, `"today":`)
		require.EqualValues(t, 1234.56789012, keyInfoObj(t, body["balance"])["wallet"], "what could be read is still there")
	})
	t.Run("today in another zone", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		f.usage.aggregateErr = context.DeadlineExceeded
		code, body, _ := f.account(t, "?timezone="+keyAccountOtherZone(t))
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"today"}, body["partial"])
		require.NotContains(t, body, "today")
	})
	t.Run("balance", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		f.users.err = busy
		code, body, raw := f.account(t, "")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"balance"}, body["partial"])
		require.NotContains(t, body, "balance")
		require.NotContains(t, raw, `"wallet":`, "an unreadable wallet is not a wallet of zero")
		require.EqualValues(t, 321, keyInfoObj(t, body["today"])["requests"])
	})
	t.Run("an owner that cannot be found is not a wallet of zero either", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		delete(f.users.users, 7)
		_, body, _ := f.account(t, "")
		require.Equal(t, []any{"balance"}, body["partial"])
		require.NotContains(t, body, "balance")
	})
	t.Run("subscriptions", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		f.subs.err = busy
		code, body, raw := f.account(t, "")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"subscriptions"}, body["partial"])
		require.NotContains(t, body, "subscriptions")
		require.NotContains(t, raw, `"subscriptions":`, "an unreadable list is not an empty list")
	})
	t.Run("everything", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		f.users.err, f.subs.err = busy, busy
		f.usage.failReads(busy)
		code, body, _ := f.account(t, "")
		require.Equal(t, http.StatusOK, code)
		require.ElementsMatch(t, []any{"balance", "subscriptions", "today"}, body["partial"])
		require.Equal(t, []string{"object", "observed_at", "partial", "schema_version", "timezone"}, keyAccountKeys(t, body))
	})
}

// ---------- subscriptions ----------

func TestKeyAccountSubscriptionRemainingIsTheTightestLimitAndForgetsRolledOverWindows(t *testing.T) {
	now := time.Now()
	midnight := timezone.StartOfDay(now)
	limit := func(v float64) *float64 { return &v }
	sub := func(group *service.Group, mutate func(*service.UserSubscription)) service.UserSubscription {
		s := service.UserSubscription{
			ID: 1, UserID: 7, GroupID: group.ID, Status: service.SubscriptionStatusActive,
			StartsAt: now.Add(-30 * 24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), Group: group,
		}
		if mutate != nil {
			mutate(&s)
		}
		return s
	}
	cases := []struct {
		name      string
		sub       service.UserSubscription
		remaining *float64 // nil: no cap
	}{
		{"one daily limit, part used",
			sub(&service.Group{ID: 1, Name: "Daily", DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
				s.DailyUsageUSD, s.DailyWindowStart = 3, &midnight
			}), limit(7)},
		{"the tightest of three limits",
			sub(&service.Group{ID: 2, Name: "Three", DailyLimitUSD: limit(10), WeeklyLimitUSD: limit(20), MonthlyLimitUSD: limit(100)}, func(s *service.UserSubscription) {
				s.DailyUsageUSD, s.WeeklyUsageUSD, s.MonthlyUsageUSD = 3, 18, 1
				s.DailyWindowStart, s.WeeklyWindowStart, s.MonthlyWindowStart = &midnight, timePtr(now.Add(-2*24*time.Hour)), timePtr(now.Add(-5*24*time.Hour))
			}), limit(2)},
		{"used up, still inside its window",
			sub(&service.Group{ID: 3, Name: "Used up", DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
				s.DailyUsageUSD, s.DailyWindowStart = 10, &midnight
			}), limit(0)},
		{"yesterday's daily usage is not today's: the window rolled over and no request has reset it yet",
			sub(&service.Group{ID: 4, Name: "Rolled daily", DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
				s.DailyUsageUSD, s.DailyWindowStart = 10, timePtr(midnight.AddDate(0, 0, -1))
			}), limit(10)},
		{"anchored 24h mode stays in window 1 before 24h elapses",
			sub(&service.Group{ID: 41, Name: "Anchored active", SubscriptionType: service.SubscriptionTypeSubscription, DailyResetMode: service.DailyResetModeAnchored24h, DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
				start := now.Add(-14 * time.Hour)
				s.StartsAt, s.ExpiresAt = start, start.Add(48*time.Hour)
				s.DailyUsageUSD, s.DailyWindowStart = 10, &start
			}), limit(0)},
		{"anchored 24h mode rolls over once 24h elapses before expiry",
			sub(&service.Group{ID: 42, Name: "Anchored rolled", SubscriptionType: service.SubscriptionTypeSubscription, DailyResetMode: service.DailyResetModeAnchored24h, DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
				start := now.Add(-25 * time.Hour)
				s.StartsAt, s.ExpiresAt = start, start.Add(48*time.Hour)
				s.DailyUsageUSD, s.DailyWindowStart = 10, &start
			}), limit(10)},
		{"a weekly window that rolled over",
			sub(&service.Group{ID: 5, Name: "Rolled weekly", WeeklyLimitUSD: limit(20)}, func(s *service.UserSubscription) {
				s.WeeklyUsageUSD, s.WeeklyWindowStart = 20, timePtr(now.Add(-8*24*time.Hour))
			}), limit(20)},
		{"a monthly window that rolled over",
			sub(&service.Group{ID: 6, Name: "Rolled monthly", MonthlyLimitUSD: limit(100)}, func(s *service.UserSubscription) {
				s.MonthlyUsageUSD, s.MonthlyWindowStart = 100, timePtr(now.Add(-31*24*time.Hour))
			}), limit(100)},
		{"a plan without any limit", sub(&service.Group{ID: 7, Name: "Unlimited"}, nil), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newKeyAccountFixture(t)
			f.subs.subs = []service.UserSubscription{tc.sub}
			_, body, raw := f.account(t, "")
			items := keyInfoList(t, body["subscriptions"])
			require.Len(t, items, 1, raw)
			item := keyInfoObj(t, items[0])
			require.Equal(t, tc.sub.Group.Name, item["group_name"])
			if tc.remaining == nil {
				require.Equal(t, true, item["unlimited"])
				require.NotContains(t, item, "remaining", "a plan without a limit has nothing 'left'")
				return
			}
			require.Equal(t, false, item["unlimited"])
			require.EqualValues(t, *tc.remaining, item["remaining"], raw)
		})
	}

	t.Run("only active subscriptions with a group are listed, and the stored data is not touched", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		rolled := sub(&service.Group{ID: 8, Name: "Rolled", DailyLimitUSD: limit(10)}, func(s *service.UserSubscription) {
			s.DailyUsageUSD, s.DailyWindowStart = 10, timePtr(midnight.AddDate(0, 0, -1))
		})
		expired := sub(&service.Group{ID: 9, Name: "Expired"}, func(s *service.UserSubscription) { s.ExpiresAt = now.Add(-time.Hour) })
		suspended := sub(&service.Group{ID: 10, Name: "Suspended"}, func(s *service.UserSubscription) { s.Status = service.SubscriptionStatusSuspended })
		groupless := sub(&service.Group{ID: 11, Name: "Gone"}, func(s *service.UserSubscription) { s.Group = nil })
		f.subs.subs = []service.UserSubscription{rolled, expired, suspended, groupless}

		_, body, _ := f.account(t, "")

		items := keyInfoList(t, body["subscriptions"])
		require.Len(t, items, 1)
		require.Equal(t, "Rolled", keyInfoObj(t, items[0])["group_name"])
		require.EqualValues(t, 10, f.subs.subs[0].DailyUsageUSD, "the rollover is applied to a copy, never to what the repository returned")
	})
}

// ---------- what must never be in the answer ----------

// The answer is pinned field by field: an addition has to be a deliberate edit of this list, and nothing about who
// the owner is, about their other keys, about the key itself or about the platform's internals can slip in.
func TestKeyAccountAnswersWithExactlyTheDocumentedFields(t *testing.T) {
	f := newKeyAccountFixture(t)
	now := time.Now()
	daily := 10.0
	secret := "group-provider-credential-sentinel"
	f.subs.subs = []service.UserSubscription{{
		ID: 1, UserID: 7, GroupID: 5, Status: service.SubscriptionStatusActive, StartsAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		Notes: "subscription-admin-note-sentinel", DailyUsageUSD: 1, DailyWindowStart: timePtr(timezone.StartOfDay(now)),
		Group: &service.Group{ID: 5, Name: "Claude Pro", DailyLimitUSD: &daily, SearchProviderAPIKey: &secret, SearchProviderBaseURL: strPtr("https://search.internal.example")},
	}}

	_, body, raw := f.account(t, "")

	require.Equal(t, []string{"balance", "object", "observed_at", "schema_version", "subscriptions", "timezone", "today"}, keyAccountKeys(t, body))
	require.Equal(t, []string{"frozen", "unit", "wallet"}, keyAccountKeys(t, body["balance"]))
	require.Equal(t, []string{"actual_cost", "cache_read_tokens", "cache_write_tokens", "cost", "end", "input_tokens", "label", "output_tokens", "requests", "start", "total_tokens"},
		keyAccountKeys(t, body["today"]))
	require.Equal(t, []string{"expires_at", "group_id", "group_name", "remaining", "unlimited"}, keyAccountKeys(t, keyInfoList(t, body["subscriptions"])[0]))

	for _, forbidden := range []string{
		// who the owner is, and what else the owner has
		"owner-email-sentinel", "owner-username-sentinel", "snapshot-email-sentinel", "snapshot-username-sentinel", "owner-admin-note-sentinel", "owner-password-hash-sentinel",
		"owner-totp-secret-sentinel", "owner-avatar-sentinel", "98765.4321", "other-key-name-sentinel", "other-key-plaintext-sentinel",
		// the key itself and its network rules
		"THE-FULL-PLAINTEXT-SECRET", "hash-should-not-leak", "203.0.113.9", "198.51.100.7", "192.0.2.55", "ci-key", "fixture1",
		// the platform's internals: the wide dashboard struct, group credentials, upstream cost, subscription notes
		"platform-breakdown-sentinel", "918273645", "55555.5", "31415926", "27182818", "16180339", "total_api_keys", "by_platform", "rpm", "tpm",
		"group-provider-credential-sentinel", "search.internal.example", "subscription-admin-note-sentinel", "777.7", "account_cost",
		"upstream", "channel", "user_id", "\"id\"", "email",
	} {
		require.NotContains(t, raw, forbidden)
	}
}

func TestKeyAccountResponseIsNeverCached(t *testing.T) {
	f := newKeyAccountFixture(t)
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/sub2api/account"+query, nil)
		key := *f.apiKey
		c.Set(string(middleware2.ContextKeyAPIKey), &key)
		f.handler.KeyAccount(c)
		return w
	}
	for name, query := range map[string]string{"site day": "", "another zone": "?timezone=" + keyAccountOtherZone(t)} {
		w := get(query)
		require.Equal(t, http.StatusOK, w.Code, name)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"), "%s: a balance must not sit in a shared cache", name)
	}
	f.usage.failReads(errors.New("busy"))
	w := get("")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"), "a partial answer is not cacheable either")
}

func TestKeyAccountRefusesAnonymousSimpleModeAndMissingServices(t *testing.T) {
	f := newKeyAccountFixture(t)

	t.Run("anonymous", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/sub2api/account", nil)
		f.handler.KeyAccount(c)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		require.NotContains(t, w.Body.String(), "1234", "an error answer carries no figures")
		require.Empty(t, f.users.asked, "nothing is read for a caller that is not authenticated")
	})
	t.Run("simple mode has no accounts to speak of", func(t *testing.T) {
		g := newKeyAccountFixture(t)
		g.handler.cfg = &config.Config{RunMode: config.RunModeSimple}
		code, _, raw := g.account(t, "")
		require.Equal(t, http.StatusNotFound, code)
		require.NotContains(t, raw, "1234")
		require.Empty(t, g.users.asked)
	})
	t.Run("a handler without its services answers 500 and not with a panic", func(t *testing.T) {
		g := newKeyAccountFixture(t)
		g.handler.usageService = nil
		code, _, raw := g.account(t, "")
		require.Equal(t, http.StatusInternalServerError, code)
		require.NotContains(t, raw, "1234")
		g = newKeyAccountFixture(t)
		g.handler.apiKeyService = nil
		code, _, _ = g.account(t, "")
		require.Equal(t, http.StatusInternalServerError, code)
	})
}

func TestKeyAccountRefusesExpiredAndQuotaExhaustedKeys(t *testing.T) {
	t.Run("expired status rejected", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		code, _, raw := f.call(t, "/v1/sub2api/account", f.handler.KeyAccount, func(k *service.APIKey) {
			k.Status = service.StatusAPIKeyExpired
		})
		require.Equal(t, http.StatusForbidden, code)
		require.NotContains(t, raw, "1234")
		require.Empty(t, f.users.asked)
	})
	t.Run("runtime expiry rejected", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		code, _, raw := f.call(t, "/v1/sub2api/account", f.handler.KeyAccount, func(k *service.APIKey) {
			past := time.Now().Add(-time.Minute)
			k.ExpiresAt = &past
		})
		require.Equal(t, http.StatusForbidden, code)
		require.NotContains(t, raw, "1234")
		require.Empty(t, f.users.asked)
	})
	t.Run("quota exhausted status rejected", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		code, _, raw := f.call(t, "/v1/sub2api/account", f.handler.KeyAccount, func(k *service.APIKey) {
			k.Status = service.StatusAPIKeyQuotaExhausted
		})
		require.Equal(t, http.StatusTooManyRequests, code)
		require.NotContains(t, raw, "1234")
		require.Empty(t, f.users.asked)
	})
	t.Run("runtime quota exhausted rejected", func(t *testing.T) {
		f := newKeyAccountFixture(t)
		code, _, raw := f.call(t, "/v1/sub2api/account", f.handler.KeyAccount, func(k *service.APIKey) {
			k.Quota = 5
			k.QuotaUsed = 5
		})
		require.Equal(t, http.StatusTooManyRequests, code)
		require.NotContains(t, raw, "1234")
		require.Empty(t, f.users.asked)
	})
}
