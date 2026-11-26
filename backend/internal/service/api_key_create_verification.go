package service

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 「创建 API Key 时是否需要验证身份」的用户开关。
//
// 背景：创建 / 批量轮换 / 会回执明文的编辑（regenerate_key）这三个出口，以前由账号绑定的因子
// 单方面决定要不要先验证身份（开了 TOTP 或绑了 Passkey 才强制，见 api_key_verification.go）。
// 老板的要求：让用户自己决定要不要这一步。
//
// 三态（存储见迁移 343 的 user_security_preferences，「auto」= 没有这一行）：
//   - auto（默认，所有存量用户）：与改造前逐字节一致——2FA 用户强制、仅有登录密码的账号不强制；
//   - on ：账号只要有任意可用因子（登录密码 / TOTP / Passkey）就必须校验，仅有密码的账号就是输登录密码；
//   - off：不要求校验，即使账号开了 2FA。请求里主动带了 verification.method 时仍按因子真验证
//     （与 verifyForUser 一贯的规则一致：带了就验，错了就拒）。
//
// 账号一个可用因子都没有（第三方登录注册、没设过本地密码、没开 2FA）时，on 无法满足：
// 一律按「不要求」处理，绝不能把人锁在创建 Key 之外；开关本身也不可配置（见 CreateVerificationConfigurable）。
//
// 改开关本身要鉴权，且只在「变宽松」时才要：用「有效要求」（effective required）改前 / 改后各算一遍，
// 从「必须校验」变成「不要求」就必须先通过一次三选一校验（与创建 Key 同一套 verifyFactor：同一份密码失败预算、
// TOTP 一次一码、Passkey 一次性票据）。这样被劫持的会话没法静默把保护关掉；变严或持平不需要校验。
// 比较的是有效要求而不是标签：auto→off 对仅有密码的账号不是放宽，on→auto 对仅有密码的账号却是放宽。
const (
	// APIKeyCreateVerificationAuto 没有显式设置：沿用默认规则。
	APIKeyCreateVerificationAuto = "auto"
	// APIKeyCreateVerificationOn 有任意可用因子就必须校验。
	APIKeyCreateVerificationOn = "on"
	// APIKeyCreateVerificationOff 不要求校验（即使开了 2FA）。
	APIKeyCreateVerificationOff = "off"

	// apiKeyCreateVerificationCASAttempts 是「读 → 判断 → 比较并交换」的最多重试次数：
	// 同一个账号的两个设置请求恰好交错才会用到第二次，三次都被抢先说明有人在疯狂改，直接 409。
	apiKeyCreateVerificationCASAttempts = 3
)

var (
	// ErrAPIKeyCreateVerificationModeInvalid mode 不是 on / off / auto。
	ErrAPIKeyCreateVerificationModeInvalid = infraerrors.BadRequest(
		"API_KEY_CREATE_VERIFICATION_MODE_INVALID", "mode must be one of: on, off, auto")
	// ErrAPIKeyCreateVerificationNotConfigurable 账号没有任何可用因子，这个开关没有意义（无处可验）。
	ErrAPIKeyCreateVerificationNotConfigurable = infraerrors.Conflict(
		"API_KEY_CREATE_VERIFICATION_NOT_CONFIGURABLE",
		"this account has no verification method (login password, two-factor code or passkey), so this setting does not apply")
	// ErrAPIKeyCreateVerificationConflict 并发修改，重试次数用尽。
	ErrAPIKeyCreateVerificationConflict = infraerrors.Conflict(
		"API_KEY_CREATE_VERIFICATION_CONFLICT", "the setting was changed at the same time, please try again")
	// ErrAPIKeyCreateVerificationRelaxRequired 把保护关掉（变宽松）但没带身份校验。
	// 机器码沿用 VERIFY_REQUIRED（前端按它弹校验框），文案专门说这一步。
	ErrAPIKeyCreateVerificationRelaxRequired = infraerrors.BadRequest(
		"VERIFY_REQUIRED",
		"Identity verification is required to turn this protection off. / 关闭这项保护需要先验证身份。")
)

