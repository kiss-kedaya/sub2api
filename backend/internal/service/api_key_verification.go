package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// API Key「创建 / 二次查看」的身份校验（三选一：登录密码 / TOTP / Passkey）。
//
// 背景（老板线上实测的两个问题）：
//  1. 创建 Key 时服务端不校验登录密码：输错密码也能创建成功，之后点「查看密钥」
//     用这个密码解密失败，只能看到「密码错误，无法解密这把 Key」。
//  2. 创建 Key 这种能长期拿到调用凭据的动作太容易：会话被劫持即可静默建 Key。
//
// 本文件是唯一判定入口，落地两条规则：
//   - 创建 Key 前的身份校验，方式从账号**实际可用**的因子里三选一；可用性只认：
//     登录密码可用 / 已开 TOTP / 已绑定 Passkey。**默认只有开了 TOTP 或绑了 Passkey 的账号
//     才强制这一步**——这是用户自己选择的更高安全级别；只有登录密码的账号（含邮箱注册、
//     用 Google / GitHub 登录的用户）默认不再被强制输入密码（见 APIKeyVerificationFactors.
//     CreateVerificationRequired）。用户可以在个人资料里自己把这一步改成必须（on）或不要（off），
//     见 api_key_create_verification.go。要求密码的是「以后还能再查看」的封存，那是创建后另外
//     勾选的可选项，不是创建的前置条件。
//   - 「以后还能再查看」的封存密文会记录本次授权因子（api_key_secrets.auth_factor），
//     再次查看时必须用同一因子重新授权，成功后才签发 5 分钟一次性 reveal grant。
//
// 安全边界：
//   - 密码只做 bcrypt 比对，不落库、不落日志、不进审计；比对前先过按用户的失败预算
//     （60 秒内最多 5 次，成功清零，见 checkLoginPassword），所有入口共用一份；
//   - TOTP 走现有校验器（自带每用户失败预算），且一个码只能用一次（VerifyCodeOnce，
//     与登录共用同一个重放标记）；
//   - Passkey 走现有 assertion 流程，成功后签发一次性票据（Redis，5 分钟）；
//   - reveal grant 只存 SHA-256 摘要，按 user+key+factor 绑定，一次性消费；
//   - 所有失败统一 fail-closed：不创建 Key / 不下发密文。
//
// ApiKeyVerificationGuardedKey 是 handler 写在 gin.Context 上的「本请求已完成
// 身份校验」标记。幂等中间件用它判断「同一次请求的瞬时重放」可以安全复用
// 回执；没有这个标记的重放（例如 24 小时内换一个连接重放同一个
// Idempotency-Key）一律返回 VERIFY_REQUIRED，不允许跳过校验。
const ApiKeyVerificationGuardPassedKey = "api_key_verification_guard_passed"

const (
	// APIKeyFactorPassword 登录密码因子。
	APIKeyFactorPassword = "password"
	// APIKeyFactorTOTP 动态口令因子（与 FactorTOTP 同一字符串）。
	APIKeyFactorTOTP = "totp"
	// APIKeyFactorPasskey 通行密钥因子（与 FactorPasskey 同一字符串）。
	APIKeyFactorPasskey = "passkey"

	// APIKeyRevealGrantTTL 二次查看授权凭证的固定有效期（5 分钟，一次性）。
	APIKeyRevealGrantTTL = 5 * time.Minute
	// APIKeyPasskeyTicketTTL Passkey 校验成功后一次性票据的有效期（5 分钟）。
	APIKeyPasskeyTicketTTL = 5 * time.Minute
	// APIKeyPasskeyTicketTTLSeconds 供前端展示倒计时。
	APIKeyPasskeyTicketTTLSeconds = int64(APIKeyPasskeyTicketTTL / time.Second)

	apiKeyVerificationTokenBytes  = 32
	apiKeyVerificationMaxTokenLen = 128
)

