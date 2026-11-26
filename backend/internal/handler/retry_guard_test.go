package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// fastRetryGuard 把退避压到毫秒级，让「很多次同账号重试」的用例不用真的睡几十秒；用完恢复默认值。
func fastRetryGuard(t *testing.T, mutate func(*config.GatewayRetryGuardConfig)) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{BackoffBaseMs: 1, BackoffMaxMs: 2}
	if mutate != nil {
		mutate(&cfg.Gateway.RetryGuard)
	}
	ConfigureRetryGuard(cfg)
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
}

// circuitGateScheduler 同时实现 TempUnscheduler 和 accountCircuitGate。
type circuitGateScheduler struct {
	mockTempUnscheduler
	open map[int64]bool
}

func (g *circuitGateScheduler) AccountCircuitOpen(accountID int64) bool { return g.open[accountID] }

// ---- 配置 --------------------------------------------------------------------

func TestConfigureRetryGuardDefaultsAndOverrides(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })

	ConfigureRetryGuard(nil)
	d := currentRetryGuard()
	require.Equal(t, 10, d.maxAttempts)
	require.Equal(t, 60*time.Second, d.maxElapsed)
	require.Equal(t, 100*time.Millisecond, d.backoffBase)
	require.Equal(t, time.Second, d.backoffMax)
	require.Equal(t, 5, d.retryAfterMinSeconds)
	require.Equal(t, 30, d.retryAfterMaxSeconds)

	cfg := &config.Config{}
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{
		MaxAttempts: 4, MaxElapsedSeconds: 20, BackoffBaseMs: 50, BackoffMaxMs: 400,
		RetryAfterMinSeconds: 2, RetryAfterMaxSeconds: 9,
	}
	ConfigureRetryGuard(cfg)
	s := currentRetryGuard()
	require.Equal(t, 4, s.maxAttempts)
	require.Equal(t, 20*time.Second, s.maxElapsed)
	require.Equal(t, 50*time.Millisecond, s.backoffBase)
	require.Equal(t, 400*time.Millisecond, s.backoffMax)
	require.Equal(t, 2, s.retryAfterMinSeconds)
	require.Equal(t, 9, s.retryAfterMaxSeconds)

	// 非法值回落默认，而不是把硬上限关掉
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{MaxAttempts: -1, MaxElapsedSeconds: -5, BackoffBaseMs: -1, BackoffMaxMs: -1}
	ConfigureRetryGuard(cfg)
	require.Equal(t, defaultRetryGuardSettings(), currentRetryGuard())

	// 区间颠倒时修正，不 panic
	cfg.Gateway.RetryGuard = config.GatewayRetryGuardConfig{BackoffBaseMs: 500, BackoffMaxMs: 100, RetryAfterMinSeconds: 20, RetryAfterMaxSeconds: 5}
	ConfigureRetryGuard(cfg)
	s = currentRetryGuard()
	require.GreaterOrEqual(t, s.backoffMax, s.backoffBase)
	require.GreaterOrEqual(t, s.retryAfterMaxSeconds, s.retryAfterMinSeconds)
}

// ---- 预算 --------------------------------------------------------------------

func TestFailoverBudgetAttemptCap(t *testing.T) {
	b := newFailoverBudget()
	for i := 1; i < 10; i++ {
		require.Empty(t, b.spend(time.Now()), "attempt %d may still be followed by another one", i)
	}
	require.Equal(t, failoverBudgetReasonAttempts, b.spend(time.Now()), "the 10th failed attempt ends the request: at most 10 upstream attempts")
	require.Equal(t, 10, b.attempts)
}

func TestFailoverBudgetElapsedCap(t *testing.T) {
	b := newFailoverBudget()
	require.Empty(t, b.spend(b.started.Add(59*time.Second)))
	require.Equal(t, failoverBudgetReasonElapsed, b.spend(b.started.Add(60*time.Second)))
	require.Equal(t, failoverBudgetReasonElapsed, b.exhaustedReason(b.started.Add(61*time.Second)))
	fresh := newFailoverBudget()
	require.Empty(t, fresh.exhaustedReason(time.Now()))
}

