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
//   - 机器凭据（x-api-key / adm_sc_）一律 403；
//   - 每个管理员每分钟 3 次、每小时 20 次（PanelRateLimiter.Admin2FAReset）。
//
// 这里做协议层校验与高危门控，再调用业务：
//   - 目标是操作者自己 → 400 RESET_2FA_SELF_TARGET：过 step-up 本身就要 2FA，真丢了
//     自己的码这条路用不了，只会便宜了窗口内的会话劫持；自己丢码走服务器命令行；
//   - 要求「新鲜」的二次验证（Admin2FAResetFreshStepUpWindow 内刚签发）：共享的
//     5 分钟免验证窗口里的旧验证不能放行这个高危操作，不够新鲜返回 STEP_UP_REQUIRED，
//     前端弹出验证码输入框、验证后自动重试；
//   - reason 必填 1~500 字（清洗成纯文本后计）→ 400；
//   - 用户不存在 → 404；已禁用/已删 → 404（不泄露「存在但被禁用」）；
//   - 用户没启用 2FA → 400「该用户未启用 2FA」。
//
// 成功返回的重置结果里带 notification_queued 与 incomplete_steps：重置提交之后的
// 善后（撤销会话、发邮件、写审计）没做完不会变成 5xx，而是在这里列出，前端必须据此
// 提示管理员，不能报「全部完成」。
func (h *UserHandler) ResetUser2FA(c *gin.Context) {
	targetUserID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || targetUserID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	actorUserID, _ := middleware.GetAuthSubjectFromContext(c)
	// 自重置先拒绝、再要验证码：不让管理员为一个必被拒绝的操作白输一次动态码。
	if actorUserID.UserID > 0 && actorUserID.UserID == targetUserID {
		response.ErrorFrom(c, service.ErrAdmin2FAResetSelfTarget)
		return
	}
	if !middleware.EnforceFreshStepUp(c, h.totpService, h.userService, h.settingService, service.Admin2FAResetFreshStepUpWindow) {
		return
	}

	var req ResetUser2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}

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
