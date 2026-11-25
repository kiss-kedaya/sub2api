package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 双因子因子策略（TOTP / Passkey）。
//
// 老板定下的策略（本文件是唯一判定入口，登录与 step-up 都必须走这里）：
//
//  1. 只绑定 TOTP：登录 = 密码 + TOTP；风险操作 step-up = TOTP（现状不变）。
//  2. 只绑定 Passkey：登录 = 密码 + Passkey；风险操作 step-up = Passkey。
//  3. 两者都绑定：登录 = 密码 + TOTP + Passkey；风险操作要求两个因子都有
//     有效的 5 分钟 step-up 凭证。
//  4. 每个因子独立签发 5 分钟凭证，同一因子 5 分钟内免重复验证。
//  5. 任何一步失败都 fail-closed：不允许用另一个因子的凭证降级通过。
//
// 「已绑定」的唯一判据：
//   - TOTP：users.totp_enabled = true；
//   - Passkey：passkey_credentials 里至少有一条该用户的记录。
//
// 全局功能开关只控制「能否注册/验证」，不参与「是否必须验证」的判定：
// 已绑定 Passkey 的账号在 Passkey 服务不可用时是 fail-closed（拒绝），不会
// 静默降级成单因子。
type Factor string

const (
	// FactorTOTP 动态口令因子。
	FactorTOTP Factor = "totp"
	// FactorPasskey WebAuthn 通行密钥因子。
	FactorPasskey Factor = "passkey"
)

// ErrTwoFactorPolicyUnavailable 因子判定失败（用户/凭据存储不可用）。
// 调用方必须 fail-closed：拒绝放行而不是按「没有因子」处理。
var ErrTwoFactorPolicyUnavailable = infraerrors.ServiceUnavailable(
	"TWO_FACTOR_POLICY_UNAVAILABLE", "two-factor policy service unavailable")

// ErrTwoFactorFactorNotEnrolled 账号没有任何可用于 step-up 的因子。
var ErrTwoFactorFactorNotEnrolled = infraerrors.Forbidden(
	"STEP_UP_TOTP_NOT_ENABLED",
	"This operation requires two-factor authentication; please enable TOTP or register a passkey first")

// ErrTwoFactorPasskeyUnavailable Passkey 因子需要验证但服务不可用（未配置/全局关闭）。
// fail-closed：已绑定 Passkey 的账号不能退回密码或 TOTP 单因子。
var ErrTwoFactorPasskeyUnavailable = infraerrors.Forbidden(
	"TWO_FACTOR_PASSKEY_UNAVAILABLE",
	"Passkey verification is required for this account but passkeys are currently unavailable")

// allFactors 是受支持因子的唯一清单。Valid 由它派生，因此「能写进 Redis 的因子」
// 必然出现在 AllFactors 里——break-glass 重置 2FA 靠它枚举并撤销每个因子的
// step-up 授权键，新增因子只改这一处就不会漏撤销。
var allFactors = [...]Factor{FactorTOTP, FactorPasskey}

// AllFactors 返回全部受支持的因子（副本，调用方可随意修改）。
func AllFactors() []Factor {
	return append([]Factor(nil), allFactors[:]...)
}

// Valid 报告因子是否是受支持的取值。
func (f Factor) Valid() bool {
	return slices.Contains(allFactors[:], f)
}

// String 返回审计/接口使用的稳定字符串。
func (f Factor) String() string { return string(f) }

// FactorFromString 解析外部输入（JSON/Redis）里的因子名。
func FactorFromString(value string) (Factor, bool) {
	factor := Factor(strings.TrimSpace(value))
	return factor, factor.Valid()
}

// FactorStrings 把因子列表转成 JSON 友好的字符串切片。
func FactorStrings(factors []Factor) []string {
	if len(factors) == 0 {
		return []string{}
	}
	result := make([]string, 0, len(factors))
	for _, factor := range factors {
		result = append(result, string(factor))
	}
	return result
}

// FactorList 把字符串切片解析成因子列表，遇到未知值返回错误（fail-closed）。
func FactorList(values []string) ([]Factor, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make([]Factor, 0, len(values))
	for _, value := range values {
		factor, ok := FactorFromString(value)
		if !ok {
			return nil, fmt.Errorf("unknown two-factor factor %q", value)
		}
		result = append(result, factor)
	}
	return result, nil
}

// HasFactor 报告因子列表是否包含指定因子。
func HasFactor(factors []Factor, target Factor) bool {
	for _, factor := range factors {
		if factor == target {
			return true
		}
	}
	return false
}

// MissingFactors 返回 required 中尚未出现在 satisfied 里的因子（保持 required 顺序）。
func MissingFactors(required, satisfied []Factor) []Factor {
	if len(required) == 0 {
		return nil
	}
	missing := make([]Factor, 0, len(required))
	for _, factor := range required {
		if !HasFactor(satisfied, factor) {
			missing = append(missing, factor)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

// sortFactorsForChallenge 固定挑战顺序：先 TOTP，再 Passkey。
// RequiredFactors 本身已按该顺序产出，这里只作为防御性排序。
func sortFactorsForChallenge(factors []Factor) []Factor {
	if len(factors) < 2 {
		return factors
	}
	rank := func(factor Factor) int {
		switch factor {
		case FactorTOTP:
			return 0
		case FactorPasskey:
			return 1
		default:
			return 2
		}
	}
	sorted := append([]Factor(nil), factors...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i]) < rank(sorted[j]) })
	return sorted
}