var (
	// ErrAPIKeyVerificationRequired 账号存在可用因子但本次请求没有带身份校验。
	//
	// 现网这条几乎只会被「更新前就打开、一直没刷新」的旧页面触发：旧页面不认识这个错误码，
	// 只会把 message 原样显示出来，所以文案要中英双语，并明确告诉人强制刷新。
	// 新页面按 reason 走自己的文案（keys.factor.*），不会显示这一句。
	ErrAPIKeyVerificationRequired = infraerrors.BadRequest(
		"VERIFY_REQUIRED",
		"Identity verification is required to create an API key. If this page was opened before the latest update, "+
			"hard-refresh it (Ctrl+F5, or Cmd+Shift+R on Mac) and try again. / "+
			"创建密钥需要先验证身份。如果这个页面是更新前打开的，请强制刷新（Ctrl+F5，Mac 用 Cmd+Shift+R）后重试。")
	// ErrAPIKeyPasswordInvalid 登录密码错误（创建 / 封存 / 二次查看）。
	ErrAPIKeyPasswordInvalid = infraerrors.BadRequest(
		"VERIFY_PASSWORD_INVALID", "password is incorrect")
	// ErrAPIKeyPasswordRateLimited 该账号短时间内密码校验失败过多（按用户的失败预算已用尽）。
	// 带 retry_after 元数据（秒），handler 据此写 Retry-After。
	ErrAPIKeyPasswordRateLimited = infraerrors.TooManyRequests(
		"VERIFY_PASSWORD_RATE_LIMITED", "too many password verification attempts, please try again later")
	// ErrAPIKeyTOTPInvalid 动态验证码错误或已失效。
	ErrAPIKeyTOTPInvalid = infraerrors.BadRequest(
		"VERIFY_TOTP_INVALID", "dynamic code is incorrect")
	// ErrAPIKeyPasskeyFailed Passkey assertion 校验失败 / 票据缺失 / 票据不属于本人。
	ErrAPIKeyPasskeyFailed = infraerrors.BadRequest(
		"VERIFY_PASSKEY_FAILED", "passkey verification failed")
	// ErrAPIKeyFactorUnavailable 请求的因子在该账号上不可用（未绑定 / 未开启）。
	ErrAPIKeyFactorUnavailable = infraerrors.BadRequest(
		"VERIFY_FACTOR_UNAVAILABLE", "this verification method is not available for this account")
	// ErrAPIKeyVerificationUnavailable 校验依赖（存储 / Passkey 服务）不可用。
	ErrAPIKeyVerificationUnavailable = infraerrors.ServiceUnavailable(
		"VERIFY_UNAVAILABLE", "identity verification is temporarily unavailable")
	// ErrAPIKeyRevealFactorMismatch 二次查看使用的因子与创建时授权因子不一致。
	ErrAPIKeyRevealFactorMismatch = infraerrors.BadRequest(
		"VERIFY_FACTOR_MISMATCH",
		"this api key was sealed with a different verification method; please use the same method")
	// ErrAPIKeyRevealGrantRequired 查看封存密文缺少 reveal grant。
	ErrAPIKeyRevealGrantRequired = infraerrors.Forbidden(
		"REVEAL_GRANT_REQUIRED", "fresh identity verification is required to view this api key")
	// ErrAPIKeyRevealGrantInvalid reveal grant 过期 / 已被使用 / 与 user+key 不匹配。
	ErrAPIKeyRevealGrantInvalid = infraerrors.Forbidden(
		"REVEAL_GRANT_INVALID", "identity verification has expired; please verify again")
)

// APIKeyVerificationFactors 是 GET /user/security/verification-factors 的响应。
//
// 只回「该账号实际可用哪些因子」和推荐顺序，不回任何凭据、绑定数量细节或
// 内部 ID。Preferred 顺序按老板要求：Passkey > TOTP > 登录密码。
type APIKeyVerificationFactors struct {
	PasswordAvailable bool `json:"password_available"`
	TOTPEnabled       bool `json:"totp_enabled"`
	// PasskeyCount 已绑定的 Passkey 数量（0 表示不可选）。
	PasskeyCount int `json:"passkey_count"`
	// Preferred 推荐因子：passkey / totp / password；一个都不可用时为空串。
	Preferred string `json:"preferred"`
	// AvailableMethods 可用因子列表（顺序即展示顺序，只含可用的）。
	AvailableMethods []string `json:"available_methods"`
	// SealAvailable 是否支持「以后还能查看」（本地解密必须用登录密码派生密钥，
	// 无密码的 OAuth-only 账号只能创建时一次性显示）。
	SealAvailable bool `json:"seal_available"`
	// CreateVerificationRequired 创建 / 轮换 Key 前是否必须先通过一次身份校验（账号的**有效**值）：
	// 默认（auto）只有已开 TOTP 或已绑定 Passkey 的账号才为 true，仅有登录密码的账号为 false——
	// 不能再因为「有密码」就把人卡在创建这一步（第三方登录用户常常根本不记得密码）。
	// 用户可以在个人资料里自己改成 on / off（见 api_key_create_verification.go），这里始终是
	// 把开关与账号因子一起算出来的结果；账号一个可用因子都没有时恒为 false。
	// AvailableMethods 仍然列出密码：二次查看（解封存密文）和已开 2FA 的账号三选一要用。
	CreateVerificationRequired bool `json:"create_verification_required"`
	// CreateVerificationMode 用户在「创建 API Key 时需要验证身份」开关上的设置：
	// auto（没设置过，沿用默认规则）/ on / off。
	CreateVerificationMode string `json:"create_verification_mode"`
	// CreateVerificationConfigurable 账号至少有一个可用因子时才为 true；
	// 为 false 时这个开关没有意义（无处可验），前端置灰并说明原因。
	CreateVerificationConfigurable bool `json:"create_verification_configurable"`
	// CreateVerificationDefaultRequired 不动开关（auto）时，这个账号创建 / 轮换 Key 是否必须校验：
	// 开了 TOTP 或绑了 Passkey 才为 true。前端把开关拨到「不需要」时据此决定写什么：默认本来就要求（true）的账号
	// 才需要显式写 off；默认本来就不要求（false，例如只有登录密码）的账号写 auto（恢复默认）就够了——
	// 否则会留下一个等账号以后开了 2FA 才突然生效的潜伏 off。
	CreateVerificationDefaultRequired bool `json:"create_verification_default_required"`
}

// Available 报告是否存在至少一个可用因子。
func (f *APIKeyVerificationFactors) Available() bool {
	return f != nil && len(f.AvailableMethods) > 0
}

// methodAvailable 报告某个因子在当前账号上是否可用。
func (f *APIKeyVerificationFactors) methodAvailable(method string) bool {
	if f == nil {
		return false
	}
	for _, candidate := range f.AvailableMethods {
		if candidate == method {
			return true
		}
	}
	return false
}

// APIKeyVerificationInput 是创建 / 二次查看请求里的 verification 字段。
type APIKeyVerificationInput struct {
	Method string
	// Password 仅 method=password 时使用：服务端只做 bcrypt 比对，不保存。
	Password string
	// TOTPCode 仅 method=totp 时使用。
	TOTPCode string
	// PasskeySessionToken 仅 method=passkey 时使用：由
	// POST /user/security/passkey/verify/finish 签发的一次性票据。
	PasskeySessionToken string
}

