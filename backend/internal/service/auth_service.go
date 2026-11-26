package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials  = infraerrors.Unauthorized("INVALID_CREDENTIALS", "invalid email or password")
	ErrUserNotActive       = infraerrors.Forbidden("USER_NOT_ACTIVE", "user is not active")
	ErrEmailExists         = infraerrors.Conflict("EMAIL_EXISTS", "email already exists")
	ErrEmailReserved       = infraerrors.BadRequest("EMAIL_RESERVED", "email is reserved")
	ErrInvalidToken        = infraerrors.Unauthorized("INVALID_TOKEN", "invalid token")
	ErrTokenExpired        = infraerrors.Unauthorized("TOKEN_EXPIRED", "token has expired")
	ErrAccessTokenExpired  = infraerrors.Unauthorized("ACCESS_TOKEN_EXPIRED", "access token has expired")
	ErrTokenTooLarge       = infraerrors.BadRequest("TOKEN_TOO_LARGE", "token too large")
	ErrTokenRevoked        = infraerrors.Unauthorized("TOKEN_REVOKED", "token has been revoked")
	ErrRefreshTokenInvalid = infraerrors.Unauthorized("REFRESH_TOKEN_INVALID", "invalid refresh token")
	ErrRefreshTokenExpired = infraerrors.Unauthorized("REFRESH_TOKEN_EXPIRED", "refresh token has expired")
	// ErrRefreshTokenReuse 表示已消费（已轮换）的 Refresh Token 在重试宽限
	// （jwt.refresh_token_reuse_leeway_seconds）之外被再次使用：
	// 按标准 OAuth 实践撤销整个 token family，合法端需要重新登录。
	ErrRefreshTokenReuse  = infraerrors.Unauthorized("REFRESH_TOKEN_REUSE", "refresh token has already been used; the session has been revoked")
	ErrRefreshTokenReused = infraerrors.Unauthorized("REFRESH_TOKEN_REUSED", "refresh token has been reused")
	// ErrRefreshTokenSessionExpired 表示该登录会话已达绝对寿命上限
	// （首次签发时间 + jwt.session_max_lifetime_days），必须重新登录。
	ErrRefreshTokenSessionExpired   = infraerrors.Unauthorized("REFRESH_TOKEN_SESSION_EXPIRED", "session lifetime exceeded, please login again")
	ErrEmailVerifyRequired          = infraerrors.BadRequest("EMAIL_VERIFY_REQUIRED", "email verification is required")
	ErrEmailSuffixNotAllowed        = infraerrors.BadRequest("EMAIL_SUFFIX_NOT_ALLOWED", "email suffix is not allowed")
	ErrEmailFormatInvalid           = infraerrors.BadRequest("EMAIL_FORMAT_INVALID", "email format is invalid")
	ErrEmailDomainRegistrationLimit = infraerrors.BadRequest(
		"EMAIL_DOMAIN_REGISTRATION_LIMIT",
		"this email domain cannot register another account; use a mainstream email or contact support to add the enterprise domain",
	)
	ErrRegDisabled             = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrServiceUnavailable      = infraerrors.ServiceUnavailable("SERVICE_UNAVAILABLE", "service temporarily unavailable")
	ErrInvitationCodeRequired  = infraerrors.BadRequest("INVITATION_CODE_REQUIRED", "invitation code is required")
	ErrInvitationCodeInvalid   = infraerrors.BadRequest("INVITATION_CODE_INVALID", "invalid or used invitation code")
	ErrOAuthInvitationRequired = infraerrors.Forbidden("OAUTH_INVITATION_REQUIRED", "invitation code required to complete oauth registration")
	// ErrLoginTOTPRequired 表示已有账号启用了 TOTP：第三方邮箱 OAuth 登录与密码登录
	// 同一策略，必须先完成动态码校验，不得直接签发会话。
	ErrLoginTOTPRequired       = infraerrors.Forbidden("LOGIN_TOTP_REQUIRED", "two-factor verification required")
	ErrCaptchaProviderConflict = infraerrors.ServiceUnavailable("CAPTCHA_PROVIDER_CONFLICT", "multiple captcha providers are enabled")
)

// ErrRefreshTokenCacheNotConfigured 表示没有可用的 refresh token 存储：这是配置问题不是瞬时故障，
// 调用方（如登录发令牌）不该为它重试。
var ErrRefreshTokenCacheNotConfigured = errors.New("refresh token cache not configured")

// maxTokenLength 限制 token 大小，避免超长 header 触发解析时的异常内存分配。
const maxTokenLength = 8192

// refreshTokenPrefix is the prefix for refresh tokens to distinguish them from access tokens.
const refreshTokenPrefix = "rt_"

// refreshTokenReuseWindow 是"已消费 Token 墓碑"的保留上限：在此窗口内重放旧
// Refresh Token 会被识别为复用并撤销整个 family；超过窗口后旧 Token 仍被拒绝
// （查不到记录），只是不再连带撤销 family，避免墓碑长期占用 Redis。
// 窗口内又分两段：消费后 jwt.refresh_token_reuse_leeway_seconds 以内的再次出现是
// 「响应丢失后的合法重试」（重发新令牌、不撤销）；更晚的才算重放。
const refreshTokenReuseWindow = 24 * time.Hour

// maxRefreshLeewayReissues 是每个已消费 Token 在宽限期内最多被「重发」新令牌的次数：
// 合法客户端最多重试一两次；超过说明有人在拿同一个旧令牌批量换新令牌，按重放处理。
const maxRefreshLeewayReissues = 3

// JWTClaims JWT载荷数据
type JWTClaims struct {
	UserID       int64  `json:"user_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"` // Used to invalidate tokens on password change
	// SessionID 会话 ID（与 refresh token family 对应），用于单会话撤销与 step-up 授权绑定。
	SessionID string `json:"sid,omitempty"`
	// BindingHash 会话指纹哈希（IP+UA），会话绑定开启时校验；空值表示旧 token（平滑升级）。
	BindingHash string `json:"bnd,omitempty"`
	// BindingVersion 会话指纹版本。0/缺失表示本特性上线前签发的旧 token，中间件
	// 不做绑定校验（否则升级瞬间所有既有会话会因指纹算法差异被踢下线）；
	// >=1 表示按 security.session_binding_mode 计算的指纹，需要强制校验。
	BindingVersion int `json:"bfv,omitempty"`
	// BindingCoarse 粗粒度指纹（浏览器家族|系统家族|IP 家族|网段短哈希），与 BindingHash 并存；
	// loose 模式下优先用它比较（忽略版本号、IPv4<->IPv6 切换）。旧 token 没有该字段。
	BindingCoarse string `json:"bcs,omitempty"`
	jwt.RegisteredClaims
}

// AuthService 认证服务
// AccessTokenRevocationStore is optionally implemented by the refresh-token
// cache. It records the instant before which access tokens must be rejected,
// so "revoke all sessions" also invalidates stateless JWTs instead of only
// dropping refresh tokens.
type AccessTokenRevocationStore interface {
	RevokeAccessTokensBefore(ctx context.Context, userID int64, before time.Time, ttl time.Duration) error
	AccessTokensRevokedBefore(ctx context.Context, userID int64) (time.Time, error)
}

const accessTokenRevocationTTL = 48 * time.Hour

type AuthService struct {
	entClient             *dbent.Client
	userRepo              UserRepository
	redeemRepo            RedeemCodeRepository
	refreshTokenCache     RefreshTokenCache
	cfg                   *config.Config
	settingService        *SettingService
	emailService          *EmailService
	turnstileService      *TurnstileService
	tencentCaptchaService *TencentCaptchaService
	aliyunCaptchaService  *AliyunCaptchaService
	emailQueueService     *EmailQueueService
	promoService          *PromoService
	affiliateService      *AffiliateService
	defaultSubAssigner    DefaultSubscriptionAssigner
	userPlatformQuotaRepo UserPlatformQuotaRepository
	billingCacheService   *BillingCacheService

	// stats 是登录会话的进程内计数（refresh 结果、会话绑定、登录发令牌兜底），按需创建。
	statsOnce sync.Once
	stats     *authSessionStats
}

type CaptchaProof struct {
	// TurnstileToken 承载 Cloudflare Turnstile token；阿里云验证码复用该字段承载 captchaVerifyParam
	TurnstileToken string
	TencentTicket  string
	TencentRandstr string
}

type DefaultSubscriptionAssigner interface {
	AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error)
}

type signupGrantPlan struct {
	Balance        float64
	Concurrency    int
	Subscriptions  []DefaultSubscriptionSetting
	PlatformQuotas map[string]*DefaultPlatformQuotaSetting
}

// NewAuthService 创建认证服务实例
func NewAuthService(
	entClient *dbent.Client,
	userRepo UserRepository,
	redeemRepo RedeemCodeRepository,
	refreshTokenCache RefreshTokenCache,
	cfg *config.Config,
	settingService *SettingService,
	emailService *EmailService,
	turnstileService *TurnstileService,
	emailQueueService *EmailQueueService,
	promoService *PromoService,
	defaultSubAssigner DefaultSubscriptionAssigner,
	affiliateService *AffiliateService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
) *AuthService {
	return &AuthService{
		entClient:             entClient,
		userRepo:              userRepo,
		redeemRepo:            redeemRepo,
		refreshTokenCache:     refreshTokenCache,
		cfg:                   cfg,
		settingService:        settingService,
		emailService:          emailService,
		turnstileService:      turnstileService,
		emailQueueService:     emailQueueService,
		promoService:          promoService,
		affiliateService:      affiliateService,
		defaultSubAssigner:    defaultSubAssigner,
		userPlatformQuotaRepo: userPlatformQuotaRepo,
	}
}

func (s *AuthService) EntClient() *dbent.Client {
	if s == nil {
		return nil
	}
	return s.entClient
}

func (s *AuthService) SetTencentCaptchaService(tencentCaptchaService *TencentCaptchaService) {
	s.tencentCaptchaService = tencentCaptchaService
}

func (s *AuthService) SetAliyunCaptchaService(aliyunCaptchaService *AliyunCaptchaService) {
	s.aliyunCaptchaService = aliyunCaptchaService
}

func (s *AuthService) SetBillingCacheService(billingCacheService *BillingCacheService) {
	s.billingCacheService = billingCacheService
}

// Register 用户注册，返回token和用户
func (s *AuthService) Register(ctx context.Context, email, password string) (string, *User, error) {
	return s.RegisterWithVerification(ctx, email, password, "", "", "", "")
}

