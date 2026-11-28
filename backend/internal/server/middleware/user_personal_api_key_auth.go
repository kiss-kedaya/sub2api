package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	intmw "github.com/Wei-Shaw/sub2api/internal/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	// ContextKeyPersonalAPIKeyPrefix 记录当前请求使用的个人接口密钥脱敏前缀（不含完整明文）。
	ContextKeyPersonalAPIKeyPrefix = "personal_api_key_prefix"

	personalAPIKeyInvalidWindowPrefix = "personal_key:invalid:ip:"
	personalAPIKeyBadFmtWindowPrefix  = "personal_key:badfmt:ip:"
	personalAPIKeyInvalidBlockPrefix  = "rate_limit:personal_key:invalid_block:ip:"
	personalAPIKeyMinBlockDuration    = 30 * time.Second
)

type userPersonalAPIKeyAuthenticator interface {
	Authenticate(ctx context.Context, rawKey, clientIP string) (*service.UserPersonalAPIKey, error)
}

type personalAPIKeyInvalidAttemptGuard interface {
	IsBlocked(ctx context.Context, clientIP string) (blocked bool, retryAfter time.Duration)
	RecordBadFormat(ctx context.Context, clientIP string) (blocked bool, retryAfter time.Duration)
	RecordInvalid(ctx context.Context, clientIP string) (blocked bool, retryAfter time.Duration)
}

type redisPersonalAPIKeyInvalidGuard struct {
	redis   *redis.Client
	limiter *intmw.RateLimiter
}

func newPersonalAPIKeyInvalidGuard(redisClient *redis.Client) personalAPIKeyInvalidAttemptGuard {
	if redisClient == nil {
		return nil
	}
	return &redisPersonalAPIKeyInvalidGuard{
		redis:   redisClient,
		limiter: intmw.NewRateLimiter(redisClient),
	}
}

func (g *redisPersonalAPIKeyInvalidGuard) IsBlocked(ctx context.Context, clientIP string) (bool, time.Duration) {
	// 与 PanelRateLimiter.PublicIP 保持一致：仅对公网单播 IP 生效，避免反代内网地址坍缩造成全站误封。
	if g == nil || g.redis == nil || !isPubliclyRoutableClientIP(clientIP) {
		return false, 0
	}
	ttl, err := g.redis.PTTL(ctx, personalAPIKeyInvalidBlockPrefix+clientIP).Result()
	if err != nil {
		// Redis 异常 fail-open，避免基础设施抖动把合法请求误杀。
		return false, 0
	}
	if ttl > 0 {
		return true, ttl
	}
	return false, 0
}

// RecordBadFormat 对畸形格式探针单独按滑动窗口限流（0 DB 查询），不写 invalid_block，
// 防止共享 NAT 下垃圾请求误封同一出口的合法 uak_ 业务。
func (g *redisPersonalAPIKeyInvalidGuard) RecordBadFormat(ctx context.Context, clientIP string) (bool, time.Duration) {
	if g == nil || g.limiter == nil || !isPubliclyRoutableClientIP(clientIP) {
		return false, 0
	}
	res, err := g.limiter.AllowSliding(
		ctx,
		personalAPIKeyBadFmtWindowPrefix+clientIP,
		service.UserPersonalAPIKeyInvalidIPMaxAttempts,
		service.UserPersonalAPIKeyInvalidIPWindow,
	)
	if err != nil {
		slog.Warn("personal api key bad-format rate limit check failed", "error", err)
		return false, 0
	}
	if !res.Allowed {
		return true, res.RetryAfter
	}
	return false, 0
}

func (g *redisPersonalAPIKeyInvalidGuard) RecordInvalid(ctx context.Context, clientIP string) (bool, time.Duration) {
	if g == nil || g.limiter == nil || !isPubliclyRoutableClientIP(clientIP) {
		return false, 0
	}
	res, err := g.limiter.AllowSliding(
		ctx,
		personalAPIKeyInvalidWindowPrefix+clientIP,
		service.UserPersonalAPIKeyInvalidIPMaxAttempts,
		service.UserPersonalAPIKeyInvalidIPWindow,
	)
	if err != nil {
		slog.Warn("personal api key invalid-attempt rate limit check failed", "error", err)
		return false, 0
	}
	if !res.Allowed {
		blockTTL := res.RetryAfter
		if blockTTL < personalAPIKeyMinBlockDuration {
			blockTTL = personalAPIKeyMinBlockDuration
		}
		if g.redis != nil {
			_ = g.redis.Set(ctx, personalAPIKeyInvalidBlockPrefix+clientIP, "1", blockTTL).Err()
		}
		return true, blockTTL
	}
	return false, 0
}