// Normalize 归一化外部输入（大小写 / 空白），保持 fail-closed。
func (in APIKeyVerificationInput) Normalize() APIKeyVerificationInput {
	return APIKeyVerificationInput{
		Method:              strings.ToLower(strings.TrimSpace(in.Method)),
		Password:            in.Password,
		TOTPCode:            strings.TrimSpace(in.TOTPCode),
		PasskeySessionToken: strings.TrimSpace(in.PasskeySessionToken),
	}
}

// APIKeyVerificationResult 是一次成功校验的结论（只含因子名，不含凭据）。
type APIKeyVerificationResult struct {
	Factor string
	// SkippedByUserSetting 本次没有做校验，是因为账号把「创建 API Key 时需要验证身份」设成了关闭（off）。
	// 只用于审计区分「用户自己关掉」与「账号没有因子 / 默认不强制」（skipped_no_factor）。
	SkippedByUserSetting bool
}

// APIKeyRevealGrant 是二次查看授权成功的回执。
//
// Token 是交给浏览器的随机不透明串（5 分钟一次性），服务端 Redis 只存它的
// SHA-256 摘要；审计/日志/响应体之外不复用。
type APIKeyRevealGrant struct {
	Token     string
	Factor    string
	ExpiresIn int64
}

// APIKeyPasskeyVerificationTicket Passkey 校验成功后的一次性票据回执。
type APIKeyPasskeyVerificationTicket struct {
	Token     string
	ExpiresIn int64
}

// APIKeyRevealGrantRecord 是 Redis 里保存的 reveal grant 绑定记录。
type APIKeyRevealGrantRecord struct {
	UserID    int64  `json:"user_id"`
	APIKeyID  int64  `json:"api_key_id"`
	Factor    string `json:"factor"`
	ExpiresAt int64  `json:"expires_at"`
}

// APIKeyPasskeyVerificationRecord 是 Redis 里保存的 Passkey 校验票据记录。
type APIKeyPasskeyVerificationRecord struct {
	UserID    int64  `json:"user_id"`
	Factor    string `json:"factor"`
	ExpiresAt int64  `json:"expires_at"`
}

// APIKeyVerificationStore 是 reveal grant / Passkey 票据的持久化端口（Redis 实现）。
// 与 TotpCache 分开定义：现有测试替身不必实现它；拿不到实现时服务 fail-closed。
type APIKeyVerificationStore interface {
	SaveAPIKeyRevealGrant(ctx context.Context, digest string, record *APIKeyRevealGrantRecord, ttl time.Duration) error
	// ConsumeAPIKeyRevealGrant 原子读出并删除（一次性）；key 不存在返回 (nil, nil)。
	ConsumeAPIKeyRevealGrant(ctx context.Context, digest string) (*APIKeyRevealGrantRecord, error)

	SaveAPIKeyPasskeyTicket(ctx context.Context, digest string, record *APIKeyPasskeyVerificationRecord, ttl time.Duration) error
	// ConsumeAPIKeyPasskeyTicket 原子读出并删除（一次性）；key 不存在返回 (nil, nil)。
	ConsumeAPIKeyPasskeyTicket(ctx context.Context, digest string) (*APIKeyPasskeyVerificationRecord, error)
}

// APIKeyPasswordAttemptLimiter 是登录密码因子「按用户」的失败预算端口。
//
// 生产实现是 middleware.NewAPIKeyPasswordRateLimiter（Redis 滑动窗口，60 秒内最多 5 次，
// 与登录 2FA / step-up 同一套实现），但使用**独立的计数桶**（key 前缀
// api_key_password:verify:user:）：与 2FA 共桶的话，密码校验成功会清掉 2FA 的失败计数，
// 持会话者也能靠故意输错密码把账号的登录 2FA 锁住一分钟。连续失败有上限、校验成功即清零、
// 维度是用户 ID 而不是 IP。未装配（nil）时不做预算，保持装配前的行为。
type APIKeyPasswordAttemptLimiter interface {
	// Check 记录一次密码尝试；allowed=false 表示预算已用尽（retryAfter 为最早一次尝试
	// 离开窗口的剩余时间）；err 表示存储故障，调用方必须 fail-closed。
	Check(ctx context.Context, userID int64) (allowed bool, retryAfter time.Duration, err error)
	// Reset 在密码校验成功后清零该用户的计数。
	Reset(ctx context.Context, userID int64)
}

// APIKeyVerificationService 是创建 / 二次查看 API Key 的身份校验服务。
type APIKeyVerificationService struct {
	userRepo   UserRepository
	apiKeyRepo APIKeyRepository
	secretRepo APIKeySecretRepository
	totp       *TotpService
	passkeys   *PasskeyService
	cache      TotpCache
	store      APIKeyVerificationStore
	// passwordLimiter 是密码因子的按用户失败预算；启动时由 router 注入一次，之后只读。
	passwordLimiter APIKeyPasswordAttemptLimiter
	// createPrefs 是「创建 API Key 时是否需要验证身份」的用户开关存储；启动时注入一次，之后只读。
	// nil 时所有账号一律按 auto（改造前的规则）。
	createPrefs UserSecurityPreferenceRepository
}

// SetPasswordAttemptLimiter 注入密码因子的失败预算（router 装配，启动期调用一次）。
func (s *APIKeyVerificationService) SetPasswordAttemptLimiter(limiter APIKeyPasswordAttemptLimiter) {
	if s == nil {
		return
	}
	s.passwordLimiter = limiter
}

