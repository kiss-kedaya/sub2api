package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/textsanitize"
	"github.com/google/uuid"
)

// 管理端「破玻璃」重置用户 TOTP。
//
// 背景：用户忘了 2FA 时，以前只能直接改库清空 totp_secret_encrypted——无审计、
// 不触发 step-up 授权撤销、前端不可见。本文件把这件事做成正规功能，老板确认的
// 三点约束：
//
//  1. 正规化：管理端接口 POST /api/v1/admin/users/:id/reset-2fa，不再手改库；
//  2. 原因必填：reason 1~500 字（入库前清洗成纯文本），缺省即 400，并写进审计；
//  3. 必须通知用户：邮件告知「双因素认证已被管理员重置」+ 原因 + 操作时间。
//
// 边界（与需求一致）：
//   - 只重置 TOTP。Passkey 凭据原样保留（不删除、不撤销），返回体与邮件都写明，
//     避免管理员误以为一次重置清空了全部因子。
//   - 目标用户是其他管理员也允许重置，审计里显式标注 target_role=admin；
//     但不允许重置自己：过 step-up 本身就要 2FA，真丢了自己的码这条路用不了，
//     只会便宜了窗口内的会话劫持。自己丢码走服务器上的命令行（-reset-admin-2fa）。
//   - 唯一的关键步骤是写库。读 Passkey 在写库之前（读失败就整体不写）；写库成功
//     之后的善后（撤销会话与授权、发邮件、写审计）任何一步失败都只记日志，并在
//     返回体的 incomplete_steps 里列出，绝不把已提交的重置变成 5xx（否则管理员
//     重试只会撞上「未启用 2FA」）；审计行在写库成功后一定会尝试落库。
//   - 撤销在写库之后：与 #641 命令行同一个清理函数（RevokeUserSessionsForTwoFactorReset，
//     撤销全部 refresh token / 已签发的 access token / step-up 授权 / 绑定预算）；写库失败时什么都不动，
//     不会出现「用户被登出了、2FA 却没变」的半成功状态。被重置的用户在所有设备上立刻掉线，需要重新登录。
//   - 审计里绝不记录 totp_secret（任何形式，含前缀/掩码），也不记目标用户邮箱
//     明文（只记不可逆指纹），只记「重置前的开关状态与设置时间」。

const (
	// Admin2FAResetReasonMaxLength 是原因字段的长度上限（按字符计，不是字节）。
	Admin2FAResetReasonMaxLength = 500
	// AuditActionAdminUserReset2FA 是审计记录的动作名，与 auditActionOverrides
	// 里的路由动作名保持一致，便于按动作检索。
	AuditActionAdminUserReset2FA = "admin.user.reset_2fa"

	// Admin2FAResetFreshStepUpWindow 是破玻璃重置要求的「新鲜」二次验证窗口：
	// 调用者的 step-up 授权必须是这么久之内刚签发的。普通敏感操作共用 5 分钟窗口
	// （StepUpGrantTTL），但重置别人的 2FA 是能降低他人账号安全强度的高危动作，
	// 窗口内任何旧验证都放行就和「旧授权不能替代当前 TOTP」不一致（见 TotpService.Disable）。
	Admin2FAResetFreshStepUpWindow = 60 * time.Second

	// admin2FAResetPostCommitTimeout 是写库提交之后善后（撤销、发邮件）的整体时限。
	admin2FAResetPostCommitTimeout = 30 * time.Second

	// 善后步骤名，出现在返回体 incomplete_steps 与审计 incomplete_steps 里。
	Admin2FAResetStepSessionRevocation = "session_revocation"
	Admin2FAResetStepNotification      = "notification"
	Admin2FAResetStepAudit             = "audit"
)

