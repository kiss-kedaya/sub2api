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
//   - 创建 Key 前必须完成一次身份校验，方式从账号**实际可用**的因子里三选一；
//     可用性只认：登录密码可用 / 已开 TOTP / 已绑定 Passkey。
//   - 「以后还能再查看」的封存密文会记录本次授权因子（api_key_secrets.auth_factor），
//     再次查看时必须用同一因子重新授权，成功后才签发 5 分钟一次性 reveal grant。
//
// 安全边界：
//   - 密码只做 bcrypt 比对，不落库、不落日志、不进审计；
//   - TOTP 走现有校验器（自带每用户失败预算）；
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
	ErrAPIKeyVerificationRequired = infraerrors.BadRequest(
		"VERIFY_REQUIRED", "identity verification is required to create an api key")
	// ErrAPIKeyPasswordInvalid 登录密码错误（创建 / 封存 / 二次查看）。
	ErrAPIKeyPasswordInvalid = infraerrors.BadRequest(
		"VERIFY_PASSWORD_INVALID", "password is incorrect")
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

// APIKeyVerificationService 是创建 / 二次查看 API Key 的身份校验服务。
type APIKeyVerificationService struct {
	userRepo   UserRepository
	apiKeyRepo APIKeyRepository
	secretRepo APIKeySecretRepository
	totp       *TotpService
	passkeys   *PasskeyService
	cache      TotpCache
	store      APIKeyVerificationStore
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

// resolveUserFactorSource 报告账号是否「知道自己的登录密码」。
//
// 说明：sub2api 的 users.password_hash 是 NOT NULL，OAuth 注册会自动塞一个
// 随机密码，所以「有 hash」不等于「用户知道密码」。这里按注册来源判定：
// GitHub / Google / OIDC / LinuxDo / 钉钉 / 微信 注册的账号视为密码不可用，
// 邮箱注册（含邀请码）视为可用。该判定只影响「展示与可选性」，不会绕过任何
// 校验；密码校验仍以 bcrypt 比对为准。
func apiKeyPasswordKnownByUser(user *User) bool {
	if user == nil || strings.TrimSpace(user.PasswordHash) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(user.SignupSource)) {
	case "github", "google", "oidc", "linuxdo", "wechat", "dingtalk", "oauth":
		return false
	default:
		return true
	}
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

// Factors 计算该账号当前可用的身份校验因子。
func (s *APIKeyVerificationService) Factors(ctx context.Context, userID int64) (*APIKeyVerificationFactors, error) {
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
	return s.factorsForUser(ctx, user)
}

func (s *APIKeyVerificationService) factorsForUser(ctx context.Context, user *User) (*APIKeyVerificationFactors, error) {
	if s == nil || user == nil {
		return nil, ErrAPIKeyVerificationUnavailable
	}
	passwordAvailable := apiKeyPasswordKnownByUser(user)
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
	return &APIKeyVerificationFactors{
		PasswordAvailable: passwordAvailable,
		TOTPEnabled:       totpEnabled,
		PasskeyCount:      count,
		Preferred:         preferred,
		AvailableMethods:  methods,
		SealAvailable:     passwordAvailable,
	}, nil
}

// VerifyForCreate 是创建 / 变更 API Key 明文的身份校验。
//
// 返回本次实际使用的因子；账号一个可用因子都没有时（OAuth-only 且未绑 2FA）
// 放行并返回空串——这类账号本来就不允许「以后查看」，创建时一次性显示明文，
// 不引入第二个解密材料。存在可用因子时未带校验直接 400，不允许创建。
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
		// 账号没有任何可用因子（OAuth-only 且未绑 2FA）：允许创建，但只能
		// 一次性显示明文；有可用因子时不带校验一律拒绝。
		if !factors.Available() {
			return &APIKeyVerificationResult{}, nil
		}
		return nil, ErrAPIKeyVerificationRequired
	}
	// 显式指定了因子就按因子真验证：未绑定的因子（含 OAuth-only 试图用密码）
	// 一律 VERIFY_FACTOR_UNAVAILABLE，不会因为「没有任何因子」被放行。
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
	if !apiKeyPasswordKnownByUser(user) {
		return ErrAPIKeyFactorUnavailable
	}
	if !user.CheckPassword(password) {
		return ErrAPIKeyPasswordInvalid
	}
	return nil
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
		if !apiKeyPasswordKnownByUser(user) {
			return nil, ErrAPIKeyFactorUnavailable
		}
		if strings.TrimSpace(in.Password) == "" || !user.CheckPassword(in.Password) {
			return nil, ErrAPIKeyPasswordInvalid
		}
	case APIKeyFactorTOTP:
		if s.totp == nil || !user.TotpEnabled {
			return nil, ErrAPIKeyFactorUnavailable
		}
		if in.TOTPCode == "" {
			return nil, ErrAPIKeyTOTPInvalid
		}
		if err := s.totp.VerifyCode(ctx, user.ID, in.TOTPCode); err != nil {
			// 校验器把「未开启 / 无密钥 / 尝试过多」也归类为错误：对调用方统一
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