// RegisterWithVerification 用户注册（支持邮件验证、优惠码、邀请码和邀请返利码），返回token和用户。
func (s *AuthService) RegisterWithVerification(ctx context.Context, email, password, verifyCode, promoCode, invitationCode, affiliateCode string) (string, *User, error) {
	// 检查是否开放注册（默认关闭：settingService 未配置时不允许注册）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return "", nil, ErrRegDisabled
	}

	// 防止用户注册 LinuxDo OAuth 合成邮箱，避免第三方登录与本地账号发生碰撞。
	if isReservedEmail(email) {
		return "", nil, ErrEmailReserved
	}
	// 注册邮箱结构校验（长度 / 字符集 / 多级域名），与前端预检同口径。
	if err := ValidateRegistrationEmailFormat(email); err != nil {
		return "", nil, err
	}
	// 检查是否需要邀请码
	var invitationRedeemCode *RedeemCode
	if s.settingService != nil && s.settingService.IsInvitationCodeEnabled(ctx) {
		if invitationCode == "" {
			return "", nil, ErrInvitationCodeRequired
		}
		// 验证邀请码
		redeemCode, err := s.redeemRepo.GetByCode(ctx, invitationCode)
		if err != nil {
			// 日志只记哈希前缀：卡密是可兑现凭据，明文进日志等于换了个库继续泄露。
			logger.LegacyPrintf("service.auth", "[Auth] Invalid invitation code: hash_prefix=%s, error: %v", redeemCodeLogRef(invitationCode), err)
			return "", nil, ErrInvitationCodeInvalid
		}
		// 检查类型和状态
		if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
			logger.LegacyPrintf("service.auth", "[Auth] Invitation code invalid: type=%s, status=%s", redeemCode.Type, redeemCode.Status)
			return "", nil, ErrInvitationCodeInvalid
		}
		invitationRedeemCode = redeemCode
	}

	// 检查是否需要邮件验证
	if s.settingService != nil && s.settingService.IsEmailVerifyEnabled(ctx) {
		// 如果邮件验证已开启但邮件服务未配置，拒绝注册
		// 这是一个配置错误，不应该允许绕过验证
		if s.emailService == nil {
			logger.LegacyPrintf("service.auth", "%s", "[Auth] Email verification enabled but email service not configured, rejecting registration")
			return "", nil, ErrServiceUnavailable
		}
		if verifyCode == "" {
			return "", nil, ErrEmailVerifyRequired
		}
		// 验证邮箱验证码
		if err := s.emailService.VerifyCode(ctx, email, verifyCode); err != nil {
			return "", nil, fmt.Errorf("verify code: %w", err)
		}
	}

	// 检查邮箱是否已存在（含 +别名 / Gmail 点号变体归一化，防止单个收件箱批量派生注册）
	existsEmail, err := s.existsByEmailOrAlias(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return "", nil, ErrServiceUnavailable
	}
	if existsEmail {
		return "", nil, ErrEmailExists
	}
	if err := s.validateRegistrationEmailQuota(ctx, email); err != nil {
		return "", nil, err
	}

	// 密码哈希
	hashedPassword, err := s.HashPassword(password)
	if err != nil {
		return "", nil, fmt.Errorf("hash password: %w", err)
	}

	grantPlan := s.resolveSignupGrantPlan(ctx, "email")

	// 新用户默认 RPM（0 = 不限制）。注册时写入，后续作为用户级兜底。
	var defaultRPMLimit int
	if s.settingService != nil {
		defaultRPMLimit = s.settingService.GetDefaultUserRPMLimit(ctx)
	}

	// 创建用户
	user := &User{
		Email:        email,
		PasswordHash: hashedPassword,
		Role:         RoleUser,
		Balance:      grantPlan.Balance,
		Concurrency:  grantPlan.Concurrency,
		RPMLimit:     defaultRPMLimit,
		Status:       StatusActive,
	}

	if err := s.createUserAndClaimInvitation(ctx, user, invitationRedeemCode); err != nil {
		// 优先检查邮箱冲突错误（竞态条件下可能发生）
		switch {
		case errors.Is(err, ErrEmailExists):
			return "", nil, ErrEmailExists
		case errors.Is(err, ErrEmailDomainRegistrationLimit):
			return "", nil, ErrEmailDomainRegistrationLimit
		case errors.Is(err, ErrInvitationCodeInvalid):
			return "", nil, ErrInvitationCodeInvalid
		default:
			logger.LegacyPrintf("service.auth", "[Auth] Database error creating user: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}
	s.postAuthUserBootstrap(ctx, user, "email", true)
	s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
	// snapshot user × platform quota（fail-open）
	_ = s.snapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
	if s.affiliateService != nil {
		if _, err := s.affiliateService.EnsureUserAffiliate(ctx, user.ID); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to initialize affiliate profile for user %d: %v", user.ID, err)
		}
		if code := strings.TrimSpace(affiliateCode); code != "" {
			if err := s.affiliateService.BindInviterByCode(ctx, user.ID, code); err != nil {
				// 邀请返利码绑定失败不影响注册，只记录日志
				logger.LegacyPrintf("service.auth", "[Auth] Failed to bind affiliate inviter for user %d: %v", user.ID, err)
			}
		}
	}

	// 邀请码占用已由 createUserAndClaimInvitation 在“用户创建 + 邀请码占用”的
	// 同一个数据库事务内原子完成（一次性约束，见函数注释），此处不再单独标记。
	// 应用优惠码（如果提供且功能已启用）
	if promoCode != "" && s.promoService != nil && s.settingService != nil && s.settingService.IsPromoCodeEnabled(ctx) {
		if err := s.promoService.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
			// 优惠码应用失败不影响注册，只记录日志
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply promo code for user %d: %v", user.ID, err)
		} else {
			// 重新获取用户信息以获取更新后的余额
			if updatedUser, err := s.userRepo.GetByID(ctx, user.ID); err == nil {
				user = updatedUser
			}
		}
	}

	// 生成token
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// SendVerifyCodeResult 发送验证码返回结果
type SendVerifyCodeResult struct {
	Countdown int `json:"countdown"` // 倒计时秒数
}

// SendVerifyCode 发送邮箱验证码（同步方式）
func (s *AuthService) SendVerifyCode(ctx context.Context, email string, locale ...string) error {
	// 检查是否开放注册（默认关闭）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		return ErrRegDisabled
	}

	if isReservedEmail(email) {
		return ErrEmailReserved
	}
	if err := ValidateRegistrationEmailFormat(email); err != nil {
		return err
	}
	// 检查邮箱是否已存在（含 +别名 / Gmail 点号变体归一化，防止单个收件箱批量派生注册）
	existsEmail, err := s.existsByEmailOrAlias(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return ErrServiceUnavailable
	}
	if existsEmail {
		// Do not disclose registration state from this unauthenticated endpoint:
		// an attacker could enumerate accounts. Pretend the code was sent and
		// skip the actual mail (the address owner can use login/password reset).
		logger.LegacyPrintf("service.auth", "[Auth] Verification code suppressed for existing email")
		return nil
	}
	if err := s.validateRegistrationEmailQuota(ctx, email); err != nil {
		return err
	}

	// 发送验证码
	if s.emailService == nil {
		return errors.New("email service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	return s.emailService.SendVerifyCode(ctx, email, siteName, firstEmailLocale(locale))
}

// SendVerifyCodeAsync 异步发送邮箱验证码并返回倒计时
func (s *AuthService) SendVerifyCodeAsync(ctx context.Context, email string, locale ...string) (*SendVerifyCodeResult, error) {
	logger.LegacyPrintf("service.auth", "[Auth] SendVerifyCodeAsync called for email: %s", logredact.MaskEmail(email))

	// 检查是否开放注册（默认关闭）
	if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Registration is disabled")
		return nil, ErrRegDisabled
	}

	if isReservedEmail(email) {
		return nil, ErrEmailReserved
	}
	if err := ValidateRegistrationEmailFormat(email); err != nil {
		return nil, err
	}
	// 检查邮箱是否已存在（含 +别名 / Gmail 点号变体归一化；在发信前拦截，避免批量脚本消耗发信配额）
	existsEmail, err := s.existsByEmailOrAlias(ctx, email)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email exists: %v", err)
		return nil, ErrServiceUnavailable
	}
	if existsEmail {
		// Same anti-enumeration rule as SendVerifyCode: answer success without
		// sending, so registered and unregistered addresses are indistinguishable.
		logger.LegacyPrintf("service.auth", "[Auth] Verification code suppressed for existing email")
		return &SendVerifyCodeResult{Countdown: 60}, nil
	}
	if err := s.validateRegistrationEmailQuota(ctx, email); err != nil {
		return nil, err
	}

	// 检查邮件队列服务是否配置
	if s.emailQueueService == nil {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Email queue service not configured")
		return nil, errors.New("email queue service not configured")
	}

	// 获取网站名称
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	// 异步发送
	logger.LegacyPrintf("service.auth", "[Auth] Enqueueing verify code for: %s", logredact.MaskEmail(email))
	if err := s.emailQueueService.EnqueueVerifyCode(email, siteName, firstEmailLocale(locale)); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to enqueue: %v", err)
		return nil, fmt.Errorf("enqueue verify code: %w", err)
	}

	logger.LegacyPrintf("service.auth", "[Auth] Verify code enqueued successfully for: %s", logredact.MaskEmail(email))
	return &SendVerifyCodeResult{
		Countdown: 60, // 60秒倒计时
	}, nil
}

// VerifyCaptchaForRegister 在注册场景下验证当前启用的验证码。
// 当邮箱验证开启且已提交验证码时，说明验证码发送阶段已完成验证码校验，
// 此处跳过二次校验，避免一次性 token 在注册提交时重复使用导致误报失败。
func (s *AuthService) VerifyCaptchaForRegister(ctx context.Context, proof CaptchaProof, remoteIP, verifyCode string) error {
	if s.IsEmailVerifyEnabled(ctx) && strings.TrimSpace(verifyCode) != "" {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Email verify flow detected, skip duplicate captcha check on register")
		return nil
	}
	return s.VerifyCaptcha(ctx, proof, remoteIP)
}

func (s *AuthService) VerifyCaptcha(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	required := s.cfg != nil && s.cfg.Server.Mode == "release" && s.cfg.Turnstile.Required
	if s.settingService == nil {
		if required {
			return ErrTurnstileNotConfigured
		}
		return nil
	}

	providerConfig, err := s.settingService.GetCaptchaProviderConfig(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return ErrServiceUnavailable
	}
	turnstileEnabled := providerConfig.TurnstileEnabled
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if captchaProvidersConflict(turnstileEnabled, tencentEnabled, aliyunEnabled) {
		return ErrCaptchaProviderConflict
	}
	if tencentEnabled {
		if s.tencentCaptchaService == nil {
			return ErrTencentCaptchaNotConfigured
		}
		return s.tencentCaptchaService.VerifyTicketWithConfig(ctx, providerConfig.Tencent, proof.TencentTicket, proof.TencentRandstr, remoteIP)
	}
	if aliyunEnabled {
		if s.aliyunCaptchaService == nil {
			return ErrAliyunCaptchaNotConfigured
		}
		return s.aliyunCaptchaService.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if turnstileEnabled {
		if s.turnstileService == nil || strings.TrimSpace(providerConfig.TurnstileSecretKey) == "" {
			return ErrTurnstileNotConfigured
		}
		return s.turnstileService.VerifyTokenWithSecret(ctx, providerConfig.TurnstileSecretKey, proof.TurnstileToken, remoteIP)
	}
	if required {
		return ErrTurnstileNotConfigured
	}
	return nil
}

// captchaProvidersConflict 同一时间仅允许启用一家人机验证服务商
func captchaProvidersConflict(enabled ...bool) bool {
	count := 0
	for _, e := range enabled {
		if e {
			count++
		}
	}
	return count > 1
}

// VerifyActionCaptchaIfEnabled 仅保护动作触发的扩展入口（OAuth 登录启动、passkey 登录），
// 腾讯天御与阿里云验证码启用时拦截；不扩大 Cloudflare Turnstile 的既有覆盖范围。
func (s *AuthService) VerifyActionCaptchaIfEnabled(ctx context.Context, proof CaptchaProof, remoteIP string) error {
	if s == nil || s.settingService == nil {
		return ErrServiceUnavailable
	}

	providerConfig, err := s.settingService.GetCaptchaProviderConfig(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "%s", "[Auth] Failed to read captcha provider settings")
		return ErrServiceUnavailable
	}
	tencentEnabled := providerConfig.Tencent.Enabled
	aliyunEnabled := providerConfig.Aliyun.Enabled
	if !tencentEnabled && !aliyunEnabled {
		return nil
	}
	if captchaProvidersConflict(providerConfig.TurnstileEnabled, tencentEnabled, aliyunEnabled) {
		return ErrCaptchaProviderConflict
	}
	if aliyunEnabled {
		if s.aliyunCaptchaService == nil {
			return ErrAliyunCaptchaNotConfigured
		}
		return s.aliyunCaptchaService.VerifyParamWithConfig(ctx, providerConfig.Aliyun, proof.TurnstileToken)
	}
	if s.tencentCaptchaService == nil {
		return ErrTencentCaptchaNotConfigured
	}
	return s.tencentCaptchaService.VerifyTicketWithConfig(
		ctx,
		providerConfig.Tencent,
		proof.TencentTicket,
		proof.TencentRandstr,
		remoteIP,
	)
}