// NewAPIKeyVerificationService 创建身份校验服务。
//
// cache 是现有 2FA Redis 缓存接口；本服务只借用同一实例上的新 key 前缀，
// 不额外引入新的 Redis 客户端依赖。测试可注入实现 APIKeyVerificationStore 的
// 替身（miniredis）来覆盖 TTL / 一次性消费。
func NewAPIKeyVerificationService(
	userRepo UserRepository,
	apiKeyRepo APIKeyRepository,
	secretRepo APIKeySecretRepository,
	totp *TotpService,
	passkeys *PasskeyService,
	cache TotpCache,
) *APIKeyVerificationService {
	svc := &APIKeyVerificationService{
		userRepo:   userRepo,
		apiKeyRepo: apiKeyRepo,
		secretRepo: secretRepo,
		totp:       totp,
		passkeys:   passkeys,
		cache:      cache,
	}
	if store, ok := cache.(APIKeyVerificationStore); ok {
		svc.store = store
	}
	return svc
}

// verificationStore 取 Redis 端口；装配缺失时 fail-closed。
func (s *APIKeyVerificationService) verificationStore() (APIKeyVerificationStore, error) {
	if s == nil || s.store == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	return s.store, nil
}

// apiKeyEmailBindIdentitySource 是 AuthService.BindEmailIdentity 写在邮箱身份
// metadata.source 里的值（见 auth_email_binding.go）。api_key_verification_email_bind_test.go
// 里的集成用例用真实的绑定流程钉住这个值：改了一边，另一边的测试会红。
const apiKeyEmailBindIdentitySource = "auth_service_email_bind"

// apiKeyOAuthSignupSource 报告注册来源是否「第三方登录注册」（这类账号注册时由系统塞了随机口令）。
func apiKeyOAuthSignupSource(signupSource string) bool {
	switch strings.ToLower(strings.TrimSpace(signupSource)) {
	case "github", "google", "oidc", "linuxdo", "wechat", "dingtalk", "oauth":
		return true
	default:
		return false
	}
}

// passwordKnownByUser 报告账号是否「知道自己的登录密码」。
//
// users.password_hash 是 NOT NULL，第三方登录注册会自动塞一个随机口令，所以「有 hash」不等于
// 「用户知道密码」：
//   - 邮箱注册（signup_source 为空 / email）：知道；
//   - 第三方登录注册：只有用户之后亲手设过本地密码才算知道。仓库里没有「设置过本地密码」的显式
//     标记（这里也不新加列），唯一可靠的持久化证据是绑定邮箱——BindEmailIdentity 必须提交新密码
//     （换绑还要先验旧密码），并留下来源为 auth_service_email_bind 的邮箱身份。
//     user_repo_create / user_repo_update / auth_service_login_backfill / auth_service_dual_write
//     是任何用户创建或登录时都会写的，不能当证据：拿它们当证据，会把只有随机口令的账号判成
//     「知道密码」，进而要求它输入一个根本不存在的口令，创建 Key 被永久卡死；
//   - 其余途径（找回密码重置、第三方注册当场设的密码）没有任何痕迹，保守按「不知道」处理：
//     这只会让该用户少一个可选因子，不会让账号失去校验，也不会把只有随机口令的账号锁在创建 Key 之外。
//
// 该判定只影响「展示与可选性」以及「有没有可用因子」，密码校验本身仍以 bcrypt 比对为准。
// 查证据失败时返回错误（调用方 fail-closed），而不是退回「没有密码因子」：那样一个设过密码、又没有
// 其它因子的账号会在数据库抖动时被判成「没有任何可用因子」而放行创建。
func (s *APIKeyVerificationService) passwordKnownByUser(ctx context.Context, user *User) (bool, error) {
	if user == nil || strings.TrimSpace(user.PasswordHash) == "" {
		return false, nil
	}
	if !apiKeyOAuthSignupSource(user.SignupSource) {
		return true, nil
	}
	records, err := s.userRepo.ListUserAuthIdentities(ctx, user.ID)
	if err != nil {
		return false, fmt.Errorf("list auth identities for api key verification: %w", err)
	}
	for _, record := range filterUserAuthIdentities(records, "email") {
		if firstStringIdentityValue(record.Metadata, "source") == apiKeyEmailBindIdentitySource {
			return true, nil
		}
	}
	return false, nil
}

// passkeyCount 返回当前账号已绑定的 Passkey 数量（服务未启用时为 0）。
func (s *APIKeyVerificationService) passkeyCount(ctx context.Context, userID int64) (int, error) {
	if s == nil || s.passkeys == nil || !s.passkeys.Enabled() || userID <= 0 {
		return 0, nil
	}
	records, err := s.passkeys.List(ctx, userID)
	if err != nil {
		// Passkey 服务未配置 / 全局关闭时 List 会返回 PASSKEY_DISABLED：
		// 对「可用因子」判定来说等价于没有 Passkey，不应把整个接口打挂。
		if errors.Is(err, ErrPasskeysDisabled) {
			return 0, nil
		}
		return 0, fmt.Errorf("list passkeys for api key verification: %w", err)
	}
	return len(records), nil
}

