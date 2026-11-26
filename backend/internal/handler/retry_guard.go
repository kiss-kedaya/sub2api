package handler

import (
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"go.uber.org/zap"
)

// 单个客户端请求的重试硬上限（gateway.retry_guard.*）。
//
// 背景：故障转移循环原本只被「账号切换次数」约束，而填充调度把切换次数下限抬到了 1024，
// 同账号重试之间又是固定间隔、没有总耗时上限。上游整体故障时，一个客户端请求可以把组内所有账号
// 逐个打一遍，客户端自己再重试，放大成风暴。这里给每个请求加三道闸：
//
//  1. 总尝试次数上限（同账号重试与换号都算一次）；
//  2. 总耗时上限（进入循环起算，超过后不再发起新的尝试）；
//  3. 同账号重试之间带抖动的指数退避，并且睡眠可被 ctx 取消打断。

const (
	defaultRetryGuardMaxAttempts          = 10
	defaultRetryGuardMaxElapsed           = 60 * time.Second
	defaultRetryGuardBackoffBase          = 100 * time.Millisecond
	defaultRetryGuardBackoffMax           = time.Second
	defaultRetryGuardRetryAfterMinSeconds = 5
	defaultRetryGuardRetryAfterMaxSeconds = 30

	failoverBudgetReasonAttempts = "max_attempts"
	failoverBudgetReasonElapsed  = "max_elapsed"
)

type retryGuardSettings struct {
	maxAttempts          int
	maxElapsed           time.Duration
	backoffBase          time.Duration
	backoffMax           time.Duration
	retryAfterMinSeconds int
	retryAfterMaxSeconds int
}

func defaultRetryGuardSettings() retryGuardSettings {
	return retryGuardSettings{
		maxAttempts:          defaultRetryGuardMaxAttempts,
		maxElapsed:           defaultRetryGuardMaxElapsed,
		backoffBase:          defaultRetryGuardBackoffBase,
		backoffMax:           defaultRetryGuardBackoffMax,
		retryAfterMinSeconds: defaultRetryGuardRetryAfterMinSeconds,
		retryAfterMaxSeconds: defaultRetryGuardRetryAfterMaxSeconds,
	}
}

// activeRetryGuard 是进程级设置：配置本身就是进程级的，在 handler 构造时设置一次。
// 没有设置过（例如单元测试）时使用默认值。
var activeRetryGuard atomic.Pointer[retryGuardSettings]

func currentRetryGuard() retryGuardSettings {
	if s := activeRetryGuard.Load(); s != nil {
		return *s
	}
	return defaultRetryGuardSettings()
}

// ConfigureRetryGuard 从配置读取重试硬上限。非法值（<=0 或区间颠倒）回落到默认值，
// 不提供「关闭」：硬上限的意义就是不能被一个写错的配置绕开。
func ConfigureRetryGuard(cfg *config.Config) {
	s := defaultRetryGuardSettings()
	if cfg != nil {
		c := cfg.Gateway.RetryGuard
		if c.MaxAttempts > 0 {
			s.maxAttempts = c.MaxAttempts
		}
		if c.MaxElapsedSeconds > 0 {
			s.maxElapsed = time.Duration(c.MaxElapsedSeconds) * time.Second
		}
		if c.BackoffBaseMs > 0 {
			s.backoffBase = time.Duration(c.BackoffBaseMs) * time.Millisecond
		}
		if c.BackoffMaxMs > 0 {
			s.backoffMax = time.Duration(c.BackoffMaxMs) * time.Millisecond
		}
		if c.RetryAfterMinSeconds > 0 {
			s.retryAfterMinSeconds = c.RetryAfterMinSeconds
		}
		if c.RetryAfterMaxSeconds > 0 {
			s.retryAfterMaxSeconds = c.RetryAfterMaxSeconds
		}
	}
	if s.backoffMax < s.backoffBase {
		s.backoffMax = s.backoffBase
	}
	if s.retryAfterMaxSeconds < s.retryAfterMinSeconds {
		s.retryAfterMaxSeconds = s.retryAfterMinSeconds
	}
	activeRetryGuard.Store(&s)
}