var (
	// ErrAdmin2FAResetReasonRequired 原因字段为空（清洗成纯文本之后为空也算）。
	ErrAdmin2FAResetReasonRequired = infraerrors.BadRequest(
		"RESET_2FA_REASON_REQUIRED", "a reset reason is required")
	// ErrAdmin2FAResetReasonTooLong 原因字段超长。
	ErrAdmin2FAResetReasonTooLong = infraerrors.BadRequest(
		"RESET_2FA_REASON_TOO_LONG",
		fmt.Sprintf("the reset reason must not exceed %d characters", Admin2FAResetReasonMaxLength))
	// ErrAdmin2FAResetTargetNotEnabled 目标用户没有启用 TOTP。
	ErrAdmin2FAResetTargetNotEnabled = infraerrors.BadRequest(
		"RESET_2FA_TARGET_NOT_ENABLED", "this user does not have 2FA enabled")
	// ErrAdmin2FAResetSelfTarget 管理员试图重置自己的 2FA。
	ErrAdmin2FAResetSelfTarget = infraerrors.BadRequest(
		"RESET_2FA_SELF_TARGET",
		"you cannot reset your own two-factor authentication through the API; if you lost your own authenticator, use the server-side break-glass command (-reset-admin-2fa)")
	// ErrAdmin2FAResetUnavailable 重置能力装配缺失。
	ErrAdmin2FAResetUnavailable = infraerrors.ServiceUnavailable(
		"RESET_2FA_UNAVAILABLE", "two-factor reset service unavailable")
)

// Admin2FAResetInput 是管理端重置请求的入参（actor 来自鉴权上下文，不在请求体里）。
type Admin2FAResetInput struct {
	TargetUserID int64
	Reason       string
	ActorUserID  int64
	ActorEmail   string
	ActorRole    string
	AuthMethod   string
	ClientIP     string
	UserAgent    string
	RequestID    string
}

// Admin2FAResetResult 是重置成功的返回体。
//
// 所有字段都会同时出现在 API 响应与审计里，因此只能是标量、无凭证。
type Admin2FAResetResult struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	// TargetRole 是目标用户的角色；为 admin 时前端要额外提示。
	TargetRole string `json:"target_role"`
	// TargetIsAdmin 便于前端直接判断（避免比较字符串）。
	TargetIsAdmin bool `json:"target_is_admin"`
	// Reason 是清洗成纯文本之后真正入库、写进邮件的原因。
	Reason string `json:"reason"`
	// TotpPreviouslyEnabled / TotpEnabledAt 是重置前的状态，重置后必为 false / 空。
	TotpPreviouslyEnabled bool       `json:"totp_previously_enabled"`
	TotpEnabledAt         *time.Time `json:"totp_enabled_at,omitempty"`
	TotpEnabledAfter      bool       `json:"totp_enabled"`
	// PasskeyBound 表示目标用户仍有 Passkey 因子（本次不重置）。只绑 TOTP 的
	// 账号重置后只剩密码；同时绑 Passkey 的账号仍需 Passkey 登录。
	PasskeyBound bool `json:"passkey_bound"`
	// NotificationQueued 表示通知邮件已发出；false 时前端必须提示管理员另行
	// 通知用户，不能报「全部完成」。
	NotificationQueued bool `json:"notification_queued"`
	// RemainingFactors 是重置后该用户仍需验证的因子（通常是 [passkey] 或空）。
	RemainingFactors []string `json:"remaining_factors"`
	// RevokedStepUpGrants 报告该用户的会话与全部因子的 step-up 授权是否确认撤销
	// （字段名沿用旧版，含义已扩展到 refresh token、已签发的 access token 与绑定预算）。
	RevokedStepUpGrants bool `json:"revoked_step_up_grants"`
	// IncompleteSteps 列出重置已经提交、但没能完成的善后步骤
	// （session_revocation / notification / audit）；空表示全部完成。
	IncompleteSteps []string  `json:"incomplete_steps"`
	ResetAt         time.Time `json:"reset_at"`
}

