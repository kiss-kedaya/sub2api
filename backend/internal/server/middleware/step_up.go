package middleware

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// StepUpAuthMiddleware 敏感操作 step-up 2FA 门控中间件类型。
type StepUpAuthMiddleware gin.HandlerFunc

// 门控「跳过验证码」时回传的响应头。
//
// 只要一次敏感请求因为会话里存在有效的 step-up grant 而没有要求 TOTP，响应就
// 必须带这两个头——不允许静默跳过。前端据此在右下角展示「已跳过 2FA 验证 ·
// 剩余免授权 4:32」的倒计时，让操作者知道此刻的 5 分钟窗口还在生效。
const (
	// StepUpSkippedHeader 值为 "1"，表示本次请求跳过了二次验证。
	StepUpSkippedHeader = "X-StepUp-Skipped"
	// StepUpRemainingHeader 回传剩余免授权秒数（向下取整）。
	StepUpRemainingHeader = "X-StepUp-Remaining"
)

// stepUpGrantChecker 抽象 TOTP step-up 授权检查能力（由 TotpService 实现）。
type stepUpGrantChecker interface {
	HasStepUpGrant(ctx context.Context, userID int64, sessionKey string) (bool, error)
	// StepUpGrantRemaining 读剩余窗口，用于回传 X-StepUp-Remaining。
	// 返回 false 表示读的瞬间授权已失效（按未授权处理）。
	StepUpGrantRemaining(ctx context.Context, userID int64, sessionKey string) (time.Duration, bool, error)
}

// stepUpUserReader 抽象用户读取能力（检查 TOTP 是否启用）。
type stepUpUserReader interface {
	GetByID(ctx context.Context, id int64) (*service.User, error)
}

// stepUpSettingReader 抽象 step-up 功能开关读取能力（由 SettingService 实现）。
// 安全门控必须能区分"开关关闭"与"设置读取失败"，后者要 fail-closed。
type stepUpSettingReader interface {
	IsStepUpEnabledStrict(ctx context.Context) (bool, error)
}

// StepUpSessionKey 计算 step-up 授权的会话键：
// 优先绑定当前会话（refresh token family），无会话 ID 的旧 token 退化为用户级键。
func StepUpSessionKey(c *gin.Context, userID int64) string {
	if sid := c.GetString(ContextKeySessionID); sid != "" {
		return sid
	}
	return fmt.Sprintf("u%d", userID)
}

// NewStepUpAuthMiddleware 创建敏感操作 step-up 2FA 门控中间件。
//
// 功能开关 step_up_enabled（默认关闭）关闭时中间件直接放行，行为与门控引入前一致。
// 开启时的通过条件（全部满足）：
//  1. 必须是 JWT 认证的真人会话——admin API key（机器凭证）一律拒绝
//  2. 当前用户已启用 TOTP（未启用则拒绝并提示先启用 2FA）
//  3. 当前会话在有效期内完成过 TOTP step-up 验证（POST /api/v1/user/totp/step-up）
//
// 失败响应使用可区分的错误码，前端据此弹出 TOTP 验证对话框后重试。
func NewStepUpAuthMiddleware(
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) StepUpAuthMiddleware {
	return StepUpAuthMiddleware(stepUpAuth(
		stepUpGrantCheckerOrNil(totpService),
		stepUpUserReaderOrNil(userService),
		stepUpSettingsOrNil(settingService),
	))
}

// stepUpGrantCheckerOrNil / stepUpUserReaderOrNil 把可能为 nil 的具体指针归一化
// 为 nil 接口，避免 typed-nil 装箱后在 enforceStepUp 里「看起来不是 nil」，
// 从而绕过装配缺失的 fail-closed 分支。
func stepUpGrantCheckerOrNil(totpService *service.TotpService) stepUpGrantChecker {
	if totpService == nil {
		return nil
	}
	return totpService
}

func stepUpUserReaderOrNil(userService *service.UserService) stepUpUserReader {
	if userService == nil {
		return nil
	}
	return userService
}

// stepUpSettingsOrNil 将可能为 nil 的具体指针归一化为接口，
// 避免 typed-nil 装箱后绕过 enforceStepUp 内的 nil 判断。
func stepUpSettingsOrNil(settingService *service.SettingService) stepUpSettingReader {
	if settingService == nil {
		return nil
	}
	return settingService
}

func stepUpAuth(grantChecker stepUpGrantChecker, userReader stepUpUserReader, settings stepUpSettingReader) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enforceStepUp(c, grantChecker, userReader, settings) {
			return
		}
		c.Next()
	}
}