// UserSecurityPreferenceRepository 是 user_security_preferences（迁移 343）的持久化端口。
type UserSecurityPreferenceRepository interface {
	// GetAPIKeyCreateVerificationMode 返回用户显式设置的值："on" / "off"；没有行返回 ""（= auto）。
	GetAPIKeyCreateVerificationMode(ctx context.Context, userID int64) (string, error)
	// CompareAndSetAPIKeyCreateVerificationMode 仅当库里现在的值等于 expected 时改成 next，返回是否写入。
	// expected / next 取 "" / "on" / "off"，"" 表示没有行（写入 "" = 删行 = 恢复 auto）。
	// (false, nil) 表示被并发修改抢先了，调用方应重新读取再判断，不是错误。
	CompareAndSetAPIKeyCreateVerificationMode(ctx context.Context, userID int64, expected, next string) (bool, error)
}

// APIKeyCreateVerificationChange 是一次开关修改的结论（只含标签与因子名，不含任何凭据）。
type APIKeyCreateVerificationChange struct {
	// From / To 是改前 / 改后的模式标签（auto / on / off）。
	From string
	To   string
	// Relaxed 这次修改把「有效要求」从必须校验变成了不要求。
	Relaxed bool
	// Changed 是否真的写了库（From == To 时为 false）。
	Changed bool
	// Factor 本次实际用来验证身份的因子（password / totp / passkey）；没有验证则为空。
	Factor string
}

// SetCreateVerificationPreferences 注入偏好仓储（wire 装配，启动期调用一次）。
// 不注入（nil）时所有账号一律按 auto 处理，行为与没有这个开关之前完全一致。
func (s *APIKeyVerificationService) SetCreateVerificationPreferences(repo UserSecurityPreferenceRepository) {
	if s == nil {
		return
	}
	s.createPrefs = repo
}

// apiKeyCreateVerificationModeLabel 把库里的值（"" / on / off）映射成对外的标签；
// 任何不认识的值都当 auto（沿用默认规则，不会因为一个异常值让创建 Key 出错）。
func apiKeyCreateVerificationModeLabel(stored string) string {
	switch stored {
	case APIKeyCreateVerificationOn:
		return APIKeyCreateVerificationOn
	case APIKeyCreateVerificationOff:
		return APIKeyCreateVerificationOff
	default:
		return APIKeyCreateVerificationAuto
	}
}

// apiKeyCreateVerificationStoredValue 是标签到库里取值的反向映射（auto = 没有行 = ""）。
func apiKeyCreateVerificationStoredValue(label string) string {
	if label == APIKeyCreateVerificationAuto {
		return ""
	}
	return label
}

// normalizeAPIKeyCreateVerificationMode 归一化外部输入；空串不算合法（必须显式给出目标）。
func normalizeAPIKeyCreateVerificationMode(mode string) (string, bool) {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case APIKeyCreateVerificationAuto, APIKeyCreateVerificationOn, APIKeyCreateVerificationOff:
		return normalized, true
	default:
		return "", false
	}
}

// NormalizeAPIKeyCreateVerificationMode 导出给 handler：返回归一化后的标签；不合法时 ok=false。
func NormalizeAPIKeyCreateVerificationMode(mode string) (string, bool) {
	return normalizeAPIKeyCreateVerificationMode(mode)
}

// apiKeyCreateVerificationRequired 是「有效要求」的唯一判定：给定模式与账号因子，创建 / 轮换 Key
// 前是不是必须先校验。GET verification-factors 的 create_verification_required、verifyForUser 的放行判断、
// 开关修改时的「改前 / 改后是否变宽松」都走它，不允许有第二份实现。
func apiKeyCreateVerificationRequired(mode string, f *APIKeyVerificationFactors) bool {
	if f == nil {
		return false
	}
	switch mode {
	case APIKeyCreateVerificationOn:
		// 没有任何可用因子时 on 无法满足：按「不要求」处理，不能把人锁死。
		return f.Available()
	case APIKeyCreateVerificationOff:
		return false
	default:
		// auto：改造前的规则——只认用户自己开启的更强因子（TOTP / Passkey）；
		// 仅有登录密码的账号不强制（第三方登录用户常常根本不记得密码）。
		return f.PasskeyCount > 0 || f.TOTPEnabled
	}
}