func TestFailoverBudgetHonorsConfiguredLimits(t *testing.T) {
	fastRetryGuard(t, func(c *config.GatewayRetryGuardConfig) { c.MaxAttempts = 3; c.MaxElapsedSeconds = 5 })
	b := newFailoverBudget()
	require.Empty(t, b.spend(time.Now()))
	require.Empty(t, b.spend(time.Now()))
	require.Equal(t, failoverBudgetReasonAttempts, b.spend(time.Now()))
}

// ---- FailoverState：总尝试次数硬上限 -------------------------------------------

// driveFailoverLoop 按 handler 的方式驱动 FailoverState：每轮挑第一个没被排除的账号，
// 上游永远失败；返回总共发起了多少次上游尝试。
func driveFailoverLoop(t *testing.T, fs *FailoverState, svc TempUnscheduler, accounts []int64, retryLimit int, failoverErr *service.UpstreamFailoverError) (attempts int, last FailoverAction) {
	t.Helper()
	for guard := 0; guard < 100000; guard++ {
		var id int64
		for _, candidate := range accounts {
			if _, excluded := fs.FailedAccountIDs[candidate]; !excluded {
				id = candidate
				break
			}
		}
		if id == 0 {
			return attempts, FailoverExhausted // 候选耗尽
		}
		attempts++
		last = fs.HandleFailoverError(context.Background(), svc, id, "anthropic", retryLimit, failoverErr)
		if last != FailoverContinue {
			return attempts, last
		}
	}
	t.Fatal("failover loop did not terminate")
	return attempts, last
}

func TestHandleFailoverErrorCapsTotalAttemptsAcrossAccountsAndRetries(t *testing.T) {
	fastRetryGuard(t, nil)
	accounts := make([]int64, 50)
	for i := range accounts {
		accounts[i] = int64(i + 1)
	}
	// 填充调度：切换预算 1024；池模式每个账号重试 3 次。没有硬上限时一个请求会打 50 x 4 = 200 次上游。
	fs := NewFillFailoverState(10, false)
	failoverErr := newTestFailoverErr(502, true, false)

	attempts, last := driveFailoverLoop(t, fs, &mockTempUnscheduler{}, accounts, 3, failoverErr)

	require.Equal(t, 10, attempts, "one client request makes at most max_attempts upstream attempts")
	require.Equal(t, FailoverExhausted, last)
	require.Same(t, failoverErr, fs.LastFailoverErr, "the last upstream error must reach the caller")
}

func TestHandleFailoverErrorCapCountsPoolModeRetriesOnOneAccount(t *testing.T) {
	fastRetryGuard(t, nil)
	// 单账号、池模式重试预算 10：旧行为是 1 + 10 次尝试，现在总共 10 次。
	fs := NewFillFailoverState(10, false)
	attempts, last := driveFailoverLoop(t, fs, &mockTempUnscheduler{}, []int64{7}, 10, newTestFailoverErr(502, true, false))
	require.Equal(t, 10, attempts)
	require.Equal(t, FailoverExhausted, last)
	require.Equal(t, 9, fs.SameAccountRetryCount[7])
}

func TestHandleFailoverErrorCapIsConfigurable(t *testing.T) {
	fastRetryGuard(t, func(c *config.GatewayRetryGuardConfig) { c.MaxAttempts = 4 })
	accounts := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	fs := NewFillFailoverState(10, false)
	attempts, _ := driveFailoverLoop(t, fs, &mockTempUnscheduler{}, accounts, 0, newTestFailoverErr(503, false, false))
	require.Equal(t, 4, attempts)
}

func TestHandleFailoverErrorElapsedBudgetStopsFurtherAttempts(t *testing.T) {
	fastRetryGuard(t, nil)
	fs := NewFillFailoverState(10, false)
	fs.budget.started = time.Now().Add(-61 * time.Second) // 这个请求已经在循环里跑了 61 秒
	failoverErr := newTestFailoverErr(502, true, false)

	action := fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, "anthropic", 3, failoverErr)

	require.Equal(t, FailoverExhausted, action)
	require.Same(t, failoverErr, fs.LastFailoverErr)
	require.Zero(t, fs.SwitchCount, "no further attempt, no switch")
}