// extractPersonalAPIKeyFromRequest 判断本次请求是否试图使用用户个人接口密钥（uak_）。
//
// 支持两种主流传递方式，方便第三方上游渠道管理与脚本集成：
//   - X-API-Key: uak_...
//   - Authorization: Bearer uak_...
//
// 若仅携带非 uak_ 的 X-API-Key 且未携带 Authorization，则同样按个人接口密钥失败路径处理，
// 避免畸形凭证落入模糊错误并使其受无效密钥 IP 限流保护。
func extractPersonalAPIKeyFromRequest(c *gin.Context) (rawKey string, isPersonalAttempt bool) {
	if c == nil {
		return "", false
	}
	xAPIKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))

	if xAPIKey != "" {
		if service.IsUserPersonalAPIKeyPrefix(xAPIKey) {
			return xAPIKey, true
		}
		// 若同时带了 Authorization（例如浏览器扩展或代理附带了无关头），优先让 Authorization 走正常校验；
		// 仅带 X-API-Key 时按个人密钥无效尝试拦截并计数。
		if authHeader == "" {
			return xAPIKey, true
		}
	}

	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])
			if service.IsUserPersonalAPIKeyPrefix(token) {
				return token, true
			}
		}
	}
	return "", false
}

// authenticatePersonalAPIKey 使用用户个人接口密钥（uak_）完成认证。
//
// 安全边界：
//   - 只按 SHA-256 哈希查库并在常数时间内比对；
//   - 查库前检查按 IP 的无效尝试封禁标记，阻断在线暴力探测；
//   - 免除浏览器会话网络指纹绑定（允许跨服务器 IP/UA 稳定调用）；
//   - 始终校验账号有效状态（停用/删除立即拒绝）；
//   - 严禁调用账户安全/凭证管理/自身密钥管理/封存密文相关路由（403 PERSONAL_API_KEY_FORBIDDEN + 审计）。
func authenticatePersonalAPIKey(
	c *gin.Context,
	rawKey string,
	authenticator userPersonalAPIKeyAuthenticator,
	invalidGuard personalAPIKeyInvalidAttemptGuard,
	userService jwtUserReader,
	activityToucher userActivityToucher,
	auditService *service.AuditLogService,
) {
	if authenticator == nil {
		AbortWithError(c, http.StatusUnauthorized, "INVALID_PERSONAL_API_KEY", "Invalid personal API key")
		return
	}

	clientIP := SecurityClientIP(c)
	// 先做零 DB 成本的格式快速拒绝：畸形请求只计入独立的 bad-format 滑动窗口，不污染同 NAT 合法 uak_ 用户的 DB 防穿透封禁桶。
	if !service.IsUserPersonalAPIKeyFormat(rawKey) {
		if invalidGuard != nil {
			if blocked, retryAfter := invalidGuard.RecordBadFormat(c.Request.Context(), clientIP); blocked {
				abortPanelRateLimited(c, retryAfter)
				return
			}
		}
		AbortWithError(c, http.StatusUnauthorized, "INVALID_PERSONAL_API_KEY", "Invalid personal API key")
		return
	}

	if invalidGuard != nil {
		if blocked, retryAfter := invalidGuard.IsBlocked(c.Request.Context(), clientIP); blocked {
			abortPanelRateLimited(c, retryAfter)
			return
		}
	}

	record, err := authenticator.Authenticate(c.Request.Context(), rawKey, clientIP)
	if err != nil {
		if errors.Is(err, service.ErrUserPersonalAPIKeyInvalid) {
			if invalidGuard != nil {
				if blocked, retryAfter := invalidGuard.RecordInvalid(c.Request.Context(), clientIP); blocked {
					abortPanelRateLimited(c, retryAfter)
					return
				}
			}
			AbortWithError(c, http.StatusUnauthorized, "INVALID_PERSONAL_API_KEY", "Invalid personal API key")
			return
		}
		if errors.Is(err, service.ErrUserPersonalAPIKeyUnavailable) {
			AbortWithError(c, http.StatusServiceUnavailable, "PERSONAL_API_KEY_UNAVAILABLE", "Personal API key service is unavailable")
			return
		}
		AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to verify personal API key")
		return
	}

	user, err := userService.GetByID(c.Request.Context(), record.UserID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			AbortWithError(c, http.StatusUnauthorized, "USER_NOT_FOUND", "User not found")
		} else {
			AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load user")
		}
		return
	}
	if user == nil || !user.IsActive() {
		AbortWithError(c, http.StatusUnauthorized, "USER_INACTIVE", "User account is not active")
		return
	}

	c.Set(string(ContextKeyUser), AuthSubject{
		UserID:      user.ID,
		Concurrency: user.Concurrency,
	})
	c.Set(string(ContextKeyUserRole), user.Role)
	c.Set(ContextKeyAuthEmail, user.Email)
	c.Set("auth_method", service.AuditAuthMethodUserAPIKey)
	c.Set(ContextKeyPersonalAPIKeyPrefix, record.KeyPrefix)

	if isPersonalAPIKeyRouteForbidden(c.Request.Method, c.FullPath(), c.Request.URL.Path) {
		recordPersonalAPIKeyForbiddenAudit(c, auditService, user, record.KeyPrefix)
		AbortWithError(
			c,
			http.StatusForbidden,
			"PERSONAL_API_KEY_FORBIDDEN",
			"Personal API key cannot access account security or credential management endpoints",
		)
		return
	}

	if activityToucher != nil && !service.CryptoAdminTestMode() {
		activityToucher.TouchLastActiveForUser(c.Request.Context(), user)
	}
	c.Next()
}

