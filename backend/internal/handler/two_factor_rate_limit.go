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

// checkTwoFactorRateLimit reserves the HTTP account budget. The returned context
// is only for the current TOTP verification; it never clears another policy's counter.
func checkTwoFactorRateLimit(c *gin.Context, limiter twoFactorRateLimiter, userID int64) (context.Context, bool) {
	ctx := c.Request.Context()
	if limiter == nil {
		return ctx, true
	}
	attemptCtx, allowed, retryAfter, err := service.ReserveAccountTOTPAttempt(ctx, userID, limiter.Check)
	if err != nil {
		middleware2.AbortTwoFactorRateLimitUnavailable(c)
		return nil, false
	}
	if !allowed {
		middleware2.AbortTwoFactorRateLimited(c, retryAfter)
		return nil, false
	}
	return attemptCtx, true
}

// resetTwoFactorRateLimit 在 2FA 校验成功后清空账号计数（成功即清）。
func resetTwoFactorRateLimit(ctx context.Context, limiter twoFactorRateLimiter, userID int64) {
	if limiter == nil {
		return
	}
	limiter.Reset(ctx, userID)
}
