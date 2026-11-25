package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ResetUser2FARequest 是「管理员破玻璃重置用户 TOTP」的请求体。
//
// 只收 reason：目标用户由路径决定，操作者身份只认鉴权上下文（不接受请求体伪造）。
type ResetUser2FARequest struct {
	Reason string `json:"reason"`
}

// ResetUser2FA 管理员破玻璃重置目标用户的 TOTP。
//
//	POST /api/v1/admin/users/:id/reset-2fa
//
// 路由级门控（adminroutepolicy，step_up_required）已经保证：
//   - 必须是真人 JWT 会话，且管理员自己刚过认证（TOTP 或 Passkey）；
//   - 机器凭据（x-api-key / adm_sc_）一律 403。
//
// 这里只做协议层校验与业务调用：
//   - reason 必填 1~500 字 → 400；
//   - 用户不存在 → 404；已禁用/已删 → 404（不泄露「存在但被禁用」）；
//   - 用户没启用 2FA → 400「该用户未启用 2FA」。
//
// 成功返回的重置结果里带 notification_queued：邮件没发出去时前端必须提示
// 管理员另行通知用户，不能报「全部完成」。
func (h *UserHandler) ResetUser2FA(c *gin.Context) {
	targetUserID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || targetUserID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req ResetUser2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

	actorUserID, _ := middleware.GetAuthSubjectFromContext(c)
	actorRole, _ := middleware.GetUserRoleFromContext(c)
	input := service.Admin2FAResetInput{
		TargetUserID: targetUserID,
		Reason:       req.Reason,
		ActorUserID:  actorUserID.UserID,
		ActorEmail:   c.GetString(middleware.ContextKeyAuthEmail),
		ActorRole:    actorRole,
		AuthMethod:   c.GetString("auth_method"),
		ClientIP:     middleware.SecurityClientIP(c),
		UserAgent:    c.Request.UserAgent(),
		RequestID:    requestIDFromGin(c),
	}

	if h.admin2FAReset == nil {
		response.ErrorFrom(c, service.ErrAdmin2FAResetUnavailable)
		return
	}
	result, err := h.admin2FAReset.AdminResetUserTOTP(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 显式记一条审计扩展：路由级审计中间件也会落一条 admin.user.reset_2fa，
	// 服务层已经同步落库了完整的一条；这里补的是「本次响应是否真的通知到用户」，
	// 便于在审计列表里直接看出哪些重置需要人工补通知。
	middleware.SetAuditExtra(c, map[string]any{
		"target_user_attribute_revision": result.UserID,
		"admin_2fa_reset_revoked_stepup": result.RevokedStepUpGrants,
		"admin_2fa_reset_notified":       result.NotificationQueued,
		"admin_2fa_reset_passkey_bound":  result.PasskeyBound,
	})
	response.Success(c, result)
}

func requestIDFromGin(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if id, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}
