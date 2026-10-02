package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newInflightTestEnv(t *testing.T, enabled bool, ttlSeconds int) (*miniredis.Miniredis, *billingCache, *service.BillingCacheService) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{
		Enabled:          enabled,
		TTLSeconds:       ttlSeconds,
		DefaultMaxTokens: 8192,
	}
	svc := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	return mr, cache, svc
}

func inflightCount(t *testing.T, cache *billingCache, userID int64) int64 {
	t.Helper()
	zkey, _ := billingInflightKeys(userID)
	n, err := cache.rdb.ZCard(context.Background(), zkey).Result()
	require.NoError(t, err)
	return n
}

func TestInflightReservation_ConcurrentAdmitsOnlyWhatBalanceCovers(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 42}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	const n = 20
	var admitted, rejected atomic.Int32
	releases := make(chan func(), n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.3)
			if err != nil {
				if !errors.Is(err, service.ErrInsufficientBalance) {
					t.Errorf("unexpected error: %v", err)
				}
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			releases <- release
		}()
	}
	close(start)
	wg.Wait()
	close(releases)

	// balance 1.0, estimate 0.3: first always admitted, then while 1.0 - inflight >= 0.3 → 3 total.
	require.Equal(t, int32(3), admitted.Load())
	require.Equal(t, int32(n-3), rejected.Load())
	require.Equal(t, int64(3), inflightCount(t, cache, user.ID))

	for release := range releases {
		release()
		release() // idempotent
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))

	// After release, capacity is available again.
	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.3)
	require.NoError(t, err)
	release()
}

func TestInflightReservation_FirstRequestAlwaysAdmitted(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 7}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0.01))

	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 5)
	require.NoError(t, err, "a lone request keeps legacy behavior even if estimate exceeds balance")
	_, err = svc.ReserveInflightBalance(ctx, user, nil, nil, 0.001)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	release()
	release2, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.001)
	require.NoError(t, err)
	release2()
}

func TestInflightReservation_TTLExpiryFreesLeakedReservation(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 1)
	ctx := context.Background()
	user := &service.User{ID: 9}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))

	_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8) // leaked: never released
	require.NoError(t, err)
	_, err = svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)

	time.Sleep(1100 * time.Millisecond)
	release, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0.8)
	require.NoError(t, err)
	require.Equal(t, int64(1), inflightCount(t, cache, user.ID))
	release()
}

func TestInflightReservation_DisabledKeepsLegacyBehavior(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 11}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0.01))
	require.False(t, svc.InflightReservationEnabled())
	for i := 0; i < 5; i++ {
		_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 10)
		require.NoError(t, err)
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

func TestInflightReservation_SubscriptionUnaffected(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 12}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 0))
	group := &service.Group{ID: 1, SubscriptionType: service.SubscriptionTypeSubscription}
	sub := &service.UserSubscription{ID: 1}
	for i := 0; i < 5; i++ {
		_, err := svc.ReserveInflightBalance(ctx, user, group, sub, 10)
		require.NoError(t, err)
	}
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

func TestInflightReservation_ZeroEstimateSkips(t *testing.T) {
	_, cache, svc := newInflightTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 13}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1))
	_, err := svc.ReserveInflightBalance(ctx, user, nil, nil, 0)
	require.NoError(t, err)
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

// downReserveCache 余额读取正常，但预留走一个已关闭的 Redis。
type downReserveCache struct {
	service.BillingCache
	down *billingCache
}

func (d *downReserveCache) ReserveInflightBalance(ctx context.Context, userID int64, requestID string, amount, balance float64, ttl time.Duration) (bool, float64, error) {
	return d.down.ReserveInflightBalance(ctx, userID, requestID, amount, balance, ttl)
}

func (d *downReserveCache) ReleaseInflightBalance(ctx context.Context, userID int64, requestID string) error {
	return d.down.ReleaseInflightBalance(ctx, userID, requestID)
}

func TestInflightReservation_RedisDownFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	up := &billingCache{rdb: rdb}
	require.NoError(t, up.SetUserBalance(context.Background(), 5, 0.01))

	downMR := miniredis.RunT(t)
	downRDB := redis.NewClient(&redis.Options{Addr: downMR.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = downRDB.Close() })
	downMR.Close()

	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
	svc := service.NewBillingCacheService(&downReserveCache{BillingCache: up, down: &billingCache{rdb: downRDB}}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	user := &service.User{ID: 5}
	for i := 0; i < 3; i++ {
		release, err := svc.ReserveInflightBalance(context.Background(), user, nil, nil, 100)
		require.NoError(t, err)
		release()
	}
}

func TestInflightReservation_ScriptErrorSurfaces(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	cache := &billingCache{rdb: rdb}
	mr.Close()
	_, _, err := cache.ReserveInflightBalance(context.Background(), 1, "r", 1, 1, time.Second)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
	_ = rdb.Close()
}
