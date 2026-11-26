package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// End to end through the real API key authentication: the key self-service endpoints must answer for a key that
// is expired or out of quota (a key must be able to say so), must refuse a key that is simply wrong or switched
// off, must never reach the group/model gates, and must not count as the key having been used.

type keyInfoRouteAPIKeyRepo struct {
	service.APIKeyRepository
	apiKey   *service.APIKey
	touched  atomic.Int32
	rateData *service.APIKeyRateLimitData
}

func (r *keyInfoRouteAPIKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if r.apiKey == nil || key != r.apiKey.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.apiKey
	return &clone, nil
}
func (r *keyInfoRouteAPIKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	r.touched.Add(1)
	return nil
}
func (r *keyInfoRouteAPIKeyRepo) GetRateLimitData(context.Context, int64) (*service.APIKeyRateLimitData, error) {
	return r.rateData, nil
}

type keyInfoRouteGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r *keyInfoRouteGroupRepo) ListActive(context.Context) ([]service.Group, error) {
	return append([]service.Group(nil), r.groups...), nil
}

type keyInfoRouteUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *keyInfoRouteUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

type keyInfoRouteSubRepo struct {
	service.UserSubscriptionRepository
}

func (keyInfoRouteSubRepo) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type keyInfoRouteUsageRepo struct {
	service.UsageLogRepository
	askedKeyIDs  []int64
	askedUserIDs []int64 // account-level reads (GET /v1/sub2api/account), see gateway_key_account_test.go
}

func (r *keyInfoRouteUsageRepo) GetAPIKeyStatsAggregated(_ context.Context, id int64, _, _ time.Time) (*usagestats.UsageStats, error) {
	r.askedKeyIDs = append(r.askedKeyIDs, id)
	return &usagestats.UsageStats{TotalRequests: 3, TotalTokens: 30, TotalCost: 0.3, TotalActualCost: 0.2}, nil
}

type keyInfoRouteRateRepo struct {
	service.UserGroupRateRepository
}

func (keyInfoRouteRateRepo) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	return nil, nil
}
func (keyInfoRouteRateRepo) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}

func newKeyInfoRouteRouter(t *testing.T, mutate func(*service.APIKey, *service.User), redisClients ...*redis.Client) (*gin.Engine, *keyInfoRouteAPIKeyRepo, string) {
	t.Helper()
	return newKeyInfoRouteRouterWith(t, mutate, &keyInfoRouteUsageRepo{}, redisClients...)
}

// newKeyInfoRouteRouterWith is newKeyInfoRouteRouter with the usage repository the caller can inspect afterwards.
func newKeyInfoRouteRouterWith(t *testing.T, mutate func(*service.APIKey, *service.User), usage *keyInfoRouteUsageRepo, redisClients ...*redis.Client) (*gin.Engine, *keyInfoRouteAPIKeyRepo, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	group := &service.Group{ID: 42, Name: "G42", Status: service.StatusActive, Hydrated: true, Platform: service.PlatformOpenAI,
		SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 0.75}
	user := &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive, Balance: 777.25}
	groupID := group.ID
	apiKey := &service.APIKey{ID: 100, UserID: user.ID, Key: "key-info-route-test-key", KeyPrefix: "key-info", Name: "route-key",
		Status: service.StatusActive, User: user, GroupID: &groupID, Group: group, CreatedAt: time.Now().Add(-time.Hour)}
	if mutate != nil {
		mutate(apiKey, user)
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	repo := &keyInfoRouteAPIKeyRepo{apiKey: apiKey}
	groupRepo := &keyInfoRouteGroupRepo{groups: []service.Group{*group}}
	apiKeyService := service.NewAPIKeyService(repo, &keyInfoRouteUserRepo{user: user}, groupRepo, keyInfoRouteSubRepo{}, keyInfoRouteRateRepo{}, nil, cfg)
	gatewayService := service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, keyInfoRouteRateRepo{}, nil, cfg, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	openAIGatewayService := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, keyInfoRouteRateRepo{}, nil, cfg, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	gatewayHandler := handler.NewGatewayHandler(
		gatewayService, openAIGatewayService, nil, nil, nil, nil, nil, service.NewUsageService(usage, nil, nil, nil),
		apiKeyService, nil, nil, nil, nil, cfg, nil,
	)
	router := gin.New()
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{Gateway: gatewayHandler, OpenAIGateway: &handler.OpenAIGatewayHandler{}},
		servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg),
		apiKeyService, nil, nil, nil, nil, cfg, redisClients...,
	)
	return router, repo, apiKey.Key
}

func keyInfoGet(router *gin.Engine, path, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGatewayRoutesKeySelfServiceEndpointsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	registered := map[string]bool{}
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet {
			registered[route.Path] = true
		}
	}
	for _, path := range []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage", "/v1/sub2api/account", "/v1/sub2api/billing"} {
		require.True(t, registered[path], "GET %s should be registered", path)
	}
}

func TestKeySelfServiceRequiresAValidKey(t *testing.T) {
	router, repo, key := newKeyInfoRouteRouter(t, nil)
	for _, path := range []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage"} {
		t.Run(path, func(t *testing.T) {
			anonymous := keyInfoGet(router, path, "")
			require.Equal(t, http.StatusUnauthorized, anonymous.Code)
			require.Contains(t, anonymous.Header().Get("Content-Type"), "application/json")
			require.NotContains(t, strings.ToLower(anonymous.Body.String()), "<!doctype html>")

			wrong := keyInfoGet(router, path, "not-the-key")
			require.Equal(t, http.StatusUnauthorized, wrong.Code)
			require.NotContains(t, wrong.Body.String(), "route-key")

			ok := keyInfoGet(router, path, key)
			require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
			require.Equal(t, "no-store", ok.Header().Get("Cache-Control"))
		})
	}
	require.Zero(t, repo.touched.Load(), "reading information about itself is not 'using' the key")
}