// Factors 计算该账号当前可用的身份校验因子，以及「创建 Key 前是否必须校验」的有效值
// （叠加了用户自己在个人资料里设的开关；没设过就是默认规则）。
func (s *APIKeyVerificationService) Factors(ctx context.Context, userID int64) (*APIKeyVerificationFactors, error) {
	user, factors, err := s.loadAccountFactors(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.applyCreateVerificationPolicy(ctx, user.ID, factors); err != nil {
		return nil, err
	}
	return factors, nil
}

// loadAccountFactors 读账号并算出它自身的因子（不读「创建时是否需要验证身份」开关）。
func (s *APIKeyVerificationService) loadAccountFactors(ctx context.Context, userID int64) (*User, *APIKeyVerificationFactors, error) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return nil, nil, ErrAPIKeyVerificationUnavailable
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("load user for api key verification: %w", err)
	}
	if user == nil {
		return nil, nil, ErrAPIKeyVerificationUnavailable
	}
	factors, err := s.factorsForUser(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	return user, factors, nil
}

// factorsForUser 只看账号自身的因子；CreateVerificationRequired / Mode 在这里是默认（auto）口径，
// 需要叠加用户开关的调用方（Factors、verifyForUser 的无校验分支）再走 applyCreateVerificationPolicy。
// 二次查看 / 封存只关心哪些因子可用，不依赖开关，所以不会因为偏好表出问题而受影响。
func (s *APIKeyVerificationService) factorsForUser(ctx context.Context, user *User) (*APIKeyVerificationFactors, error) {
	if s == nil || user == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	passwordAvailable, err := s.passwordKnownByUser(ctx, user)
	if err != nil {
		return nil, err
	}
	totpEnabled := user.TotpEnabled
	count, err := s.passkeyCount(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	methods := make([]string, 0, 3)
	if count > 0 {
		methods = append(methods, APIKeyFactorPasskey)
	}
	if totpEnabled {
		methods = append(methods, APIKeyFactorTOTP)
	}
	if passwordAvailable {
		methods = append(methods, APIKeyFactorPassword)
	}
	preferred := ""
	if len(methods) > 0 {
		preferred = methods[0]
	}
	factors := &APIKeyVerificationFactors{
		PasswordAvailable:              passwordAvailable,
		TOTPEnabled:                    totpEnabled,
		PasskeyCount:                   count,
		Preferred:                      preferred,
		AvailableMethods:               methods,
		SealAvailable:                  passwordAvailable,
		CreateVerificationMode:         APIKeyCreateVerificationAuto,
		CreateVerificationConfigurable: len(methods) > 0,
	}
	// 默认口径：只认用户自己开启的更强因子；登录密码单独存在时不强制（见字段注释）。
	factors.CreateVerificationRequired = apiKeyCreateVerificationRequired(APIKeyCreateVerificationAuto, factors)
	factors.CreateVerificationDefaultRequired = factors.CreateVerificationRequired
	return factors, nil
}

// VerifyForCreate 是创建 / 变更 API Key 明文的身份校验。
//
// 返回本次实际使用的因子；账号的「有效要求」是不要求校验时（默认规则下没开 TOTP、也没绑 Passkey 的账号，
// 含只有登录密码的账号、OAuth-only 账号；或者用户自己把开关设成了 off）放行并返回空串——创建时一次性显示
// 明文，「以后还能查看」是创建后另外勾选的封存（那里才要登录密码）。账号的有效要求是必须校验（开了 TOTP
// 或绑了 Passkey 且没关掉开关，或用户把开关设成了 on）时未带校验直接 400。
// 请求里主动带了因子（旧页面的密码步骤等）时照常按因子真验证，错了就拒绝。
//
// 所有会下发 / 变更明文的入口都必须走这里：创建（POST /keys）、批量轮换
// （POST /keys/batch-rotate）、把密钥回滚成旧值的编辑（PUT /keys/:id）。
// 其余编辑（改名、改配额、换分组、加白名单等）不下发明文，不要求校验。
func (s *APIKeyVerificationService) VerifyForCreate(ctx context.Context, userID int64, in *APIKeyVerificationInput) (*APIKeyVerificationResult, error) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	return s.verifyForUser(ctx, userID, in)
}

// VerifyForKeyEdit 是「会回执明文」的编辑（PUT /keys/:id）的身份校验。
//
// 与创建同语义，额外要求：先确认这把 Key 属于该用户，否则按 404 处理，
// 避免在身份校验前泄露「这个 ID 存在」。只有本次请求真的会产生明文回执
// （regenerate_key=true，或带 custom_key 时调用方负责判定）才调用本方法。
func (s *APIKeyVerificationService) VerifyForKeyEdit(
	ctx context.Context,
	userID, apiKeyID int64,
	in *APIKeyVerificationInput,
) (*APIKeyVerificationResult, error) {
	if s == nil || s.apiKeyRepo == nil || userID <= 0 {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	if apiKeyID <= 0 {
		return nil, ErrAPIKeyNotFound
	}
	key, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	return s.VerifyForCreate(ctx, userID, in)
}

// verifyForUser 是校验的核心实现（创建与编辑共用）。
func (s *APIKeyVerificationService) verifyForUser(ctx context.Context, userID int64, in *APIKeyVerificationInput) (*APIKeyVerificationResult, error) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user for api key verification: %w", err)
	}
	if user == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	factors, err := s.factorsForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	if in == nil || strings.TrimSpace(in.Method) == "" {
		// 没带校验材料：是否放行取决于「有效要求」——默认规则下没开 TOTP、也没绑 Passkey 的账号不强制
		// （只有登录密码的账号、OAuth-only 账号都在这里），用户自己把开关设成 off 的账号也不强制；
		// 其余账号不带校验一律拒绝。账号没有任何可用因子时恒为不强制（on 无法满足，不能把人锁死）。
		// 带了校验材料的请求不需要读开关：照常往下按因子真验证。
		if err := s.applyCreateVerificationPolicy(ctx, user.ID, factors); err != nil {
			return nil, err
		}
		if !factors.CreateVerificationRequired {
			return &APIKeyVerificationResult{
				SkippedByUserSetting: factors.CreateVerificationMode == APIKeyCreateVerificationOff,
			}, nil
		}
		return nil, ErrAPIKeyVerificationRequired
	}
	// 显式指定了因子就按因子真验证：未绑定的因子（含 OAuth-only 试图用密码）
	// 一律 VERIFY_FACTOR_UNAVAILABLE，不会因为「没有任何因子」被放行。
	return s.verifyFactor(ctx, user, factors, in)
}

