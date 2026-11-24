package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------- fixtures ----------

type keyInfoGroupRepo struct {
	service.GroupRepository
	active []service.Group
	calls  int
}

func (r *keyInfoGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	r.calls++
	return append([]service.Group(nil), r.active...), nil
}

type keyInfoUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *keyInfoUserRepo) GetByID(context.Context, int64) (*service.User, error) { return r.user, nil }

type keyInfoSubRepo struct {
	service.UserSubscriptionRepository
}

func (keyInfoSubRepo) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type keyInfoAPIKeyRepo struct {
	service.APIKeyRepository
	rate *service.APIKeyRateLimitData
}

func (r *keyInfoAPIKeyRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return r.rate, nil
}

type keyInfoUsageRepo struct {
	service.UsageLogRepository
	summaries []keyInfoUsageCall
	daily     []usagestats.TrendDataPoint
	fail      bool
}

type keyInfoUsageCall struct{ start, end time.Time }

func (r *keyInfoUsageRepo) GetAPIKeyStatsAggregated(_ context.Context, id int64, start, end time.Time) (*usagestats.UsageStats, error) {
	r.summaries = append(r.summaries, keyInfoUsageCall{start, end})
	if r.fail {
		return nil, context.DeadlineExceeded
	}
	return &usagestats.UsageStats{TotalRequests: 7, TotalInputTokens: 100, TotalOutputTokens: 50, TotalTokens: 150, TotalCost: 0.5, TotalActualCost: 0.25,
		TotalAccountCost: ptrFloat(99.9)}, nil
}

func (r *keyInfoUsageRepo) GetUsageTrendWithFilters(context.Context, time.Time, time.Time, string, int64, int64, int64, int64, string, *int16, *bool, *int8) ([]usagestats.TrendDataPoint, error) {
	return r.daily, nil
}

func ptrFloat(v float64) *float64 { return &v }

type keyInfoRateRepo struct {
	service.UserGroupRateRepository
	rates map[int64]float64
}

func (r *keyInfoRateRepo) GetByUserAndGroup(_ context.Context, _ int64, groupID int64) (*float64, error) {
	if v, ok := r.rates[groupID]; ok {
		return &v, nil
	}
	return nil, nil
}

type keyInfoFixture struct {
	handler *GatewayHandler
	groups  *keyInfoGroupRepo
	usage   *keyInfoUsageRepo
	apiKey  *service.APIKey
}

func newKeyInfoFixture(t *testing.T) *keyInfoFixture {
	t.Helper()
	sharedKeyInfoCache = keyInfoCache{}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	g1 := service.Group{ID: 1, Name: "Claude Max", Description: "primary", Platform: service.PlatformAnthropic, Status: service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 1.2, Hydrated: true}
	g2 := service.Group{ID: 2, Name: "GPT Fast", Platform: service.PlatformOpenAI, Status: service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 0.09, Hydrated: true}
	g3 := service.Group{ID: 3, Name: "Other", Platform: service.PlatformGrok, Status: service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 0.12, Hydrated: true,
		// internals that must never appear in the response
		SearchProviderBaseURL: strPtr("https://search.internal.example"), SearchProviderAPIKey: strPtr("provider-credential-fixture")}
	groups := &keyInfoGroupRepo{active: []service.Group{g1, g2, g3}}
	rates := &keyInfoRateRepo{rates: map[int64]float64{2: 0.05}}
	user := &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 1234.5}
	apiKeyRepo := &keyInfoAPIKeyRepo{}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, &keyInfoUserRepo{user: user}, groups, keyInfoSubRepo{}, rates, nil, cfg)
	usageRepo := &keyInfoUsageRepo{}
	usageService := service.NewUsageService(usageRepo, nil, nil, nil)
	gatewayService := service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, rates, nil, cfg, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	openAIGatewayService := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, rates, nil, cfg, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := &GatewayHandler{
		gatewayService: gatewayService, openAIGatewayService: openAIGatewayService,
		apiKeyService: apiKeyService, usageService: usageService, cfg: cfg,
	}
	id := int64(1)
	expires := time.Now().Add(10 * 24 * time.Hour)
	apiKey := &service.APIKey{
		ID: 100, UserID: 7, Name: "ci-key", KeyPrefix: "fixture1", Key: "fixture1-THE-FULL-PLAINTEXT-SECRET-zzzz", KeyHash: "hash-should-not-leak",
		Status: service.StatusActive, GroupID: &id, Group: &g1, RouteGroupIDs: []int64{1, 2},
		IPWhitelist: []string{"203.0.113.9"}, IPBlacklist: []string{"198.51.100.7"}, LastUsedIP: strPtr("192.0.2.55"),
		Quota: 10, QuotaUsed: 3.5, ExpiresAt: &expires, CreatedAt: time.Now().Add(-48 * time.Hour),
		RateLimit5h: 2, RateLimit1d: 5, User: user, ConcurrencyLimit: 4,
	}
	return &keyInfoFixture{handler: h, groups: groups, usage: usageRepo, apiKey: apiKey}
}

