package handler

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// handlerKeyQuotaCache 内存版 key 配额缓存：语义与 Redis Lua 一致
// （ready 惰性播种、used + inflight + estimate > quota 拒绝、释放幂等）。
type handlerKeyQuotaCache struct {
	service.BillingCache
	mu       sync.Mutex
	used     map[int64]float64
	inflight map[int64]map[string]float64
}

func newHandlerKeyQuotaCache() *handlerKeyQuotaCache {
	return &handlerKeyQuotaCache{
		used:     map[int64]float64{},
		inflight: map[int64]map[string]float64{},
	}
}

func (m *handlerKeyQuotaCache) SeedAPIKeyQuotaUsage(_ context.Context, keyID int64, quotaUsed float64, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.used[keyID]; !ok {
		m.used[keyID] = quotaUsed
	}
	return nil
}

func (m *handlerKeyQuotaCache) ReserveAPIKeyQuota(_ context.Context, keyID int64, requestID string, estimate, quota float64, _ time.Duration) (bool, bool, float64, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	used, ok := m.used[keyID]
	if !ok {
		return false, false, 0, 0, nil
	}
	entries := m.inflight[keyID]
	inflight := 0.0
	for _, v := range entries {
		inflight += v
	}
	if quota > 0 && used+inflight+estimate > quota {
		return true, false, used, inflight, nil
	}
	if entries == nil {
		entries = map[string]float64{}
		m.inflight[keyID] = entries
	}
	entries[requestID] = estimate
	return true, true, used, inflight, nil
}

func (m *handlerKeyQuotaCache) ReleaseAPIKeyQuota(_ context.Context, keyID int64, requestID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.inflight[keyID], requestID)
	return nil
}

func (m *handlerKeyQuotaCache) IncrementAPIKeyQuotaUsage(_ context.Context, keyID int64, cost float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.used[keyID] += cost
	return nil
}

func (m *handlerKeyQuotaCache) InvalidateAPIKeyQuotaUsage(_ context.Context, keyID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.used, keyID)
	return nil
}

func (m *handlerKeyQuotaCache) RenewAPIKeyQuota(context.Context, int64, string, time.Duration) (bool, error) {
	return true, nil
}

func (m *handlerKeyQuotaCache) inflightCount(keyID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inflight[keyID])
}

func newKeyQuotaHandlerBilling(t *testing.T, cache service.BillingCache) *service.BillingCacheService {
	t.Helper()
	billing := service.NewBillingCacheService(cache, nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(billing.Stop)
	return billing
}

// 审计 finding #2/#8 的 handler 级复现：余额在途开关关闭也不影响 key 配额的并发门禁。
func TestReserveInflightBalance_KeyQuotaEnforcedWithoutBalanceSwitch(t *testing.T) {
	cache := newHandlerKeyQuotaCache()
	billing := newKeyQuotaHandlerBilling(t, cache)
	require.False(t, billing.InflightReservationEnabled(), "余额在途开关默认关闭")
	require.True(t, billing.APIKeyQuotaReservationEnabled())

	apiKey := &service.APIKey{ID: 21, User: &service.User{ID: 7}, Quota: 1.0}

	const n = 20
	var admitted, rejected atomic.Int32
	dones := make(chan func(), n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			est := &countingEstimator{cost: 0.3, priced: true}
			done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("m", nil))
			if err != nil {
				if !errors.Is(err, service.ErrAPIKeyQuotaExhausted) {
					t.Errorf("unexpected error: %v", err)
				}
				rejected.Add(1)
				return
			}
			admitted.Add(1)
			dones <- done
		}()
	}
	close(start)
	wg.Wait()
	close(dones)

	require.Equal(t, int32(3), admitted.Load(), "quota=1.0、单笔 0.3 只允许 3 笔并发")
	require.Equal(t, int32(n-3), rejected.Load())
	require.Equal(t, 3, cache.inflightCount(apiKey.ID))

	for done := range dones {
		done()
		done() // 幂等
	}
	require.Equal(t, 0, cache.inflightCount(apiKey.ID))

	// 释放后额度重新可用（串行路径）。
	done, err := reserveInflightBalance(newInflightTestGinContext(), billing, &countingEstimator{cost: 0.3, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	require.Equal(t, 1, cache.inflightCount(apiKey.ID))
	done()
	require.Equal(t, 0, cache.inflightCount(apiKey.ID))
}

// 线上 2.3.10 复现参数：quota=0.002、单请求上界 0.0036。修复后不再全 200。
func TestReserveInflightBalance_TinyQuotaRejectsOversizedRequest(t *testing.T) {
	cache := newHandlerKeyQuotaCache()
	billing := newKeyQuotaHandlerBilling(t, cache)
	apiKey := &service.APIKey{ID: 22, User: &service.User{ID: 8}, Quota: 0.002}

	_, err := reserveInflightBalance(newInflightTestGinContext(), billing, &countingEstimator{cost: 0.0036, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.ErrorIs(t, err, service.ErrAPIKeyQuotaExhausted)
	require.Equal(t, 0, cache.inflightCount(apiKey.ID))
}

// quota=0 = 不限额：不预估、不预占，行为与修复前完全一致。
func TestReserveInflightBalance_QuotaZeroUnaffected(t *testing.T) {
	cache := newHandlerKeyQuotaCache()
	billing := newKeyQuotaHandlerBilling(t, cache)
	apiKey := &service.APIKey{ID: 23, User: &service.User{ID: 9}, Quota: 0}
	est := &countingEstimator{cost: 10, priced: true}

	for i := 0; i < 5; i++ {
		done, err := reserveInflightBalance(newInflightTestGinContext(), billing, est, apiKey, nil, tokenInflightEstimate("m", nil))
		require.NoError(t, err)
		done()
	}
	require.Equal(t, 0, est.calls, "quota=0 且余额在途关闭时不应触发估算")
	require.Equal(t, 0, cache.inflightCount(apiKey.ID))
}

// key 配额预留由计费任务接管引用：handler 已返回但结算未落地时不得提前释放。
func TestReserveInflightBalance_KeyQuotaHeldUntilBillingTaskDone(t *testing.T) {
	cache := newHandlerKeyQuotaCache()
	billing := newKeyQuotaHandlerBilling(t, cache)
	apiKey := &service.APIKey{ID: 24, User: &service.User{ID: 10}, Quota: 1.0}

	c := newInflightTestGinContext()
	done, err := reserveInflightBalance(c, billing, &countingEstimator{cost: 0.9, priced: true}, apiKey, nil, tokenInflightEstimate("m", nil))
	require.NoError(t, err)
	require.Equal(t, 1, cache.inflightCount(apiKey.ID))

	task, abandon := wrapUsageRecordTaskContext(c.Request.Context(), func(context.Context) {})
	done() // handler 返回，计费任务仍在队列中
	require.Equal(t, 1, cache.inflightCount(apiKey.ID), "结算前必须保持预占")

	task(context.Background())
	require.Equal(t, 0, cache.inflightCount(apiKey.ID))
	abandon() // 幂等
}

func TestBillingErrorDetails_APIKeyQuotaExhausted(t *testing.T) {
	status, errType, message, retryAfter := billingErrorDetails(service.ErrAPIKeyQuotaExhausted)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "API_KEY_QUOTA_EXHAUSTED", errType)
	require.NotEmpty(t, message)
	require.Zero(t, retryAfter)
}