// VerifyTurnstileForRegister 保留旧内部接口，生产 handler 使用 VerifyCaptchaForRegister。
func (s *AuthService) VerifyTurnstileForRegister(ctx context.Context, token, remoteIP, verifyCode string) error {
	return s.VerifyCaptchaForRegister(ctx, CaptchaProof{TurnstileToken: token}, remoteIP, verifyCode)
}

// VerifyTurnstile 保留旧内部接口，生产 handler 使用 VerifyCaptcha。
func (s *AuthService) VerifyTurnstile(ctx context.Context, token string, remoteIP string) error {
	return s.VerifyCaptcha(ctx, CaptchaProof{TurnstileToken: token}, remoteIP)
}

// IsTurnstileEnabled 检查是否启用Turnstile验证
func (s *AuthService) IsTurnstileEnabled(ctx context.Context) bool {
	if s.turnstileService == nil {
		return false
	}
	return s.turnstileService.IsEnabled(ctx)
}

// IsRegistrationEnabled 检查是否开放注册
func (s *AuthService) IsRegistrationEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false // 安全默认：settingService 未配置时关闭注册
	}
	return s.settingService.IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled 检查是否开启邮件验证
func (s *AuthService) IsEmailVerifyEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false
	}
	return s.settingService.IsEmailVerifyEnabled(ctx)
}

// loginTimingDummyHash 是「账号不存在」时用来做等成本 bcrypt 比对的占位哈希。
// 它对应一个随机口令，不属于任何账号，只用于抹平登录响应时间差（防账号枚举）。
const loginTimingDummyHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// Login 用户登录，返回JWT token
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *User, error) {
	// 查找用户
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 防时序枚举：账号不存在时也执行一次与真实校验等成本的 bcrypt，
			// 响应时间不随账号是否存在变化（登录响应体已统一，见 handler.Login）。
			s.EqualizePasswordTiming(password)
			return "", nil, ErrInvalidCredentials
		}
		// 记录数据库错误但不暴露给用户
		logger.LegacyPrintf("service.auth", "[Auth] Database error during login: %v", err)
		return "", nil, ErrServiceUnavailable
	}

	// 验证密码
	if !s.CheckPassword(password, user.PasswordHash) {
		return "", nil, ErrInvalidCredentials
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 生成JWT token
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}

	return token, user, nil
}

// LoginOrRegisterOAuth 用于第三方 OAuth/SSO 登录：
// - 如果邮箱已存在：直接登录（不需要本地密码）
// - 如果邮箱不存在：创建新用户并登录
//
// 注意：该函数用于 LinuxDo OAuth 登录场景（不同于上游账号的 OAuth，例如 Claude/OpenAI/Gemini）。
// 为了满足现有数据库约束（需要密码哈希），新用户会生成随机密码并进行哈希保存。
func (s *AuthService) LoginOrRegisterOAuth(ctx context.Context, email, username string) (string, *User, error) {
	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	// 第三方显示名先过滤再截断：用户名会进入管理面、邮件与通知，禁止携带标记字符。
	username = SanitizeUsername(username)

	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册（fail-close：settingService 未配置时不允许注册）
			if s.settingService == nil || !s.settingService.IsRegistrationEnabled(ctx) {
				return "", nil, ErrRegDisabled
			}

			randomPassword, err := randomHexString(32)
			if err != nil {
				logger.LegacyPrintf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return "", nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return "", nil, fmt.Errorf("hash password: %w", err)
			}

			signupSource := inferLegacySignupSource(email)
			grantPlan := s.resolveSignupGrantPlan(ctx, signupSource)
			var defaultRPMLimit int
			if s.settingService != nil {
				defaultRPMLimit = s.settingService.GetDefaultUserRPMLimit(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      grantPlan.Balance,
				Concurrency:  grantPlan.Concurrency,
				RPMLimit:     defaultRPMLimit,
				Status:       StatusActive,
				SignupSource: signupSource,
			}

			if err := s.userRepo.Create(ctx, newUser); err != nil {
				if errors.Is(err, ErrEmailExists) {
					// 并发场景：GetByEmail 与 Create 之间用户被创建。
					user, err = s.userRepo.GetByEmail(ctx, email)
					if err != nil {
						logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
						return "", nil, ErrServiceUnavailable
					}
				} else {
					logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
					return "", nil, ErrServiceUnavailable
				}
			} else {
				user = newUser
				s.postAuthUserBootstrap(ctx, user, signupSource, false)
				s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
				// snapshot user × platform quota（fail-open）
				_ = s.snapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
			}
		} else {
			logger.LegacyPrintf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return "", nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return "", nil, ErrUserNotActive
	}

	// 尽力补全：当用户名为空时，使用第三方返回的用户名回填。
	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.userRepo.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}
	token, err := s.GenerateToken(ctx, user)
	if err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	return token, user, nil
}

// canBypassRegistrationDisabledForOAuth 在钉钉企业模式（internal_only）且
// dingtalk_connect_bypass_registration=true 时，允许跳过全局 registration_enabled 检查。
func (s *AuthService) canBypassRegistrationDisabledForOAuth(ctx context.Context, signupSource string) bool {
	if signupSource != "dingtalk" {
		return false
	}
	cfg, err := s.settingService.GetDingTalkConnectOAuthConfig(ctx)
	if err != nil || !cfg.Enabled || !cfg.BypassRegistration {
		return false
	}
	return cfg.CorpRestrictionPolicy == "internal_only"
}

// LoginOrRegisterOAuthWithTokenPair 用于第三方 OAuth/SSO 登录，返回完整的 TokenPair。
// 与 LoginOrRegisterOAuth 功能相同，但返回 TokenPair 而非单个 token。
// invitationCode 仅在邀请码注册模式下新用户注册时使用；已有账号登录时忽略。
// affiliateCode 用于邀请返利绑定，仅在新用户注册时使用。
// signupSource 标识来源渠道（"dingtalk"/"linuxdo"/"wechat"/"oidc" 等），仅用于豁免检查。
func (s *AuthService) LoginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, signupSource string) (*TokenPair, *User, error) {
	return s.loginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, "", signupSource)
}

// LoginOrRegisterOAuthWithTokenPairAndPromoCode behaves like
// LoginOrRegisterOAuthWithTokenPair and applies promoCode only when a new user
// is created.
func (s *AuthService) LoginOrRegisterOAuthWithTokenPairAndPromoCode(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	return s.loginOrRegisterOAuthWithTokenPair(ctx, email, username, invitationCode, affiliateCode, promoCode, signupSource)
}

func (s *AuthService) loginOrRegisterOAuthWithTokenPair(ctx context.Context, email, username, invitationCode, affiliateCode, promoCode, signupSource string) (*TokenPair, *User, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, nil, errors.New("refresh token cache not configured")
	}

	email = strings.TrimSpace(email)
	if email == "" || len(email) > 255 {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, nil, infraerrors.BadRequest("INVALID_EMAIL", "invalid email")
	}

	// 第三方显示名先过滤再截断：用户名会进入管理面、邮件与通知，禁止携带标记字符。
	username = SanitizeUsername(username)

	user, err := s.userRepo.GetByEmail(ctx, email)
	created := false
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// OAuth 首次登录视为注册
			if s.settingService == nil || (!s.settingService.IsRegistrationEnabled(ctx) && !s.canBypassRegistrationDisabledForOAuth(ctx, signupSource)) {
				return nil, nil, ErrRegDisabled
			}

			// 第三方直登注册同样必须遵守注册邮箱策略：精确黑名单（含收件箱别名归一）、
			// 别名去重/禁止、邮箱域名白名单与域名限量。此前这条路径只检查注册总开关，
			// 导致 LinuxDo/钉钉/OIDC 等渠道可以绕过邮箱黑名单/域名额度批量建号。
			// 合成邮箱是内部占位（渠道没返回真实邮箱时使用），不参与真实邮箱策略。
			if !isReservedEmail(email) {
				if err := s.validateRegistrationEmailQuota(ctx, email); err != nil {
					return nil, nil, err
				}
			}

			// 检查是否需要邀请码
			var invitationRedeemCode *RedeemCode
			if s.settingService != nil && s.settingService.IsInvitationCodeEnabled(ctx) {
				if invitationCode == "" {
					return nil, nil, ErrOAuthInvitationRequired
				}
				redeemCode, err := s.redeemRepo.GetByCode(ctx, invitationCode)
				if err != nil {
					return nil, nil, ErrInvitationCodeInvalid
				}
				if redeemCode.Type != RedeemTypeInvitation || !redeemCode.CanUse() {
					return nil, nil, ErrInvitationCodeInvalid
				}
				invitationRedeemCode = redeemCode
			}

			randomPassword, err := randomHexString(32)
			if err != nil {
				logger.LegacyPrintf("service.auth", "[Auth] Failed to generate random password for oauth signup: %v", err)
				return nil, nil, ErrServiceUnavailable
			}
			hashedPassword, err := s.HashPassword(randomPassword)
			if err != nil {
				return nil, nil, fmt.Errorf("hash password: %w", err)
			}

			// 优先用 caller 显式传入的 signupSource（如 "dingtalk" / "linuxdo" / "oidc" / "wechat"），
			// 否则才按邮箱后缀推断——避免有真实邮箱的 OAuth 用户被推断为 "email" 渠道，导致渠道授权错读。
			if strings.TrimSpace(signupSource) == "" {
				signupSource = inferLegacySignupSource(email)
			}
			grantPlan := s.resolveSignupGrantPlan(ctx, signupSource)
			var defaultRPMLimit int
			if s.settingService != nil {
				defaultRPMLimit = s.settingService.GetDefaultUserRPMLimit(ctx)
			}

			newUser := &User{
				Email:        email,
				Username:     username,
				PasswordHash: hashedPassword,
				Role:         RoleUser,
				Balance:      grantPlan.Balance,
				Concurrency:  grantPlan.Concurrency,
				RPMLimit:     defaultRPMLimit,
				Status:       StatusActive,
				SignupSource: signupSource,
			}

			if s.entClient != nil && invitationRedeemCode != nil {
				tx, err := s.entClient.Tx(ctx)
				if err != nil {
					logger.LegacyPrintf("service.auth", "[Auth] Failed to begin transaction for oauth registration: %v", err)
					return nil, nil, ErrServiceUnavailable
				}
				defer func() { _ = tx.Rollback() }()
				txCtx := dbent.NewTxContext(ctx, tx)

				if err := s.createOAuthRegisteredUser(txCtx, newUser); err != nil {
					if errors.Is(err, ErrEmailExists) {
						user, err = s.userRepo.GetByEmail(ctx, email)
						if err != nil {
							logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
							return nil, nil, ErrServiceUnavailable
						}
					} else {
						logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else {
					if err := s.redeemRepo.Use(txCtx, invitationRedeemCode.ID, newUser.ID); err != nil {
						return nil, nil, ErrInvitationCodeInvalid
					}
					if err := tx.Commit(); err != nil {
						logger.LegacyPrintf("service.auth", "[Auth] Failed to commit oauth registration transaction: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
					user = newUser
					created = true
					s.postAuthUserBootstrap(ctx, user, signupSource, false)
					s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
					// snapshot user × platform quota（fail-open）
					_ = s.snapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
					s.bindOAuthAffiliate(ctx, user.ID, affiliateCode)
				}
			} else {
				if err := s.createOAuthRegisteredUser(ctx, newUser); err != nil {
					if errors.Is(err, ErrEmailExists) {
						user, err = s.userRepo.GetByEmail(ctx, email)
						if err != nil {
							logger.LegacyPrintf("service.auth", "[Auth] Database error getting user after conflict: %v", err)
							return nil, nil, ErrServiceUnavailable
						}
					} else {
						logger.LegacyPrintf("service.auth", "[Auth] Database error creating oauth user: %v", err)
						return nil, nil, ErrServiceUnavailable
					}
				} else {
					user = newUser
					created = true
					s.postAuthUserBootstrap(ctx, user, signupSource, false)
					s.assignSubscriptions(ctx, user.ID, grantPlan.Subscriptions, "auto assigned by signup defaults")
					// snapshot user × platform quota（fail-open）
					_ = s.snapshotPlatformQuotaDefaults(ctx, user.ID, &grantPlan)
					s.bindOAuthAffiliate(ctx, user.ID, affiliateCode)
					if invitationRedeemCode != nil {
						if err := s.redeemRepo.Use(ctx, invitationRedeemCode.ID, user.ID); err != nil {
							return nil, nil, ErrInvitationCodeInvalid
						}
					}
				}
			}
		} else {
			logger.LegacyPrintf("service.auth", "[Auth] Database error during oauth login: %v", err)
			return nil, nil, ErrServiceUnavailable
		}
	}

	if !user.IsActive() {
		return nil, nil, ErrUserNotActive
	}

	if user.Username == "" && username != "" {
		user.Username = username
		if err := s.userRepo.Update(ctx, user, UserUpdateFields{Username: true}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to update username after oauth login: %v", err)
		}
	}
	if created {
		user = s.applyOAuthSignupPromoCode(ctx, user, promoCode)
	}
	tokenPair, err := s.GenerateTokenPair(ctx, user, "")
	if err != nil {
		return nil, nil, fmt.Errorf("generate token pair: %w", err)
	}
	return tokenPair, user, nil
}

func (s *AuthService) ApplyOAuthSignupPromoCode(ctx context.Context, userID int64, promoCode string) {
	if userID <= 0 {
		return
	}
	s.applyOAuthSignupPromoCode(ctx, &User{ID: userID}, promoCode)
}

func (s *AuthService) applyOAuthSignupPromoCode(ctx context.Context, user *User, promoCode string) *User {
	promoCode = strings.TrimSpace(promoCode)
	if user == nil || user.ID <= 0 || promoCode == "" || s.promoService == nil || s.settingService == nil || !s.settingService.IsPromoCodeEnabled(ctx) {
		return user
	}
	if err := s.promoService.ApplyPromoCode(ctx, user.ID, promoCode); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to apply promo code for oauth user %d: %v", user.ID, err)
		return user
	}
	if updatedUser, err := s.userRepo.GetByID(ctx, user.ID); err == nil {
		return updatedUser
	}
	return user
}

