package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	intmw "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	// TwoFactorRateLimitMaxAttempts 是单账号在滚动窗口内允许的 2FA 校验次数。
	TwoFactorRateLimitMaxAttempts = 5
	// TwoFactorRateLimitWindow 是账号维度 2FA 校验的滚动窗口。
	TwoFactorRateLimitWindow = 60 * time.Second
)

// twoFactorSlidingLimiter 抽象滑动窗口限流原语（由 internal/middleware.RateLimiter 实现），
// 便于单测注入。复用既有 Redis 限流实现与前缀，不另造一套计数器。
type twoFactorSlidingLimiter interface {
	AllowSliding(ctx context.Context, key string, limit int, window time.Duration) (intmw.AllowResult, error)
	Delete(ctx context.Context, key string) error
}

// TwoFactorRateLimiter 是「按账号」维度的 2FA 校验限速器：
// 同一账号 60 秒滚动窗口内最多 5 次校验尝试（成功验证后可重置）。
//
// 维度是 user id（不是 IP）：共享出口/NAT 下的用户互不误伤，也挡住换 IP 的爆破。
// Redis 语义：读取/写入失败时调用方 fail-closed（拒绝本次 2FA 校验），
// 但登录流程的其它步骤（密码校验、发码、passkey 等）不受影响。
type TwoFactorRateLimiter struct {
	limiter twoFactorSlidingLimiter
	// keyPrefix 为空表示 2FA 校验的账号桶（twoFactorRateLimitKeyPrefix）；
	// API Key 身份校验的登录密码因子用独立的桶，见 NewAPIKeyPasswordRateLimiter。
	keyPrefix string
}

const (
	twoFactorRateLimitKeyPrefix = "2fa:verify:user:"
	// apiKeyPasswordRateLimitKeyPrefix 是 API Key 身份校验里登录密码因子的账号桶。
	apiKeyPasswordRateLimitKeyPrefix = "api_key_password:verify:user:"
)

// NewTwoFactorRateLimiter 创建账号维度 2FA 限速器；redisClient 为 nil 时返回 nil，
// 调用方需按 fail-closed 语义处理未装配场景之外的情况（装配缺失仅出现在单测/单机）。
func NewTwoFactorRateLimiter(redisClient *redis.Client) *TwoFactorRateLimiter {
	if redisClient == nil {
		return nil
	}
	return &TwoFactorRateLimiter{limiter: intmw.NewRateLimiter(redisClient)}
}

// NewAPIKeyPasswordRateLimiter 创建 API Key 身份校验里登录密码因子的「按用户失败预算」：
// 与 2FA 同一套实现（Redis 滑动窗口，60 秒内最多 5 次，成功清零，按用户 ID 而不是 IP），
// 但计数桶独立。
//
// 不和 2FA 共用一个桶是刻意的：共用的话，（1）密码校验成功会清掉 2FA 的失败计数，持会话又知道
// 密码的人可以靠它给 TOTP 猜码续命；（2）持会话者故意输错密码就能把账号的登录 2FA 锁住一分钟。
// redisClient 为 nil 时返回 nil。
func NewAPIKeyPasswordRateLimiter(redisClient *redis.Client) *TwoFactorRateLimiter {
	limiter := NewTwoFactorRateLimiter(redisClient)
	if limiter != nil {
		limiter.keyPrefix = apiKeyPasswordRateLimitKeyPrefix
	}
	return limiter
}

// 同一个实现也满足 API Key 密码因子的失败预算端口（见 service.APIKeyPasswordAttemptLimiter）。
var _ service.APIKeyPasswordAttemptLimiter = (*TwoFactorRateLimiter)(nil)

// TwoFactorRateLimitKey 返回 2FA 校验的账号维度限流 key。
func TwoFactorRateLimitKey(userID int64) string {
	return twoFactorRateLimitKeyPrefix + strconv.FormatInt(userID, 10)
}

// key 返回本限速器自己的账号桶 key。
func (l *TwoFactorRateLimiter) key(userID int64) string {
	prefix := l.keyPrefix
	if prefix == "" {
		prefix = twoFactorRateLimitKeyPrefix
	}
	return prefix + strconv.FormatInt(userID, 10)
}

// Check 记录一次 2FA 校验尝试并返回是否放行。
// 允许：allowed=true；超限：allowed=false 且 retryAfter 为窗口内最早尝试离开窗口的剩余时间；
// 存储故障：返回 error，调用方应 fail-closed。
func (l *TwoFactorRateLimiter) Check(ctx context.Context, userID int64) (allowed bool, retryAfter time.Duration, err error) {
	if l == nil || l.limiter == nil || userID <= 0 {
		return false, 0, errors.New("account two-factor verification budget unavailable")
	}
	result, err := l.limiter.AllowSliding(ctx, l.key(userID), TwoFactorRateLimitMaxAttempts, TwoFactorRateLimitWindow)
	if err != nil {
		return false, 0, err
	}
	return result.Allowed, result.RetryAfter, nil
}

// Reset 清空账号的 2FA 校验计数（成功验证后调用，成功即清）。
func (l *TwoFactorRateLimiter) Reset(ctx context.Context, userID int64) {
	if l == nil || l.limiter == nil || userID <= 0 {
		return
	}
	_ = l.limiter.Delete(ctx, l.key(userID))
}

// AbortTwoFactorRateLimited 写出 429 + Retry-After；响应只说明稍后重试，
// 不暴露窗口内剩余次数、账号是否存在等信息。
func AbortTwoFactorRateLimited(c *gin.Context, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = TwoFactorRateLimitWindow
	}
	seconds := int64(retryAfter / time.Second)
	if retryAfter%time.Second > 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	c.Header("Cache-Control", "no-store")
	response.ErrorWithDetails(c, http.StatusTooManyRequests,
		"Too many two-factor verification attempts, please try again later",
		"TWO_FACTOR_RATE_LIMITED", nil)
	c.Abort()
}

// AbortTwoFactorRateLimitUnavailable 在限流存储不可用时 fail-closed 拒绝本次 2FA 校验。
func AbortTwoFactorRateLimitUnavailable(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	response.ErrorWithDetails(c, http.StatusServiceUnavailable,
		"Two-factor verification is temporarily unavailable, please try again later",
		"TWO_FACTOR_RATE_LIMIT_UNAVAILABLE", nil)
	c.Abort()
}