// SetPasskeyRepository 注入 Passkey 凭据仓库，供 RequiredFactors 判定绑定状态。
// 装配缺失（nil）时视为「该部署没有 Passkey 因子」，与 Passkey 功能未配置的
// 旧部署保持一致；生产装配（service.ProvideTotpService）必定注入。
func (s *TotpService) SetPasskeyRepository(repo PasskeyRepository) {
	if s == nil {
		return
	}
	s.passkeys = repo
}

// RequiredFactors 返回该用户已绑定、因此在安全操作中必须验证的因子。
// 顺序固定：先 TOTP 后 Passkey。存储错误一律返回错误，调用方 fail-closed。
func (s *TotpService) RequiredFactors(ctx context.Context, userID int64) ([]Factor, error) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return nil, ErrTwoFactorPolicyUnavailable
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user for two-factor policy: %w", err)
	}
	return s.RequiredFactorsForUser(ctx, user)
}

// RequiredFactorsForUser 是 RequiredFactors 的已加载用户版本。
func (s *TotpService) RequiredFactorsForUser(ctx context.Context, user *User) ([]Factor, error) {
	if s == nil {
		return nil, ErrTwoFactorPolicyUnavailable
	}
	if user == nil {
		return nil, ErrTwoFactorPolicyUnavailable
	}
	factors := make([]Factor, 0, 2)
	if user.TotpEnabled {
		factors = append(factors, FactorTOTP)
	}
	// Passkey 因子只看「是否绑定了凭据」——不看全局开关。已绑定的凭据是账号
	// 安全边界的一部分，全局关闭 Passkey 服务时也必须 fail-closed。
	if s.passkeys != nil {
		records, err := s.passkeys.ListByUserID(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("list passkeys for two-factor policy: %w", err)
		}
		if len(records) > 0 {
			factors = append(factors, FactorPasskey)
		}
	}
	return sortFactorsForChallenge(factors), nil
}

// LoginRequiredFactors 返回「密码登录」必须完成的因子。
//
// 与 RequiredFactors 的唯一区别是 TOTP 沿用现有全局开关语义
// （SettingService.LoginRequiresTOTP：全局 TOTP 关闭时不要求登录动态码），
// 保证只绑 TOTP 的账号「现状不变」；Passkey 仍按是否绑定严格判定。
func (s *TotpService) LoginRequiredFactors(ctx context.Context, user *User) ([]Factor, error) {
	if s == nil || user == nil {
		return nil, ErrTwoFactorPolicyUnavailable
	}
	factors, err := s.RequiredFactorsForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	if !HasFactor(factors, FactorTOTP) {
		return factors, nil
	}
	if s.settingService == nil {
		return factors, nil
	}
	if s.settingService.LoginRequiresTOTP(ctx, user) {
		return factors, nil
	}
	// 全局 TOTP 登录要求关闭：登录不要求 TOTP，但 Passkey 绑定仍然要求。
	return MissingFactors(factors, []Factor{FactorTOTP}), nil
}

// LoginFactorsForEmail 返回邮箱对应的「已启用账号」，以及密码正确时它会被要求完成的登录因子
// （与 LoginRequiredFactors 同一判定）。账号不存在 / 未启用 / 没有任何登录因子时返回 (nil, nil, nil)。
//
// 只给登录首步的「凭据无效」伪装用（login_shape_disguise）：它要让「密码错」的诱饵挑战和
// 「密码对」的真实挑战形状一致，就必须知道真实形状。读取失败返回错误，调用方退回稳定的伪装形状。
func (s *TotpService) LoginFactorsForEmail(ctx context.Context, email string) (*User, []Factor, error) {
	if s == nil || s.userRepo == nil {
		return nil, nil, ErrTwoFactorPolicyUnavailable
	}
	// 与 AuthService.Login 用同样的原始邮箱查用户，大小写 / 别名口径保持一致。
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("load user for login disguise: %w", err)
	}
	if user == nil || !user.IsActive() {
		return nil, nil, nil
	}
	factors, err := s.LoginRequiredFactors(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	if len(factors) == 0 {
		return nil, nil, nil
	}
	return user, factors, nil
}

// StepUpEnabled 读取全局 step-up 开关（严格模式：读取失败返回错误，由调用方
// fail-closed 处理）。仅用于策略展示；真正的门控在 middleware.step_up。
func (s *TotpService) StepUpEnabled(ctx context.Context) (bool, error) {
	if s == nil || s.settingService == nil {
		return true, nil
	}
	return s.settingService.IsStepUpEnabledStrict(ctx)
}

// PasskeyFactorBound 报告用户是否绑定了 Passkey（登录/step-up 编排用）。
func (s *TotpService) PasskeyFactorBound(ctx context.Context, userID int64) (bool, error) {
	if s == nil || s.passkeys == nil || userID <= 0 {
		return false, nil
	}
	records, err := s.passkeys.ListByUserID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("list passkeys: %w", err)
	}
	return len(records) > 0, nil
}