// isPersonalAPIKeyRouteForbidden 判断某个用户端路由是否禁止由个人接口密钥调用。
//
// 个人接口密钥定位于「远程服务端免 AT/RT 自动化管理（查询余额/分组/渠道/用量、创建/轮换/管理 API Key、
// 兑换/订阅/工单等）」，绝不允许借此接管账号、削弱双因素/密码保护、读取浏览器封存密文或递归管理自身。
// 同时匹配 Gin 路由模板与归一化原始路径，避免斜杠/路径清洗绕过。
func isPersonalAPIKeyRouteForbidden(method, fullPath, rawPath string) bool {
	return matchPersonalAPIKeyForbiddenPath(method, fullPath) ||
		matchPersonalAPIKeyForbiddenPath(method, rawPath)
}

func matchPersonalAPIKeyForbiddenPath(method, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	cleaned := path.Clean(target)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}

	// 1. 任何方法都禁止访问的高敏安全与凭证管理前缀。
	forbiddenPrefixes := []string{
		"/api/v1/user/personal-api-key",
		"/api/v1/user/password",
		"/api/v1/user/account-bindings",
		"/api/v1/user/auth-identities",
		"/api/v1/user/totp",
		"/api/v1/user/passkeys",
		"/api/v1/user/step-up",
		"/api/v1/user/2fa",
		"/api/v1/user/notify-email",
		"/api/v1/user/bark",
		"/api/v1/user/security/verification-factors",
		"/api/v1/user/security/api-key-create-verification",
		"/api/v1/user/security/passkey",
		"/api/v1/user/security/key-rotation-risk-ack",
		"/api/v1/user/aff/transfer",
		"/api/v1/auth/revoke-all-sessions",
		"/api/v1/auth/oauth/bind-token",
	}
	for _, prefix := range forbiddenPrefixes {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}

	// 2. 浏览器端封存 API Key 密文与二次查看授权（/api/v1/user/api-keys/:id/secret-seal|reveal-authorize）。
	if strings.HasPrefix(cleaned, "/api/v1/user/api-keys/") &&
		(strings.HasSuffix(cleaned, "/secret-seal") || strings.HasSuffix(cleaned, "/reveal-authorize")) {
		return true
	}

	// 3. 支付订单写操作（下单/取消/退款申请/核单）仅限交互式会话；查询订单与配置（GET）照常放行。
	if (cleaned == "/api/v1/payment/orders" || strings.HasPrefix(cleaned, "/api/v1/payment/orders/")) &&
		!strings.EqualFold(method, http.MethodGet) && !strings.EqualFold(method, http.MethodHead) {
		return true
	}

	// 4. 修改个人资料/余额告警配置（PUT /api/v1/user）仅限交互式会话；查询资料（GET /api/v1/user/profile）照常放行。
	if cleaned == "/api/v1/user" && !strings.EqualFold(method, http.MethodGet) && !strings.EqualFold(method, http.MethodHead) {
		return true
	}

	return false
}

func recordPersonalAPIKeyForbiddenAudit(c *gin.Context, auditService *service.AuditLogService, user *service.User, keyPrefix string) {
	if c == nil || auditService == nil || service.CryptoAdminTestReadOnly() {
		return
	}
	p := c.FullPath()
	if p == "" && c.Request != nil && c.Request.URL != nil {
		p = c.Request.URL.Path
	}
	var actorID *int64
	actorEmail := ""
	actorRole := ""
	if user != nil {
		id := user.ID
		actorID = &id
		actorEmail = user.Email
		actorRole = user.Role
	}
	entry := &service.AuditLog{
		CreatedAt:        time.Now().UTC(),
		ActorUserID:      actorID,
		ActorEmail:       actorEmail,
		ActorRole:        actorRole,
		AuthMethod:       service.AuditAuthMethodUserAPIKey,
		CredentialMasked: MaskedRequestCredential(c),
		Action:           service.AuditActionUserPersonalAPIKeyForbidden,
		Method:           c.Request.Method,
		Path:             p,
		ClientIP:         SecurityClientIP(c),
		UserAgent:        c.Request.UserAgent(),
		StatusCode:       http.StatusForbidden,
		Extra: map[string]any{
			"personal_api_key_prefix": keyPrefix,
			"error_code":              "PERSONAL_API_KEY_FORBIDDEN",
		},
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	auditService.Record(entry)
}
