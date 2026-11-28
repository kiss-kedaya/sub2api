package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RateLimitFailureMode Redis 故障策略
type RateLimitFailureMode int

const (
	RateLimitFailOpen RateLimitFailureMode = iota
	RateLimitFailClose
)

// RateLimitOptions 限流可选配置
type RateLimitOptions struct {
	FailureMode RateLimitFailureMode
}

var rateLimitScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
local repaired = 0
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
elseif ttl == -1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  repaired = 1
end
return {current, repaired}
`)

// rateLimitSlidingScript 是滑动窗口计数：ZSET 成员为本次尝试的时间戳（纳秒），
// 每次调用先清理窗口外的旧成员，再判断窗口内计数是否达到上限。
// 返回 {窗口内计数, 超限时最早成员离开窗口还需的毫秒数（未超限为 0）}。
var rateLimitSlidingScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local key = KEYS[1]
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local count = redis.call('ZCARD', key)
if count < limit then
  redis.call('ZADD', key, now, ARGV[4])
  redis.call('PEXPIRE', key, window)
  return {count + 1, 0, 1}
end
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
local retry = 0
if oldest[2] then
  retry = tonumber(oldest[2]) + window - now
  if retry < 0 then retry = 0 end
end
return {count, retry, 0}
`)

// rateLimitSlidingRun 允许测试覆写滑动窗口脚本执行逻辑。
// 返回 (窗口内计数, 超限重试毫秒, 是否放行)。
var rateLimitSlidingRun = func(ctx context.Context, client *redis.Client, key string, windowMillis int64, limit int64, member string) (int64, int64, bool, error) {
	values, err := rateLimitSlidingScript.Run(ctx, client, []string{key}, time.Now().UnixNano()/int64(time.Millisecond), windowMillis, limit, member).Slice()
	if err != nil {
		return 0, 0, false, err
	}
	if len(values) < 3 {
		return 0, 0, false, fmt.Errorf("rate limit sliding script returned %d values", len(values))
	}
	count, err := parseInt64(values[0])
	if err != nil {
		return 0, 0, false, err
	}
	retry, err := parseInt64(values[1])
	if err != nil {
		return 0, 0, false, err
	}
	allowed, err := parseInt64(values[2])
	if err != nil {
		return 0, 0, false, err
	}
	return count, retry, allowed == 1, nil
}

// rateLimitRun 允许测试覆写脚本执行逻辑
var rateLimitRun = func(ctx context.Context, client *redis.Client, key string, windowMillis int64) (int64, bool, error) {
	values, err := rateLimitScript.Run(ctx, client, []string{key}, windowMillis).Slice()
	if err != nil {
		return 0, false, err
	}
	if len(values) < 2 {
		return 0, false, fmt.Errorf("rate limit script returned %d values", len(values))
	}
	count, err := parseInt64(values[0])
	if err != nil {
		return 0, false, err
	}
	repaired, err := parseInt64(values[1])
	if err != nil {
		return 0, false, err
	}
	return count, repaired == 1, nil
}

// RateLimiter Redis 速率限制器
type RateLimiter struct {
	redis  *redis.Client
	prefix string
}

// NewRateLimiter 创建速率限制器实例
func NewRateLimiter(redisClient *redis.Client) *RateLimiter {
	return &RateLimiter{
		redis:  redisClient,
		prefix: "rate_limit:",
	}
}

// AllowResult 单次固定窗口限流判定结果。
type AllowResult struct {
	// Allowed 是否放行
	Allowed bool
	// Count 当前窗口内累计请求数（含本次）
	Count int64
	// RetryAfter 超限时距窗口重置的剩余时间（尽力而为；PTTL 不可用时回退为完整窗口）
	RetryAfter time.Duration
}

// Allow 对给定 key（不含 "rate_limit:" 前缀）执行一次固定窗口计数判定。
// 供需要自定义限流维度（如按用户 ID）的调用方使用；Redis 错误由调用方决定 fail-open/close。
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (AllowResult, error) {
	redisKey := r.prefix + key
	windowMillis := windowTTLMillis(window)

	count, repaired, err := rateLimitRun(ctx, r.redis, redisKey, windowMillis)
	if err != nil {
		return AllowResult{}, err
	}
	if repaired {
		log.Printf("[RateLimit] ttl repaired: key=%s window_ms=%d", redisKey, windowMillis)
	}

	result := AllowResult{Allowed: count <= int64(limit), Count: count}
	if !result.Allowed {
		result.RetryAfter = window
		if ttl, ttlErr := r.redis.PTTL(ctx, redisKey).Result(); ttlErr == nil && ttl > 0 {
			result.RetryAfter = ttl
		}
	}
	return result, nil
}