// failoverBudget 是单个客户端请求的重试预算：总尝试次数 + 总耗时。
// 非并发使用（每个请求一份，在请求自己的 goroutine 里推进）。
type failoverBudget struct {
	settings retryGuardSettings
	started  time.Time
	attempts int
}

func newFailoverBudget() failoverBudget {
	return failoverBudget{settings: currentRetryGuard(), started: time.Now()}
}

// spend 记录「又有一次上游尝试失败了」，并返回此后是否还允许发起新的尝试：
// 空字符串表示允许；非空是耗尽原因。第 N 次失败且 N 已达上限时耗尽，所以一个请求最多发起 maxAttempts 次尝试。
func (b *failoverBudget) spend(now time.Time) string {
	b.ensureInitialized(now)
	b.attempts++
	if b.attempts >= b.settings.maxAttempts {
		return failoverBudgetReasonAttempts
	}
	if now.Sub(b.started) >= b.settings.maxElapsed {
		return failoverBudgetReasonElapsed
	}
	return ""
}

// exhaustedReason 不消耗尝试，只检查耗时（选号失败后的退避循环用）。
func (b *failoverBudget) exhaustedReason(now time.Time) string {
	b.ensureInitialized(now)
	if b.attempts >= b.settings.maxAttempts {
		return failoverBudgetReasonAttempts
	}
	if now.Sub(b.started) >= b.settings.maxElapsed {
		return failoverBudgetReasonElapsed
	}
	return ""
}

func (b *failoverBudget) elapsed(now time.Time) time.Duration { return now.Sub(b.started) }

// ensureInitialized 兜底零值：不经 newFailoverBudget 构造的 FailoverState（结构体字面量）也要按默认上限工作，
// 而不是因为 maxAttempts==0 在第一次失败就判定「预算用尽」。
func (b *failoverBudget) ensureInitialized(now time.Time) {
	if b.settings.maxAttempts <= 0 {
		b.settings = currentRetryGuard()
	}
	if b.started.IsZero() {
		b.started = now
	}
}

// poolRetryBackoff 是同账号重试（池模式等）的默认退避：base、2*base、4*base…，上限 backoffMax。
func poolRetryBackoff(retryCount int) time.Duration {
	s := currentRetryGuard()
	delay := s.backoffBase
	for i := 1; i < retryCount; i++ {
		if delay >= s.backoffMax/2 {
			return s.backoffMax
		}
		delay *= 2
	}
	if delay > s.backoffMax {
		return s.backoffMax
	}
	return delay
}

// jitterDelay 在 [0.8d, 1.2d] 内取随机值并以 limit 封顶（limit<=0 不封顶）。
// 抖动让同一时刻失败的一批请求不会在同一毫秒一起重试。
func jitterDelay(d, limit time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	out := time.Duration(float64(d) * (0.8 + 0.4*rand.Float64())) //nolint:gosec // G404: 退避抖动不需要密码学随机
	if limit > 0 && out > limit {
		return limit
	}
	return out
}

// jitterDelayUp 只向上抖动 [d, 1.2d]：用于上游/协议明确要求的最小重试间隔，不能比它更早重试。
func jitterDelayUp(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(float64(d) * (1 + 0.2*rand.Float64())) //nolint:gosec // G404: 退避抖动不需要密码学随机
}

// logFailoverBudgetExhausted 记录一次「重试预算用尽」：请求不再发起新的上游尝试，把最后一次上游错误返回给客户端。
func logFailoverBudgetExhausted(log *zap.Logger, reason string, accountID int64, upstreamStatus int, b *failoverBudget) {
	if log == nil {
		return
	}
	log.Warn("gateway.failover_budget_exhausted",
		zap.String("reason", reason),
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", upstreamStatus),
		zap.Int("attempts", b.attempts),
		zap.Int("max_attempts", b.settings.maxAttempts),
		zap.Duration("elapsed", b.elapsed(time.Now())),
		zap.Duration("max_elapsed", b.settings.maxElapsed),
	)
}