// EnforceStepUp 对当前请求执行与 StepUpAuthMiddleware 相同语义的 step-up 门控，
// 供 handler 在需要按请求内容条件触发时调用（如仅当把用户角色提升为管理员时）。
// 校验失败时写入错误响应并中止请求，返回 false；通过返回 true。
func EnforceStepUp(
	c *gin.Context,
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) bool {
	return enforceStepUp(c,
		stepUpGrantCheckerOrNil(totpService),
		stepUpUserReaderOrNil(userService),
		stepUpSettingsOrNil(settingService),
	)
}

// EnforceStepUpAlways 与 EnforceStepUp 语义相同但不读取功能开关，无条件执行门控。
// 供调用方已确知门控必须生效的场景使用（如"关闭 step-up 开关"本身：调用方刚从
// 持久化设置读到开关为开启状态，不应依赖二次读取——读取失败会导致门控被跳过）。
func EnforceStepUpAlways(
	c *gin.Context,
	totpService *service.TotpService,
	userService *service.UserService,
) bool {
	return enforceStepUp(c,
		stepUpGrantCheckerOrNil(totpService),
		stepUpUserReaderOrNil(userService),
		nil,
	)
}

func enforceStepUp(c *gin.Context, grantChecker stepUpGrantChecker, userReader stepUpUserReader, settings stepUpSettingReader) bool {
	// 功能开关关闭时直接放行（含 admin API key），恢复门控引入前的行为。
	// settings 为 nil 时保持门控（fail-closed）：正常装配不会出现 nil。
	// 开关读取失败同样 fail-closed：不能因为存储抖动把安全门控静默关掉。
	if settings != nil {
		enabled, err := settings.IsStepUpEnabledStrict(c.Request.Context())
		if err != nil {
			AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
			return false
		}
		if !enabled {
			return true
		}
	}

	// 机器凭据（旧版全权 admin_api_key 与受限 adm_sc_ 密钥）一律无法完成
	// step-up：敏感资源（含只读）只能由真人 JWT 会话访问。
	switch c.GetString("auth_method") {
	case service.AuditAuthMethodAdminAPIKey, service.AuditAuthMethodScopedAdminAPIKey:
		// 标记给审计中间件：机器凭据访问敏感资源被拒要落安全事件。
		c.Set(ContextKeyStepUpMachineDenied, true)
		AbortWithError(c, 403, "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
			"Admin API key cannot access this endpoint; a two-factor verified admin session is required")
		return false
	}

	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		AbortWithError(c, 401, "UNAUTHORIZED", "Authorization required")
		return false
	}

	// 装配缺失（totp/user 依赖为 nil）时 fail-closed。handler 级 EnforceStepUp
	// 调用点如果拿不到依赖，绝不能因为「没法检查」而放行。
	if grantChecker == nil || userReader == nil {
		AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
		return false
	}

	user, err := userReader.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to load user")
		return false
	}
	if !user.TotpEnabled {
		AbortWithError(c, 403, "STEP_UP_TOTP_NOT_ENABLED",
			"This operation requires two-factor authentication; please enable TOTP first")
		return false
	}

	sessionKey := StepUpSessionKey(c, subject.UserID)
	granted, err := grantChecker.HasStepUpGrant(c.Request.Context(), subject.UserID, sessionKey)
	if err != nil {
		// 安全门控故障时选择 fail-closed。
		AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
		return false
	}
	if !granted {
		AbortWithError(c, 403, "STEP_UP_REQUIRED",
			"This operation requires recent two-factor verification")
		return false
	}

	// 走到这里说明本次敏感操作「本来该要验证码，但因为 5 分钟窗口还有效而跳过」。
	// 必须把这件事显式回传，不能静默跳过：前端右下角据此展示剩余免授权倒计时。
	remaining, ok, err := grantChecker.StepUpGrantRemaining(c.Request.Context(), subject.UserID, sessionKey)
	if err != nil {
		// 安全门控故障时选择 fail-closed：读不到剩余窗口就不放行。
		AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
		return false
	}
	if !ok {
		// 授权在 Has 与 Remaining 两次读取之间过期：按未验证处理，让前端重新弹码。
		AbortWithError(c, 403, "STEP_UP_REQUIRED",
			"This operation requires recent two-factor verification")
		return false
	}
	remainingSeconds := int64(remaining / time.Second)
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	c.Header(StepUpSkippedHeader, "1")
	c.Header(StepUpRemainingHeader, strconv.FormatInt(remainingSeconds, 10))

	return true
}