// VerifyAPIKeyOwnership 仅校验指定 API Key 是否属于当前用户（轻量 DB 查表，不查 Redis 并发队列）。
// 供个人接口密钥（uak_）调用 PUT /keys/:id 轮换路径时复用，防越权探测他人 Key ID。
func (s *APIKeyVerificationService) VerifyAPIKeyOwnership(ctx context.Context, userID, apiKeyID int64) error {
	if s == nil || s.apiKeyRepo == nil || userID <= 0 {
		return ErrAPIKeyVerificationUnavailable
	}
	if apiKeyID <= 0 {
		return ErrAPIKeyNotFound
	}
	key, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return err
	}
	if key == nil || key.UserID != userID {
		return ErrAPIKeyNotFound
	}
	return nil
}

// VerifyStrict 强制要求账号在存在任意可用校验因子（Passkey / TOTP / 登录密码）时必须完成校验。
//
// 与 VerifyForCreate 不同：不读取 user_security_preferences（「创建 API Key 时是否验证」开关），
// 专用于签发 / 重置个人接口密钥这类高敏感凭据入口——避免会话被临时接管时静默签发长期密钥。
// 账号确实没有任何可用因子（纯第三方登录且未绑 2FA）时放行，避免把无因子账号永久锁死；
// 若请求主动携带因子，仍按因子严格校验。
func (s *APIKeyVerificationService) VerifyStrict(ctx context.Context, userID int64, in *APIKeyVerificationInput) (*APIKeyVerificationResult, error) {
	user, factors, err := s.loadAccountFactors(ctx, userID)
	if err != nil {
		return nil, err
	}
	if in == nil || strings.TrimSpace(in.Method) == "" {
		if !factors.Available() {
			return &APIKeyVerificationResult{}, nil
		}
		return nil, ErrAPIKeyVerificationRequired
	}
	return s.verifyFactor(ctx, user, factors, in)
}

// VerifyForSeal 校验「封存密文用的登录密码」。
//
// 封存后的本地解密只认登录密码（PBKDF2），所以只要用户选择「以后查看」，
// 就必须证明这个密码是对的——否则又会出现「创建成功、以后解密失败」的旧问题。
func (s *APIKeyVerificationService) VerifyForSeal(ctx context.Context, userID int64, password string) error {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return ErrAPIKeyVerificationUnavailable
	}
	if strings.TrimSpace(password) == "" {
		return ErrAPIKeyPasswordInvalid
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("load user for api key seal: %w", err)
	}
	if user == nil {
		return ErrAPIKeyVerificationUnavailable
	}
	known, err := s.passwordKnownByUser(ctx, user)
	if err != nil {
		return err
	}
	if !known {
		return ErrAPIKeyFactorUnavailable
	}
	return s.checkLoginPassword(ctx, user, password)
}

// VerifyForLegacySeal 处理「封存请求既没带 password 也没带 auth_factor」的旧客户端兼容分支。
//
// 这个分支不能是无校验的后门：持会话者不带任何校验材料就能覆盖已封存的密文，还把 auth_factor
// 清成 NULL（NULL 代表「改造前的旧密文」，二次查看不再限制因子）。所以账号存在可用因子时它也必须
// 走校验：旧客户端提供不了校验材料，只能被拒（VERIFY_REQUIRED），升级到新前端后带登录密码封存即可；
// 账号没有任何可用因子（第三方注册且未绑 2FA）时保持放行，与创建 Key 是同一条语义。
func (s *APIKeyVerificationService) VerifyForLegacySeal(ctx context.Context, userID int64) error {
	// 只关心「有没有可用因子」，不读创建开关：封存路径不依赖 user_security_preferences。
	_, factors, err := s.loadAccountFactors(ctx, userID)
	if err != nil {
		return err
	}
	if factors.Available() {
		return ErrAPIKeyVerificationRequired
	}
	return nil
}

// checkLoginPassword 是登录密码的唯一比对入口（创建 / 轮换 / 封存 / 二次查看共用）：
// 先走按用户的失败预算，再做 bcrypt，成功后清零。
//
// 顺序是刻意的：预算用尽时不做 bcrypt，所以持会话者即使猜对也只会拿到 429，
// 不能把任何一个校验入口当成「不限次数的密码 oracle」；同一个用户的所有入口共用一份预算。
// 预算存储故障 fail-closed（VERIFY_UNAVAILABLE），与本文件其余校验一致。
func (s *APIKeyVerificationService) checkLoginPassword(ctx context.Context, user *User, password string) error {
	if strings.TrimSpace(password) == "" {
		// 空口令没有任何猜测价值，不消耗预算。
		return ErrAPIKeyPasswordInvalid
	}
	limiter := s.passwordLimiter
	if limiter != nil {
		allowed, retryAfter, err := limiter.Check(ctx, user.ID)
		if err != nil {
			return ErrAPIKeyVerificationUnavailable
		}
		if !allowed {
			return apiKeyPasswordRateLimited(retryAfter)
		}
	}
	if !user.CheckPassword(password) {
		return ErrAPIKeyPasswordInvalid
	}
	if limiter != nil {
		limiter.Reset(ctx, user.ID)
	}
	return nil
}

