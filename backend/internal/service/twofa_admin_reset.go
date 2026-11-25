package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 管理端「破玻璃」重置用户 TOTP。
//
// 背景：用户忘了 2FA 时，以前只能直接改库清空 totp_secret_encrypted——无审计、
// 不触发 step-up 授权撤销、前端不可见。本文件把这件事做成正规功能，老板确认的
// 三点约束：
//
//  1. 正规化：管理端接口 POST /api/v1/admin/users/:id/reset-2fa，不再手改库；
//  2. 原因必填：reason 1~500 字，缺省即 400，并原样写进审计；
//  3. 必须通知用户：邮件告知「双因素认证已被管理员重置」+ 原因 + 操作时间。
//
// 边界（与需求一致）：
//   - 只重置 TOTP。Passkey 凭据原样保留（不删除、不撤销），返回体与邮件都写明，
//     避免管理员误以为一次重置清空了全部因子。
//   - 目标用户是管理员也允许重置：管理员自己忘了 2FA 时这是唯一的自救路径，
//     但审计里会显式标注 target_role=admin。
//   - 无论写库成败，都撤销该用户所有因子的 step-up 授权：2FA 状态一变更，
//     旧的 5 分钟免验证窗口就必须失效（复用 #588 的按因子枚举撤销）。
//   - 审计里绝不记录 totp_secret（任何形式，含前缀/掩码），只记「重置前的开关
//     状态与设置时间」。

const (
	// Admin2FAResetReasonMaxLength 是原因字段的长度上限（按字符计，不是字节）。
	Admin2FAResetReasonMaxLength = 500
	// AuditActionAdminUserReset2FA 是审计记录的动作名，与 auditActionOverrides
	// 里的路由动作名保持一致，便于按动作检索。
	AuditActionAdminUserReset2FA = "admin.user.reset_2fa"
)

var (
	// ErrAdmin2FAResetReasonRequired 原因字段为空。
	ErrAdmin2FAResetReasonRequired = infraerrors.BadRequest(
		"RESET_2FA_REASON_REQUIRED", "a reset reason is required")
	// ErrAdmin2FAResetReasonTooLong 原因字段超长。
	ErrAdmin2FAResetReasonTooLong = infraerrors.BadRequest(
		"RESET_2FA_REASON_TOO_LONG",
		fmt.Sprintf("the reset reason must not exceed %d characters", Admin2FAResetReasonMaxLength))
	// ErrAdmin2FAResetTargetNotEnabled 目标用户没有启用 TOTP。
	ErrAdmin2FAResetTargetNotEnabled = infraerrors.BadRequest(
		"RESET_2FA_TARGET_NOT_ENABLED", "this user does not have 2FA enabled")
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
	TargetIsAdmin bool   `json:"target_is_admin"`
	Reason        string `json:"reason"`
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
	// RevokedStepUpGrants 报告全部因子的 step-up 授权是否确认撤销。
	RevokedStepUpGrants bool      `json:"revoked_step_up_grants"`
	ResetAt             time.Time `json:"reset_at"`
}

