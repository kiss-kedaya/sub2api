//go:build integration

package repository

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type revocationCacheProbe struct {
	service.APIKeyCache
	reads atomic.Int64
}

func (c *revocationCacheProbe) GetAuthCache(ctx context.Context, key string) (*service.APIKeyAuthCacheEntry, error) {
	c.reads.Add(1)
	return c.APIKeyCache.GetAuthCache(ctx, key)
}

func TestAPIKeyRevocationReachesBothWarmOrigins(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()

	group := mustCreateGroup(t, integrationEntClient, &service.Group{
		Name:     fmt.Sprintf("revocation-fixture-%d", time.Now().UnixNano()),
		Platform: service.PlatformOpenAI,
		Status:   service.StatusActive,
	})
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:  group.Name + "@example.invalid",
		Status: service.StatusActive,
	})
	redisFixture := miniredis.RunT(t)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.APIKeyAuth = config.APIKeyAuthCacheConfig{L1Size: 1000, L1TTLSeconds: 120, L2TTLSeconds: 120}
	repo := NewAPIKeyRepository(integrationEntClient, integrationDB)

	buildGateway := func() (*service.APIKeyService, *revocationCacheProbe, *gin.Engine) {
		client := redis.NewClient(&redis.Options{Addr: redisFixture.Addr()})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		cache := &revocationCacheProbe{APIKeyCache: NewAPIKeyCache(client)}
		svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
		svc.StartAuthCacheInvalidationSubscriber(ctx)
		t.Cleanup(svc.StopAuthCacheInvalidationSubscriber)
		router := gin.New()
		router.Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(svc, nil, cfg)))
		router.GET("/fixture", func(c *gin.Context) { c.Status(http.StatusOK) })
		return svc, cache, router
	}

	first, firstCache, inbound := buildGateway()
	second, secondCache, fallback := buildGateway()
	require.Eventually(t, func() bool {
		return first.AuthCacheInvalidationSubscriberHealth().Connected && second.AuthCacheInvalidationSubscriberHealth().Connected
	}, 3*time.Second, 15*time.Millisecond)

	secret, err := first.GenerateKey()
	require.NoError(t, err)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Name: "revocation fixture", Key: secret, Status: service.StatusAPIKeyActive}
	require.NoError(t, repo.Create(ctx, key))
	digest := service.HashAPIKey(secret)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, err := integrationDB.ExecContext(cleanup, "DELETE FROM api_keys WHERE id = $1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanup, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanup, "DELETE FROM groups WHERE id = $1", group.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(cleanup, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", digest)
		require.NoError(t, err)
	})

	request := func(gateway *gin.Engine) int {
		r := httptest.NewRequest(http.MethodGet, "/fixture", nil).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		gateway.ServeHTTP(w, r)
		return w.Code
	}
	assertWarm := func(gateway *gin.Engine, cache *revocationCacheProbe) {
		t.Helper()
		require.Equal(t, http.StatusOK, request(gateway))
		require.Eventually(t, func() bool {
			before := cache.reads.Load()
			return request(gateway) == http.StatusOK && cache.reads.Load() == before
		}, 3*time.Second, 15*time.Millisecond, "authorization must reach the warm local cache")
	}
	assertWarm(inbound, firstCache)
	assertWarm(fallback, secondCache)
	require.True(t, redisFixture.Exists(apiKeyAuthCacheKey(digest)))

	// A revocation from storage relies on the durable outbox, not the local
	// application handler to invalidate both processes.
	require.NoError(t, repo.DeleteWithAudit(ctx, key.ID))
	var pending int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1", digest).Scan(&pending))
	require.Positive(t, pending)

	worker := service.NewAuthCacheInvalidationWorker(NewAuthCacheInvalidationOutboxRepository(integrationDB), firstCache, first)
	worker.Start()
	t.Cleanup(worker.Stop)
	require.Eventually(t, func() bool {
		return request(inbound) == http.StatusUnauthorized && request(fallback) == http.StatusUnauthorized
	}, 10*time.Second, 40*time.Millisecond, "both origins must reject the old credential before either cache can expire")
}