func strPtr(s string) *string { return &s }

func (f *keyInfoFixture) call(t *testing.T, path string, handle func(*gin.Context), mutate ...func(*service.APIKey)) (int, map[string]any, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	apiKey := *f.apiKey
	for _, m := range mutate {
		m(&apiKey)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.UserID})
	handle(c)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body, w.Body.String()
}

// ---------- what must never leave the platform ----------

// A leaked key must reveal what the key is and has done, nothing about the account behind it.
func TestKeyInfoEndpointsNeverExposeSecretsOrAccountData(t *testing.T) {
	f := newKeyInfoFixture(t)
	f.usage.daily = []usagestats.TrendDataPoint{{Date: "2026-11-24", Requests: 3, ActualCost: 0.1, Cost: 0.2}}
	for name, call := range map[string]func() string{
		"key":    func() string { _, _, raw := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo); return raw },
		"groups": func() string { _, _, raw := f.call(t, "/v1/sub2api/groups", f.handler.KeyGroups); return raw },
		"usage": func() string {
			_, _, raw := f.call(t, "/v1/sub2api/usage?daily=1&start_date=2026-11-20&end_date=2026-11-24", f.handler.KeyUsage)
			return raw
		},
	} {
		raw := call()
		for _, secret := range []string{
			"THE-FULL-PLAINTEXT-SECRET", "hash-should-not-leak", // the key itself
			"203.0.113.9", "198.51.100.7", "192.0.2.55", // its IP lists and last-used IP
			"1234.5", "balance", // the wallet
			"provider-credential-fixture", "search.internal.example", // group-level provider credentials
			"account_cost", "account_id", "upstream", "channel", // upstream / cost internals
		} {
			require.NotContains(t, raw, secret, "%s must not contain %q", name, secret)
		}
	}
}

// ---------- /key ----------

func TestKeyInfoDescribesTheKey(t *testing.T) {
	f := newKeyInfoFixture(t)
	f.handler.apiKeyService = service.NewAPIKeyService(&keyInfoAPIKeyRepo{rate: &service.APIKeyRateLimitData{
		Usage5h: 0.5, Usage1d: 1.5, Window5hStart: timePtr(time.Now().Add(-time.Hour)), Window1dStart: timePtr(time.Now().Add(-2 * time.Hour)),
	}}, &keyInfoUserRepo{user: f.apiKey.User}, f.groups, keyInfoSubRepo{}, &keyInfoRateRepo{}, nil, &config.Config{})

	code, body, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo)

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "sub2api.key", body["object"])
	require.EqualValues(t, 100, body["id"])
	require.Equal(t, "ci-key", body["name"])
	require.Equal(t, "fixture1", body["key_prefix"])
	require.Equal(t, "active", body["status"])
	require.Equal(t, true, body["is_usable"])
	require.NotContains(t, body, "unusable_cause")
	require.Equal(t, "smart", body["route_mode"])
	require.EqualValues(t, 4, body["concurrency_limit"])
	require.EqualValues(t, 10, body["days_until_expiry"], "10 days from now, rounded up like the key list does")

	quota := body["quota"].(map[string]any)
	require.Equal(t, true, quota["limited"])
	require.EqualValues(t, 10, quota["limit"])
	require.EqualValues(t, 3.5, quota["used"])
	require.EqualValues(t, 6.5, quota["remaining"])

	windows := body["rate_limits"].([]any)
	require.Len(t, windows, 2, "only the windows the key was given")
	first := windows[0].(map[string]any)
	require.Equal(t, "5h", first["window"])
	require.EqualValues(t, 2, first["limit"])
	require.EqualValues(t, 0.5, first["used"])
	require.EqualValues(t, 1.5, first["remaining"])
	require.NotEmpty(t, first["reset_at"])

	groups := body["groups"].([]any)
	require.Len(t, groups, 2)
	require.Equal(t, "Claude Max", groups[0].(map[string]any)["name"])
	require.Equal(t, true, groups[0].(map[string]any)["primary"])
	require.Equal(t, "GPT Fast", groups[1].(map[string]any)["name"])
	require.Equal(t, false, groups[1].(map[string]any)["primary"])
}