func (s *AuthService) assignSubscriptions(ctx context.Context, userID int64, items []DefaultSubscriptionSetting, notes string) {
	if s.settingService == nil || s.defaultSubAssigner == nil || userID <= 0 {
		return
	}
	for _, item := range items {
		if _, _, err := s.defaultSubAssigner.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:       userID,
			GroupID:      item.GroupID,
			ValidityDays: item.ValidityDays,
			Notes:        notes,
		}); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to assign default subscription: user_id=%d group_id=%d err=%v", userID, item.GroupID, err)
		}
	}
}

func (s *AuthService) resolveSignupGrantPlan(ctx context.Context, signupSource string) signupGrantPlan {
	plan := signupGrantPlan{}
	if s != nil && s.cfg != nil {
		plan.Balance = s.cfg.Default.UserBalance
		plan.Concurrency = s.cfg.Default.UserConcurrency
	}
	if s == nil || s.settingService == nil {
		return plan
	}

	plan.Balance = s.settingService.GetDefaultBalance(ctx)
	plan.Concurrency = s.settingService.GetDefaultConcurrency(ctx)
	plan.Subscriptions = s.settingService.GetDefaultSubscriptions(ctx)

	// ============ 全局 quota 装载（必须在 ResolveAuthSourceGrantSettings 之前） ============
	// 无论 auth source 是否 enabled，全局层都要先装载，确保 !enabled 早退路径也携带全局 quota。
	if quotas, err := s.settingService.GetDefaultPlatformQuotas(ctx); err == nil {
		plan.PlatformQuotas = quotas
	} else {
		logger.LegacyPrintf("service.auth", "[Auth] Warning: load default platform quotas failed: %v (fail-open)", err)
	}
	// ============================================================================================

	resolved, enabled, err := s.settingService.ResolveAuthSourceGrantSettings(ctx, signupSource, false)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to load auth source signup defaults for %s: %v", signupSource, err)
		return plan
	}
	if !enabled {
		return plan // plan.PlatformQuotas 已含全局层
	}

	plan.Balance = resolved.Balance
	plan.Concurrency = resolved.Concurrency
	plan.Subscriptions = resolved.Subscriptions

	// ============ auth source quota merge（仅在 enabled 分支内） ============
	asQuotas := s.settingService.GetAuthSourcePlatformQuotas(ctx, signupSource)
	if plan.PlatformQuotas != nil {
		for platform, patch := range asQuotas {
			if dst := plan.PlatformQuotas[platform]; dst != nil {
				mergePlatformQuotaDefaults(dst, patch)
			}
		}
	}
	// ==============================================================================

	return plan
}

func authSourceSignupSettings(defaults *AuthSourceDefaultSettings, signupSource string) (ProviderDefaultGrantSettings, bool) {
	if defaults == nil {
		return ProviderDefaultGrantSettings{}, false
	}

	switch strings.ToLower(strings.TrimSpace(signupSource)) {
	case "email":
		return defaults.Email, true
	case "linuxdo":
		return defaults.LinuxDo, true
	case "oidc":
		return defaults.OIDC, true
	case "wechat":
		return defaults.WeChat, true
	case "github":
		return defaults.GitHub, true
	case "google":
		return defaults.Google, true
	case "dingtalk":
		return defaults.DingTalk, true
	default:
		return ProviderDefaultGrantSettings{}, false
	}
}

// bindOAuthAffiliate initializes the affiliate profile and binds the inviter
// for an OAuth-registered user. Failures are logged but never block registration.
func (s *AuthService) bindOAuthAffiliate(ctx context.Context, userID int64, affiliateCode string) {
	if s.affiliateService == nil || userID <= 0 {
		return
	}
	if _, err := s.affiliateService.EnsureUserAffiliate(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to initialize affiliate profile for user %d: %v", userID, err)
	}
	if code := strings.TrimSpace(affiliateCode); code != "" {
		if err := s.affiliateService.BindInviterByCode(ctx, userID, code); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to bind affiliate inviter for user %d: %v", userID, err)
		}
	}
}

func (s *AuthService) postAuthUserBootstrap(ctx context.Context, user *User, signupSource string, touchLogin bool) {
	if user == nil || user.ID <= 0 {
		return
	}

	if strings.TrimSpace(signupSource) == "" {
		signupSource = "email"
	}
	s.updateUserSignupSource(ctx, user.ID, signupSource)

	if touchLogin {
		s.touchUserLogin(ctx, user.ID)
	}
}

func (s *AuthService) updateUserSignupSource(ctx context.Context, userID int64, signupSource string) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return
	}
	if strings.TrimSpace(signupSource) == "" {
		return
	}
	if err := s.entClient.User.UpdateOneID(userID).
		SetSignupSource(signupSource).
		Exec(ctx); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to update signup source: user_id=%d source=%s err=%v", userID, signupSource, err)
	}
}

func (s *AuthService) touchUserLogin(ctx context.Context, userID int64) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return
	}
	now := time.Now().UTC()
	if err := s.entClient.User.UpdateOneID(userID).
		SetLastLoginAt(now).
		SetLastActiveAt(now).
		Exec(ctx); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to touch login timestamps: user_id=%d err=%v", userID, err)
	}
}

func (s *AuthService) backfillEmailIdentityOnSuccessfulLogin(ctx context.Context, user *User) {
	if s == nil || user == nil || user.ID <= 0 {
		return
	}
	identity, created := s.ensureEmailAuthIdentity(ctx, user, "auth_service_login_backfill")
	if s.shouldApplyEmailFirstBindDefaults(ctx, user.ID, identity, created) {
		if err := s.ApplyProviderDefaultSettingsOnFirstBind(ctx, user.ID, "email"); err != nil {
			logger.LegacyPrintf("service.auth", "[Auth] Failed to apply email first bind defaults: user_id=%d err=%v", user.ID, err)
		}
	}
}

func (s *AuthService) shouldApplyEmailFirstBindDefaults(
	ctx context.Context,
	userID int64,
	identity *dbent.AuthIdentity,
	created bool,
) bool {
	// ensureEmailAuthIdentity 在没有 ent client / 保留邮箱时返回 (nil,false)；
	// 这里必须先挡住 nil，否则成功登录的回填路径会空指针。
	if identity == nil {
		return false
	}
	source := emailAuthIdentitySource(identity.Metadata)
	if source == "auth_service_login_backfill" {
		return false
	}
	if created {
		return true
	}
	if s == nil || s.entClient == nil || userID <= 0 || identity == nil || identity.UserID != userID {
		return false
	}
	if source != "auth_service_dual_write" {
		return false
	}

	hasGrant, err := s.hasProviderGrantRecord(ctx, userID, "email", "first_bind")
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to inspect email first bind grant state: user_id=%d err=%v", userID, err)
		return false
	}
	return !hasGrant
}

