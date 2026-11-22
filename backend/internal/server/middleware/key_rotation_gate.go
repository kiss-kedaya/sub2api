package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// KeyRotationGateMiddleware 密钥轮换门禁中间件类型（用于 wire 注入区分）。
type KeyRotationGateMiddleware gin.HandlerFunc

// NewKeyRotationGateMiddleware 创建密钥轮换门禁中间件。
//
// 挂在兑换码 / 充值订单 / USDT 充值三组路由上（必须挂在 jwt 认证之后）：
// 用户所有启用中的 API Key 都创建于门槛时间之前、且都没有 IP 白名单时，
// 返回 403 KEY_ROTATION_REQUIRED，并写入一条
// action=user.recharge.blocked_key_rotation 的审计（user_id / ip / path）。
//
// 这里不拦截模型调用、登录、查余额与密钥管理，用户必须先能完成整改动作。
func NewKeyRotationGateMiddleware(
	gate *service.KeyRotationGateService,
	auditService *service.AuditLogService,
) KeyRotationGateMiddleware {
	return KeyRotationGateMiddleware(func(c *gin.Context) {
		if c == nil || gate == nil {
			if c != nil {
				c.Next()
			}
			return
		}
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			// 挂载位置在认证之后；没有主体说明路由装配有误，交回后续中间件处理，
			// 不要在门禁层伪造 401。
			c.Next()
			return
		}

		status, blocked, err := gate.RequireUser(c.Request.Context(), subject.UserID)
		if err != nil {
			// 查询失败不拦：门禁是安全事件响应措施，不是鉴权边界。
			// 让充值/兑换在数据库抖动时保持可用，同时留下排障日志。
			slog.Warn("key rotation gate check failed; allowing request",
				"user_id", subject.UserID, "path", c.Request.URL.Path, "err", err)
			c.Next()
			return
		}
		if !blocked {
			if status != nil && status.Reason == service.KeyRotationReasonRiskAcknowledged {
				recordKeyRotationRiskAckAllowAudit(c, subject.UserID, status, auditService)
			}
			c.Next()
			return
		}

		recordKeyRotationBlockedAudit(c, subject.UserID, status, auditService)
		// 用户组已挂审计中间件：本次拒绝已经单独落库，避免重复记录。
		SkipAudit(c)
		c.Set("key_rotation_required", true)

		response.ErrorWithDetails(
			c,
			http.StatusForbidden,
			service.KeyRotationRequiredMessage,
			service.KeyRotationRequiredCode,
			map[string]string{
				"error_code":       service.KeyRotationRequiredCode,
				"remediation_path": service.KeyRotationRemediationPath,
				"reason":           status.Reason,
			},
		)
		c.Abort()
	})
}

// recordKeyRotationBlockedAudit 写入一条门禁拒绝审计。
// 直达 AuditLogService（而不是依赖外层 AuditLog 中间件），因为 crypto 充值组
// 的接口是 GET，审计中间件默认不记录 GET 请求。
func recordKeyRotationBlockedAudit(
	c *gin.Context,
	userID int64,
	status *service.KeyRotationGateStatus,
	auditService *service.AuditLogService,
) {
	if auditService == nil || status == nil {
		return
	}
	uid := userID
	authMethod := c.GetString("auth_method")
	if authMethod == "" {
		authMethod = service.AuditAuthMethodJWT
	}
	fullPath := c.FullPath()
	if fullPath == "" {
		fullPath = c.Request.URL.Path
	}
	ip := SecurityClientIP(c)

	entry := &service.AuditLog{
		CreatedAt:   time.Now().UTC(),
		Action:      service.AuditActionRechargeBlockedKeyRotation,
		Method:      c.Request.Method,
		Path:        fullPath,
		ClientIP:    ip,
		UserAgent:   c.Request.UserAgent(),
		StatusCode:  http.StatusForbidden,
		ActorUserID: &uid,
		AuthMethod:  authMethod,
		Extra: map[string]any{
			"user_id":           uid,
			"ip":                ip,
			"path":              c.Request.URL.Path,
			"error_code":        service.KeyRotationRequiredCode,
			"reason":            status.Reason,
			"active_key_count":  status.ActiveKeyCount,
			"pending_key_count": status.PendingKeyCount,
			"cutoff":            status.Cutoff.Format(time.RFC3339),
			"remediation_path":  status.RemediationPath,
		},
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	auditService.Record(entry)
}

// recordKeyRotationRiskAckAllowAudit 写入一条「靠风险确认放行」的审计。
//
// 充值/兑换本身很稀疏，这条记录让运维能反查每一次「没整改也充值成功」：
// 谁、从哪个 IP、确认的有效期到什么时候、当时 pending 的 key 有几把。
// 写完跳过通用审计，避免同一请求出现两条不同 action 的记录。
func recordKeyRotationRiskAckAllowAudit(
	c *gin.Context,
	userID int64,
	status *service.KeyRotationGateStatus,
	auditService *service.AuditLogService,
) {
	if auditService == nil || status == nil || status.RiskAckExpiresAt == nil {
		return
	}
	uid := userID
	authMethod := c.GetString("auth_method")
	if authMethod == "" {
		authMethod = service.AuditAuthMethodJWT
	}
	fullPath := c.FullPath()
	if fullPath == "" {
		fullPath = c.Request.URL.Path
	}
	ip := SecurityClientIP(c)
	entry := &service.AuditLog{
		CreatedAt:   time.Now().UTC(),
		Action:      service.AuditActionRechargeAllowedRiskAck,
		Method:      c.Request.Method,
		Path:        fullPath,
		ClientIP:    ip,
		UserAgent:   c.Request.UserAgent(),
		StatusCode:  http.StatusOK,
		ActorUserID: &uid,
		AuthMethod:  authMethod,
		Extra: map[string]any{
			"user_id":             uid,
			"ip":                  ip,
			"path":                c.Request.URL.Path,
			"reason":              status.Reason,
			"risk_ack_expires_at": status.RiskAckExpiresAt.UTC().Format(time.RFC3339),
			"active_key_count":    status.ActiveKeyCount,
			"pending_key_count":   status.PendingKeyCount,
			"cutoff":              status.Cutoff.Format(time.RFC3339),
		},
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	auditService.Record(entry)
	SkipAudit(c)
}
