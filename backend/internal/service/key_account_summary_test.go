package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// The reads behind GET /v1/sub2api/account. The endpoint itself is tested in the handler and routes packages; what
// is pinned here is the part only the service decides: which of the two existing usage reads answers a day, and that
// nothing is ever turned into zero on the way.

type acctSummaryUsageRepo struct {
	UsageLogRepository
	dashboard      *usagestats.UserDashboardStats
	dashboardErr   error
	dashboardCalls []int64
	aggregate      *usagestats.UsageStats
	aggregateErr   error
	aggregateCalls []acctSummaryRange
}

type acctSummaryRange struct {
	userID     int64
	start, end time.Time
}

func (r *acctSummaryUsageRepo) GetUserDashboardStats(_ context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	r.dashboardCalls = append(r.dashboardCalls, userID)
	return r.dashboard, r.dashboardErr
}

func (r *acctSummaryUsageRepo) GetUserStatsAggregated(_ context.Context, userID int64, start, end time.Time) (*usagestats.UsageStats, error) {
	r.aggregateCalls = append(r.aggregateCalls, acctSummaryRange{userID: userID, start: start, end: end})
	return r.aggregate, r.aggregateErr
}

// Every figure has its own value, so two fields swapped in a mapping cannot cancel out.
func acctSummaryRepo() *acctSummaryUsageRepo {
	return &acctSummaryUsageRepo{
		dashboard: &usagestats.UserDashboardStats{
			TodayRequests: 11, TodayInputTokens: 12, TodayOutputTokens: 13, TodayCacheCreationTokens: 14, TodayCacheReadTokens: 15,
			TodayTokens: 16, TodayCost: 1.7, TodayActualCost: 1.8,
			TotalRequests: 910, TotalActualCost: 920.5, // lifetime figures: never the day's
		},
		aggregate: &usagestats.UsageStats{
			TotalRequests: 21, TotalInputTokens: 22, TotalOutputTokens: 23, TotalCacheCreationTokens: 24, TotalCacheReadTokens: 25,
			TotalTokens: 26, TotalCost: 2.7, TotalActualCost: 2.8,
		},
	}
}

func TestUserDayUsageSummaryAsksTheDashboardOnlyForTheSitesCurrentDay(t *testing.T) {
	today := timezone.Today()
	day := 24 * time.Hour
	cases := []struct {
		name       string
		start, end time.Time
		dashboard  bool
	}{
		{"the site's current day", today, today.AddDate(0, 0, 1), true},
		// The dashboard only knows today: any other day, even one that starts at a site midnight, is a range read.
		{"yesterday", today.AddDate(0, 0, -1), today, false},
		{"tomorrow", today.AddDate(0, 0, 1), today.AddDate(0, 0, 2), false},
		{"a zone with another midnight", today.Add(3 * time.Hour), today.Add(27 * time.Hour), false},
		{"a window that is not one day", today, today.AddDate(0, 0, 2), false},
		{"a window that stops early", today, today.Add(day - time.Second), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := acctSummaryRepo()
			svc := NewUsageService(repo, nil, nil, nil)

			got, err := svc.GetUserDayUsageSummary(context.Background(), 7, tc.start, tc.end)

			require.NoError(t, err)
			if tc.dashboard {
				require.Equal(t, []int64{7}, repo.dashboardCalls)
				require.Empty(t, repo.aggregateCalls, "the day the dashboard counts is answered by the dashboard's cached read")
				require.Equal(t, &usagestats.APIKeyUsageSummary{
					Requests: 11, InputTokens: 12, OutputTokens: 13, CacheWriteTokens: 14, CacheReadTokens: 15, TotalTokens: 16, Cost: 1.7, ActualCost: 1.8,
				}, got, "today's columns, not the lifetime totals")
				return
			}
			require.Empty(t, repo.dashboardCalls, "the dashboard's today must never stand in for another day")
			require.Len(t, repo.aggregateCalls, 1)
			require.EqualValues(t, 7, repo.aggregateCalls[0].userID)
			require.True(t, repo.aggregateCalls[0].start.Equal(tc.start) && repo.aggregateCalls[0].end.Equal(tc.end), "the exact range is asked")
			require.Equal(t, &usagestats.APIKeyUsageSummary{
				Requests: 21, InputTokens: 22, OutputTokens: 23, CacheWriteTokens: 24, CacheReadTokens: 25, TotalTokens: 26, Cost: 2.7, ActualCost: 2.8,
			}, got)
		})
	}
}