func emailAuthIdentitySource(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	raw, ok := metadata["source"]
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func (s *AuthService) hasProviderGrantRecord(
	ctx context.Context,
	userID int64,
	providerType string,
	grantReason string,
) (bool, error) {
	if s == nil || s.entClient == nil || userID <= 0 {
		return false, nil
	}

	rows, err := s.entClient.QueryContext(
		ctx,
		`SELECT 1 FROM user_provider_default_grants WHERE user_id = $1 AND provider_type = $2 AND grant_reason = $3 LIMIT 1`,
		userID,
		strings.TrimSpace(providerType),
		strings.TrimSpace(grantReason),
	)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	return rows.Next(), rows.Err()
}

func (s *AuthService) ensureEmailAuthIdentity(ctx context.Context, user *User, source string) (*dbent.AuthIdentity, bool) {
	if s == nil || s.entClient == nil || user == nil || user.ID <= 0 {
		return nil, false
	}

	email := strings.ToLower(strings.TrimSpace(user.Email))
	if email == "" || isReservedEmail(email) {
		return nil, false
	}
	if strings.TrimSpace(source) == "" {
		source = "auth_service_dual_write"
	}

	client := s.entClient
	if tx := dbent.TxFromContext(ctx); tx != nil {
		client = tx.Client()
	}

	buildQuery := func() *dbent.AuthIdentityQuery {
		return client.AuthIdentity.Query().Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			AuthIdentitySubjectLookupPredicate("email", email),
		)
	}

	existed, err := buildQuery().Exist(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to inspect email auth identity: user_id=%d email=%s err=%v", user.ID, MaskEmail(email), err)
		return nil, false
	}

	if !existed {
		create := client.AuthIdentity.Create().
			SetUserID(user.ID).
			SetProviderType("email").
			SetProviderKey("email").
			SetVerifiedAt(time.Now().UTC()).
			SetMetadata(map[string]any{
				"source": strings.TrimSpace(source),
			})
		create = ApplyAuthIdentitySubjectCreate(create, "email", email)
		if err = create.
			OnConflictColumns(AuthIdentitySubjectHashConflictColumns()...).
			DoNothing().
			Exec(ctx); err != nil {
			// 滚动窗口兼容：旧实例写入的行只有明文、hash 还是 NULL，冲突会落在
			// 明文唯一索引上。这里不当作失败，交给下面的查询按 hash/明文回退定位，
			// 并做归属校验。
			if !isSQLNoRowsError(err) && !dbent.IsConstraintError(err) {
				logger.LegacyPrintf("service.auth", "[Auth] Failed to ensure email auth identity: user_id=%d email=%s err=%v", user.ID, MaskEmail(email), err)
				return nil, false
			}
		}
	}

	identity, err := buildQuery().Only(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to reload email auth identity: user_id=%d email=%s err=%v", user.ID, MaskEmail(email), err)
		return nil, false
	}
	if identity.UserID != user.ID {
		logger.LegacyPrintf("service.auth", "[Auth] Email auth identity ownership mismatch: user_id=%d email=%s owner_id=%d", user.ID, MaskEmail(email), identity.UserID)
		return nil, false
	}

	return identity, !existed
}

func inferLegacySignupSource(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	switch {
	case strings.HasSuffix(normalized, DingTalkConnectSyntheticEmailDomain):
		return "dingtalk"
	case strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain):
		return "linuxdo"
	case strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain):
		return "oidc"
	case strings.HasSuffix(normalized, WeChatConnectSyntheticEmailDomain):
		return "wechat"
	default:
		return "email"
	}
}

// validateRegistrationEmailPolicy 校验邮箱能否用于新建账号或绑定为账号邮箱：精确黑名单与域名白名单。
func (s *AuthService) validateRegistrationEmailPolicy(ctx context.Context, email string) error {
	if s.settingService == nil {
		return nil
	}
	if s.settingService.IsRegistrationEmailBlacklisted(ctx, email) {
		return ErrEmailBlocked
	}
	return s.validateRegistrationEmailSuffixPolicy(ctx, email)
}

// validateRegistrationEmailSuffixPolicy 只校验域名白名单。
func (s *AuthService) validateRegistrationEmailSuffixPolicy(ctx context.Context, email string) error {
	if s.settingService == nil {
		return nil
	}
	whitelist := s.settingService.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixAllowed(email, whitelist) {
		return buildEmailSuffixNotAllowedError(whitelist)
	}
	return nil
}

// validateRegistrationEmailQuota 精确黑名单命中直接拒绝；保留白名单为空时的全放行行为；
// 配置白名单后，非白名单域名默认直接拒绝（严格白名单模式）；仅当域名限量注册开关开启时，
// 非白名单域名每个最多允许一个账户。
func (s *AuthService) validateRegistrationEmailQuota(ctx context.Context, email string) error {
	if err := s.validateRegistrationEmailAlias(ctx, email); err != nil {
		return err
	}
	if s.settingService == nil {
		return nil
	}
	if s.settingService.IsRegistrationEmailBlacklisted(ctx, email) {
		return ErrEmailBlocked
	}
	whitelist := s.settingService.GetRegistrationEmailSuffixWhitelist(ctx)
	if !IsRegistrationEmailSuffixLimited(email, whitelist) {
		return nil
	}
	if !s.settingService.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return buildEmailSuffixNotAllowedError(whitelist)
	}

	domain := RegistrationEmailDomain(email)
	if domain == "" {
		return buildEmailSuffixNotAllowedError(whitelist)
	}
	quotaRepo, ok := s.userRepo.(RegistrationEmailDomainRepository)
	if !ok {
		// 生产装配必须提供原子仓储能力；没有数据库的 unit 测试桩保留旧路径，
		// 避免无关测试被注册专用依赖干扰。
		if s.entClient != nil {
			return ErrServiceUnavailable
		}
		return nil
	}
	count, err := quotaRepo.CountUsersByEmailDomain(ctx, domain)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to count registration email domain %s: %v", domain, err)
		return ErrServiceUnavailable
	}
	if count > 0 {
		return ErrEmailDomainRegistrationLimit
	}
	return nil
}

// createOAuthRegisteredUser 创建第三方登录/注册产生的新账号。
// 真实邮箱必须走与邮箱注册相同的原子守卫（别名去重 + 域名限量），
// 渠道合成邮箱是内部占位，不参与真实邮箱策略。
func (s *AuthService) createOAuthRegisteredUser(ctx context.Context, user *User) error {
	if user == nil {
		return ErrServiceUnavailable
	}
	if isReservedEmail(user.Email) {
		return s.userRepo.Create(ctx, user)
	}
	return s.createUserWithRegistrationEmailGuard(ctx, user)
}

func (s *AuthService) createUserWithRegistrationEmailGuard(ctx context.Context, user *User) error {
	if s == nil || s.userRepo == nil {
		return ErrServiceUnavailable
	}
	whitelist := []string{}
	if s.settingService != nil {
		whitelist = s.settingService.GetRegistrationEmailSuffixWhitelist(ctx)
	}
	domain := RegistrationEmailDomain(user.Email)
	if !IsRegistrationEmailSuffixLimited(user.Email, whitelist) {
		return s.userRepo.CreateWithEmailAliasGuard(ctx, user)
	}
	// 开关关闭时非白名单域名在校验阶段已被拒绝；此处兜底防御设置竞态变更。
	if s.settingService == nil || !s.settingService.IsRegistrationEmailDomainQuotaEnabled(ctx) {
		return buildEmailSuffixNotAllowedError(whitelist)
	}
	if domain == "" {
		return buildEmailSuffixNotAllowedError(whitelist)
	}
	quotaRepo, ok := s.userRepo.(RegistrationEmailDomainRepository)
	if !ok {
		if s.entClient != nil {
			return ErrServiceUnavailable
		}
		return s.userRepo.CreateWithEmailAliasGuard(ctx, user)
	}
	return quotaRepo.CreateWithEmailAliasGuardAndDomainLimit(ctx, user, domain)
}

