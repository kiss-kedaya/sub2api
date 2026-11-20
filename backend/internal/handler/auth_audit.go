package handler

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// 第三方登录 / 2FA 审计埋点。
//
// 这些函数只写 gin context 里的审计标记，由审计中间件在请求结束后统一落库；
// 不改变任何登录/注册逻辑，也不影响响应内容。
// 敏感约束（与中间件保持一致）：
//   - 不记录 token / code / state / temp_token 明文（请求体对认证路由整体省略）；
//   - 邮箱只落 AuditLog.actor_email 的脱敏值与 extra.email_hash 指纹；
//   - 失败原因只落错误码 + 粗分类，不落上游返回原文。

// markAuthAuditSuccess 标记一次登录/注册成功：写入操作者 user_id 与脱敏邮箱。
// 在签发会话/refresh token 的分支调用。
func markAuthAuditSuccess(c *gin.Context, userID int64, email string) {
	servermiddleware.MarkAuthAuditResult(c, service.AuditResultSuccess, "")
	servermiddleware.MarkAuthAuditActor(c, userID, email)
}

// markAuthAuditChallenge 标记 2FA 挑战已下发（未签发会话）。
// 动作名切换为 auth.login.2fa.challenge，方便单独检索「挑战下发但从不验证」的绕过尝试。
func markAuthAuditChallenge(c *gin.Context, userID int64, email string) {
	servermiddleware.SetAuditAction(c, service.AuditActionLogin2FAChallenge)
	servermiddleware.MarkAuthAuditResult(c, service.AuditResultChallengeIssued, "")
	servermiddleware.MarkAuthAuditRequires2FA(c)
	servermiddleware.MarkAuthAuditActor(c, userID, email)
}

// markAuthAuditPending 标记「回调已处理，但还需要用户在前端继续」的中间态。
func markAuthAuditPending(c *gin.Context, reason string) {
	servermiddleware.MarkAuthAuditResult(c, service.AuditResultPending, reason)
}

// markAuthAuditFailure 标记认证失败并携带失败原因码（如 invalid_state / provider_error）。
func markAuthAuditFailure(c *gin.Context, reason string) {
	servermiddleware.MarkAuthAuditResult(c, service.AuditResultFailure, reason)
}

// markAuthAuditFailureFromError 从 error 提取 infra error reason 作为失败原因。
func markAuthAuditFailureFromError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	servermiddleware.MarkAuthAuditResult(c, service.AuditResultFailure, infraerrors.Reason(err))
}

// markAuthAuditEmail 只登记被尝试账号的邮箱指纹，用于失败路径反查撞库/定向攻击。
// 绝不把邮箱明文写进审计。
func markAuthAuditEmail(c *gin.Context, email string) {
	servermiddleware.MarkAuthAuditEmail(c, email)
}

// markAuthAuditProvider 覆盖路由推导出的 provider（通用 pending 兑换等无法从路径判断渠道的场景）。
func markAuthAuditProvider(c *gin.Context, provider string) {
	servermiddleware.MarkAuthAuditProvider(c, provider)
}

// pendingOAuthAuditProvider 决定 pending 流程审计里的 provider：
// 优先 URL 指定的渠道，其次 pending session 里的真实渠道。
func pendingOAuthAuditProvider(routeProvider, sessionProvider string) string {
	if provider := strings.TrimSpace(routeProvider); provider != "" {
		return provider
	}
	provider := strings.TrimSpace(sessionProvider)
	if provider == "" {
		return "oauth"
	}
	return provider
}