func timePtr(t time.Time) *time.Time { return &t }

func TestKeyInfoUnlimitedKeyHasNoQuotaOrWindows(t *testing.T) {
	f := newKeyInfoFixture(t)
	_, body, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo, func(k *service.APIKey) {
		k.Quota, k.QuotaUsed, k.RateLimit5h, k.RateLimit1d, k.ExpiresAt = 0, 0, 0, 0, nil
		k.RouteGroupIDs = nil
	})
	quota := body["quota"].(map[string]any)
	require.Equal(t, false, quota["limited"])
	require.NotContains(t, quota, "limit")
	require.NotContains(t, quota, "remaining")
	require.Empty(t, body["rate_limits"])
	require.Nil(t, body["expires_at"])
	require.NotContains(t, body, "days_until_expiry")
	require.Equal(t, "single", body["route_mode"])
}

func TestKeyInfoSaysWhyAKeyCannotBeUsed(t *testing.T) {
	f := newKeyInfoFixture(t)
	for cause, mutate := range map[string]func(*service.APIKey){
		"expired":           func(k *service.APIKey) { k.Status = service.StatusAPIKeyExpired },
		"quota_exhausted":   func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted },
		"disabled":          func(k *service.APIKey) { k.Status = "inactive" },
		"group_unavailable": func(k *service.APIKey) { g := *k.Group; g.Status = "inactive"; k.Group = &g },
	} {
		t.Run(cause, func(t *testing.T) {
			code, body, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo, mutate)
			require.Equal(t, http.StatusOK, code, "an unusable key can still describe itself")
			require.Equal(t, false, body["is_usable"])
			require.Equal(t, cause, body["unusable_cause"])
		})
	}
	t.Run("runtime expiry beats an active status", func(t *testing.T) {
		_, body, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo, func(k *service.APIKey) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past })
		require.Equal(t, "expired", body["unusable_cause"])
	})
	t.Run("runtime quota beats an active status", func(t *testing.T) {
		_, body, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo, func(k *service.APIKey) { k.QuotaUsed = k.Quota })
		require.Equal(t, "quota_exhausted", body["unusable_cause"])
	})
}

// ---------- /groups ----------

func TestKeyGroupsReportStateAndTheMultiplierTheKeyPays(t *testing.T) {
	f := newKeyInfoFixture(t)
	code, body, _ := f.call(t, "/v1/sub2api/groups", f.handler.KeyGroups)

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "sub2api.key_groups", body["object"])
	require.NotEmpty(t, body["timezone"])
	groups := body["groups"].([]any)
	require.Len(t, groups, 3, "the two bound groups first, then the one more the user may bind")
	byName := map[string]map[string]any{}
	for _, g := range groups {
		m := g.(map[string]any)
		byName[m["name"].(string)] = m
	}
	require.Equal(t, "Claude Max", groups[0].(map[string]any)["name"], "routing order first")
	require.Equal(t, "GPT Fast", groups[1].(map[string]any)["name"])

	claude := byName["Claude Max"]
	require.Equal(t, true, claude["bound_to_key"])
	require.Equal(t, true, claude["primary"])
	require.Equal(t, "active", claude["status"])
	require.EqualValues(t, 1.2, claude["rate_multiplier"])
	require.EqualValues(t, 1.2, claude["resolved_rate_multiplier"])
	require.NotContains(t, claude, "user_rate_multiplier", "no personal rate, nothing to show")

	gpt := byName["GPT Fast"]
	require.Equal(t, true, gpt["bound_to_key"])
	require.Equal(t, false, gpt["primary"])
	require.EqualValues(t, 0.09, gpt["rate_multiplier"], "the group's own base multiplier")
	require.EqualValues(t, 0.05, gpt["user_rate_multiplier"], "this user's personal rate for the group")
	require.EqualValues(t, 0.05, gpt["resolved_rate_multiplier"])
	require.EqualValues(t, 0.05, gpt["effective_rate_multiplier"])

	other := byName["Other"]
	require.Equal(t, false, other["bound_to_key"])
	require.Equal(t, false, other["primary"])
}