// createUserAndClaimInvitation 原子化完成“用户创建 + 邀请码占用”。
//
// 背景：邀请码属于一次性凭证，必须保证“一个邀请码最多注册一个账号”。旧实现先检查
// CanUse()、再创建用户、最后才 redeemRepo.Use()（且失败仅记日志），检查与消耗分离且
// 不在同一事务，并发注册可在同一邀请码上同时通过检查并各自创建账号（TOCTOU 竞态）。
//
// 本实现把两者放入同一个数据库事务：
//   - 占用走 redeemRepo.Use 的条件更新（WHERE status='unused'，乐观锁）；
//   - 并发下只有一个事务能占用成功，其余事务回滚——既不产生多余账号，也不让码被烧掉；
//   - 事务回滚同时撤销用户创建，避免“账号已建、码被占用”的中间态。
//
// 无邀请码时保持原单次创建路径（不开事务）；entClient 缺失的异常配置下退化为顺序执行，
// 并发正确性仍由 Use 的条件更新兜底（可能产生孤儿用户，但不会放行第二个注册）。
func (s *AuthService) createUserAndClaimInvitation(ctx context.Context, user *User, invitation *RedeemCode) error {
	commitUser := func(execCtx context.Context) error {
		if err := s.createUserWithRegistrationEmailGuard(execCtx, user); err != nil {
			return err
		}
		if invitation == nil {
			return nil
		}
		// createUserWithRegistrationEmailGuard 会回填 user.ID（applyUserEntityToService），
		// 直接以其原子占用邀请码；占用失败即整体回滚（含用户创建，见 user_repo.create
		// 对外部事务的复用）。
		if err := s.redeemRepo.Use(execCtx, invitation.ID, user.ID); err != nil {
			// 并发下唯一的合法失败路径：另一个注册已占用该码
			logger.LegacyPrintf("service.auth",
				"[Auth] Rejected registration: invitation code %s already claimed (user_id=%d err=%v)",
				invitation.Code, user.ID, err)
			return ErrInvitationCodeInvalid
		}
		return nil
	}

	if invitation == nil {
		return commitUser(ctx)
	}
	if s.entClient == nil {
		return commitUser(ctx)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to start registration transaction: %v", err)
		return ErrServiceUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	execCtx := dbent.NewTxContext(ctx, tx)
	if err := commitUser(execCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to commit registration transaction: %v", err)
		return ErrServiceUnavailable
	}
	return nil
}

func buildEmailSuffixNotAllowedError(whitelist []string) error {
	if len(whitelist) == 0 {
		return ErrEmailSuffixNotAllowed
	}

	allowed := strings.Join(whitelist, ", ")
	return infraerrors.BadRequest(
		"EMAIL_SUFFIX_NOT_ALLOWED",
		fmt.Sprintf("email suffix is not allowed, allowed suffixes: %s", allowed),
	).WithMetadata(map[string]string{
		"allowed_suffixes":     strings.Join(whitelist, ","),
		"allowed_suffix_count": strconv.Itoa(len(whitelist)),
	})
}

// ValidateToken 验证JWT token并返回用户声明
func (s *AuthService) ValidateToken(tokenString string) (*JWTClaims, error) {
	// 先做长度校验，尽早拒绝异常超长 token，降低 DoS 风险。
	if len(tokenString) > maxTokenLength {
		return nil, ErrTokenTooLarge
	}

	// 使用解析器并限制可接受的签名算法，防止算法混淆。
	parser := jwt.NewParser(jwt.WithValidMethods([]string{
		jwt.SigningMethodHS256.Name,
		jwt.SigningMethodHS384.Name,
		jwt.SigningMethodHS512.Name,
	}))

	// 保留默认 claims 校验（exp/nbf），避免放行过期或未生效的 token。
	token, err := parser.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (any, error) {
		// 验证签名方法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.cfg.JWT.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			// token 过期但仍返回 claims（用于 RefreshToken 等场景）
			// jwt-go 在解析时即使遇到过期错误，token.Claims 仍会被填充
			if claims, ok := token.Claims.(*JWTClaims); ok {
				return claims, ErrTokenExpired
			}
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

func randomHexString(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 16
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isReservedEmail(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, WeChatConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, DingTalkConnectSyntheticEmailDomain)
}

// IsSyntheticConnectEmail 报告邮箱是否为第三方 OAuth/SSO 登录时内部生成的
// 占位地址（@*-connect.invalid）。这类地址没有真实收件箱，不能用于登录或
// 密码找回，因此后台在判断「改邮箱是否等于转移账号控制权」时需要区分。
func IsSyntheticConnectEmail(email string) bool {
	return isReservedEmail(email)
}

// sessionBindingHash 按 security.session_binding_mode 计算当前请求的会话指纹哈希。
// mode=off 时返回空串（完全不绑定）；其余模式把 IP/UA 绑定写入 access/refresh token，
// 由中间件与 refresh 路径校验。配置缺失时按 strict 处理（兼容旧部署与测试）。
func (s *AuthService) sessionBindingHash(ctx context.Context) string {
	if s == nil || s.cfg == nil || s.cfg.SessionBindingDisabled() {
		return ""
	}
	return SessionBindingFromContext(ctx).HashForMode(s.cfg.Security.SessionBindingMode)
}

// sessionBindingForIssue 返回签发令牌时写入的会话指纹：旧算法的哈希 + 粗粒度指纹
// （两者都为空 = 不绑定）。哈希保持旧算法，新旧版本混跑时旧实例仍能正确比对。
func (s *AuthService) sessionBindingForIssue(ctx context.Context) (hash, coarse string) {
	hash = s.sessionBindingHash(ctx)
	if hash == "" {
		return "", ""
	}
	return hash, SessionBindingFromContext(ctx).CoarseFingerprint()
}

// GenerateToken 生成JWT access token
// 使用新的access_token_expire_minutes配置项（默认60分钟），显式配置为0时回退到expire_hour。
// 会话指纹（IP/UA）从 ctx 中提取（由 HTTP 入口中间件注入），缺失时生成不带绑定的 token。
func (s *AuthService) GenerateToken(ctx context.Context, user *User) (string, error) {
	sessionID, err := randomHexString(8)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	bindingHash, bindingCoarse := s.sessionBindingForIssue(ctx)
	return s.generateAccessToken(user, sessionID, bindingHash, bindingCoarse)
}

// sessionBindingVersion 是当前签发 token 时写入的会话指纹版本。
// 老 token 没有该字段（版本 0），中间件据此跳过校验以平滑升级。
const sessionBindingVersion = 1

// generateAccessToken 生成带会话 ID 与绑定指纹的 access token：bindingHash 是旧算法的指纹哈希
// （bnd claim，新旧版本混跑时旧实例仍能比对），bindingCoarse 是粗粒度指纹（bcs claim，可为空）。
func (s *AuthService) generateAccessToken(user *User, sessionID, bindingHash, bindingCoarse string) (string, error) {
	now := time.Now()
	var expiresAt time.Time
	if s.cfg.JWT.AccessTokenExpireMinutes > 0 {
		expiresAt = now.Add(time.Duration(s.cfg.JWT.AccessTokenExpireMinutes) * time.Minute)
	} else {
		// 向后兼容：使用旧的expire_hour配置
		expiresAt = now.Add(time.Duration(s.cfg.JWT.ExpireHour) * time.Hour)
	}

	claims := &JWTClaims{
		UserID:        user.ID,
		Email:         user.Email,
		Role:          user.Role,
		TokenVersion:  resolvedTokenVersion(user),
		SessionID:     sessionID,
		BindingHash:   bindingHash,
		BindingCoarse: bindingCoarse,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	if bindingHash != "" {
		claims.BindingVersion = sessionBindingVersion
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.cfg.JWT.Secret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return tokenString, nil
}

// GetAccessTokenExpiresIn 返回Access Token的有效期（秒）
// 用于前端设置刷新定时器
func (s *AuthService) GetAccessTokenExpiresIn() int {
	if s.cfg.JWT.AccessTokenExpireMinutes > 0 {
		return s.cfg.JWT.AccessTokenExpireMinutes * 60
	}
	return s.cfg.JWT.ExpireHour * 3600
}

// HashPassword 使用bcrypt加密密码
func (s *AuthService) HashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

// CheckPassword 验证密码是否匹配
func (s *AuthService) CheckPassword(password, hashedPassword string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// EqualizePasswordTiming 执行一次等成本的 bcrypt 比对，用于「账号不存在」分支
// 抹平响应时间差。结果固定为 false，不参与任何判断。
func (s *AuthService) EqualizePasswordTiming(password string) {
	_ = bcrypt.CompareHashAndPassword([]byte(loginTimingDummyHash), []byte(password))
}

// RefreshToken 刷新token
func (s *AuthService) RefreshToken(ctx context.Context, oldTokenString string) (string, error) {
	// 验证旧token（即使过期也允许，用于刷新）
	claims, err := s.ValidateToken(oldTokenString)
	if err != nil && !errors.Is(err, ErrTokenExpired) {
		return "", err
	}

	// 获取最新的用户信息
	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", ErrInvalidToken
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error refreshing token: %v", err)
		return "", ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", ErrUserNotActive
	}

	// Security: Check TokenVersion to prevent refreshing revoked tokens
	// This ensures tokens issued before a password change cannot be refreshed
	if claims.TokenVersion != resolvedTokenVersion(user) {
		return "", ErrTokenRevoked
	}

	// 会话绑定检查：指纹变化的旧 token 的处置由 security.session_binding_on_mismatch 决定：
	// rebind（默认）放行，下面签发的新 token 带当前指纹，即「重绑」；revoke 撤销整个会话家族。
	// refresh 路径始终校验绑定（不再受后台 session_binding_enabled 开关控制），
	// 只有 security.session_binding_mode=off 才整体关闭。
	if s.cfg != nil && !s.cfg.SessionBindingDisabled() && claims.BindingHash != "" {
		binding := SessionBindingFromContext(ctx)
		if !binding.Matches(s.cfg.Security.SessionBindingMode, claims.BindingHash, claims.BindingCoarse) &&
			s.cfg.SessionBindingRevokeOnMismatch() {
			_ = s.RevokeSessionFamily(ctx, claims.SessionID)
			return "", ErrSessionBindingMismatch
		}
	}

	// 生成新token
	return s.GenerateToken(ctx, user)
}

// IsPasswordResetEnabled 检查是否启用密码重置功能
// 要求：必须同时开启邮件验证且 SMTP 配置正确
func (s *AuthService) IsPasswordResetEnabled(ctx context.Context) bool {
	if s.settingService == nil {
		return false
	}
	// Must have email verification enabled and SMTP configured
	if !s.settingService.IsEmailVerifyEnabled(ctx) {
		return false
	}
	return s.settingService.IsPasswordResetEnabled(ctx)
}

// preparePasswordReset validates the password reset request and returns necessary data
// Returns (siteName, resetURL, shouldProceed)
// shouldProceed is false when we should silently return success (to prevent enumeration)
func (s *AuthService) preparePasswordReset(ctx context.Context, email, frontendBaseURL string) (string, string, bool) {
	// Check if user exists (but don't reveal this to the caller)
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Security: Log but don't reveal that user doesn't exist
			logger.LegacyPrintf("service.auth", "[Auth] Password reset requested for non-existent email: %s", logredact.MaskEmail(email))
			return "", "", false
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error checking email for password reset: %v", err)
		return "", "", false
	}

	// Check if user is active
	if !user.IsActive() {
		logger.LegacyPrintf("service.auth", "[Auth] Password reset requested for inactive user: %s", email)
		return "", "", false
	}

	// Get site name
	siteName := "Sub2API"
	if s.settingService != nil {
		siteName = s.settingService.GetSiteName(ctx)
	}

	// Build reset URL base
	resetURL := fmt.Sprintf("%s/reset-password", strings.TrimSuffix(frontendBaseURL, "/"))

	return siteName, resetURL, true
}

// RequestPasswordReset 请求密码重置（同步发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordReset(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailService == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.preparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.emailService.SendPasswordResetEmail(ctx, email, siteName, resetURL, firstEmailLocale(locale)); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to send password reset email to %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset email sent to: %s", email)
	return nil
}

// RequestPasswordResetAsync 异步请求密码重置（队列发送）
// Security: Returns the same response regardless of whether the email exists (prevent user enumeration)
func (s *AuthService) RequestPasswordResetAsync(ctx context.Context, email, frontendBaseURL string, locale ...string) error {
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}
	if s.emailQueueService == nil {
		return ErrServiceUnavailable
	}

	siteName, resetURL, shouldProceed := s.preparePasswordReset(ctx, email, frontendBaseURL)
	if !shouldProceed {
		return nil // Silent success to prevent enumeration
	}

	if err := s.emailQueueService.EnqueuePasswordReset(email, siteName, resetURL, firstEmailLocale(locale)); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to enqueue password reset email for %s: %v", email, err)
		return nil // Silent success to prevent enumeration
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset email enqueued for: %s", logredact.MaskEmail(email))
	return nil
}

// ResetPassword 重置密码
// Security: Increments TokenVersion to invalidate all existing JWT tokens
func (s *AuthService) ResetPassword(ctx context.Context, email, token, newPassword string) error {
	// Check if password reset is enabled
	if !s.IsPasswordResetEnabled(ctx) {
		return infraerrors.Forbidden("PASSWORD_RESET_DISABLED", "password reset is not enabled")
	}

	if s.emailService == nil {
		return ErrServiceUnavailable
	}

	// Verify and consume the reset token (one-time use)
	if err := s.emailService.ConsumePasswordResetToken(ctx, email, token); err != nil {
		return err
	}

	// Get user
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return ErrInvalidResetToken // Token was valid but user was deleted
		}
		logger.LegacyPrintf("service.auth", "[Auth] Database error getting user for password reset: %v", err)
		return ErrServiceUnavailable
	}

	// Check if user is active
	if !user.IsActive() {
		return ErrUserNotActive
	}

	// Hash new password
	hashedPassword, err := s.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// Update password and increment TokenVersion
	user.PasswordHash = hashedPassword
	user.TokenVersion++ // Invalidate all existing tokens

	// TokenVersion 无对应数据库列（见 resolvedTokenVersion：由 email+password_hash 指纹推导），
	// 写回 password_hash 本身即可让旧 token 失效。
	if err := s.userRepo.Update(ctx, user, UserUpdateFields{PasswordHash: true}); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Database error updating password for user %d: %v", user.ID, err)
		return ErrServiceUnavailable
	}

	// Also revoke all refresh tokens for this user
	if err := s.RevokeAllUserSessions(ctx, user.ID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh tokens for user %d: %v", user.ID, err)
		// Don't return error - password was already changed successfully
	}

	logger.LegacyPrintf("service.auth", "[Auth] Password reset successful for user: %s", email)
	return nil
}

// ==================== Refresh Token Methods ====================

// TokenPair 包含Access Token和Refresh Token
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // Access Token有效期（秒）
}

// TokenPairWithUser extends TokenPair with user role for backend mode checks
type TokenPairWithUser struct {
	TokenPair
	UserRole string
}

// GenerateTokenPair 生成Access Token和Refresh Token对（新会话）。
// familyID: 可选的Token家族ID，用于Token轮转时保持家族关系
func (s *AuthService) GenerateTokenPair(ctx context.Context, user *User, familyID string) (*TokenPair, error) {
	return s.GenerateTokenPairForSession(ctx, user, familyID, time.Time{})
}