func TestHandleFailoverErrorWithinBudgetStillRetriesAndSwitches(t *testing.T) {
	// 对照：预算没用完时行为不变（同账号重试、耗尽后换号）。
	fastRetryGuard(t, nil)
	fs := NewFillFailoverState(10, false)
	failoverErr := newTestFailoverErr(429, true, false)
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, "openai", 2, failoverErr))
	require.Equal(t, 1, fs.SameAccountRetryCount[1])
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, "openai", 2, failoverErr))
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, "openai", 2, failoverErr))
	require.Equal(t, 1, fs.SwitchCount)
	require.Contains(t, fs.FailedAccountIDs, int64(1))
}

func TestHandleSelectionExhaustedRespectsBudget(t *testing.T) {
	// 单账号退避重试分支：预算用尽后不能再清空排除集重来。
	fastRetryGuard(t, nil)
	fs := NewFailoverState(100, false) // 非填充调度才有这条退避分支
	fs.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs.FailedAccountIDs[1] = struct{}{}
	fs.SwitchCount = 1
	fs.budget.attempts = 10
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
	require.Contains(t, fs.FailedAccountIDs, int64(1), "exclusion set must not be cleared once the budget is spent")

	fs2 := NewFailoverState(100, false)
	fs2.LastFailoverErr = newTestFailoverErr(503, false, false)
	fs2.FailedAccountIDs[1] = struct{}{}
	fs2.SwitchCount = 1
	fs2.budget.started = time.Now().Add(-61 * time.Second)
	require.Equal(t, FailoverExhausted, fs2.HandleSelectionExhausted(context.Background()))
}

// ---- 熔断后停止同账号重试 ------------------------------------------------------

func TestHandleFailoverErrorStopsSameAccountRetryWhenCircuitOpens(t *testing.T) {
	fastRetryGuard(t, nil)
	svc := &circuitGateScheduler{open: map[int64]bool{}}
	fs := NewFillFailoverState(10, false)
	failoverErr := newTestFailoverErr(502, true, false)

	// 第一次失败：账号还没被熔断，照常同账号重试。
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), svc, 5, "anthropic", 3, failoverErr))
	require.Equal(t, 1, fs.SameAccountRetryCount[5])
	require.NotContains(t, fs.FailedAccountIDs, int64(5))

	// 此时断路器把账号停掉：在途请求不再重试它，立刻加入排除集并换号（重试预算还剩 2 次也不用）。
	svc.open[5] = true
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), svc, 5, "anthropic", 3, failoverErr))
	require.Equal(t, 1, fs.SameAccountRetryCount[5], "no further same-account retry once the circuit is open")
	require.Contains(t, fs.FailedAccountIDs, int64(5), "the paused account is excluded for the rest of this request")
	require.Equal(t, 1, fs.SwitchCount)
}

func TestHandleFailoverErrorCircuitGateIgnoresServicesWithoutIt(t *testing.T) {
	fastRetryGuard(t, nil)
	fs := NewFillFailoverState(10, false)
	// 没有实现 accountCircuitGate 的服务（测试替身、旧实现）：按「未熔断」处理，行为不变。
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 5, "anthropic", 3, newTestFailoverErr(502, true, false)))
	require.Equal(t, 1, fs.SameAccountRetryCount[5])
	require.False(t, accountCircuitOpen(nil, 5))
	require.False(t, accountCircuitOpen(&mockTempUnscheduler{}, 5))
	require.True(t, accountCircuitOpen(&circuitGateScheduler{open: map[int64]bool{5: true}}, 5))
}

// ---- 退避：带抖动、封顶、可取消 --------------------------------------------------