// applyCreateVerificationPolicy 在 factorsForUser 的结果（auto 口径）上叠加用户开关，
// 把 CreateVerificationMode / CreateVerificationRequired 改成账号的真实有效值。
// 读偏好失败一律向上返回错误（fail-closed）：宁可这次创建失败，也不能在数据库抖动时
// 把「on」悄悄当成 auto 放行。
func (s *APIKeyVerificationService) applyCreateVerificationPolicy(ctx context.Context, userID int64, f *APIKeyVerificationFactors) error {
	mode := APIKeyCreateVerificationAuto
	if s.createPrefs != nil {
		stored, err := s.createPrefs.GetAPIKeyCreateVerificationMode(ctx, userID)
		if err != nil {
			return fmt.Errorf("load api key create verification preference: %w", err)
		}
		mode = apiKeyCreateVerificationModeLabel(stored)
	}
	f.CreateVerificationMode = mode
	f.CreateVerificationRequired = apiKeyCreateVerificationRequired(mode, f)
	return nil
}

// SetCreateVerificationMode 修改「创建 API Key 时是否需要验证身份」开关（mode：on / off / auto）。
//
// 鉴权规则（失败一律 fail-closed，不写库）：
//   - 请求里带了 verification.method 就按因子真验证（与创建 Key 一致），错的直接拒绝；
//   - 这次修改让「有效要求」从必须校验变成不要求（变宽松）时，必须已经验证通过，否则 VERIFY_REQUIRED；
//   - 变严或持平不需要校验。
//
// 写库是带期望值的比较并交换：读到旧值 → 判断是否放宽 → 写入时要求库里仍是这个旧值。
// 否则两个请求交错时，一个按旧值判断过的请求可以把别人刚刚收紧的设置悄悄改回宽松。
// 被抢先时重新读取再判断（已经通过的校验在同一个请求里继续有效，不会再消耗第二个动态码）。
func (s *APIKeyVerificationService) SetCreateVerificationMode(
	ctx context.Context,
	userID int64,
	mode string,
	in *APIKeyVerificationInput,
) (*APIKeyCreateVerificationChange, error) {
	if s == nil || s.userRepo == nil || s.createPrefs == nil || userID <= 0 {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	target, ok := normalizeAPIKeyCreateVerificationMode(mode)
	if !ok {
		return nil, ErrAPIKeyCreateVerificationModeInvalid
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user for api key create verification: %w", err)
	}
	if user == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	factors, err := s.factorsForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	// 没有任何可用因子：on / off 都没有意义（on 无法满足，off 与 auto 效果相同），不落库，
	// 只允许 auto（清掉可能残留的旧行）。
	if !factors.Available() && target != APIKeyCreateVerificationAuto {
		return nil, ErrAPIKeyCreateVerificationNotConfigurable
	}

	verifiedFactor := ""
	if in != nil && strings.TrimSpace(in.Method) != "" {
		result, err := s.verifyFactor(ctx, user, factors, in)
		if err != nil {
			return nil, err
		}
		verifiedFactor = result.Factor
	}

	for attempt := 0; attempt < apiKeyCreateVerificationCASAttempts; attempt++ {
		stored, err := s.createPrefs.GetAPIKeyCreateVerificationMode(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("load api key create verification preference: %w", err)
		}
		current := apiKeyCreateVerificationModeLabel(stored)
		relaxed := apiKeyCreateVerificationRequired(current, factors) && !apiKeyCreateVerificationRequired(target, factors)
		if relaxed && verifiedFactor == "" {
			return nil, ErrAPIKeyCreateVerificationRelaxRequired
		}
		change := &APIKeyCreateVerificationChange{From: current, To: target, Relaxed: relaxed, Factor: verifiedFactor}
		if current == target {
			return change, nil
		}
		swapped, err := s.createPrefs.CompareAndSetAPIKeyCreateVerificationMode(
			ctx, userID, stored, apiKeyCreateVerificationStoredValue(target))
		if err != nil {
			return nil, fmt.Errorf("save api key create verification preference: %w", err)
		}
		if swapped {
			change.Changed = true
			return change, nil
		}
	}
	return nil, ErrAPIKeyCreateVerificationConflict
}