func TestUserDayUsageSummaryNeverTurnsAFailureIntoZero(t *testing.T) {
	boom := errors.New("database busy")
	today := timezone.Today()

	t.Run("dashboard error", func(t *testing.T) {
		repo := acctSummaryRepo()
		repo.dashboardErr = boom
		got, err := NewUsageService(repo, nil, nil, nil).GetUserDayUsageSummary(context.Background(), 7, today, today.AddDate(0, 0, 1))
		require.ErrorIs(t, err, boom)
		require.Nil(t, got)
	})
	t.Run("range error", func(t *testing.T) {
		repo := acctSummaryRepo()
		repo.aggregateErr = boom
		got, err := NewUsageService(repo, nil, nil, nil).GetUserDayUsageSummary(context.Background(), 7, today.AddDate(0, 0, -1), today)
		require.ErrorIs(t, err, boom)
		require.Nil(t, got)
	})
	t.Run("an empty answer is an error, not a day without usage", func(t *testing.T) {
		repo := acctSummaryRepo()
		repo.dashboard, repo.aggregate = nil, nil
		svc := NewUsageService(repo, nil, nil, nil)
		got, err := svc.GetUserDayUsageSummary(context.Background(), 7, today, today.AddDate(0, 0, 1))
		require.Error(t, err)
		require.Nil(t, got)
		got, err = svc.GetUserDayUsageSummary(context.Background(), 7, today.AddDate(0, 0, -1), today)
		require.Error(t, err)
		require.Nil(t, got)
	})
	t.Run("no user, nothing is read", func(t *testing.T) {
		repo := acctSummaryRepo()
		svc := NewUsageService(repo, nil, nil, nil)
		for _, id := range []int64{0, -1} {
			_, err := svc.GetUserDayUsageSummary(context.Background(), id, today, today.AddDate(0, 0, 1))
			require.Error(t, err)
		}
		require.Empty(t, repo.dashboardCalls)
		require.Empty(t, repo.aggregateCalls)
	})
}

type acctSummaryUserRepo struct {
	UserRepository
	user  *User
	err   error
	asked []int64
}

func (r *acctSummaryUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	r.asked = append(r.asked, id)
	return r.user, r.err
}

type acctSummarySubRepo struct {
	UserSubscriptionRepository
	subs  []UserSubscription
	err   error
	asked []int64
}

func (r *acctSummarySubRepo) ListActiveByUserID(_ context.Context, userID int64) ([]UserSubscription, error) {
	r.asked = append(r.asked, userID)
	return r.subs, r.err
}

func TestAPIKeyServiceAccountReads(t *testing.T) {
	boom := errors.New("database busy")
	newService := func(users *acctSummaryUserRepo, subs *acctSummarySubRepo) *APIKeyService {
		return NewAPIKeyService(nil, users, nil, subs, nil, nil, &config.Config{})
	}

	t.Run("the wallet is the user row's balance and frozen amount, at full precision", func(t *testing.T) {
		users := &acctSummaryUserRepo{user: &User{ID: 7, Balance: 1234.56789012, FrozenBalance: 2.5, Email: "someone@example.test"}}
		wallet, err := newService(users, &acctSummarySubRepo{}).GetAccountWallet(context.Background(), 7)
		require.NoError(t, err)
		require.Equal(t, &AccountWallet{Balance: 1234.56789012, Frozen: 2.5}, wallet)
		require.Equal(t, []int64{7}, users.asked)
	})
	t.Run("a failed read is an error, never a wallet of zero", func(t *testing.T) {
		wallet, err := newService(&acctSummaryUserRepo{err: boom}, &acctSummarySubRepo{}).GetAccountWallet(context.Background(), 7)
		require.ErrorIs(t, err, boom)
		require.Nil(t, wallet)
		wallet, err = newService(&acctSummaryUserRepo{}, &acctSummarySubRepo{}).GetAccountWallet(context.Background(), 7)
		require.ErrorIs(t, err, ErrUserNotFound, "no user row at all")
		require.Nil(t, wallet)
	})
	t.Run("no user, nothing is read", func(t *testing.T) {
		users := &acctSummaryUserRepo{user: &User{ID: 7, Balance: 1}}
		subs := &acctSummarySubRepo{}
		svc := newService(users, subs)
		for _, id := range []int64{0, -5} {
			_, err := svc.GetAccountWallet(context.Background(), id)
			require.Error(t, err)
			_, err = svc.ListActiveSubscriptions(context.Background(), id)
			require.Error(t, err)
		}
		require.Empty(t, users.asked)
		require.Empty(t, subs.asked)
	})
	t.Run("subscriptions come from the repository's active list for that user", func(t *testing.T) {
		want := []UserSubscription{{ID: 1, UserID: 7, GroupID: 5}}
		subs := &acctSummarySubRepo{subs: want}
		got, err := newService(&acctSummaryUserRepo{}, subs).ListActiveSubscriptions(context.Background(), 7)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Equal(t, []int64{7}, subs.asked)
		_, err = newService(&acctSummaryUserRepo{}, &acctSummarySubRepo{err: boom}).ListActiveSubscriptions(context.Background(), 7)
		require.ErrorIs(t, err, boom)
	})
}
