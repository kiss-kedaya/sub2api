package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// GET /v1/sub2api/account end to end through the real API key authentication. Like the other self-service
// endpoints it must answer for a key that is expired, out of quota or whose owner has an empty wallet (a key must be
// able to say so), refuse a key that is simply wrong or switched off, and never count as the key having been used.
// It carries the owner's money, so it also must never answer anything for a caller that is not authenticated.

const keyAccountRoutePath = "/v1/sub2api/account"

func (r *keyInfoRouteUsageRepo) GetUserDashboardStats(_ context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	r.askedUserIDs = append(r.askedUserIDs, userID)
	return &usagestats.UserDashboardStats{TodayRequests: 42, TodayInputTokens: 100, TodayOutputTokens: 20, TodayTokens: 120, TodayCost: 0.5, TodayActualCost: 0.42}, nil
}

func (r *keyInfoRouteUsageRepo) GetUserStatsAggregated(_ context.Context, userID int64, _, _ time.Time) (*usagestats.UsageStats, error) {
	r.askedUserIDs = append(r.askedUserIDs, userID)
	return &usagestats.UsageStats{TotalRequests: 24}, nil
}

func keyAccountBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &body), raw)
	return body
}

func keyAccountWallet(t *testing.T, body map[string]any) any {
	t.Helper()
	balance, ok := body["balance"].(map[string]any)
	require.True(t, ok, "want a balance block, got %T", body["balance"])
	return balance["wallet"]
}

func TestKeyAccountRouteNeedsAValidKeyAndAnswersWithTheOwnersFigures(t *testing.T) {
	usage := &keyInfoRouteUsageRepo{}
	router, repo, key := newKeyInfoRouteRouterWith(t, nil, usage)

	anonymous := keyInfoGet(router, keyAccountRoutePath, "")
	require.Equal(t, http.StatusUnauthorized, anonymous.Code)
	require.Contains(t, anonymous.Header().Get("Content-Type"), "application/json")
	require.NotContains(t, strings.ToLower(anonymous.Body.String()), "<!doctype html>")
	require.NotContains(t, anonymous.Body.String(), "777.25")

	wrong := keyInfoGet(router, keyAccountRoutePath, "not-the-key")
	require.Equal(t, http.StatusUnauthorized, wrong.Code)
	require.NotContains(t, wrong.Body.String(), "777.25")
	require.Empty(t, usage.askedUserIDs, "nothing is read for a caller that failed authentication")

	ok := keyInfoGet(router, keyAccountRoutePath, key)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.Equal(t, "no-store", ok.Header().Get("Cache-Control"))
	body := keyAccountBody(t, ok.Body.String())
	require.Equal(t, "sub2api.account", body["object"])
	require.EqualValues(t, 777.25, keyAccountWallet(t, body))
	today, isObject := body["today"].(map[string]any)
	require.True(t, isObject, "want a today block, got %T", body["today"])
	require.EqualValues(t, 42, today["requests"])
	require.EqualValues(t, 0.42, today["actual_cost"])
	require.NotContains(t, body, "partial")
	require.Equal(t, []int64{7}, usage.askedUserIDs, "the account is the key's owner")
	require.Zero(t, repo.touched.Load(), "reading the account is not 'using' the key")
	require.NotContains(t, ok.Body.String(), key)
}