// GenerateTokenPairForSession 生成Access Token和Refresh Token对，并显式指定
// 该登录会话的首次签发时间（sessionStartedAt）。轮换 refresh token 时必须
// 传入原会话的 SessionStart，使会话绝对寿命上限（jwt.session_max_lifetime_days）
// 不会被滑动续期绕过；传零值表示这是一次全新的登录。
func (s *AuthService) GenerateTokenPairForSession(ctx context.Context, user *User, familyID string, sessionStartedAt time.Time) (*TokenPair, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, ErrRefreshTokenCacheNotConfigured
	}

	// 提前确定家族ID：作为 access token 的会话ID（sid），保证同一会话的
	// access/refresh token 可以互相关联（单会话撤销、step-up 授权绑定）。
	if familyID == "" {
		familyBytes := make([]byte, 16)
		if _, err := rand.Read(familyBytes); err != nil {
			return nil, fmt.Errorf("generate family id: %w", err)
		}
		familyID = hex.EncodeToString(familyBytes)
	}

	// 生成Access Token（携带会话ID与绑定指纹）
	bindingHash, bindingCoarse := s.sessionBindingForIssue(ctx)
	accessToken, err := s.generateAccessToken(user, familyID, bindingHash, bindingCoarse)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	// 生成Refresh Token
	refreshToken, err := s.generateRefreshToken(ctx, user, familyID, sessionStartedAt)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    s.GetAccessTokenExpiresIn(),
	}, nil
}

// refreshTokenSlidingTTL 返回每枚 refresh token 的滑动有效期（jwt.refresh_token_expire_days）。
// 配置校验保证生产环境 > 0；这里对 0 做防御性回退（默认值），避免测试或未走完整配置
// 校验的调用方生成 0 TTL 的令牌。
func (s *AuthService) refreshTokenSlidingTTL() time.Duration {
	days := config.DefaultRefreshTokenExpireDays
	if s != nil && s.cfg != nil && s.cfg.JWT.RefreshTokenExpireDays > 0 {
		days = s.cfg.JWT.RefreshTokenExpireDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// sessionMaxLifetime 返回登录会话从首次登录起的绝对寿命上限（jwt.session_max_lifetime_days）。
// 轮换只延长单枚令牌的有效期，不延长这个上限。上限不会小于一枚令牌的滑动有效期
// （配置校验保证；这里对手写 Config 再兜一层，避免令牌一发出就超过会话上限）。
func (s *AuthService) sessionMaxLifetime() time.Duration {
	days := config.DefaultSessionMaxLifetimeDays
	if s != nil && s.cfg != nil && s.cfg.JWT.SessionMaxLifetimeDays > 0 {
		days = s.cfg.JWT.SessionMaxLifetimeDays
	}
	maxLifetime := time.Duration(days) * 24 * time.Hour
	if sliding := s.refreshTokenSlidingTTL(); maxLifetime < sliding {
		maxLifetime = sliding
	}
	return maxLifetime
}

// refreshTokenReuseLeeway 返回已消费 refresh token 的重试宽限；0 表示关闭（旧行为：任何重放都撤销）。
func (s *AuthService) refreshTokenReuseLeeway() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.JWT.RefreshTokenReuseLeewaySeconds <= 0 {
		return 0
	}
	return time.Duration(s.cfg.JWT.RefreshTokenReuseLeewaySeconds) * time.Second
}

// generateRefreshToken 生成并存储Refresh Token。
// 有效期是滑动的：ExpiresAt = min(现在 + jwt.refresh_token_expire_days,
// 会话起点 + jwt.session_max_lifetime_days)——每次轮换从「现在」重新计时，但绝不超过会话的
// 绝对上限。sessionStartedAt 是会话首次签发时间，零值表示新会话，以当前时刻为起点。
func (s *AuthService) generateRefreshToken(ctx context.Context, user *User, familyID string, sessionStartedAt time.Time) (string, error) {
	// 生成随机Token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawToken := refreshTokenPrefix + hex.EncodeToString(tokenBytes)

	// 计算Token哈希（存储哈希而非原始Token）
	tokenHash := hashToken(rawToken)

	// 如果没有提供familyID，生成新的
	if familyID == "" {
		familyBytes := make([]byte, 16)
		if _, err := rand.Read(familyBytes); err != nil {
			return "", fmt.Errorf("generate family id: %w", err)
		}
		familyID = hex.EncodeToString(familyBytes)
	}

	now := time.Now()
	if sessionStartedAt.IsZero() {
		sessionStartedAt = now
	}
	sessionDeadline := sessionStartedAt.Add(s.sessionMaxLifetime())
	if !now.Before(sessionDeadline) {
		return "", ErrRefreshTokenSessionExpired
	}
	slidingTTL := s.refreshTokenSlidingTTL()
	expiresAt := now.Add(slidingTTL)
	if expiresAt.After(sessionDeadline) {
		expiresAt = sessionDeadline
	}
	ttl := time.Until(expiresAt)

	bindingHash, bindingCoarse := s.sessionBindingForIssue(ctx)
	data := &RefreshTokenData{
		UserID:           user.ID,
		TokenVersion:     resolvedTokenVersion(user),
		FamilyID:         familyID,
		BindingHash:      bindingHash,
		BindingCoarse:    bindingCoarse,
		SessionStartedAt: sessionStartedAt,
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}

	// 存储Token数据
	if err := s.refreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}

	// 两个集合（用户/家族）的 TTL 取一枚令牌的完整滑动有效期，而不是这枚令牌的剩余寿命：
	// 令牌的剩余寿命可能因为接近会话上限而很短，集合若跟着缩短，就会在同一用户/家族里
	// 另一枚更长寿的令牌过期之前先消失，让「撤销全部会话 / 撤销整个家族」漏掉那枚令牌。
	// 每次写入都会把集合 TTL 重置为完整的滑动有效期，所以集合总是比它包含的任何令牌活得久。
	// 添加到用户Token集合
	if err := s.refreshTokenCache.AddToUserTokenSet(ctx, user.ID, tokenHash, slidingTTL); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to add token to user set: %v", err)
		// 不影响主流程
	}

	// 添加到家族Token集合
	if err := s.refreshTokenCache.AddToFamilyTokenSet(ctx, familyID, tokenHash, slidingTTL); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to add token to family set: %v", err)
		// 不影响主流程
	}

	return rawToken, nil
}

// RefreshTokenPair 使用Refresh Token刷新Token对。
//
// Token轮转：每次刷新都会签发新的 Refresh Token，旧 Token 被标记为"已消费"（留一条墓碑）。
// 顺序是「先发后消费」：先创建并存好新令牌，再把旧令牌标记为已消费——新令牌存不进去时旧令牌
// 保持可用，客户端可以安全重试，不会出现「旧的作废了、新的没发出去」把用户锁在外面。
//
// 已消费的旧 Token 再次出现时：
//   - 消费之后 jwt.refresh_token_reuse_leeway_seconds 以内（且重发次数未达上限）：视为响应丢失
//     （弱网/超时）后的合法重试，不撤销会话，再签发一对新令牌（同一家族、同一会话起点；第一次
//     轮换签发的新令牌仍然有效，客户端存着哪一枚都能继续用）；
//   - 其余情况：视为重放，撤销整个 token family。这是安全底线：即使攻击者先用了被盗 Token，
//     合法会话也会随之失效，而不是让攻击者与受害者长期并行持有会话。
//
// 会话有两个寿命：每枚令牌滑动 jwt.refresh_token_expire_days（每次轮换重新计时），以及会话
// 从首次登录起的绝对上限 jwt.session_max_lifetime_days（轮换不延长）。
func (s *AuthService) RefreshTokenPair(ctx context.Context, refreshToken string) (*TokenPairWithUser, error) {
	stats := s.authStats()

	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		stats.inc(statRefreshInvalid)
		return nil, ErrRefreshTokenInvalid
	}

	// 验证Token格式
	if !strings.HasPrefix(refreshToken, refreshTokenPrefix) {
		stats.inc(statRefreshInvalid)
		return nil, ErrRefreshTokenInvalid
	}

	tokenHash := hashToken(refreshToken)

	// 获取Token数据
	data, err := s.refreshTokenCache.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// Token不存在：已过期、所属会话已被撤销，或是超过重放窗口的旧令牌
			stats.inc(statRefreshInvalid)
			return nil, ErrRefreshTokenInvalid
		}
		stats.inc(statRefreshUnavailable)
		if !isContextDone(err) {
			s.warnEvent("refresh_unavailable", "refresh token lookup failed", "error", err)
		}
		return nil, ErrServiceUnavailable
	}

	now := time.Now()
	leewayRetry := false
	if data.Consumed {
		if !s.refreshReuseWithinLeeway(data, now) {
			// 宽限之外的重放（或旧墓碑/宽限关闭）：撤销整个 token family。
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			stats.inc(statRefreshReuse)
			s.warnEvent("refresh_reuse", "refresh token replay detected: session family revoked", "user_id", data.UserID)
			return nil, ErrRefreshTokenReuse
		}
		leewayRetry = true
	}

	// 检查Token是否过期
	if now.After(data.ExpiresAt) {
		// 删除过期Token
		_ = s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
		stats.inc(statRefreshExpired)
		return nil, ErrRefreshTokenExpired
	}

	// 会话绝对寿命上限：轮换不能把会话无限续期。首次签发时间（旧数据回退到
	// CreatedAt）+ jwt.session_max_lifetime_days 到期后必须重新登录。
	if now.After(data.SessionStart().Add(s.sessionMaxLifetime())) {
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		stats.inc(statRefreshSessionExpired)
		s.warnEvent("refresh_session_expired", "refresh session reached its absolute lifetime", "user_id", data.UserID)
		return nil, ErrRefreshTokenSessionExpired
	}

	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, data.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 用户已删除，撤销整个Token家族
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			stats.inc(statRefreshInvalid)
			return nil, ErrRefreshTokenInvalid
		}
		stats.inc(statRefreshUnavailable)
		if !isContextDone(err) {
			s.warnEvent("refresh_unavailable", "load user for token refresh failed", "user_id", data.UserID, "error", err)
		}
		return nil, ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		// 用户被禁用，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		stats.inc(statRefreshRejected)
		return nil, ErrUserNotActive
	}

	// 检查TokenVersion（密码更改后所有Token失效）
	if data.TokenVersion != resolvedTokenVersion(user) {
		// TokenVersion不匹配，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		stats.inc(statRefreshRejected)
		return nil, ErrTokenRevoked
	}

	// 会话绑定检查：指纹不一致时的处置由 security.session_binding_on_mismatch 决定——
	// rebind（默认）放行，下面签发的新令牌带当前指纹（即「重绑」），只留一条限频 WARN；
	// revoke 撤销整个会话家族（升级前的旧行为）。
	// refresh 路径始终校验绑定（不再受后台 session_binding_enabled 开关控制），
	// 只有 security.session_binding_mode=off 才整体关闭；mode=loose（默认）只比较粗粒度指纹。
	rebind := false
	if s.refreshBindingMismatch(ctx, data) {
		if s.cfg.SessionBindingRevokeOnMismatch() {
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			stats.inc(statRefreshBindingRevoke)
			s.warnEvent("refresh_binding_revoke", "session binding mismatch on refresh: session family revoked",
				"user_id", data.UserID, "from", BindingLabel(data.BindingCoarse), "to", BindingLabel(SessionBindingFromContext(ctx).CoarseFingerprint()))
			return nil, ErrSessionBindingMismatch
		}
		rebind = true
	}

	// 先发后消费：先创建并存好新令牌（同一个家族ID与会话起点，绝对寿命上限不随轮换延长）。
	// 存不进去时旧令牌原样保留——客户端可以安全重试，不会被锁在外面。
	pair, err := s.GenerateTokenPairForSession(ctx, user, data.FamilyID, data.SessionStart())
	if err != nil {
		if errors.Is(err, ErrRefreshTokenSessionExpired) {
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			stats.inc(statRefreshSessionExpired)
			return nil, ErrRefreshTokenSessionExpired
		}
		stats.inc(statRefreshUnavailable)
		if !isContextDone(err) {
			s.warnEvent("refresh_unavailable", "issuing the new refresh token failed; the old token stays valid so the client can retry",
				"user_id", data.UserID, "error", err)
		}
		return nil, ErrServiceUnavailable
	}

	// 新令牌已就位，再处理旧令牌：首次轮换 → 标记已消费；宽限内的重试 → 只累加重发计数
	// （消费时间不变，宽限窗口与墓碑到期时间都不因重试被延长）。
	if leewayRetry {
		s.recordLeewayReissue(ctx, tokenHash, data, now)
	} else {
		s.consumeRefreshToken(ctx, tokenHash, data, now)
	}

	stats.inc(statRefreshOK)
	if leewayRetry {
		stats.inc(statRefreshReuseLeewayRetry)
		s.warnEvent("refresh_leeway_retry", "refresh token retried within the leeway window: a new token pair was issued and the session kept",
			"user_id", data.UserID, "reissues", data.LeewayReissues)
	}
	if rebind {
		stats.inc(statRefreshBindingRebind)
		s.warnEvent("refresh_binding_rebind", "session binding mismatch on refresh: session kept and re-bound to the current client",
			"user_id", data.UserID, "from", BindingLabel(data.BindingCoarse), "to", BindingLabel(SessionBindingFromContext(ctx).CoarseFingerprint()))
	}
	return &TokenPairWithUser{
		TokenPair: *pair,
		UserRole:  user.Role,
	}, nil
}