func TestKeySelfServiceAnswersForExpiredAndExhaustedKeysButNotDisabledOnes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*service.APIKey, *service.User)
		status int
		cause  string
	}{
		{"expired by status", func(k *service.APIKey, _ *service.User) { k.Status = service.StatusAPIKeyExpired }, http.StatusOK, "expired"},
		{"expired by clock", func(k *service.APIKey, _ *service.User) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past }, http.StatusOK, "expired"},
		{"quota exhausted by status", func(k *service.APIKey, _ *service.User) { k.Status = service.StatusAPIKeyQuotaExhausted }, http.StatusOK, "quota_exhausted"},
		{"quota exhausted by counter", func(k *service.APIKey, _ *service.User) { k.Quota, k.QuotaUsed = 5, 5 }, http.StatusOK, "quota_exhausted"},
		{"wallet empty", func(_ *service.APIKey, u *service.User) { u.Balance = 0 }, http.StatusOK, ""},
		{"switched off", func(k *service.APIKey, _ *service.User) { k.Status = "inactive" }, http.StatusUnauthorized, ""},
		{"owner disabled", func(_ *service.APIKey, u *service.User) { u.Status = "disabled" }, http.StatusUnauthorized, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _, key := newKeyInfoRouteRouter(t, tc.mutate)
			w := keyInfoGet(router, "/v1/sub2api/key", key)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status != http.StatusOK {
				return
			}
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			if tc.cause == "" {
				require.Equal(t, true, body["is_usable"])
			} else {
				require.Equal(t, false, body["is_usable"])
				require.Equal(t, tc.cause, body["unusable_cause"])
			}
			// The other two answer too.
			require.Equal(t, http.StatusOK, keyInfoGet(router, "/v1/sub2api/groups", key).Code)
			require.Equal(t, http.StatusOK, keyInfoGet(router, "/v1/sub2api/usage", key).Code)
		})
	}
}

func TestKeySelfServiceHonoursTheKeyIPRules(t *testing.T) {
	router, _, key := newKeyInfoRouteRouter(t, func(k *service.APIKey, _ *service.User) {
		k.IPWhitelist = []string{"203.0.113.0/24"}
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sub2api/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.RemoteAddr = "198.51.100.9:4321"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, "an IP the key does not allow cannot read key information either")
	require.NotContains(t, w.Body.String(), "route-key")
}

func TestKeySelfServiceNeverExposesTheWalletOrTheKey(t *testing.T) {
	router, _, key := newKeyInfoRouteRouter(t, nil)
	for _, path := range []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage"} {
		raw := keyInfoGet(router, path, key).Body.String()
		require.NotContains(t, raw, key, path)
		require.NotContains(t, raw, "777.25", path+" must not carry the wallet balance")
		require.NotContains(t, strings.ToLower(raw), "balance", path)
	}
}

// The limiter must actually be on the routes. The other tests run without Redis, where it lets everything
// through, so this one uses a real (in-memory) Redis: the 31st call to one endpoint inside a minute is refused,
// the other endpoints keep their own budget, and another key is untouched.
func TestKeySelfServiceEndpointsAreRateLimitedPerKeyPerEndpoint(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	// RegisterGatewayRoutes installs process-wide risk-store state when it is given a Redis client; do not leave
	// it pointing at this test's server.
	t.Cleanup(func() { service.ConfigureAPIKeyRiskStore(nil) })

	router, _, key := newKeyInfoRouteRouter(t, nil, client)
	limit := servermiddleware.KeyInfoRateLimitPerMinute
	endpoints := []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage"}

	// Every endpoint, on its own, allows exactly `limit` calls a minute and refuses the next one. Each one is
	// exercised before the others have been touched, so none of them can borrow another's budget.
	for _, path := range endpoints {
		for i := 1; i <= limit; i++ {
			w := keyInfoGet(router, path, key)
			require.Equal(t, http.StatusOK, w.Code, "%s call %d is inside the budget: %s", path, i, w.Body.String())
		}
		limited := keyInfoGet(router, path, key)
		require.Equal(t, http.StatusTooManyRequests, limited.Code, path)
		require.NotEmpty(t, limited.Header().Get("Retry-After"), path)
		require.Contains(t, limited.Body.String(), "key_info_rate_limited", path)
	}

	// The limit is per key: a different key is not affected by the first one running out (it is refused here
	// only because it is unknown, which is a 401 from authentication, not a 429).
	other := keyInfoGet(router, "/v1/sub2api/key", "some-other-key")
	require.Equal(t, http.StatusUnauthorized, other.Code)
}

func TestKeySelfServiceRateLimitDoesNotTouchOtherGatewayEndpoints(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(func() { service.ConfigureAPIKeyRiskStore(nil) })
	router, _, key := newKeyInfoRouteRouter(t, nil, client)

	for i := 0; i < servermiddleware.KeyInfoRateLimitPerMinute+5; i++ {
		keyInfoGet(router, "/v1/sub2api/key", key)
	}
	// The older key endpoints are not part of this limiter.
	require.NotEqual(t, http.StatusTooManyRequests, keyInfoGet(router, "/v1/sub2api/billing", key).Code)
	require.NotEqual(t, http.StatusTooManyRequests, keyInfoGet(router, "/v1/models", key).Code)
}