func TestKeyAccountRouteAnswersForExpiredExhaustedAndEmptyWalletKeysButNotSwitchedOffOnes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*service.APIKey, *service.User)
		status int
		wallet float64
	}{
		{"expired by status", func(k *service.APIKey, _ *service.User) { k.Status = service.StatusAPIKeyExpired }, http.StatusOK, 777.25},
		{"expired by clock", func(k *service.APIKey, _ *service.User) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past }, http.StatusOK, 777.25},
		{"quota exhausted by status", func(k *service.APIKey, _ *service.User) { k.Status = service.StatusAPIKeyQuotaExhausted }, http.StatusOK, 777.25},
		{"quota exhausted by counter", func(k *service.APIKey, _ *service.User) { k.Quota, k.QuotaUsed = 5, 5 }, http.StatusOK, 777.25},
		// An empty wallet is exactly when the owner wants to look: the answer is a wallet of 0, read, not refused.
		{"wallet empty", func(_ *service.APIKey, u *service.User) { u.Balance = 0 }, http.StatusOK, 0},
		{"switched off", func(k *service.APIKey, _ *service.User) { k.Status = "inactive" }, http.StatusUnauthorized, 0},
		{"owner disabled", func(_ *service.APIKey, u *service.User) { u.Status = "disabled" }, http.StatusUnauthorized, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := &keyInfoRouteUsageRepo{}
			router, _, key := newKeyInfoRouteRouterWith(t, tc.mutate, usage)
			w := keyInfoGet(router, keyAccountRoutePath, key)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status != http.StatusOK {
				require.NotContains(t, w.Body.String(), "balance")
				require.Empty(t, usage.askedUserIDs, "a refused key reads nothing")
				return
			}
			body := keyAccountBody(t, w.Body.String())
			require.EqualValues(t, tc.wallet, keyAccountWallet(t, body))
			require.NotContains(t, body, "partial", "the wallet was read; it is not missing")
		})
	}
}

func TestKeyAccountRouteHonoursTheKeyIPRules(t *testing.T) {
	router, _, key := newKeyInfoRouteRouter(t, func(k *service.APIKey, _ *service.User) {
		k.IPWhitelist = []string{"203.0.113.0/24"}
	})
	get := func(remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, keyAccountRoutePath, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.RemoteAddr = remote
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	denied := get("198.51.100.9:4321")
	require.Equal(t, http.StatusForbidden, denied.Code, "an IP the key does not allow cannot read the account either")
	require.NotContains(t, denied.Body.String(), "777.25")
	require.NotContains(t, denied.Body.String(), "balance")
	require.Equal(t, http.StatusOK, get("203.0.113.7:4321").Code)
}

// The account endpoint has its own counter: 30 calls a minute, the 31st refused with Retry-After, and neither side
// borrows from the other endpoints. Each direction runs against a fresh counter store.
func TestKeyAccountRouteIsRateLimitedOnItsOwnCounter(t *testing.T) {
	newRouter := func(t *testing.T) (*gin.Engine, string) {
		t.Helper()
		server := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = client.Close() })
		// RegisterGatewayRoutes installs process-wide risk-store state when it is given a Redis client.
		t.Cleanup(func() { service.ConfigureAPIKeyRiskStore(nil) })
		r, _, k := newKeyInfoRouteRouter(t, nil, client)
		return r, k
	}
	get := func(router *gin.Engine, path, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	limit := servermiddleware.KeyInfoRateLimitPerMinute

	t.Run("the account counter runs out on the 31st call and leaves the others alone", func(t *testing.T) {
		router, key := newRouter(t)
		for i := 1; i <= limit; i++ {
			w := get(router, keyAccountRoutePath, key)
			require.Equal(t, http.StatusOK, w.Code, "call %d is inside the budget: %s", i, w.Body.String())
		}
		limited := get(router, keyAccountRoutePath, key)
		require.Equal(t, http.StatusTooManyRequests, limited.Code)
		require.NotEmpty(t, limited.Header().Get("Retry-After"))
		require.Equal(t, "no-store", limited.Header().Get("Cache-Control"))
		require.Contains(t, limited.Body.String(), "key_info_rate_limited")
		require.NotContains(t, limited.Body.String(), "777.25", "a refusal carries no figures")
		for _, path := range []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage"} {
			require.Equal(t, http.StatusOK, get(router, path, key).Code, "%s keeps its own budget", path)
		}
	})
	t.Run("the other counters running out leave the account endpoint alone", func(t *testing.T) {
		router, key := newRouter(t)
		for _, path := range []string{"/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage"} {
			for i := 0; i <= limit; i++ {
				get(router, path, key)
			}
			require.Equal(t, http.StatusTooManyRequests, get(router, path, key).Code, path)
		}
		require.Equal(t, http.StatusOK, get(router, keyAccountRoutePath, key).Code, "the account counter is untouched")
	})
}
