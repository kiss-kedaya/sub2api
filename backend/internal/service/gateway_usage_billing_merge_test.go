//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 合并结算模式下服务层看到的是 NewBalance=nil 的扣费结果（BILL-1 指出这条路径没有测试）。
// 下面几个用例钉住它的两个行为：余额缓存按「相对扣减」维护、低余额提醒由结算 worker 负责。

func newMergeBillingParams(balanceSnapshot, cost float64) *postUsageBillingParams {
	return &postUsageBillingParams{
		Cost: &CostBreakdown{ActualCost: cost, TotalCost: cost},
		User: &User{
			ID:                   1,
			Balance:              balanceSnapshot,
			BalanceNotifyEnabled: true,
		},
		Account: &Account{ID: 9},
	}
}

func newMergeBillingDeps(t *testing.T, mergeEnabled bool) (*billingDeps, *balanceEligibilityCacheStub, *settlementPushSpy) {
	t.Helper()
	cache := &balanceEligibilityCacheStub{balance: 100}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	cfg.Billing.BalanceMergeSettlementEnabled = mergeEnabled
	cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(cacheSvc.Stop)

	notify, settings := newBalanceNotifyServiceForTest()
	settings.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	settings.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	push := &settlementPushSpy{}
	notify.SetPushNotifier(push)
	return &billingDeps{billingCacheService: cacheSvc, balanceNotifyService: notify, cfg: cfg}, cache, push
}

func deferredResult() *UsageBillingApplyResult {
	return &UsageBillingApplyResult{Applied: true, BalanceSettlementDeferred: true}
}

func TestMergeSettlementDeferredCharge_KeepsTheBalanceCacheRelative(t *testing.T) {
	deps, cache, _ := newMergeBillingDeps(t, true)
	p := newMergeBillingParams(50, 0.25)

	// The queued charge has no post-deduction balance (NewBalance == nil) and
	// users.balance has not moved yet, so the only correct cache update is the
	// relative decrement. An absolute write here would re-publish the stale row.
	finalizePostUsageBilling(context.Background(), p, deps, deferredResult(), false)

	require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, int64(0), cache.setCalls.Load(), "no absolute balance may be published for a deferred charge")
	require.Equal(t, int64(0), cache.invalidateCalls.Load())
}

func TestMergeSettlementDeferredCharge_InflightReservationDeductsSynchronously(t *testing.T) {
	cache := newMemInflightCache(1)
	svc := newInflightSvc(t, cache, 60)
	p := newMergeBillingParams(50, 0.25)
	deps := &billingDeps{billingCacheService: svc, cfg: &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}}

	syncBalanceCacheAfterDeduction(context.Background(), p, deps, deferredResult())

	// With in-flight reservations the deduction must land before the billing task
	// returns (the reservation is released right after), exactly as for a
	// synchronous charge; a deferred charge only differs by having no NewBalance.
	require.Equal(t, int32(1), cache.deducts.Load())
	balance, _ := cache.GetUserBalance(context.Background(), 1)
	require.InDelta(t, 0.75, balance, 1e-12)
}

// 退回同步扣费（积压保护）或本进程没有结算消费者时，开关仍然是开的：库里的 users.balance
// 落后于队列，同步扣费返回的 NewBalance 不是用户的真实余额。缓存必须继续相对扣减，
// 否则「只降不升」的绝对值写入会让队列里还没结算的那部分在缓存里消失。
func TestMergeSettlementSynchronousCharge_DoesNotPublishTheLaggingBalance(t *testing.T) {
	deps, cache, _ := newMergeBillingDeps(t, true)
	p := newMergeBillingParams(50, 0.25)
	newBalance := 49.75
	result := &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}

	syncBalanceCacheAfterDeduction(context.Background(), p, deps, result)

	require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, int64(0), cache.setCalls.Load())
}

func TestMergeSettlementSynchronousCharge_ExhaustedBalanceIsStillPublishedExactly(t *testing.T) {
	deps, cache, _ := newMergeBillingDeps(t, true)
	p := newMergeBillingParams(50, 0.25)
	newBalance := 0.005 // below the 0.01 reserve: the lower DB value is a safe upper bound
	result := &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}

	syncBalanceCacheAfterDeduction(context.Background(), p, deps, result)

	require.Equal(t, int64(1), cache.setCalls.Load())
	require.Equal(t, int64(0), cache.deductCalls.Load())
}

