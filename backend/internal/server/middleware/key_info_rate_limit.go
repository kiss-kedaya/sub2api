package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// KeyInfoRateLimitPerMinute is how many times one API key may call EACH key self-service endpoint
// (/v1/sub2api/key, /groups, /usage) per minute; every endpoint has its own counter, so a status bar that polls all
// three every few seconds stays far below it, while a loop that polls as fast as it can does not get to turn a
// read-only endpoint into database load.
const KeyInfoRateLimitPerMinute = 30

const keyInfoRateLimitWindow = time.Minute

// KeyInfoRateLimit limits one key self-service endpoint per API key (never per IP: behind the reverse proxy
// every client shares the same source address). endpoint names the counter ("key", "groups", "usage"), so the
// endpoints do not draw from one another's budget. It must run after API key authentication.
//
// Redis is optional and failures are tolerated: this is a protection for a read-only endpoint, and it must not
// be able to take the endpoint down. Without a Redis client (single node, tests) it lets everything through.
func KeyInfoRateLimit(redisClient *redis.Client, endpoint string) gin.HandlerFunc {
	if redisClient == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return keyInfoRateLimit(middleware.NewRateLimiter(redisClient), KeyInfoRateLimitPerMinute, endpoint)
}

func keyInfoRateLimit(limiter panelRateLimitAllower, limit int, endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.ID <= 0 || limiter == nil || limit <= 0 {
			c.Next()
			return
		}
		result, err := limiter.Allow(c.Request.Context(), "key_info:"+endpoint+":"+strconv.FormatInt(apiKey.ID, 10), limit, keyInfoRateLimitWindow)
		if err != nil {
			slog.Warn("key info rate limit check failed, allowing request", "error", err)
			c.Next()
			return
		}
		if result.Allowed {
			c.Next()
			return
		}
		retryAfter := result.RetryAfter
		if retryAfter <= 0 {
			retryAfter = keyInfoRateLimitWindow
		}
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second > 0 {
			seconds++
		}
		// The caller sent a valid request faster than allowed: that is a client-side limit, not a gateway fault,
		// so it must not count toward the error rates operators alert on.
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonKeyInfoRateLimit)
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
		c.Header("Cache-Control", "no-store")
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "rate_limit_error",
				"code":    "key_info_rate_limited",
				"message": "Too many requests to the key information endpoints, please retry after " + strconv.FormatInt(seconds, 10) + " seconds",
			},
		})
	}
}

// isKeySelfServicePath reports whether path is one of the endpoints an API key uses to read information about
// itself. They only need the key to be recognised: a key that is expired or out of quota must still be able to
// say so, no billing check applies, and reading them must not count as "the key was used" (a status poll would
// otherwise keep refreshing last_used_at and hide real activity).
func isKeySelfServicePath(path string) bool {
	switch path {
	case "/v1/sub2api/billing", "/v1/sub2api/key", "/v1/sub2api/groups", "/v1/sub2api/usage":
		return true
	}
	return false
}