// apiKeyPasswordRateLimited 生成带 retry_after（向上取整的秒数）的 429。
func apiKeyPasswordRateLimited(retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = time.Minute
	}
	seconds := int64(retryAfter / time.Second)
	if retryAfter%time.Second > 0 {
		seconds++
	}
	return ErrAPIKeyPasswordRateLimited.WithMetadata(map[string]string{"retry_after": strconv.FormatInt(seconds, 10)})
}

// AuthorizeReveal 为「再次查看 Key 明文」做身份校验并签发 5 分钟一次性 grant。
func (s *APIKeyVerificationService) AuthorizeReveal(
	ctx context.Context,
	userID, apiKeyID int64,
	in APIKeyVerificationInput,
) (*APIKeyRevealGrant, error) {
	if s == nil || s.userRepo == nil || s.apiKeyRepo == nil || s.secretRepo == nil || userID <= 0 || apiKeyID <= 0 {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	key, err := s.apiKeyRepo.GetByID(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	secret, err := s.secretRepo.GetByAPIKeyID(ctx, key.ID, userID)
	if err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load user for api key reveal: %w", err)
	}
	if user == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	factors, err := s.factorsForUser(ctx, user)
	if err != nil {
		return nil, err
	}
	in = in.Normalize()
	if !factors.methodAvailable(in.Method) {
		return nil, ErrAPIKeyFactorUnavailable
	}
	// 创建时选了 TOTP / Passkey 的 Key，二次查看必须用同一因子。
	if required := strings.ToLower(strings.TrimSpace(secret.AuthFactor)); required != "" && required != in.Method {
		return nil, ErrAPIKeyRevealFactorMismatch
	}
	verified, err := s.verifyFactor(ctx, user, factors, &in)
	if err != nil {
		return nil, err
	}
	store, err := s.verificationStore()
	if err != nil {
		return nil, err
	}
	token, err := newAPIKeyVerificationToken()
	if err != nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	now := time.Now()
	record := &APIKeyRevealGrantRecord{
		UserID:    userID,
		APIKeyID:  key.ID,
		Factor:    verified.Factor,
		ExpiresAt: now.Add(APIKeyRevealGrantTTL).Unix(),
	}
	if err := store.SaveAPIKeyRevealGrant(ctx, apiKeyVerificationDigest(token), record, APIKeyRevealGrantTTL); err != nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	return &APIKeyRevealGrant{
		Token:     token,
		Factor:    verified.Factor,
		ExpiresIn: int64(APIKeyRevealGrantTTL / time.Second),
	}, nil
}

// ConsumeRevealGrant 校验并消费一次 reveal grant，返回授权因子。
//
// 绑定 user + api_key，且一次性消费：密文每被下发一次都必须重新授权，
// 避免「一次验证 5 分钟内反复导出密文」。
func (s *APIKeyVerificationService) ConsumeRevealGrant(ctx context.Context, token string, userID, apiKeyID int64) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrAPIKeyRevealGrantRequired
	}
	if len(token) > apiKeyVerificationMaxTokenLen || userID <= 0 || apiKeyID <= 0 {
		return "", ErrAPIKeyRevealGrantInvalid
	}
	store, err := s.verificationStore()
	if err != nil {
		return "", err
	}
	record, err := store.ConsumeAPIKeyRevealGrant(ctx, apiKeyVerificationDigest(token))
	if err != nil {
		return "", ErrAPIKeyVerificationUnavailable
	}
	if record == nil || record.UserID != userID || record.APIKeyID != apiKeyID {
		return "", ErrAPIKeyRevealGrantInvalid
	}
	if record.ExpiresAt > 0 && time.Now().Unix() > record.ExpiresAt {
		return "", ErrAPIKeyRevealGrantInvalid
	}
	if !apiKeyVerificationMethodValid(record.Factor) {
		return "", ErrAPIKeyRevealGrantInvalid
	}
	return record.Factor, nil
}

// BeginPasskeyVerification 为「创建 / 二次查看 Key」生成 Passkey assertion 挑战。
func (s *APIKeyVerificationService) BeginPasskeyVerification(ctx context.Context, userID int64) (*protocol.CredentialAssertion, string, error) {
	if s == nil || s.passkeys == nil || !s.passkeys.Enabled() {
		return nil, "", ErrAPIKeyFactorUnavailable
	}
	if userID <= 0 {
		return nil, "", ErrAPIKeyVerificationUnavailable
	}
	assertion, sessionToken, err := s.passkeys.BeginLoginForUser(ctx, userID, PasskeySessionKindKeyVerify)
	if err != nil {
		return nil, "", err
	}
	return assertion, sessionToken, nil
}

// FinishPasskeyVerification 校验 Passkey assertion，成功后签发一次性校验票据。
func (s *APIKeyVerificationService) FinishPasskeyVerification(
	ctx context.Context,
	userID int64,
	sessionToken string,
	request *http.Request,
) (*APIKeyPasskeyVerificationTicket, error) {
	if s == nil || s.passkeys == nil || !s.passkeys.Enabled() {
		return nil, ErrAPIKeyFactorUnavailable
	}
	if userID <= 0 || strings.TrimSpace(sessionToken) == "" || request == nil {
		return nil, ErrAPIKeyPasskeyFailed
	}
	if _, err := s.passkeys.FinishLoginForUser(ctx, userID, PasskeySessionKindKeyVerify, sessionToken, request); err != nil {
		return nil, err
	}
	store, err := s.verificationStore()
	if err != nil {
		return nil, err
	}
	token, err := newAPIKeyVerificationToken()
	if err != nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	now := time.Now()
	record := &APIKeyPasskeyVerificationRecord{
		UserID:    userID,
		Factor:    APIKeyFactorPasskey,
		ExpiresAt: now.Add(APIKeyPasskeyTicketTTL).Unix(),
	}
	if err := store.SaveAPIKeyPasskeyTicket(ctx, apiKeyVerificationDigest(token), record, APIKeyPasskeyTicketTTL); err != nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	return &APIKeyPasskeyVerificationTicket{
		Token:     token,
		ExpiresIn: APIKeyPasskeyTicketTTLSeconds,
	}, nil
}