// 开关关闭时同步扣费路径必须和改动前一样：发布权威的扣费后余额。
func TestMergeSettlementSwitchOff_SynchronousChargeStillPublishesTheAuthoritativeBalance(t *testing.T) {
	deps, cache, _ := newMergeBillingDeps(t, false)
	p := newMergeBillingParams(50, 0.25)
	newBalance := 49.75
	result := &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}

	syncBalanceCacheAfterDeduction(context.Background(), p, deps, result)

	require.Eventually(t, func() bool { return cache.setCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, int64(0), cache.deductCalls.Load())
}

func TestMergeSettlementSwitchOff_NilConfigIsTheLegacyMode(t *testing.T) {
	var nilDeps *billingDeps
	require.False(t, nilDeps.mergeSettlementEnabled())
	require.False(t, (&billingDeps{}).mergeSettlementEnabled())
	require.True(t, (&billingDeps{cfg: &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}}).mergeSettlementEnabled())
}

// 旧行为的对照：请求路径用鉴权快照判断。快照 10.05、单笔 0.1，会判成「刚穿过阈值 10」。
func TestMergeSettlementLowBalance_LegacyRequestPathAlertsFromTheSnapshot(t *testing.T) {
	resetSettlementProcessState(t)
	deps, _, push := newMergeBillingDeps(t, false)
	p := newMergeBillingParams(10.05, 0.1)

	notifyBalanceLow(p, deps, &UsageBillingApplyResult{Applied: true})

	require.Eventually(t, func() bool { return len(push.snapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)
}

// 合并结算且结算 worker 在发提醒：延迟入队的扣费不再用过期快照在请求路径上判断，
// 否则同一窗口内的每个请求都会重复触发一次提醒。
func TestMergeSettlementLowBalance_RequestPathStaysQuietWhenTheWorkerOwnsTheAlert(t *testing.T) {
	resetSettlementProcessState(t)
	deps, _, push := newMergeBillingDeps(t, true)
	usageBalanceSettlementNotifierActive.Store(true)

	for i := 0; i < 20; i++ {
		// 20 requests of one burst, every one of them carrying the same stale snapshot.
		notifyBalanceLow(newMergeBillingParams(10.05, 0.1), deps, deferredResult())
	}
	require.Never(t, func() bool { return len(push.snapshot()) > 0 }, 200*time.Millisecond, 10*time.Millisecond)

	// Preauth finalization is queued the same way and is settled by the same worker.
	notifyBalanceLow(newMergeBillingParams(10.05, 0.1), deps, &UsageBillingApplyResult{Applied: true, BalanceFinalizationPending: true})
	require.Never(t, func() bool { return len(push.snapshot()) > 0 }, 100*time.Millisecond, 10*time.Millisecond)
}

// 同步扣费（含积压保护的退回路径）仍带着精确的 NewBalance，请求路径照常判断；
// worker 不会替它提醒，因为这笔扣费根本没进队列。
func TestMergeSettlementLowBalance_SynchronousChargesStillAlertFromTheRequestPath(t *testing.T) {
	resetSettlementProcessState(t)
	deps, _, push := newMergeBillingDeps(t, true)
	usageBalanceSettlementNotifierActive.Store(true)
	newBalance := 9.95
	result := &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}

	notifyBalanceLow(newMergeBillingParams(10.05, 0.1), deps, result)

	require.Eventually(t, func() bool { return len(push.snapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)
}

// 没有在运行的提醒观察者（开关关闭、没有消费者）时，请求路径照旧负责，
// 即使结果是延迟入队的：不能出现谁都不发提醒的真空。
func TestMergeSettlementLowBalance_RequestPathStaysInChargeWithoutAnObserver(t *testing.T) {
	resetSettlementProcessState(t)
	deps, _, push := newMergeBillingDeps(t, true)
	require.False(t, UsageBalanceSettlementNotifierActive())

	notifyBalanceLow(newMergeBillingParams(10.05, 0.1), deps, deferredResult())

	require.Eventually(t, func() bool { return len(push.snapshot()) == 1 }, 2*time.Second, 5*time.Millisecond)
}

func TestBalanceLowOwnedBySettlementWorker(t *testing.T) {
	resetSettlementProcessState(t)
	deferred := deferredResult()
	finalizing := &UsageBillingApplyResult{Applied: true, BalanceFinalizationPending: true}
	balance := 5.0
	synchronous := &UsageBillingApplyResult{Applied: true, NewBalance: &balance}

	require.False(t, balanceLowOwnedBySettlementWorker(deferred), "no observer running")
	usageBalanceSettlementNotifierActive.Store(true)
	require.True(t, balanceLowOwnedBySettlementWorker(deferred))
	require.True(t, balanceLowOwnedBySettlementWorker(finalizing))
	require.False(t, balanceLowOwnedBySettlementWorker(synchronous))
	require.False(t, balanceLowOwnedBySettlementWorker(&UsageBillingApplyResult{Applied: true}))
	require.False(t, balanceLowOwnedBySettlementWorker(nil))
}

func TestGatewayBillingDepsPropagateMergeSettlementConfig(t *testing.T) {
	enabledCfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}
	disabledCfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: false}}

	require.True(t, (&GatewayService{cfg: enabledCfg}).billingDeps().mergeSettlementEnabled())
	require.False(t, (&GatewayService{cfg: disabledCfg}).billingDeps().mergeSettlementEnabled())
	require.False(t, (&GatewayService{}).billingDeps().mergeSettlementEnabled())

	require.True(t, (&OpenAIGatewayService{cfg: enabledCfg}).billingDeps().mergeSettlementEnabled())
	require.False(t, (&OpenAIGatewayService{cfg: disabledCfg}).billingDeps().mergeSettlementEnabled())
	require.False(t, (&OpenAIGatewayService{}).billingDeps().mergeSettlementEnabled())
}