// Admin2FAResetService 是管理端破玻璃重置的依赖装配点。
//
// 单独成一个服务（而不是塞进 TotpService）的原因：
//   - TotpService 的构造函数是用户自助 2FA 流程用的，破玻璃只需要「清 TOTP
//     字段」+「撤销授权」+「邮件」+「审计」四件事，不该让 TotpService 额外
//     依赖审计仓库与通知邮件服务；
//   - 装配顺序上（wire_gen）通知邮件服务与审计服务都已就绪，可以走 Provide 函数。
type Admin2FAResetService struct {
	userRepo      UserRepository
	totp          *TotpService
	refreshCache  RefreshTokenCache
	stepUp        StepUpGrantRevoker
	notifications *NotificationEmailService
	audit         *AuditLogService
}

// Admin2FAResetDeps 是构造参数包，避免构造函数长到不可读。
type Admin2FAResetDeps struct {
	UserRepo      UserRepository
	Totp          *TotpService
	RefreshCache  RefreshTokenCache
	StepUp        StepUpGrantRevoker
	Notifications *NotificationEmailService
	Audit         *AuditLogService
}

// StepUpGrantRevoker 撤销某用户全部会话、全部因子的 step-up 授权键。
// 由 repository.TotpCache 实现（键格式只在那一个文件定义）。
//
// 三个入参都是撤销所需的最小上下文：userID 定位用户，sessionKeys 是已知的
// 会话键（refresh token family），加上实现内部自动补的遗留键 u<userId>。
type StepUpGrantRevoker interface {
	RevokeStepUpGrantsForUser(ctx context.Context, userID int64, sessionKeys []string) (int64, error)
}

// NewAdmin2FAResetService 构造管理端破玻璃重置服务。
func NewAdmin2FAResetService(deps Admin2FAResetDeps) *Admin2FAResetService {
	return &Admin2FAResetService{
		userRepo:      deps.UserRepo,
		totp:          deps.Totp,
		refreshCache:  deps.RefreshCache,
		stepUp:        deps.StepUp,
		notifications: deps.Notifications,
		audit:         deps.Audit,
	}
}

// TotpAdminResetter 把「按 ID 无条件清空 TOTP 字段」收敛成一个可测试的端口。
//
// 与 TotpRoleConditionalDisabler 的区别：后者绑定「验证时的角色 + 密码密文」，
// 是用户自助关闭用的；管理员破玻璃没有用户的动态码可验证，只有管理端鉴权 +
// step-up + 必填原因。但 SQL 层仍然要求「当前处于启用状态」，避免并发重绑后
// 把新鲜密文误清（详见 ResetTotpForAdmin 实现）。
type TotpAdminResetter interface {
	ResetTotpForAdmin(ctx context.Context, userID int64) error
}

