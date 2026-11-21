// Package middleware provides HTTP middleware for authentication, authorization, and request processing.
package middleware

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// scopedAdminKeysDisabled 是紧急回退开关：为 true 时忽略所有 adm_sc_ 受限密钥，
// 旧版单 key 路径完全不受影响。启动时读取环境变量 SCOPED_ADMIN_API_KEYS_DISABLED，
// 修改后重启服务生效。
var scopedAdminKeysDisabled = strings.EqualFold(strings.TrimSpace(os.Getenv("SCOPED_ADMIN_API_KEYS_DISABLED")), "true")

// NewAdminAuthMiddleware 创建管理员认证中间件
func NewAdminAuthMiddleware(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	cfg *config.Config,
) AdminAuthMiddleware {
	// 回退开关等价于「分权失效、旧版全权 key 独占 API key 通道」，启动时必须显式可见。
	if scopedAdminKeysDisabled {
		slog.Warn("SCOPED_ADMIN_API_KEYS_DISABLED=true: all scoped admin API keys are rejected and the legacy full-power admin API key path remains active")
	}
	return AdminAuthMiddleware(adminAuth(authService, userService, settingService, auditService, cfg))
}

// adminAuth 管理员认证中间件实现
// 支持两种认证方式（通过不同的 header 区分）：
// 1. Admin API Key: x-api-key: <admin-api-key>
// 2. JWT Token: Authorization: Bearer <jwt-token> (需要管理员角色)
func adminAuth(
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
	cfg *config.Config,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// WebSocket upgrade requests cannot set Authorization headers in browsers.
		// For admin WebSocket endpoints (e.g. Ops realtime), allow passing the JWT via
		// Sec-WebSocket-Protocol (subprotocol list) using a prefixed token item:
		//   Sec-WebSocket-Protocol: sub2api-admin, jwt.<token>
		if isWebSocketUpgradeRequest(c) {
			if token := extractJWTFromWebSocketSubprotocol(c); token != "" {
				if !validateJWTForAdmin(c, token, authService, userService, settingService, auditService) {
					return
				}
				if !enforceAdminObserverAccess(c) {
					return
				}
				setAdminAccountOwnerScope(c, cfg)
				c.Next()
				return
			}
		}

		// 检查 x-api-key header（Admin API Key 认证）
		apiKey := c.GetHeader("x-api-key")
		if apiKey != "" {
			if strings.HasPrefix(apiKey, "adm_sc_") {
				if scopedAdminKeysDisabled {
					AbortWithError(c, 401, "INVALID_ADMIN_KEY", "Invalid admin API key")
					recordAdminKeyRequest(c, auditService, nil)
					return
				}
				authenticateScopedAdminRequest(c, apiKey, settingService, userService, auditService, cfg)
				return
			}
			if !validateAdminAPIKey(c, apiKey, settingService, userService) {
				return
			}
			// 统一默认拒绝：机器凭据只能调用集中注册表里的 machine_allowlisted
			// 接口（健康检查 / 只读统计聚合 / 无敏感数据的自动化触发）。其余
			// 路由一律 403 STEP_UP_ADMIN_API_KEY_FORBIDDEN 并记安全事件。
			if !enforceAdminMachineRoutePolicy(c, auditService) {
				return
			}
			if !enforceAdminObserverAccess(c) {
				return
			}
			setAdminAccountOwnerScope(c, cfg)
			// 审计中间件只记录变更类请求与白名单敏感读；这里兜底记录其它调用，
			// 保证机器凭据每次调用都有可追溯记录（只含前缀）。
			defer recordAdminMachineAllowedRequest(c, auditService)
			c.Next()
			return
		}

		// 检查 Authorization header（JWT 认证）
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				token := strings.TrimSpace(parts[1])
				if token == "" {
					AbortWithError(c, 401, "UNAUTHORIZED", "Authorization required")
					return
				}
				if !validateJWTForAdmin(c, token, authService, userService, settingService, auditService) {
					return
				}
				if !enforceAdminObserverAccess(c) {
					return
				}
				setAdminAccountOwnerScope(c, cfg)
				c.Next()
				return
			}
		}

		// 无有效认证信息
		AbortWithError(c, 401, "UNAUTHORIZED", "Authorization required")
	}
}

// setAdminAccountOwnerScope is called only after administrator authentication.
// ID 1 always owns the full pool. The legacy owner scope cannot grant a
// secondary administrator access past the observer boundary above.
func setAdminAccountOwnerScope(c *gin.Context, cfg *config.Config) {
	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return
	}
	if subject.UserID == PrimaryAdminUserID {
		c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), subject.UserID, PrimaryAdminUserID))
		return
	}
	if cfg == nil || cfg.Security.AccountPoolOwnerUserID <= 0 {
		return
	}
	c.Request = c.Request.WithContext(service.WithAccountOwnerScope(c.Request.Context(), subject.UserID, cfg.Security.AccountPoolOwnerUserID))
}

func isWebSocketUpgradeRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	// RFC6455 handshake uses:
	//   Connection: Upgrade
	//   Upgrade: websocket
	upgrade := strings.ToLower(strings.TrimSpace(c.GetHeader("Upgrade")))
	if upgrade != "websocket" {
		return false
	}
	connection := strings.ToLower(c.GetHeader("Connection"))
	return strings.Contains(connection, "upgrade")
}

func extractJWTFromWebSocketSubprotocol(c *gin.Context) string {
	if c == nil {
		return ""
	}
	raw := strings.TrimSpace(c.GetHeader("Sec-WebSocket-Protocol"))
	if raw == "" {
		return ""
	}

	// The header is a comma-separated list of tokens. We reserve the prefix "jwt."
	// for carrying the admin JWT.
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "jwt.") {
			token := strings.TrimSpace(strings.TrimPrefix(p, "jwt."))
			if token != "" {
				return token
			}
		}
	}
	return ""
}

// validateAdminAPIKey 验证旧版全权管理员 API Key（x-api-key）。
//
// 只存哈希：优先用哈希等值比较；历史明文行只做只读兼容（启动/状态接口负责
// 迁移）。未配置与 key 错误返回完全一致的 401，避免探测"是否配置过"。
func validateAdminAPIKey(
	c *gin.Context,
	key string,
	settingService *service.SettingService,
	userService *service.UserService,
) bool {
	result, err := settingService.VerifyAdminAPIKey(c.Request.Context(), key)
	if err != nil {
		AbortWithError(c, 500, "INTERNAL_ERROR", "Internal server error")
		return false
	}
	if !result.Verified {
		AbortWithError(c, 401, "INVALID_ADMIN_KEY", "Invalid admin API key")
		return false
	}

	// 获取真实的管理员用户
	admin, err := userService.GetByID(c.Request.Context(), PrimaryAdminUserID)
	if err != nil {
		AbortWithError(c, 500, "INTERNAL_ERROR", "No admin user found")
		return false
	}
	if admin == nil || admin.ID != PrimaryAdminUserID || !admin.IsActive() || !admin.IsAdmin() {
		AbortWithError(c, 403, "FORBIDDEN", "Active admin access required")
		return false
	}

	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      admin.ID,
		Concurrency: admin.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), admin.Role)
	c.Set(ContextKeyAuthEmail, admin.Email)
	c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
	c.Set(ContextKeyAdminCredentialKind, AdminCredentialKindLegacy)
	c.Set(ContextKeyAdminCredentialPrefix, result.Hint)
	// last_used_at 只对哈希存储生效（历史明文模式交给迁移流程，不在认证热路径写库）。
	if result.HashStorage {
		settingService.TouchAdminAPIKeyLastUsedAsync(c.Request.Context(), result.Hint)
	}
	return true
}

// validateJWTForAdmin 验证 JWT 并检查管理员权限
func validateJWTForAdmin(
	c *gin.Context,
	token string,
	authService *service.AuthService,
	userService *service.UserService,
	settingService *service.SettingService,
	auditService *service.AuditLogService,
) bool {
	// 验证 JWT token
	claims, err := authService.ValidateToken(token)
	if err != nil {
		if errors.Is(err, service.ErrTokenExpired) {
			AbortWithError(c, 401, "TOKEN_EXPIRED", "Token has expired")
			return false
		}
		AbortWithError(c, 401, "INVALID_TOKEN", "Invalid token")
		return false
	}

	// 从数据库获取用户
	user, err := userService.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			AbortWithError(c, 401, "USER_NOT_FOUND", "User not found")
		} else {
			AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to load user")
		}
		return false
	}

	// 检查用户状态
	if !user.IsActive() {
		AbortWithError(c, 401, "USER_INACTIVE", "User account is not active")
		return false
	}

	// 校验 TokenVersion，确保管理员改密后旧 token 失效
	if claims.TokenVersion != user.TokenVersion {
		AbortWithError(c, 401, "TOKEN_REVOKED", "Token has been revoked (password changed)")
		return false
	}

	// "撤销所有会话"必须同样让无状态 access token 失效：管理面与用户面共用同一
	// 撤销时间线（auth:revoked_before）。此前只有 jwtAuth 中间件检查这里，
	// 管理员在面板点过"撤销所有会话"后，被盗的管理员 access token 仍可继续访问
	// /api/v1/admin/* 直到自然过期。
	if claims.IssuedAt != nil && authService.IsAccessTokenRevoked(c.Request.Context(), claims.UserID, claims.IssuedAt.Time) {
		AbortWithError(c, 401, "TOKEN_REVOKED", "Token has been revoked")
		return false
	}

	// 会话绑定校验：IP/UA 任一变化即撤销会话（功能可在系统设置中关闭）
	if !enforceSessionBinding(c, authService, settingService, auditService, claims) {
		return false
	}

	// 检查管理员权限
	if !user.IsAdmin() {
		AbortWithError(c, 403, "FORBIDDEN", "Admin access required")
		return false
	}

	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      user.ID,
		Concurrency: user.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), user.Role)
	c.Set(ContextKeyAuthEmail, user.Email)
	c.Set(ContextKeySessionID, claims.SessionID)
	c.Set("auth_method", "jwt")

	return true
}