// Admin2FAResetService 是管理端破玻璃重置的依赖装配点。
//
// 单独成一个服务（而不是塞进 TotpService）的原因：
//   - TotpService 的构造函数是用户自助 2FA 流程用的，破玻璃只需要「清 TOTP
//     字段」+「撤销授权」+「邮件」+「审计」四件事，不该让 TotpService 额外
//     依赖审计仓库与通知邮件服务；
//   - 装配顺序上（wire_gen）通知邮件服务与审计服务都已就绪，可以走 Provide 函数。
type Admin2FAResetService struct {
	userRepo     UserRepository
	totp         *TotpService
	refreshCache RefreshTokenCache
	stepUp       StepUpGrantRevoker
	notifications *NotificationEmailService
	audit        *AuditLogService
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
		userRepo:     deps.UserRepo,
		totp:         deps.Totp,
		refreshCache: deps.RefreshCache,
		stepUp:       deps.StepUp,
		notifications: deps.Notifications,
		audit:        deps.Audit,
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
// 调用方（handler）负责 step-up 门控与机器凭据拒绝；本函数只做业务：
// 参数校验 → 读目标用户 → 撤销全部因子的 step-up 授权 → 清 TOTP → 发邮件 → 审计。
//
// 错误语义：
//   - 目标用户不存在 → ErrUserNotFound（HTTP 404）
//   - 目标用户没启用 2FA → ErrAdmin2FAResetTargetNotEnabled（HTTP 400）
//   - reason 空/超长 → 400
func (s *Admin2FAResetService) AdminResetUserTOTP(ctx context.Context, input Admin2FAResetInput) (*Admin2FAResetResult, error) {
	if s == nil || s.userRepo == nil || s.totp == nil {
		return nil, ErrAdmin2FAResetUnavailable
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		return nil, ErrAdmin2FAResetReasonRequired
	}
	if utf8.RuneCountInString(reason) > Admin2FAResetReasonMaxLength {
		return nil, ErrAdmin2FAResetReasonTooLong
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

	// 撤销放在写库之前：即使后面写库失败，也不能让旧的 5 分钟免验证窗口活着。
	revoked := true
	if err := s.revokeStepUpGrants(ctx, user.ID); err != nil {
		revoked = false
		slog.Warn("admin 2fa reset: revoke step-up grants failed",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID, "err", err)
	}

	resetter, ok := s.userRepo.(TotpAdminResetter)
	if !ok {
		return nil, ErrAdmin2FAResetUnavailable
	}
	if err := resetter.ResetTotpForAdmin(ctx, user.ID); err != nil {
		// 撤销已经发生，但重置没成：把真实错误上抛，由 handler 映射成 400/5xx。
		return nil, err
	}

	// Passkey 因子刻意不动：只读一次用于返回体与邮件说明。
	passkeyBound, err := s.totp.PasskeyFactorBound(ctx, user.ID)
	if err != nil {
		// 读不到就按「未绑定」返回会让邮件错误地宣称「只剩密码」，直接 5xx。
		return nil, fmt.Errorf("read passkey factor after admin 2fa reset: %w", err)
	}
	remaining := make([]string, 0, 1)
	if passkeyBound {
		remaining = append(remaining, FactorPasskey.String())
	}

	resetAt := time.Now().UTC()
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
		RevokedStepUpGrants:   revoked,
		ResetAt:               resetAt,
	}
	result.NotificationQueued = s.sendNotice(ctx, input, user, result)

	// 审计：结构化的重置前后状态，绝不写 totp_secret。
	s.auditReset(input, user, result)
	return result, nil
}

// revokeStepUpGrants 撤销目标用户「所有因子 × 所有会话」的 step-up 授权。
//
// 两类授权都要撤：
//  1. 2FA 登录凭证（two_factor_grant:*，按用户索引集合删）——用户用旧的
//     "5 分钟免重复验证" Cookie 登录另一个会话的风险；
//  2. step-up 免验证窗口（totp:stepup:<uid>:<factor>:<session>）——重置前刚
//     过 2FA 的浏览器还能在 5 分钟内跑敏感操作。
//
// 会话键来自该用户的 refresh token 会话族（FamilyID）+ 遗留键 u<uid>，
// 绝不 SCAN 整库。任一环节失败都返回错误，由调用方如实上报，不谎报「已撤销」。
func (s *Admin2FAResetService) revokeStepUpGrants(ctx context.Context, userID int64) error {
	var errs []error
	if s.totp != nil {
		if err := s.totp.RevokeTwoFactorGrantsForUser(ctx, userID); err != nil {
			errs = append(errs, fmt.Errorf("revoke two-factor grants: %w", err))
		}
	}
	if s.stepUp == nil {
		errs = append(errs, errors.New("step-up grant store unavailable"))
	} else {
		sessionKeys := s.sessionKeysForUser(ctx, userID)
		if _, err := s.stepUp.RevokeStepUpGrantsForUser(ctx, userID, sessionKeys); err != nil {
			errs = append(errs, fmt.Errorf("revoke step-up grants: %w", err))
		}
	}
	return errors.Join(errs...)
}

// sessionKeysForUser 列出该用户已知的会话键（refresh token 会话族 ID）。
//
// 列举失败时返回空切片：撤销函数内部仍会处理遗留键 u<uid>，比因为读不到
// 会话列表就整体跳过撤销更安全。
func (s *Admin2FAResetService) sessionKeysForUser(ctx context.Context, userID int64) []string {
	if s.refreshCache == nil || userID <= 0 {
		return nil
	}
	hashes, err := s.refreshCache.GetUserTokenHashes(ctx, userID)
	if err != nil {
		slog.Warn("admin 2fa reset: list refresh tokens for session keys failed",
			"target_user_id", userID, "err", err)
		return nil
	}
	keys := make([]string, 0, len(hashes))
	seen := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		data, err := s.refreshCache.GetRefreshToken(ctx, hash)
		if err != nil || data == nil || data.FamilyID == "" || data.UserID != userID {
			continue
		}
		if _, ok := seen[data.FamilyID]; ok {
			continue
		}
		seen[data.FamilyID] = struct{}{}
		keys = append(keys, data.FamilyID)
	}
	return keys
}