// AdminResetUserTOTP 是管理端破玻璃重置的唯一实现入口。
//
// 调用方（handler）负责 step-up 门控、新鲜度与机器凭据拒绝；本函数只做业务：
// 参数校验 → 读目标用户与 Passkey → 清 TOTP → 撤销会话与授权 → 发邮件 → 审计。
//
// 错误语义（全部发生在写库之前，什么都没改）：
//   - 目标是操作者自己 → ErrAdmin2FAResetSelfTarget（HTTP 400）
//   - 目标用户不存在 → ErrUserNotFound（HTTP 404）
//   - 目标用户没启用 2FA → ErrAdmin2FAResetTargetNotEnabled（HTTP 400）
//   - reason 清洗后为空/超长 → 400
//
// 写库成功之后本函数不再返回错误：善后失败写进 result.IncompleteSteps。
func (s *Admin2FAResetService) AdminResetUserTOTP(ctx context.Context, input Admin2FAResetInput) (*Admin2FAResetResult, error) {
	if s == nil || s.userRepo == nil || s.totp == nil {
		return nil, ErrAdmin2FAResetUnavailable
	}
	if input.ActorUserID > 0 && input.ActorUserID == input.TargetUserID {
		return nil, ErrAdmin2FAResetSelfTarget
	}
	reason, err := normalizeAdmin2FAResetReason(input.Reason)
	if err != nil {
		return nil, err
	}
	if input.TargetUserID <= 0 {
		return nil, ErrUserNotFound
	}

	user, err := s.userRepo.GetByID(ctx, input.TargetUserID)
	if err != nil {
		// 仓储层把「不存在」翻成 ErrUserNotFound；其他错误原样上抛（5xx）。
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	if !user.TotpEnabled {
		return nil, ErrAdmin2FAResetTargetNotEnabled
	}
	resetter, ok := s.userRepo.(TotpAdminResetter)
	if !ok {
		return nil, ErrAdmin2FAResetUnavailable
	}

	// Passkey 因子刻意不动：只读一次用于返回体与邮件说明。读失败就按「未绑定」
	// 写邮件会错误地宣称「只剩密码」，所以必须在写库之前读：读不到就整体不写。
	passkeyBound, err := s.totp.PasskeyFactorBound(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("read passkey factor before admin 2fa reset: %w", err)
	}

	// 唯一的关键步骤。失败原样上抛，此时没有撤销任何东西、没有发邮件、没有审计。
	if err := resetter.ResetTotpForAdmin(ctx, user.ID); err != nil {
		return nil, err
	}

	// ---- 重置已提交。从这里开始不再返回错误，只记录没做完的善后步骤。----
	remaining := make([]string, 0, 1)
	if passkeyBound {
		remaining = append(remaining, FactorPasskey.String())
	}
	result := &Admin2FAResetResult{
		UserID:                user.ID,
		Email:                 user.Email,
		TargetRole:            user.Role,
		TargetIsAdmin:         user.IsAdmin(),
		Reason:                reason,
		TotpPreviouslyEnabled: user.TotpEnabled,
		TotpEnabledAt:         user.TotpEnabledAt,
		TotpEnabledAfter:      false,
		PasskeyBound:          passkeyBound,
		RemainingFactors:      remaining,
		IncompleteSteps:       []string{},
		ResetAt:               time.Now().UTC(),
	}

	// 善后不跟随请求取消：管理员在写库提交后断开连接，不代表这次重置没发生，旧会话照样
	// 必须撤、用户照样必须被通知。给它们一个独立的有限时限，免得 Redis/SMTP 卡住拖死请求。
	postCtx, cancelPost := context.WithTimeout(context.WithoutCancel(ctx), admin2FAResetPostCommitTimeout)
	defer cancelPost()

	if err := s.revokeSessionsAfterReset(postCtx, user.ID); err != nil {
		slog.Warn("admin 2fa reset: session and step-up revocation incomplete",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID, "err", err)
		result.IncompleteSteps = append(result.IncompleteSteps, Admin2FAResetStepSessionRevocation)
	} else {
		result.RevokedStepUpGrants = true
	}

	result.NotificationQueued = s.sendNotice(postCtx, input, user, result)
	if !result.NotificationQueued {
		result.IncompleteSteps = append(result.IncompleteSteps, Admin2FAResetStepNotification)
	}

	// 审计：结构化的重置前后状态，绝不写 totp_secret。审计行本身写失败时只能
	// 在返回体里说明（它没法记录自己的失败），路由级审计中间件仍会留一条请求记录。
	if !s.auditReset(input, user, result) {
		result.IncompleteSteps = append(result.IncompleteSteps, Admin2FAResetStepAudit)
	}
	return result, nil
}

// normalizeAdmin2FAResetReason 把原因清洗成纯文本并校验长度。
//
// 清洗与工单标题、用户名、Key 名用同一套（textsanitize）：HTML 标记、控制字符、
// 零宽字符与 U+202E 之类的双向覆写字符都会被去掉。NUL 尤其要去掉：它会让
// PostgreSQL 的 JSONB 拒收，重置成功了但服务层审计行丢失。清洗后为空按「必填」拒绝。
func normalizeAdmin2FAResetReason(raw string) (string, error) {
	reason := textsanitize.PlainText(raw)
	if reason == "" {
		return "", ErrAdmin2FAResetReasonRequired
	}
	if utf8.RuneCountInString(reason) > Admin2FAResetReasonMaxLength {
		return "", ErrAdmin2FAResetReasonTooLong
	}
	return reason, nil
}

// revokeSessionsAfterReset 撤销目标用户「所有会话 × 所有因子」的旧状态。
//
// 三类都要撤：
//  1. refresh token / 已签发的 access token / step-up 授权（totp:stepup:<uid>:<factor>:<session>，
//     含遗留键 u<uid>）/ 绑定预算（totp:attempts）/ 绑定会话（totp:setup）：与 #641 命令行共用
//     RevokeUserSessionsForTwoFactorReset，重置前刚过 2FA 的浏览器不能在 5 分钟内
//     继续跑敏感操作，账号被盗时攻击者已有的会话（含尚未过期的 access token）也被踢下线，
//     忘码输错 5 次的用户重新绑定不会被旧预算卡住；
//  2. 2FA 登录凭证（two_factor_grant:*，按用户索引集合删）——用户用旧的
//     "5 分钟免重复验证" Cookie 登录另一个会话的风险。
//
// 任一环节失败都继续做完其余环节，错误合并返回，由调用方如实上报，不谎报「已撤销」。
func (s *Admin2FAResetService) revokeSessionsAfterReset(ctx context.Context, userID int64) error {
	var errs []error
	if err := s.totp.RevokeTwoFactorGrantsForUser(ctx, userID); err != nil {
		errs = append(errs, fmt.Errorf("revoke two-factor grants: %w", err))
	}
	var state TwoFactorResetStateStore
	if s.totp.cache != nil {
		state = s.totp.cache
	}
	if err := RevokeUserSessionsForTwoFactorReset(ctx, userID, s.refreshCache, s.stepUp, state); err != nil {
		errs = append(errs, fmt.Errorf("revoke sessions and step-up grants: %w", err))
	}
	return errors.Join(errs...)
}

// admin2FAResetSourceID 生成每次重置独有的邮件投递台账键：投递台账按 SourceID 去重，
// 键里只有时间戳时（旧实现用 UnixNano）同一用户两次重置在时钟精度内会撞键，第二封
// 邮件被静默吞掉却仍报「已发出」。带上随机 UUID，每次重置必然是独立的一封。
func admin2FAResetSourceID(userID int64) string {
	return fmt.Sprintf("%d-%s", userID, uuid.NewString())
}

// sendNotice 发送「管理员重置了你的双因素认证」通知邮件。
//
// 返回「是否已发出」。邮件是必须通知用户的动作，但重置本身已提交，不能因为
// SMTP 抖动让整个接口报失败——那样管理员会重试，而重试会拿到「该用户未启用
// 2FA」的 400。所以失败只降级成 result 标记 + 日志 + 审计字段。
//
// 邮件正文只写「管理员」+ 原因 + 时间，不带操作者的邮箱/姓名：邮件发给目标用户，
// 目标账号可能正是被攻击者控制的那个，操作管理员的邮箱不该泄给它；用户质疑时
// 凭操作时间 + 审计记录对账即可。
func (s *Admin2FAResetService) sendNotice(
	ctx context.Context,
	input Admin2FAResetInput,
	user *User,
	result *Admin2FAResetResult,
) bool {
	if s.notifications == nil {
		slog.Warn("admin 2fa reset: notification email service unavailable",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID)
		return false
	}
	recipient := strings.TrimSpace(user.Email)
	if recipient == "" {
		slog.Warn("admin 2fa reset: target user has no email", "target_user_id", user.ID)
		return false
	}
	// 重新绑定入口在前端的个人资料页（/profile，ProfileTotpCard）。基址沿用工单邮件
	// 同一套取法：站点前端地址（frontend_url）优先，取不到才回退 api_base_url，
	// 并校验协议/主机，非法值一律当作没配。
	rebindURL := ticketEmailLink(ctx, s.notifications.settingRepo, "/profile")
	variables := map[string]string{
		"reset_reason":        result.Reason,
		"reset_at":            result.ResetAt.Format("2006-01-02 15:04:05 UTC"),
		"remaining_factors":   admin2FAResetRemainingFactorText(result.RemainingFactors),
		"passkey_retained":    admin2FAResetYesNo(result.PasskeyBound),
		"target_is_admin":     admin2FAResetYesNo(result.TargetIsAdmin),
		"rebind_url":          rebindURL,
		"rebind_link_display": firstNonEmpty(rebindURL, "-"),
	}
	err := s.notifications.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventAdminSecurityTwoFactorReset,
		RecipientEmail: recipient,
		RecipientName:  firstNonEmpty(strings.TrimSpace(user.Username), emailRecipientName(recipient)),
		UserID:         user.ID,
		SourceType:     "admin_2fa_reset",
		SourceID:       admin2FAResetSourceID(user.ID),
		Variables:      variables,
	})
	if err != nil {
		slog.Warn("admin 2fa reset: notification email failed",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID,
			"recipient_hash", notificationEmailHash(recipient), "err", err)
		return false
	}
	return true
}

