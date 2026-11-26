package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// registerKeySelfServiceRoutes mounts what an API key may ask about itself: the key (status, expiry, quota,
// limits, bound groups), the groups it can use with their state and the multiplier it pays, its own usage, and the
// account behind it (wallet balance, subscription allowance, today's usage of all the owner's keys).
//
// It is called on the /v1 group right after authentication and BEFORE the group/model gates are mounted, on
// purpose and like /v1/sub2api/billing: these requests carry no model, so the model allowlist and the composite
// rewrite have nothing to decide, and requireGroupAnthropic must not refuse a key just because its group is not
// an Anthropic one. Authentication still applies in full (key, owner, group state, IP rules); only the billing
// checks are skipped, so an expired or exhausted key can still say so. The path list that tells authentication to
// skip them is middleware.isKeySelfServicePath: a new endpoint here must be added there too.
//
// Each endpoint has its own per-key counter (see middleware.KeyInfoRateLimit).
func registerKeySelfServiceRoutes(gateway *gin.RouterGroup, h *handler.Handlers, redisClient *redis.Client) {
	gateway.GET("/sub2api/key", middleware.KeyInfoRateLimit(redisClient, "key"), h.Gateway.KeyInfo)
	gateway.GET("/sub2api/groups", middleware.KeyInfoRateLimit(redisClient, "groups"), h.Gateway.KeyGroups)
	gateway.GET("/sub2api/usage", middleware.KeyInfoRateLimit(redisClient, "usage"), h.Gateway.KeyUsage)
	gateway.GET("/sub2api/account", middleware.KeyInfoRateLimit(redisClient, "account"), h.Gateway.KeyAccount)
}