// verifyFactor 按 method 真验证，返回本次使用的因子。
func (s *APIKeyVerificationService) verifyFactor(
	ctx context.Context,
	user *User,
	factors *APIKeyVerificationFactors,
	raw *APIKeyVerificationInput,
) (*APIKeyVerificationResult, error) {
	in := raw.Normalize()
	if !apiKeyVerificationMethodValid(in.Method) || !factors.methodAvailable(in.Method) {
		return nil, ErrAPIKeyFactorUnavailable
	}
	switch in.Method {
	case APIKeyFactorPassword:
		// factors 已经按 passwordKnownByUser 算过（上面的 methodAvailable），这里不再查第二遍。
		if !factors.PasswordAvailable {
			return nil, ErrAPIKeyFactorUnavailable
		}
		if err := s.checkLoginPassword(ctx, user, in.Password); err != nil {
			return nil, err
		}
	case APIKeyFactorTOTP:
		if s.totp == nil || !user.TotpEnabled {
			return nil, ErrAPIKeyFactorUnavailable
		}
		if in.TOTPCode == "" {
			return nil, ErrAPIKeyTOTPInvalid
		}
		// 动态码只能用一次：校验通过的同一个（时间步, 码）在窗口内不能再用第二次，
		// 否则被旁观 / 钓鱼拿到的码能在 90 秒内反复下发 Key 明文。consume 拿到的是真正匹配上的
		// 时间步（不是请求时钟所在的步），所以跨 30 秒边界也不能把同一个码用两遍。
		//
		// consume 用 ConsumeLoginCode：与登录共用同一个重放标记，登录用过的码不能再拿来校验、
		// 反过来也一样；启用了支付校验的部署上它还会一并占用支付的重放标记，保持 VerifyCode 对
		// 管理员原有的约束（同一个码不能既用于校验又用于资金操作）。
		code := in.TOTPCode
		if err := s.totp.VerifyCodeOnce(ctx, user.ID, code, func(counter int64) error {
			return s.totp.ConsumeLoginCode(ctx, user.ID, counter, code)
		}); err != nil {
			// 校验器把「未开启 / 无密钥 / 尝试过多 / 码已被使用」也归类为错误：对调用方统一
			// 按验证码错误返回，不泄露账号的 TOTP 配置细节。
			return nil, ErrAPIKeyTOTPInvalid
		}
	case APIKeyFactorPasskey:
		if factors.PasskeyCount <= 0 || s.passkeys == nil || !s.passkeys.Enabled() {
			return nil, ErrAPIKeyFactorUnavailable
		}
		if err := s.consumePasskeyTicket(ctx, user.ID, in.PasskeySessionToken); err != nil {
			return nil, err
		}
	default:
		return nil, ErrAPIKeyFactorUnavailable
	}
	return &APIKeyVerificationResult{Factor: in.Method}, nil
}

// consumePasskeyTicket 一次性消费 Passkey 校验票据，绑定 user + factor。
func (s *APIKeyVerificationService) consumePasskeyTicket(ctx context.Context, userID int64, token string) error {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > apiKeyVerificationMaxTokenLen {
		return ErrAPIKeyPasskeyFailed
	}
	store, err := s.verificationStore()
	if err != nil {
		return err
	}
	record, err := store.ConsumeAPIKeyPasskeyTicket(ctx, apiKeyVerificationDigest(token))
	if err != nil {
		return ErrAPIKeyVerificationUnavailable
	}
	if record == nil || record.UserID != userID || record.Factor != APIKeyFactorPasskey {
		return ErrAPIKeyPasskeyFailed
	}
	if record.ExpiresAt > 0 && time.Now().Unix() > record.ExpiresAt {
		return ErrAPIKeyPasskeyFailed
	}
	return nil
}

// apiKeyVerificationMethodValid 报告因子名是否属于三选一。
func apiKeyVerificationMethodValid(method string) bool {
	switch method {
	case APIKeyFactorPassword, APIKeyFactorTOTP, APIKeyFactorPasskey:
		return true
	default:
		return false
	}
}

// APIKeyVerificationMethodValid 导出给 handler/仓储做入参校验。
func APIKeyVerificationMethodValid(method string) bool {
	return apiKeyVerificationMethodValid(strings.ToLower(strings.TrimSpace(method)))
}

// NormalizeAPIKeyAuthFactor 归一化密文元数据里的授权因子。
// 空串表示改造前的旧密文（NULL），查看时不限制因子。
func NormalizeAPIKeyAuthFactor(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || !apiKeyVerificationMethodValid(normalized) {
		return ""
	}
	return normalized
}

// apiKeyVerificationDigest 计算 token 的服务端存储摘要（服务端不存 token 明文）。
func apiKeyVerificationDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newAPIKeyVerificationToken 生成 32 字节 CSPRNG 随机 token（base64url）。
func newAPIKeyVerificationToken() (string, error) {
	buf := make([]byte, apiKeyVerificationTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
