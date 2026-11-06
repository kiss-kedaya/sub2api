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

func newKeyQuotaTestEnv(t *testing.T, balanceInflightEnabled bool, ttlSeconds int) (*miniredis.Miniredis, *billingCache, *service.BillingCacheService) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &billingCache{rdb: rdb}
	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{
		Enabled:    balanceInflightEnabled,
		TTLSeconds: ttlSeconds,
	}
	svc := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	return mr, cache, svc
}

func keyQuotaInflightCount(t *testing.T, cache *billingCache, keyID int64) int64 {
	t.Helper()
	_, zsetKey, _ := apiKeyQuotaKeys(keyID)
	n, err := cache.rdb.ZCard(context.Background(), zsetKey).Result()
	require.NoError(t, err)
	return n
}

// 回归（审计 finding #2/#8）：quota 只在准入时读鉴权快照，20 个并发请求全部放行。
// 现在 key 配额在准入处原子预占，20 个并发只会放行配额覆盖得下的数量。
func TestAPIKeyQuotaReservation_ConcurrentAdmitsOnlyWhatQuotaCovers(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 42}
	apiKey := &service.APIKey{ID: 7, User: user, Quota: 1.0}

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
			res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.3)
			if err != nil {
				if !errors.Is(err, service.ErrAPIKeyQuotaExhausted) {
					t.Errorf("unexpected error: %v", err)
				}
				rejected.Add(1)
				return
			}
			if res == nil {
				t.Error("quota reservation must return a handle when admitted")
				return
			}
			admitted.Add(1)
			releases <- res.HandlerDone
		}()
	}
	close(start)
	wg.Wait()
	close(releases)

	// quota 1.0、单笔 0.3：0.3 / 0.6 / 0.9 放行，第 4 笔 1.2 > 1.0 拒绝。
	require.Equal(t, int32(3), admitted.Load())
	require.Equal(t, int32(n-3), rejected.Load())
	require.Equal(t, int64(3), keyQuotaInflightCount(t, cache, apiKey.ID))

	for release := range releases {
		release()
		release() // 幂等
	}
	require.Equal(t, int64(0), keyQuotaInflightCount(t, cache, apiKey.ID))

	// 释放后额度重新可用。
	res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.9)
	require.NoError(t, err)
	require.NotNil(t, res)
	res.HandlerDone()
	require.Equal(t, int64(0), keyQuotaInflightCount(t, cache, apiKey.ID))
}

// 串行路径：预占释放后按实际扣费累加实时用量镜像，用满之后下一笔被拒。
func TestAPIKeyQuotaReservation_SerialRejectsAfterSettledUsage(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 43}
	apiKey := &service.APIKey{ID: 8, User: user, Quota: 1.0}

	first, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.4)
	require.NoError(t, err)
	require.NotNil(t, first)
	svc.IncrementAPIKeyQuotaUsage(apiKey.ID, 0.4) // 计费 worker 结算
	first.HandlerDone()

	second, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.4)
	require.NoError(t, err, "0.4(已结算) + 0.4 <= 1.0")
	require.NotNil(t, second)
	svc.IncrementAPIKeyQuotaUsage(apiKey.ID, 0.4)
	second.HandlerDone()

	_, err = svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.4)
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted, "0.8(已结算) + 0.4 > 1.0")
	require.Equal(t, int64(0), keyQuotaInflightCount(t, cache, apiKey.ID))

	// 管理员重置配额（数据库 quota_used 归零 + 镜像失效）后额度重新可用。
	apiKey.QuotaUsed = 0
	require.NoError(t, svc.InvalidateAPIKeyQuotaUsage(ctx, apiKey.ID))
	res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.4)
	require.NoError(t, err)
	require.NotNil(t, res)
	res.HandlerDone()
}

// 审计复现：quota=0.002 的 key，单笔上界 0.0036，即使并发 20 个也不能再全放行。
func TestAPIKeyQuotaReservation_TinyQuotaRejectsOversizedRequest(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 44}
	apiKey := &service.APIKey{ID: 9, User: user, Quota: 0.002}

	for i := 0; i < 20; i++ {
		_, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.0036)
		require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted)
	}
	require.Equal(t, int64(0), keyQuotaInflightCount(t, cache, apiKey.ID))
}