// auditReset 写一条同步审计（不经异步队列），因为破玻璃是低频高风险操作，
// 必须保证「接口返回成功时审计已在库里」。返回审计行是否确认落库。
func (s *Admin2FAResetService) auditReset(input Admin2FAResetInput, user *User, result *Admin2FAResetResult) bool {
	if s.audit == nil {
		slog.Error("admin 2fa reset: audit service unavailable, reset not audited",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID)
		return false
	}
	entry := &AuditLog{
		CreatedAt:  result.ResetAt,
		Action:     AuditActionAdminUserReset2FA,
		Method:     "POST",
		Path:       fmt.Sprintf("/api/v1/admin/users/%d/reset-2fa", user.ID),
		RequestID:  input.RequestID,
		ClientIP:   input.ClientIP,
		UserAgent:  input.UserAgent,
		StatusCode: 200,
		ActorEmail: input.ActorEmail,
		ActorRole:  input.ActorRole,
		AuthMethod: input.AuthMethod,
		Extra: map[string]any{
			"target_user_id": user.ID,
			// 只存不可逆指纹，不存邮箱明文（与认证审计的 email_hash 同一套）。
			"target_email_hash":      HashAuditEmail(user.Email),
			"target_role":            user.Role,
			"target_is_admin":        user.IsAdmin(),
			"reason":                 result.Reason,
			"totp_enabled_before":    result.TotpPreviouslyEnabled,
			"totp_enabled_at_before": admin2FAResetEnabledAtValue(result.TotpEnabledAt),
			"totp_enabled_after":     result.TotpEnabledAfter,
			"passkey_bound":          result.PasskeyBound,
			"remaining_factors":      strings.Join(result.RemainingFactors, ","),
			"revoked_step_up_grants": result.RevokedStepUpGrants,
			"notification_queued":    result.NotificationQueued,
			"incomplete_steps":       strings.Join(result.IncompleteSteps, ","),
			"reset_at":               result.ResetAt.Format(time.RFC3339),
		},
	}
	if input.ActorUserID > 0 {
		actorID := input.ActorUserID
		entry.ActorUserID = &actorID
	}
	// 审计写入不能跟随请求取消：管理员断开连接不代表这次重置没发生。
	if err := s.audit.RecordNow(context.WithoutCancel(context.Background()), entry); err != nil {
		slog.Error("admin 2fa reset: audit insert failed",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID, "err", err)
		return false
	}
	return true
}

func admin2FAResetEnabledAtValue(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func admin2FAResetRemainingFactorText(factors []string) string {
	if len(factors) == 0 {
		return "-"
	}
	return strings.Join(factors, ", ")
}

func admin2FAResetYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
