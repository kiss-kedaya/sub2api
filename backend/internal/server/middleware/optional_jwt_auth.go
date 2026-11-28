package middleware

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// NewOptionalJWTAuthMiddleware 创建可选 JWT 认证中间件（保持既有签名，兼容单测直接构造）。
//
// 无 Authorization / X-API-Key header 时直接放行（匿名，context 中不设置 AuthSubject）；
// 带 header 则委托严格校验（JWT 或个人接口密钥），失败返回 401——
// 前端 API client 对 401 会自动走 refresh-token 重试，因此不做静默降级。
func NewOptionalJWTAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) OptionalJWTAuthMiddleware {
	strict := jwtAuth(authService, userService, userService, settingService, auditService)
	return optionalJWTAuthFromStrict(strict)
}

// ProvideOptionalJWTAuthMiddleware 创建同时支持 JWT 与用户个人接口密钥（uak_）的可选认证中间件（wire 生产装配使用）。
func ProvideOptionalJWTAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	personalKeyService *service.UserPersonalAPIKeyService,
	redisClient *redis.Client,
) OptionalJWTAuthMiddleware {
	strict := jwtAuthWithPersonalKey(
		authService,
		userService,
		userService,
		settingService,
		auditService,
		personalKeyService,
		newPersonalAPIKeyInvalidGuard(redisClient),
	)
	return optionalJWTAuthFromStrict(strict)
}

func optionalJWTAuthFromStrict(strict gin.HandlerFunc) OptionalJWTAuthMiddleware {
	return OptionalJWTAuthMiddleware(func(c *gin.Context) {
		if strings.TrimSpace(c.GetHeader("Authorization")) == "" && strings.TrimSpace(c.GetHeader("X-API-Key")) == "" {
			c.Next()
			return
		}
		strict(c)
	})
}