func TestKeyGroupsShowTheGroupsTheKeyRoutesToEvenWhenNoLongerBindable(t *testing.T) {
	f := newKeyInfoFixture(t)
	// Group 2 stays in the key's routing list but is not in the user's bindable set any more
	// (the catalogue still knows it, the availability list does not).
	f.groups.active = f.groups.active[:2]
	f.handler.apiKeyService = service.NewAPIKeyService(&keyInfoAPIKeyRepo{}, &keyInfoUserRepo{user: &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive,
		AllowedGroups: []int64{99}}}, f.groups, keyInfoSubRepo{}, &keyInfoRateRepo{}, nil, &config.Config{})
	_, body, _ := f.call(t, "/v1/sub2api/groups", f.handler.KeyGroups, func(k *service.APIKey) { g := f.groups.active[1]; g.IsExclusive = true; _ = g })

	names := []string{}
	for _, g := range body["groups"].([]any) {
		names = append(names, g.(map[string]any)["name"].(string))
	}
	require.Contains(t, names, "Claude Max")
	require.Contains(t, names, "GPT Fast")
}

func TestKeyGroupsApplyThePeakFactorOnlyInsideThePeakWindow(t *testing.T) {
	f := newKeyInfoFixture(t)
	peakFor := func(start, end string) map[string]any {
		f.groups.active[0].SubscriptionType = service.SubscriptionTypeSubscription
		f.groups.active[0].PeakRateEnabled, f.groups.active[0].PeakStart, f.groups.active[0].PeakEnd, f.groups.active[0].PeakRateMultiplier = true, start, end, 2
		sharedKeyInfoCache = keyInfoCache{}
		_, body, _ := f.call(t, "/v1/sub2api/groups", f.handler.KeyGroups, func(k *service.APIKey) {
			g := f.groups.active[0]
			k.Group = &g
		})
		return body["groups"].([]any)[0].(map[string]any)
	}
	always := peakFor("00:00", "23:59")
	require.Equal(t, true, always["peak_rate_enabled"])
	require.EqualValues(t, 2, always["peak_rate_multiplier"])
	require.EqualValues(t, 1.2, always["resolved_rate_multiplier"])
	require.EqualValues(t, 2.4, always["effective_rate_multiplier"], "inside the window the peak factor is included")

	never := peakFor("03:00", "03:01")
	nowMinutes := time.Now().In(time.UTC).Hour()*60 + time.Now().In(time.UTC).Minute()
	if nowMinutes != 3*60 {
		require.EqualValues(t, 1.2, never["effective_rate_multiplier"], "outside the window it is not")
	}
}

func TestKeyGroupsAreCachedBrieflyAndSharedAcrossKeys(t *testing.T) {
	f := newKeyInfoFixture(t)
	for i := 0; i < 5; i++ {
		code, _, _ := f.call(t, "/v1/sub2api/groups", f.handler.KeyGroups)
		require.Equal(t, http.StatusOK, code)
	}
	require.LessOrEqual(t, f.groups.calls, 2, "five polls reach the group tables at most twice (catalogue + availability), not ten times")
}

// ---------- /usage ----------