func TestSameAccountRetryWaitIsJitteredAndCapped(t *testing.T) {
	t.Cleanup(func() { ConfigureRetryGuard(nil) })
	ConfigureRetryGuard(nil) // 100ms 起、上限 1s
	poolErr := &service.UpstreamFailoverError{RetryableOnSameAccount: true}

	for retryCount, base := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 400 * time.Millisecond, 4: 800 * time.Millisecond, 5: time.Second, 9: time.Second} {
		seen := map[time.Duration]struct{}{}
		for i := 0; i < 200; i++ {
			w := sameAccountRetryWait(poolErr, retryCount)
			seen[w] = struct{}{}
			require.GreaterOrEqual(t, w, time.Duration(float64(base)*0.8)-time.Microsecond, "retry %d", retryCount)
			require.LessOrEqual(t, w, time.Second, "retry %d: the cap is hard, jitter must not exceed it", retryCount)
			require.LessOrEqual(t, w, time.Duration(float64(base)*1.2)+time.Microsecond, "retry %d", retryCount)
		}
		require.Greater(t, len(seen), 20, "retry %d: delays must be jittered, not a fixed value", retryCount)
	}
}

func TestSameAccountRetryWaitNeverShortensProtocolDelays(t *testing.T) {
	// 上游/协议给出的最小间隔（OAuth 429 Retry-After 等）只能向上抖动。
	explicit := &service.UpstreamFailoverError{SameAccountRetryDelay: 3 * time.Second}
	transient := &service.UpstreamFailoverError{RequestScopedTransient: true}
	for i := 0; i < 300; i++ {
		w := sameAccountRetryWait(explicit, 1)
		require.GreaterOrEqual(t, w, 3*time.Second)
		require.LessOrEqual(t, w, time.Duration(float64(3*time.Second)*1.2)+time.Millisecond)
		w = sameAccountRetryWait(transient, 3) // 2s 基础值
		require.GreaterOrEqual(t, w, 2*time.Second)
	}
}

func TestPoolRetryBackoffRespectsConfiguredBaseAndCap(t *testing.T) {
	fastRetryGuard(t, func(c *config.GatewayRetryGuardConfig) { c.BackoffBaseMs = 10; c.BackoffMaxMs = 35 })
	require.Equal(t, 10*time.Millisecond, poolRetryBackoff(1))
	require.Equal(t, 20*time.Millisecond, poolRetryBackoff(2))
	require.Equal(t, 35*time.Millisecond, poolRetryBackoff(3), "40ms is capped to the configured 35ms")
	require.Equal(t, 35*time.Millisecond, poolRetryBackoff(8))
}

func TestHandleFailoverErrorBackoffSleepIsCancelable(t *testing.T) {
	// 同账号重试的退避睡眠必须响应 ctx 取消（客户端断开立刻停）。
	ConfigureRetryGuard(nil) // 默认 100ms 起
	ctx, cancel := context.WithCancel(context.Background())
	fs := NewFillFailoverState(10, false)
	failoverErr := newTestFailoverErr(502, true, false)
	failoverErr.SameAccountRetryDelay = 30 * time.Second // 如果睡眠不响应取消，这个测试会挂 30 秒
	time.AfterFunc(30*time.Millisecond, cancel)

	start := time.Now()
	action := fs.HandleFailoverError(ctx, &mockTempUnscheduler{}, 1, "openai", 3, failoverErr)

	require.Equal(t, FailoverCanceled, action)
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestFailoverStateWithoutConstructorStillGetsTheDefaultBudget(t *testing.T) {
	// 结构体字面量构造（没走 NewFailoverState）时预算是零值：必须按默认上限工作，
	// 而不是第一次失败就判定预算用尽。
	fastRetryGuard(t, nil)
	fs := &FailoverState{
		MaxSwitches:           1024,
		FailedAccountIDs:      map[int64]struct{}{},
		SameAccountRetryCount: map[int64]int{},
	}
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 1, "openai", 0, newTestFailoverErr(502, false, false)))

	var b failoverBudget
	for i := 1; i < 10; i++ {
		require.Empty(t, b.spend(time.Now()), "attempt %d", i)
	}
	require.Equal(t, failoverBudgetReasonAttempts, b.spend(time.Now()))
}