type deferredMediaJobRepoStub struct {
	MediaBillingJobRepository
	result    *UsageBillingApplyResult
	completed bool
}

func (s *deferredMediaJobRepoStub) ApplyMediaBillingJob(context.Context, string) (*UsageBillingApplyResult, error) {
	return s.result, nil
}

func (s *deferredMediaJobRepoStub) CompleteMediaBillingJob(context.Context, string) error {
	s.completed = true
	return nil
}

type deferredUsageBillingRepoStub struct {
	UsageBillingRepository
	result *UsageBillingApplyResult
}

func (s *deferredUsageBillingRepoStub) Apply(context.Context, *UsageBillingCommand) (*UsageBillingApplyResult, error) {
	return s.result, nil
}

func TestMediaBillingSettlement_DeferredUsesRelativeCacheDeduction(t *testing.T) {
	newJob := func() *MediaBillingJob {
		return &MediaBillingJob{
			ID:      "media_img_1",
			Kind:    MediaBillingKindImage,
			UserID:  42,
			GroupID: 9,
			Intent: &MediaBillingIntent{
				Command: UsageBillingCommand{
					RequestID:   "req-img-1",
					APIKeyID:    7,
					UserID:      42,
					AccountID:   11,
					BalanceCost: 1.5,
				},
				Usage: UsageLog{
					RequestID:  "req-img-1",
					APIKeyID:   7,
					UserID:     42,
					AccountID:  11,
					ActualCost: 1.5,
					TotalCost:  1.5,
				},
			},
		}
	}

	t.Run("deferred settlement deducts cache relatively instead of invalidating", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		jobRepo := &deferredMediaJobRepoStub{result: deferredResult()}
		svc := &OpenAIGatewayService{
			cfg:                 cfg,
			mediaBillingJobs:    jobRepo,
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.settleMediaBillingJob(context.Background(), newJob(), nil))
		require.True(t, jobRepo.completed)
		require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
		require.Zero(t, cache.invalidateCalls.Load(), "deferred media billing must not invalidate and resurrect unsettled balance")
	})

	t.Run("merge enabled backlog fallback also deducts relatively instead of invalidating", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true, MinimumBalanceReserve: 0.01}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		newBalance := 98.5
		jobRepo := &deferredMediaJobRepoStub{result: &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}}
		svc := &OpenAIGatewayService{
			cfg:                 cfg,
			mediaBillingJobs:    jobRepo,
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.settleMediaBillingJob(context.Background(), newJob(), nil))
		require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
		require.Zero(t, cache.invalidateCalls.Load())
	})

	t.Run("merge enabled duplicate retry does not double-deduct or invalidate", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		jobRepo := &deferredMediaJobRepoStub{result: &UsageBillingApplyResult{Applied: false}}
		svc := &OpenAIGatewayService{
			cfg:                 cfg,
			mediaBillingJobs:    jobRepo,
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.settleMediaBillingJob(context.Background(), newJob(), nil))
		require.Never(t, func() bool { return cache.deductCalls.Load() > 0 || cache.invalidateCalls.Load() > 0 }, 100*time.Millisecond, 10*time.Millisecond)
	})

	t.Run("flag off keeps legacy invalidation", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: false, MinimumBalanceReserve: 0.01}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		newBalance := 98.5
		jobRepo := &deferredMediaJobRepoStub{result: &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}}
		svc := &OpenAIGatewayService{
			cfg:                 cfg,
			mediaBillingJobs:    jobRepo,
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.settleMediaBillingJob(context.Background(), newJob(), nil))
		require.Equal(t, int64(1), cache.invalidateCalls.Load())
		require.Zero(t, cache.deductCalls.Load())
	})
}