// refreshReuseWithinLeeway 报告一个已消费的 Token 此刻再次出现是否仍算「响应丢失后的合法重试」。
// 旧墓碑没有 ConsumedAt、宽限为 0（关闭）、超过宽限、或重发次数已达上限，都不算（按重放处理）。
func (s *AuthService) refreshReuseWithinLeeway(data *RefreshTokenData, now time.Time) bool {
	leeway := s.refreshTokenReuseLeeway()
	if leeway <= 0 || data.ConsumedAt.IsZero() {
		return false
	}
	if now.Sub(data.ConsumedAt) > leeway {
		return false
	}
	return data.LeewayReissues < maxRefreshLeewayReissues
}

// refreshBindingMismatch 报告这次 refresh 请求的指纹与令牌里记录的指纹是否不一致。
// 兼容：data.BindingHash 为空表示绑定上线前签发的旧会话，放行并在轮转时补齐绑定。
// 旧数据没有 SessionStartedAt，指纹是按升级前的严格算法写入的；升级到 loose
// 模式后直接比对会误杀全部存量会话，因此旧会话放行一次，轮换后写入新指纹。
func (s *AuthService) refreshBindingMismatch(ctx context.Context, data *RefreshTokenData) bool {
	if s.cfg == nil || s.cfg.SessionBindingDisabled() || data.BindingHash == "" || data.SessionStartedAt.IsZero() {
		return false
	}
	return !SessionBindingFromContext(ctx).Matches(s.cfg.Security.SessionBindingMode, data.BindingHash, data.BindingCoarse)
}

// consumeRefreshToken 把已经换发过新令牌的旧令牌标记为"已消费"：保留一条短 TTL 的墓碑，
// 使下一次重放能定位 family。尽力而为——新令牌已经存好，旧令牌没标记成功也不该让这次刷新失败：
// 墓碑写不进去就退而求其次直接删除旧令牌（之后重放只会得到 invalid，不会连带撤销家族）。
func (s *AuthService) consumeRefreshToken(ctx context.Context, tokenHash string, data *RefreshTokenData, now time.Time) {
	data.Consumed = true
	data.ConsumedAt = now
	data.LeewayReissues = 0
	ttl := time.Until(data.ExpiresAt)
	if ttl > refreshTokenReuseWindow {
		ttl = refreshTokenReuseWindow
	}
	if ttl > 0 {
		err := s.refreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl)
		if err == nil {
			return
		}
		if !isContextDone(err) {
			s.warnEvent("refresh_consume_failed", "could not mark the old refresh token consumed, deleting it instead", "user_id", data.UserID, "error", err)
		}
	}
	if err := s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash); err != nil && !isContextDone(err) {
		s.warnEvent("refresh_consume_failed", "could not delete the old refresh token", "user_id", data.UserID, "error", err)
	}
}

// recordLeewayReissue 在宽限期内的重试成功重发新令牌之后，把重发次数累加到墓碑上。
// 墓碑的消费时间与自然到期时间保持不变。尽力而为：写失败只是少计一次，不影响这次刷新。
func (s *AuthService) recordLeewayReissue(ctx context.Context, tokenHash string, data *RefreshTokenData, now time.Time) {
	data.LeewayReissues++
	ttl := refreshTokenReuseWindow - now.Sub(data.ConsumedAt)
	if until := time.Until(data.ExpiresAt); until < ttl {
		ttl = until
	}
	if ttl <= 0 {
		return
	}
	if err := s.refreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl); err != nil && !isContextDone(err) {
		s.warnEvent("refresh_consume_failed", "could not record the leeway reissue count", "user_id", data.UserID, "error", err)
	}
}

// RevokeRefreshToken 撤销单个Refresh Token
func (s *AuthService) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	if !strings.HasPrefix(refreshToken, refreshTokenPrefix) {
		return ErrRefreshTokenInvalid
	}

	tokenHash := hashToken(refreshToken)
	data, err := s.refreshTokenCache.GetRefreshToken(ctx, tokenHash)
	if err != nil && !errors.Is(err, ErrRefreshTokenNotFound) {
		return err
	}
	if data != nil && data.FamilyID != "" {
		if err := s.RevokeSessionFamily(ctx, data.FamilyID); err != nil {
			return err
		}
	}
	return s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
}

// RevokeCryptoGrantSessionForAccessToken authenticates logout's optional access
// token before revoking its payment capability. It never changes JWT validity.
func (s *AuthService) RevokeCryptoGrantSessionForAccessToken(ctx context.Context, tokenString string) error {
	claims, err := s.ValidateToken(tokenString)
	if err != nil {
		return err
	}
	sessionID := claims.SessionID
	if sessionID == "" {
		sessionID = "jwt:" + hashToken(tokenString)
	}
	return s.RevokeSessionFamily(ctx, sessionID)
}

// RevokeSessionFamily 撤销单个会话家族（该会话的所有 refresh token）。
// 用于会话绑定失效等单会话级撤销场景，不影响用户的其他设备会话。
func (s *AuthService) RevokeSessionFamily(ctx context.Context, familyID string) error {
	if s.refreshTokenCache == nil || familyID == "" {
		return nil
	}
	return s.refreshTokenCache.DeleteTokenFamily(ctx, familyID)
}

// RevokeAllUserSessions 撤销用户的所有会话（所有Refresh Token）
// 用于密码更改或用户主动登出所有设备
func (s *AuthService) RevokeAllUserSessions(ctx context.Context, userID int64) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	return s.refreshTokenCache.DeleteUserRefreshTokens(ctx, userID)
}

// RevokeAllUserTokens invalidates both stateless access tokens and refresh sessions.
//
// 注意：users 表没有 token_version 列（resolvedTokenVersion 由 email+password_hash
// 指纹推导），因此对 user.TokenVersion 自增只影响内存副本。之前紧跟其后的整行
// Update 不写任何有效数据，却会用旧快照覆盖并发写入的列，故已移除。
// 会话撤销由下面的 refresh session 清理承担；改密路径通过 password_hash 变化
// 改变指纹，从而使旧 token 失效。
func (s *AuthService) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	if _, err := s.userRepo.GetByID(ctx, userID); err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke refresh sessions after token invalidation for user %d: %v", userID, err)
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	// Refresh tokens alone do not stop a leaked access token: JWTs stay valid
	// until they expire. Record the revocation instant so the auth middleware
	// rejects every token issued at or before now.
	if err := s.RevokeAccessTokensBefore(ctx, userID, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Failed to revoke access tokens for user %d: %v", userID, err)
		return fmt.Errorf("revoke access tokens: %w", err)
	}
	return nil
}

// RevokeAccessTokensBefore rejects access tokens issued before the given time.
// It is a no-op when the cache does not support revocation.
func (s *AuthService) RevokeAccessTokensBefore(ctx context.Context, userID int64, before time.Time) error {
	if s == nil || userID <= 0 || before.IsZero() {
		return nil
	}
	store, ok := s.refreshTokenCache.(AccessTokenRevocationStore)
	if !ok {
		return nil
	}
	return store.RevokeAccessTokensBefore(ctx, userID, before, accessTokenRevocationTTL)
}

// IsAccessTokenRevoked reports whether the token's iat falls at or before the
// last "revoke all sessions" instant. Read errors fail open (the token already
// passed signature/expiry/version checks); an outage must not lock everyone out.
func (s *AuthService) IsAccessTokenRevoked(ctx context.Context, userID int64, issuedAt time.Time) bool {
	if s == nil || userID <= 0 || issuedAt.IsZero() {
		return false
	}
	store, ok := s.refreshTokenCache.(AccessTokenRevocationStore)
	if !ok {
		return false
	}
	revokedBefore, err := store.AccessTokensRevokedBefore(ctx, userID)
	if err != nil || revokedBefore.IsZero() {
		return false
	}
	return !issuedAt.After(revokedBefore)
}

// hashToken 计算Token的SHA256哈希
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func resolvedTokenVersion(user *User) int64 {
	if user == nil {
		return 0
	}
	if user.TokenVersionResolved {
		return user.TokenVersion
	}

	material := strings.ToLower(strings.TrimSpace(user.Email)) + "\n" + user.PasswordHash
	sum := sha256.Sum256([]byte(material))
	fingerprint := int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
	return user.TokenVersion ^ fingerprint
}

// snapshotPlatformQuotaDefaults 把 plan.PlatformQuotas（platform × 3 window）以
// BulkInsertInitial 形式写入 user_platform_quotas 表。失败 fail-open（仅 warn log）。
func (s *AuthService) snapshotPlatformQuotaDefaults(ctx context.Context, userID int64, plan *signupGrantPlan) error {
	if s.userPlatformQuotaRepo == nil || plan == nil || len(plan.PlatformQuotas) == 0 {
		return nil
	}
	// 平台配额快照是 best-effort（fail-open）：必须脱离调用方事务执行。
	// 否则某平台违反 user_platform_quotas 的 CHECK 约束（如尚未进约束的新平台）会让
	// 整个调用方事务被 Postgres 标记 aborted，把"无关紧要的默认配额快照"放大成
	// "整笔注册失败"（OAuth pending 路径曾因此 500 → 清 cookie → 404）。
	ctx = dbent.WithoutTx(ctx)
	records := make([]UserPlatformQuotaRecord, 0, len(plan.PlatformQuotas))
	for platform, q := range plan.PlatformQuotas {
		rec := UserPlatformQuotaRecord{
			UserID:   userID,
			Platform: platform,
		}
		if q != nil {
			rec.DailyLimitUSD = q.DailyLimitUSD
			rec.WeeklyLimitUSD = q.WeeklyLimitUSD
			rec.MonthlyLimitUSD = q.MonthlyLimitUSD
		}
		records = append(records, rec)
	}
	if err := s.userPlatformQuotaRepo.BulkInsertInitial(ctx, records); err != nil {
		logger.LegacyPrintf("service.auth", "[Auth] Warning: snapshot platform quota failed user=%d: %v (fail-open)", userID, err)
		return nil // fail-open：返回 nil，让调用方继续
	}
	return nil
}