// quota=0 表示不限额：不读缓存、不写预占，行为与修复前完全一致。
func TestAPIKeyQuotaReservation_QuotaZeroUntouched(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 45}
	apiKey := &service.APIKey{ID: 10, User: user, Quota: 0}

	for i := 0; i < 5; i++ {
		res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 10)
		require.NoError(t, err)
		require.Nil(t, res)
	}
	counterKey, zsetKey, amountKey := apiKeyQuotaKeys(apiKey.ID)
	for _, key := range []string{counterKey, zsetKey, amountKey} {
		exists, err := cache.rdb.Exists(ctx, key).Result()
		require.NoError(t, err)
		require.Zero(t, exists, "quota=0 不得产生任何配额缓存 key: %s", key)
	}
}

// 计数器缺失时用鉴权快照里的 quota_used 播种，已用量不会被当成 0 重新放行。
func TestAPIKeyQuotaReservation_SeedsFromAuthSnapshot(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, false, 60)
	ctx := context.Background()
	user := &service.User{ID: 46}
	apiKey := &service.APIKey{ID: 11, User: user, Quota: 1.0, QuotaUsed: 0.8}

	_, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.3)
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted, "0.8(快照) + 0.3 > 1.0")

	counterKey, _, _ := apiKeyQuotaKeys(apiKey.ID)
	used, err := cache.rdb.HGet(ctx, counterKey, apiKeyQuotaUsedField).Result()
	require.NoError(t, err)
	require.Equal(t, "0.8", used)

	res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.1)
	require.NoError(t, err)
	require.NotNil(t, res)
	res.HandlerDone()
}

// 预留 TTL 到期后（进程崩溃/泄漏）额度自动回收。
func TestAPIKeyQuotaReservation_TTLExpiryFreesLeakedReservation(t *testing.T) {
	_, cache, _ := newKeyQuotaTestEnv(t, false, 1)
	ctx := context.Background()
	keyID := int64(12)
	require.NoError(t, cache.SeedAPIKeyQuotaUsage(ctx, keyID, 0, time.Minute))

	ready, allowed, _, _, err := cache.ReserveAPIKeyQuota(ctx, keyID, "leaked", 0.6, 1.0, time.Second)
	require.NoError(t, err)
	require.True(t, ready)
	require.True(t, allowed)
	ready, allowed, _, inflight, err := cache.ReserveAPIKeyQuota(ctx, keyID, "next", 0.6, 1.0, time.Second)
	require.NoError(t, err)
	require.True(t, ready)
	require.False(t, allowed)
	require.InDelta(t, 0.6, inflight, 1e-9)

	time.Sleep(1100 * time.Millisecond)
	ready, allowed, _, _, err = cache.ReserveAPIKeyQuota(ctx, keyID, "after-ttl", 0.6, 1.0, time.Second)
	require.NoError(t, err)
	require.True(t, ready)
	require.True(t, allowed, "过期预留必须在 Lua 内被清掉")
}

// 续期：长请求期间 TTL 被续期，不会在中途释放额度。
func TestAPIKeyQuotaReservation_RenewalKeepsReservationAlive(t *testing.T) {
	_, _, svc := newKeyQuotaTestEnv(t, false, 1)
	ctx := context.Background()
	user := &service.User{ID: 48}
	apiKey := &service.APIKey{ID: 13, User: user, Quota: 1.0}

	res, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.6)
	require.NoError(t, err)
	time.Sleep(1500 * time.Millisecond) // 超过 1s TTL，续期协程已续过两次
	_, err = svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.6)
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted, "续期后的预留必须继续占用额度")
	res.HandlerDone()
}