// clientIPForRateLimit 返回 IP 维度限流使用的客户端地址。
// 与审计日志/会话绑定/API Key IP ACL 共用同一套安全客户端 IP 解析
// （SessionBindingContext 快照：兼容开关开启时信任反代转发头，关闭时走
// server.trusted_proxies 可信链）。避免默认反代部署下 Gin ClientIP 恒等于
// 代理地址、所有用户坍缩进同一个限流桶造成整体误拦截。
func clientIPForRateLimit(c *gin.Context) string {
	if resolved := ippkg.GetSecurityClientIP(c, false); resolved != "" {
		return resolved
	}
	return c.ClientIP()
}

// Limit 返回速率限制中间件
// key: 限制类型标识
// limit: 时间窗口内最大请求数
// window: 时间窗口
func (r *RateLimiter) Limit(key string, limit int, window time.Duration) gin.HandlerFunc {
	return r.LimitWithOptions(key, limit, window, RateLimitOptions{})
}

// LimitWithOptions 返回速率限制中间件（带可选配置）
func (r *RateLimiter) LimitWithOptions(key string, limit int, window time.Duration, opts RateLimitOptions) gin.HandlerFunc {
	failureMode := opts.FailureMode
	if failureMode != RateLimitFailClose {
		failureMode = RateLimitFailOpen
	}

	return func(c *gin.Context) {
		result, err := r.Allow(c.Request.Context(), key+":"+clientIPForRateLimit(c), limit, window)
		if err != nil {
			log.Printf("[RateLimit] redis error: key=%s mode=%s err=%v", r.prefix+key, failureModeLabel(failureMode), err)
			if failureMode == RateLimitFailClose {
				abortRateLimit(c, window)
				return
			}
			// Redis 错误时放行，避免影响正常服务
			c.Next()
			return
		}

		// 超过限制
		if !result.Allowed {
			abortRateLimit(c, result.RetryAfter)
			return
		}

		c.Next()
	}
}

var slidingMemberSeq uint64

// AllowSliding 对给定 key 执行滑动窗口计数判定：任意 window 时长内最多 limit 次。
// 超限时 RetryAfter 为最早一次尝试离开窗口还剩的时间；Redis 错误由调用方决定
// fail-open / fail-close。与固定窗口的 Allow 共用 RateLimiter 实例与前缀。
func (r *RateLimiter) AllowSliding(ctx context.Context, key string, limit int, window time.Duration) (AllowResult, error) {
	if r == nil || r.redis == nil {
		return AllowResult{}, fmt.Errorf("rate limiter not configured")
	}
	if limit <= 0 {
		return AllowResult{Allowed: true}, nil
	}
	redisKey := r.prefix + key
	windowMillis := windowTTLMillis(window)
	seq := atomic.AddUint64(&slidingMemberSeq, 1)
	member := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatUint(seq, 36)

	count, retryMillis, allowed, err := rateLimitSlidingRun(ctx, r.redis, redisKey, windowMillis, int64(limit), member)
	if err != nil {
		return AllowResult{}, err
	}
	result := AllowResult{Allowed: allowed, Count: count}
	if !result.Allowed {
		result.RetryAfter = time.Duration(retryMillis) * time.Millisecond
		if result.RetryAfter <= 0 {
			result.RetryAfter = window
		}
	}
	return result, nil
}

// Delete 清除某个限流 key（如 2FA 验证成功后重置账号维度计数）。
func (r *RateLimiter) Delete(ctx context.Context, key string) error {
	if r == nil || r.redis == nil {
		return fmt.Errorf("rate limiter not configured")
	}
	return r.redis.Del(ctx, r.prefix+key).Err()
}

func windowTTLMillis(window time.Duration) int64 {
	ttl := window.Milliseconds()
	if ttl < 1 {
		return 1
	}
	return ttl
}

func abortRateLimit(c *gin.Context, retryAfter time.Duration) {
	if retryAfter > 0 {
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second > 0 {
			seconds++
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":   "rate limit exceeded",
		"message": "Too many requests, please try again later",
	})
}

func failureModeLabel(mode RateLimitFailureMode) string {
	if mode == RateLimitFailClose {
		return "fail-close"
	}
	return "fail-open"
}

func parseInt64(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unexpected value type %T", value)
	}
}
