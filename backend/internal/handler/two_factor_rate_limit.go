package handler

import (
	"context"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// twoFactorRateLimiter 抽象「账号维度 2FA 校验限速」能力
// （由 middleware.TwoFactorRateLimiter 实现，Redis 滑动窗口，60s 内最多 5 次）。
type twoFactorRateLimiter interface {
	Check(ctx context.Context, userID int64) (allowed bool, retryAfter time.Duration, err error)
	Reset(ctx context.Context, userID int64)
}

// checkTwoFactorRateLimit 在 2FA 校验前执行账号维度限速。
//
//   - 超限：写出 429 + Retry-After（不含剩余次数等细节），返回 false；
//   - 限流存储故障：fail-closed 写出 503，只影响本次 2FA 校验，不影响登录其它步骤；
//   - 未装配限速器（仅单测/单机）放行。
//
// 放行时同步清空服务层旧的 TOTP 失败预算，避免「60 秒窗口恢复后仍被 15 分钟计数锁死」；
// 未接入本限速器的深层接口（crypto、后台审计读取等）仍由服务层预算保护。
func checkTwoFactorRateLimit(
	c *gin.Context,
	limiter twoFactorRateLimiter,
	totpService *service.TotpService,
	userID int64,
) bool {
	if limiter == nil {
		return true
	}
	allowed, retryAfter, err := limiter.Check(c.Request.Context(), userID)
	if err != nil {
		middleware2.AbortTwoFactorRateLimitUnavailable(c)
		return false
	}
	if !allowed {
		middleware2.AbortTwoFactorRateLimited(c, retryAfter)
		return false
	}
	if totpService != nil {
		_ = totpService.ResetAccountVerificationBudget(c.Request.Context(), userID)
	}
	return true
}

// resetTwoFactorRateLimit 在 2FA 校验成功后清空账号计数（成功即清）。
func resetTwoFactorRateLimit(ctx context.Context, limiter twoFactorRateLimiter, userID int64) {
	if limiter == nil {
		return
	}
	limiter.Reset(ctx, userID)
}