// key 配额与余额在途同时开启：两个维度一起预占、一起释放；
// 余额维度拒绝时不能把已经登记的 key 配额泄漏到 TTL。
func TestAPIKeyQuotaReservation_CombinedWithBalanceInflight(t *testing.T) {
	_, cache, svc := newKeyQuotaTestEnv(t, true, 60)
	ctx := context.Background()
	user := &service.User{ID: 49}
	require.NoError(t, cache.SetUserBalance(ctx, user.ID, 1.0))
	apiKey := &service.APIKey{ID: 14, User: user, Quota: 10}

	first, err := svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.9)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, int64(1), keyQuotaInflightCount(t, cache, apiKey.ID))
	require.Equal(t, int64(1), inflightCount(t, cache, user.ID))

	// 余额 1.0 只够一笔 0.9：第二笔被余额拒绝，key 配额不能残留。
	_, err = svc.ReserveAdmission(ctx, user, apiKey, nil, nil, 0.9)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.Equal(t, int64(1), keyQuotaInflightCount(t, cache, apiKey.ID))
	require.Equal(t, int64(1), inflightCount(t, cache, user.ID))

	first.HandlerDone()
	require.Equal(t, int64(0), keyQuotaInflightCount(t, cache, apiKey.ID))
	require.Equal(t, int64(0), inflightCount(t, cache, user.ID))
}

// downKeyQuotaCache 余额可用，但 key 配额写入走一个已关闭的 Redis（模拟 Redis 故障）。
type downKeyQuotaCache struct {
	service.BillingCache
	down *billingCache
}

func (d *downKeyQuotaCache) SeedAPIKeyQuotaUsage(ctx context.Context, keyID int64, quotaUsed float64, ttl time.Duration) error {
	return d.down.SeedAPIKeyQuotaUsage(ctx, keyID, quotaUsed, ttl)
}

func (d *downKeyQuotaCache) ReserveAPIKeyQuota(ctx context.Context, keyID int64, requestID string, estimate, quota float64, ttl time.Duration) (bool, bool, float64, float64, error) {
	return d.down.ReserveAPIKeyQuota(ctx, keyID, requestID, estimate, quota, ttl)
}

func (d *downKeyQuotaCache) ReleaseAPIKeyQuota(ctx context.Context, keyID int64, requestID string) error {
	return d.down.ReleaseAPIKeyQuota(ctx, keyID, requestID)
}

func (d *downKeyQuotaCache) IncrementAPIKeyQuotaUsage(ctx context.Context, keyID int64, cost float64) error {
	return d.down.IncrementAPIKeyQuotaUsage(ctx, keyID, cost)
}

func (d *downKeyQuotaCache) InvalidateAPIKeyQuotaUsage(ctx context.Context, keyID int64) error {
	return d.down.InvalidateAPIKeyQuotaUsage(ctx, keyID)
}

func (d *downKeyQuotaCache) RenewAPIKeyQuota(ctx context.Context, keyID int64, requestID string, ttl time.Duration) (bool, error) {
	return d.down.RenewAPIKeyQuota(ctx, keyID, requestID, ttl)
}

// Redis 不可用时 key 配额 fail-open（退回鉴权快照门禁），不得把请求挡死。
func TestAPIKeyQuotaReservation_RedisDownFailsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	up := &billingCache{rdb: rdb}

	downMR := miniredis.RunT(t)
	downRDB := redis.NewClient(&redis.Options{Addr: downMR.Addr(), MaxRetries: -1, DialTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = downRDB.Close() })
	downMR.Close()

	cfg := &config.Config{}
	cfg.Billing.InflightReservation = config.InflightReservationConfig{TTLSeconds: 60}
	svc := service.NewBillingCacheService(&downKeyQuotaCache{BillingCache: up, down: &billingCache{rdb: downRDB}}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	user := &service.User{ID: 50}
	apiKey := &service.APIKey{ID: 15, User: user, Quota: 1.0}
	for i := 0; i < 3; i++ {
		res, err := svc.ReserveAdmission(context.Background(), user, apiKey, nil, nil, 10)
		require.NoError(t, err)
		require.Nil(t, res, "Redis 故障时 fail-open，不登记预占")
	}
}
