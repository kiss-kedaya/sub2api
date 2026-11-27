package middleware

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// NewJWTAuthMiddleware 创建 JWT 认证中间件（保持既有签名，兼容单测直接构造）
func NewJWTAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) JWTAuthMiddleware {
	return JWTAuthMiddleware(jwtAuthWithPersonalKey(authService, userService, userService, settingService, auditService, nil, nil))
}

// ProvideJWTAuthMiddleware 创建支持 JWT 与用户个人接口密钥（uak_）的认证中间件（wire 生产装配使用）。
func ProvideJWTAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	personalKeyService *service.UserPersonalAPIKeyService,
	redisClient *redis.Client,
) JWTAuthMiddleware {
	return JWTAuthMiddleware(jwtAuthWithPersonalKey(
		authService,
		userService,
		userService,
		settingService,
		auditService,
		personalKeyService,
		newPersonalAPIKeyInvalidGuard(redisClient),
	))
}

type jwtUserReader interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
}

type userActivityToucher interface {
	TouchLastActiveForUser(ctx context.Context, user *service.User)
}

// jwtAuth JWT认证中间件实现（兼容包内既有单测）
func jwtAuth(
	authService *service.AuthService,
	userService jwtUserReader,
	activityToucher userActivityToucher,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) gin.HandlerFunc {
	return jwtAuthWithPersonalKey(authService, userService, activityToucher, settingService, auditService, nil, nil)
}

func jwtAuthWithPersonalKey(
	authService *service.AuthService,
	userService jwtUserReader,
	activityToucher userActivityToucher,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	personalKeyAuth userPersonalAPIKeyAuthenticator,
	personalKeyGuard personalAPIKeyInvalidAttemptGuard,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 优先识别用户个人接口密钥（X-API-Key: uak_... 或 Authorization: Bearer uak_...），
		// 走 SHA-256 哈希鉴权 + IP 无效尝试防爆破，免浏览器会话指纹绑定。
		if rawKey, isPersonal := extractPersonalAPIKeyFromRequest(c); isPersonal {
			authenticatePersonalAPIKey(c, rawKey, personalKeyAuth, personalKeyGuard, userService, activityToucher, auditService)
			return
		}

		// 从Authorization header中提取token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			AbortWithError(c, 401, "UNAUTHORIZED", "Authorization header is required")
			return
		}

		// 验证Bearer scheme
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			AbortWithError(c, 401, "INVALID_AUTH_HEADER", "Authorization header format must be 'Bearer {token}'")
			return
		}

		tokenString := strings.TrimSpace(parts[1])
		if tokenString == "" {
			AbortWithError(c, 401, "EMPTY_TOKEN", "Token cannot be empty")
			return
		}

		// 验证token
		claims, err := authService.ValidateToken(tokenString)
		if err != nil {
			if errors.Is(err, service.ErrTokenExpired) {
				AbortWithError(c, 401, "TOKEN_EXPIRED", "Token has expired")
				return
			}
			AbortWithError(c, 401, "INVALID_TOKEN", "Invalid token")
			return
		}

		// 从数据库获取最新的用户信息
		user, err := userService.GetByID(c.Request.Context(), claims.UserID)
		if err != nil {
			if errors.Is(err, service.ErrUserNotFound) {
				AbortWithError(c, 401, "USER_NOT_FOUND", "User not found")
			} else {
				AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to load user")
			}
			return
		}

		// 检查用户状态
		if !user.IsActive() {
			AbortWithError(c, 401, "USER_INACTIVE", "User account is not active")
			return
		}

		// Security: Validate TokenVersion to ensure token hasn't been invalidated
		// This check ensures tokens issued before a password change are rejected
		if claims.TokenVersion != user.TokenVersion {
			AbortWithError(c, 401, "TOKEN_REVOKED", "Token has been revoked (password changed)")
			return
		}

		// "Revoke all sessions" must also invalidate stateless access tokens.
		if claims.IssuedAt != nil && authService.IsAccessTokenRevoked(c.Request.Context(), claims.UserID, claims.IssuedAt.Time) {
			AbortWithError(c, 401, "TOKEN_REVOKED", "Token has been revoked")
			return
		}

		// 会话绑定校验：IP/UA 任一变化即撤销会话（功能可在系统设置中关闭）
		if !enforceSessionBinding(c, authService, settingService, auditService, claims) {
			return
		}

		c.Set(string(ContextKeyUser), AuthSubject{
			UserID:      user.ID,
			Concurrency: user.Concurrency,
		})
		c.Set(string(ContextKeyUserRole), user.Role)
		c.Set(ContextKeyAuthEmail, user.Email)
		c.Set(ContextKeySessionID, claims.SessionID)
		if activityToucher != nil && !service.CryptoAdminTestMode() {
			activityToucher.TouchLastActiveForUser(c.Request.Context(), user)
		}

		c.Next()
	}
}

// Deprecated: prefer GetAuthSubjectFromContext in auth_subject.go.