// 每次请求查库，未登记的管理动作默认拒绝。
func authenticateScopedAdminRequest(c *gin.Context, key string, settings *service.SettingService, users *service.UserService, audit *service.AuditLogService, cfg *config.Config) {
	if settings == nil || users == nil {
		AbortWithError(c, 401, "INVALID_ADMIN_KEY", "Invalid admin API key")
		recordAdminKeyRequest(c, audit, nil)
		return
	}
	item, err := settings.AuthenticateScopedAdminKey(c.Request.Context(), key, SecurityClientIP(c))
	defer func() { recordAdminKeyRequest(c, audit, item) }()
	if err != nil {
		AbortWithError(c, 401, "INVALID_ADMIN_KEY", "Invalid admin API key")
		return
	}
	user, err := users.GetByID(c.Request.Context(), item.CreatedBy)
	if err != nil || user == nil || !user.IsActive() || !user.IsAdmin() {
		AbortWithError(c, 401, "INVALID_ADMIN_KEY", "Invalid admin API key")
		return
	}
	principal := &adminaccess.Principal{KeyID: item.ID, KeyName: item.Name, CreatorID: item.CreatedBy, Policy: item.Policy}
	c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), principal))
	c.Set(string(ContextKeyUser), AuthSubject{UserID: user.ID, Concurrency: user.Concurrency})
	c.Set(string(ContextKeyUserRole), user.Role)
	c.Set(ContextKeyAuthEmail, user.Email)
	c.Set("auth_method", service.AuditAuthMethodScopedAdminAPIKey)
	c.Set(ContextKeyAdminCredentialKind, AdminCredentialKindScoped)
	c.Set(ContextKeyAdminCredentialPrefix, item.KeyPrefix)
	// 不跳过审计中间件：受限密钥是自动化身份，变更操作必须与真人会话一样
	// 留下动作名、脱敏请求体和路径参数。密钥身份通过专门的标量字段补充。
	SetAuditExtra(c, map[string]any{"admin_key_id": item.ID, "admin_key_name": item.Name})
	// 密钥只能由主管理员通过 JWT 创建（handler 强制），因此受限密钥等价于主管理员
	// 的自动化身份；这里沿用旧版全权密钥的账户归属作用域，不再叠加观察员边界。
	setAdminAccountOwnerScope(c, cfg)
	// 受限密钥是显式授权、可过期/轮换、带 IP 限制的自动化身份，仍按自身 policy
	// 逐路由授权；但敏感资源（含只读）由 step-up 门控统一拒绝机器凭据，
	// 它同样过不去。旧版全权 key 的默认拒绝在 legacy 分支里。
	if strings.Contains(c.FullPath(), "/settings/admin-api-key") || !adminaccess.RouteAllowed(item.Policy, c.Request.Method, c.FullPath()) {
		c.Set("admin_key_denial_reason", "permission_denied")
		AbortWithError(c, 403, "FORBIDDEN", "管理密钥未获此操作权限")
		return
	}
	c.Next()
}
func recordAdminKeyRequest(c *gin.Context, audit *service.AuditLogService, item *service.ScopedAdminKey) {
	if audit == nil {
		return
	}
	// 变更类请求和敏感读取已由审计中间件落库（含密钥身份），不重复补记。
	// 认证/授权失败发生在审计中间件之前，因此仍需在此兜底留痕。
	if c.GetBool(auditCtxKeyRecorded) {
		return
	}
	entry := &service.AuditLog{CreatedAt: time.Now().UTC(), AuthMethod: service.AuditAuthMethodScopedAdminAPIKey, Action: "admin.api_key.request", Method: c.Request.Method, Path: c.Request.URL.Path, StatusCode: c.Writer.Status(), ClientIP: SecurityClientIP(c), UserAgent: c.Request.UserAgent()}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	if item != nil {
		entry.Extra = map[string]any{"admin_key_id": item.ID, "admin_key_name": item.Name}
		uid := item.CreatedBy
		entry.ActorUserID = &uid
	}
	if reason := c.GetString("admin_key_denial_reason"); reason != "" {
		if entry.Extra == nil {
			entry.Extra = map[string]any{}
		}
		entry.Extra["denial_reason"] = reason
	}
	audit.Record(entry)
}