func TestGeminiCachedContentCharge_DeferredUsesRelativeCacheDeduction(t *testing.T) {
	newIntent := func() *MediaBillingIntent {
		reqID := GeminiCachedContentUsageRequestIDPrefix + "cache-1"
		return &MediaBillingIntent{
			Command: UsageBillingCommand{
				RequestID:   reqID,
				APIKeyID:    7,
				UserID:      42,
				AccountID:   11,
				BalanceCost: 0.75,
			},
			Usage: UsageLog{
				RequestID:  reqID,
				APIKeyID:   7,
				UserID:     42,
				AccountID:  11,
				ActualCost: 0.75,
				TotalCost:  0.75,
			},
		}
	}

	t.Run("deferred charge deducts cache relatively instead of invalidating", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		svc := &GatewayService{
			cfg:                 cfg,
			usageBillingRepo:    &deferredUsageBillingRepoStub{result: deferredResult()},
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.ApplyCachedContentCharge(context.Background(), newIntent()))
		require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
		require.Zero(t, cache.invalidateCalls.Load(), "deferred cached content charge must not invalidate and resurrect unsettled balance")
	})

	t.Run("merge enabled backlog fallback deducts relatively instead of invalidating", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: true, MinimumBalanceReserve: 0.01}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		newBalance := 99.25
		svc := &GatewayService{
			cfg:                 cfg,
			usageBillingRepo:    &deferredUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}},
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.ApplyCachedContentCharge(context.Background(), newIntent()))
		require.Eventually(t, func() bool { return cache.deductCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
		require.Zero(t, cache.invalidateCalls.Load())
	})

	t.Run("flag off keeps legacy invalidation", func(t *testing.T) {
		cache := &balanceEligibilityCacheStub{balance: 100}
		cfg := &config.Config{Billing: config.BillingConfig{BalanceMergeSettlementEnabled: false}}
		cacheSvc := NewBillingCacheService(cache, nil, nil, nil, nil, nil, cfg, nil)
		t.Cleanup(cacheSvc.Stop)
		newBalance := 99.25
		svc := &GatewayService{
			cfg:                 cfg,
			usageBillingRepo:    &deferredUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true, NewBalance: &newBalance}},
			billingCacheService: cacheSvc,
			usageLogRepo:        &mediaUsageMemoryRepo{},
		}

		require.NoError(t, svc.ApplyCachedContentCharge(context.Background(), newIntent()))
		require.Equal(t, int64(1), cache.invalidateCalls.Load())
		require.Zero(t, cache.deductCalls.Load())
	})
}