// sendNotice 发送「管理员重置了你的双因素认证」通知邮件。
//
// 返回「是否已发出」。邮件是必须通知用户的动作，但重置本身已提交，不能因为
// SMTP 抖动让整个接口报失败——那样管理员会重试，而重试会拿到「该用户未启用
// 2FA」的 400。所以失败只降级成 result 标记 + 日志 + 审计字段。
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
	baseURL := strings.TrimSpace(s.notifications.baseURL(ctx))
	rebindURL := ""
	if baseURL != "" {
		rebindURL = strings.TrimSuffix(baseURL, "/") + "/settings/security"
	}
	variables := map[string]string{
		"reset_reason":        result.Reason,
		"reset_at":            result.ResetAt.Format("2006-01-02 15:04:05 UTC"),
		"remaining_factors":   admin2FAResetRemainingFactorText(result.RemainingFactors),
		"passkey_retained":    admin2FAResetYesNo(result.PasskeyBound),
		"target_is_admin":     admin2FAResetYesNo(result.TargetIsAdmin),
		"operator_name":       admin2FAResetOperatorName(input),
		"rebind_url":          rebindURL,
		"rebind_link_display": firstNonEmpty(rebindURL, "-"),
	}
	// 每次重置都必须是独立的一封：SourceID 带时间戳，否则投递台账会把同一用户的
	// 第二次重置当成「已发过」而静默吞掉。
	sourceID := fmt.Sprintf("%d-%d", user.ID, result.ResetAt.UnixNano())
	err := s.notifications.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventAdminSecurityTwoFactorReset,
		RecipientEmail: recipient,
		RecipientName:  firstNonEmpty(strings.TrimSpace(user.Username), emailRecipientName(recipient)),
		UserID:         user.ID,
		SourceType:     "admin_2fa_reset",
		SourceID:       sourceID,
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
// 必须保证「接口返回成功时审计已在库里」。
func (s *Admin2FAResetService) auditReset(input Admin2FAResetInput, user *User, result *Admin2FAResetResult) {
	if s.audit == nil {
		slog.Error("admin 2fa reset: audit service unavailable, reset not audited",
			"target_user_id", user.ID, "actor_user_id", input.ActorUserID)
		return
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
			"target_user_id":         user.ID,
			"target_user_email":      user.Email,
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
	}
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

// admin2FAResetOperatorName 只回传管理员邮箱，不回传任何凭证。
// 邮件里让人知道「是哪个管理员操作的」，便于用户质疑时对账。
func admin2FAResetOperatorName(input Admin2FAResetInput) string {
	if email := strings.TrimSpace(input.ActorEmail); email != "" {
		return email
	}
	if input.ActorUserID > 0 {
		return "#" + strconv.FormatInt(input.ActorUserID, 10)
	}
	return "-"
}

// errorsIsUserSecurityStateChanged 供 handler 判定并发冲突（目标用户在此期间
// 自己重绑/关闭了 TOTP）。
func errorsIsUserSecurityStateChanged(err error) bool {
	return errors.Is(err, ErrUserSecurityStateChanged)
}