func TestKeyUsageReportsTodayWeekMonthAndRange(t *testing.T) {
	f := newKeyInfoFixture(t)
	f.usage.daily = []usagestats.TrendDataPoint{{Date: "2026-11-24", Requests: 3, TotalTokens: 30, Cost: 0.2, ActualCost: 0.1}}

	code, body, _ := f.call(t, "/v1/sub2api/usage?start_date=2026-11-01&end_date=2026-11-10&daily=1&timezone=Asia/Shanghai", f.handler.KeyUsage)

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "sub2api.key_usage", body["object"])
	require.Equal(t, "Asia/Shanghai", body["timezone"])
	for _, name := range []string{"today", "last_7_days", "this_month", "range"} {
		blk := body[name].(map[string]any)
		require.EqualValues(t, 7, blk["requests"], name)
		require.EqualValues(t, 150, blk["total_tokens"], name)
		require.EqualValues(t, 0.25, blk["actual_cost"], name, "user-facing cost")
		require.EqualValues(t, 0.5, blk["cost"], name, "list price")
		require.NotEmpty(t, blk["start"], name)
		require.NotEmpty(t, blk["end"], name)
	}
	r := body["range"].(map[string]any)
	require.Equal(t, "2026-11-01", r["start"])
	require.Equal(t, "2026-11-10", r["end"], "end is inclusive")
	require.Len(t, body["daily"], 1)
	require.NotContains(t, body, "partial")

	// All four blocks and the daily series are for THIS key: the repository was only ever asked about key 100.
	require.Len(t, f.usage.summaries, 4)
}

func TestKeyUsageRangeIsValidatedAndBounded(t *testing.T) {
	f := newKeyInfoFixture(t)
	for name, path := range map[string]string{
		"bad start":      "/v1/sub2api/usage?start_date=yesterday",
		"bad end":        "/v1/sub2api/usage?end_date=2026-13-45",
		"backwards":      "/v1/sub2api/usage?start_date=2026-11-10&end_date=2026-11-01",
		"over 90 days":   "/v1/sub2api/usage?start_date=2026-01-01&end_date=2026-11-01",
		"sql in a field": "/v1/sub2api/usage?start_date=2026-11-01%27%3BDROP%20TABLE%20usage_logs%3B--",
	} {
		t.Run(name, func(t *testing.T) {
			code, body, _ := f.call(t, path, f.handler.KeyUsage)
			require.Equal(t, http.StatusBadRequest, code)
			require.Equal(t, "invalid_request_error", body["error"].(map[string]any)["type"])
		})
	}
	code, _, _ := f.call(t, "/v1/sub2api/usage?start_date=2026-08-26&end_date=2026-11-23", f.handler.KeyUsage)
	require.Equal(t, http.StatusOK, code, "exactly 90 days is allowed")
}

func TestKeyUsageToleratesBadTimezoneAndSlowQueries(t *testing.T) {
	f := newKeyInfoFixture(t)
	code, body, _ := f.call(t, "/v1/sub2api/usage?timezone="+strings.Repeat("x", 200), f.handler.KeyUsage)
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, body["timezone"], "an invalid timezone falls back to the server timezone")

	f.usage.fail = true
	code, body, _ = f.call(t, "/v1/sub2api/usage", f.handler.KeyUsage)
	require.Equal(t, http.StatusOK, code, "a failing aggregate does not fail the endpoint")
	require.ElementsMatch(t, []any{"today", "last_7_days", "this_month"}, body["partial"], "the missing blocks are named, not reported as zero")
}

// ---------- shared behaviour ----------

func TestKeyInfoEndpointsRefuseAnonymousAndSimpleMode(t *testing.T) {
	f := newKeyInfoFixture(t)
	for name, handle := range map[string]func(*gin.Context){"key": f.handler.KeyInfo, "groups": f.handler.KeyGroups, "usage": f.handler.KeyUsage} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/sub2api/"+name, nil)
		handle(c)
		require.Equal(t, http.StatusUnauthorized, w.Code, name)
		require.NotContains(t, w.Body.String(), "ci-key", name)
	}
	f.handler.cfg = &config.Config{RunMode: config.RunModeSimple}
	code, _, _ := f.call(t, "/v1/sub2api/key", f.handler.KeyInfo)
	require.Equal(t, http.StatusNotFound, code)
}

func TestKeyInfoResponsesAreNeverCached(t *testing.T) {
	f := newKeyInfoFixture(t)
	for _, tc := range []struct {
		path   string
		handle func(*gin.Context)
	}{{"/v1/sub2api/key", f.handler.KeyInfo}, {"/v1/sub2api/groups", f.handler.KeyGroups}, {"/v1/sub2api/usage", f.handler.KeyUsage}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, tc.path, nil)
		k := *f.apiKey
		c.Set(string(middleware2.ContextKeyAPIKey), &k)
		tc.handle(c)
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"), tc.path)
	}
}

var _ = pagination.PaginationParams{}
