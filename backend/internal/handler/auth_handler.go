package handler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AuthHandler handles authentication-related requests
type AuthHandler struct {
	cfg                  *config.Config
	authService          *service.AuthService
	userService          *service.UserService
	settingSvc           *service.SettingService
	promoService         *service.PromoService
	redeemService        *service.RedeemService
	totpService          *service.TotpService
	userAttributeService *service.UserAttributeService
	// passkeys 供密码登录第二步的 Passkey 因子挑战/校验使用。
	passkeys *service.PasskeyService
	// twoFactorLimiter 按账号维度限制 2FA 校验尝试（60s 滚动窗口 5 次）。
	twoFactorLimiter twoFactorRateLimiter
	// disguise 为登录首步「凭据无效」挑战提供按邮箱稳定的伪装素材（审计 A-P1-02）。
	// 没有可用密钥时为 nil；是否启用由 SettingKeyLoginShapeDisguiseEnabled 决定（默认关闭）。
	disguise *service.LoginDisguise

	dingTalkClientInstance *DingTalkClient
	dingTalkClientMu       sync.Mutex

	// currentUserCache reuses one /auth/me payload for a few seconds; the route
	// is the busiest API call in the product.
	currentUserCache authCurrentUserCache
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(cfg *config.Config, authService *service.AuthService, userService *service.UserService, settingService *service.SettingService, promoService *service.PromoService, redeemService *service.RedeemService, totpService *service.TotpService, userAttributeService *service.UserAttributeService) *AuthHandler {
	var disguise *service.LoginDisguise
	if cfg != nil {
		disguise = service.NewLoginDisguise(cfg.JWT.Secret)
	}
	return &AuthHandler{
		cfg:                  cfg,
		authService:          authService,
		userService:          userService,
		settingSvc:           settingService,
		promoService:         promoService,
		redeemService:        redeemService,
		totpService:          totpService,
		userAttributeService: userAttributeService,
		disguise:             disguise,
	}
}

// RegisterRequest represents the registration request payload
type RegisterRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	Password              string `json:"password" binding:"required,min=6"`
	VerifyCode            string `json:"verify_code"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
	PromoCode             string `json:"promo_code"`      // 注册优惠码
	InvitationCode        string `json:"invitation_code"` // 邀请码
	AffCode               string `json:"aff_code"`        // 邀请返利码
}

// SendVerifyCodeRequest 发送验证码请求
type SendVerifyCodeRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

// SendVerifyCodeResponse 发送验证码响应
type SendVerifyCodeResponse struct {
	Message   string `json:"message"`
	Countdown int    `json:"countdown"` // 倒计时秒数
}

// LoginRequest represents the login request payload
type LoginRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	Password              string `json:"password" binding:"required"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

func captchaProof(turnstileToken, tencentTicket, tencentRandstr string) service.CaptchaProof {
	return service.CaptchaProof{
		TurnstileToken: turnstileToken,
		TencentTicket:  tencentTicket,
		TencentRandstr: tencentRandstr,
	}
}

// AuthResponse 认证响应格式（匹配前端期望）
type AuthResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"` // 新增：Refresh Token
	ExpiresIn    int       `json:"expires_in,omitempty"`    // 新增：Access Token有效期（秒）
	TokenType    string    `json:"token_type"`
	User         *dto.User `json:"user"`
}

func ensureLoginUserActive(user *service.User) error {
	if user == nil {
		return infraerrors.Unauthorized("INVALID_USER", "user not found")
	}
	if !user.IsActive() {
		return service.ErrUserNotActive
	}
	return nil
}

// SetTwoFactorRateLimiter 注入账号维度 2FA 限速器（由 router 装配）。
func (h *AuthHandler) SetTwoFactorRateLimiter(limiter twoFactorRateLimiter) {
	if h == nil {
		return
	}
	h.twoFactorLimiter = limiter
}

// SetPasskeyService 注入 Passkey 因子服务（由 wire 在装配后调用）。
// 保持 NewAuthHandler 签名不变，避免大量既有测试构造点改动。
func (h *AuthHandler) SetPasskeyService(passkeys *service.PasskeyService) {
	if h == nil {
		return
	}
	h.passkeys = passkeys
}

// respondWithTokenPair 生成 Token 对并返回认证响应。
// Token 对生成失败时先短退避重试一次；仍失败才回退到只返回 Access Token（向后兼容，会计数并留限频 ERROR）。
func (h *AuthHandler) respondWithTokenPair(c *gin.Context, user *service.User) {
	respondWithTokenPair(c, h.authService, user)
}

// loginTokenPairRetryBackoff 是登录发令牌失败后、重试一次之前的短退避（测试里可调小）。
var loginTokenPairRetryBackoff = 150 * time.Millisecond

// generateLoginTokenPair 为登录签发 Token 对；失败时短退避后重试一次。
// Redis 抖动/超时多是瞬时的，一次重试就能避免用户拿到「access token 过期后必须重新登录」的残缺会话。
// refresh token 存储没配置（配置问题，重试没用）与请求已取消（客户端已经走了）不重试。
func generateLoginTokenPair(ctx context.Context, authService *service.AuthService, user *service.User) (*service.TokenPair, error) {
	pair, err := authService.GenerateTokenPair(ctx, user, "")
	if err == nil || errors.Is(err, service.ErrRefreshTokenCacheNotConfigured) || ctx.Err() != nil {
		return pair, err
	}
	timer := time.NewTimer(loginTokenPairRetryBackoff)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil, err
	}
	pair, retryErr := authService.GenerateTokenPair(ctx, user, "")
	authService.NoteLoginTokenPairRetry(retryErr == nil)
	if retryErr != nil {
		return nil, retryErr
	}
	return pair, nil
}

func respondWithTokenPair(c *gin.Context, authService *service.AuthService, user *service.User) {
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tokenPair, err := generateLoginTokenPair(c.Request.Context(), authService, user)
	if err != nil {
		// 重试之后仍然失败：不能让登录因此整体失败，退回只返回 Access Token（向后兼容）。
		// 这样的会话在 access token 过期后必须重新登录，所以要计数并留一条限频 ERROR。
		authService.NoteLoginTokenPairFallback(user.ID, err)
		token, tokenErr := authService.GenerateToken(c.Request.Context(), user)
		if tokenErr != nil {
			response.InternalError(c, "Failed to generate token")
			return
		}
		response.Success(c, AuthResponse{
			AccessToken: token,
			TokenType:   "Bearer",
			User:        dto.UserFromService(user),
		})
		return
	}
	response.Success(c, AuthResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
		User:         dto.UserFromService(user),
	})
}

func (h *AuthHandler) ensureBackendModeAllowsUser(ctx context.Context, user *service.User) error {
	if user == nil {
		return infraerrors.Unauthorized("INVALID_USER", "user not found")
	}
	if h == nil || !h.isBackendModeEnabled(ctx) || user.IsAdmin() {
		return nil
	}
	return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Only admin login is allowed.")
}

func (h *AuthHandler) ensureBackendModeAllowsNewUserLogin(ctx context.Context) error {
	if h == nil || !h.isBackendModeEnabled(ctx) {
		return nil
	}
	return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Only admin login is allowed.")
}

func (h *AuthHandler) isBackendModeEnabled(ctx context.Context) bool {
	if h == nil || h.settingSvc == nil {
		return false
	}
	settings, err := h.settingSvc.GetPublicSettings(ctx)
	if err == nil && settings != nil {
		return settings.BackendModeEnabled
	}
	return h.settingSvc.IsBackendModeEnabled(ctx)
}

// Register handles user registration
// POST /api/v1/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 验证当前启用的验证码（邮箱验证码注册场景避免重复校验一次性票据）
	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptchaForRegister(c.Request.Context(), proof, ip.GetClientIP(c), req.VerifyCode); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	_, user, err := h.authService.RegisterWithVerification(
		c.Request.Context(),
		req.Email,
		req.Password,
		req.VerifyCode,
		req.PromoCode,
		req.InvitationCode,
		req.AffCode,
	)
	if err != nil {
		markAuthAuditEmail(c, req.Email)
		response.ErrorFrom(c, err)
		return
	}

	markAuthAuditSuccess(c, user.ID, user.Email)
	h.respondWithTokenPair(c, user)
}

const verifyCodeSendCountdown = 60

// isRegisterEmailTaken reports whether a send-verify-code failure is the
// "email already registered" case. The handler must answer such requests with
// the same success payload as a real send, otherwise the endpoint becomes a
// reliable account-enumeration oracle (audit finding #14).
func isRegisterEmailTaken(err error) bool {
	return errors.Is(err, service.ErrEmailExists)
}

// SendVerifyCode 发送邮箱验证码
// POST /api/v1/auth/send-verify-code
func (h *AuthHandler) SendVerifyCode(c *gin.Context) {
	var req SendVerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	result, err := h.authService.SendVerifyCodeAsync(c.Request.Context(), req.Email, c.GetHeader("Accept-Language"))
	if err != nil {
		// 注册发码不能暴露邮箱是否已注册：已注册邮箱与正常发码返回完全相同的 200，
		// 只是不会真的发信。审计 finding #14。
		if isRegisterEmailTaken(err) {
			response.Success(c, SendVerifyCodeResponse{
				Message:   "Verification code sent successfully",
				Countdown: verifyCodeSendCountdown,
			})
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, SendVerifyCodeResponse{
		Message:   "Verification code sent successfully",
		Countdown: result.Countdown,
	})
}

// Login handles user login
// POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	// 登录响应包含令牌/挑战，任何中间层都不得缓存。
	c.Header("Cache-Control", "no-store")
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	token, user, err := h.authService.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		// 审计：失败也登记被尝试账号的邮箱指纹（不含明文），便于反查撞库。
		markAuthAuditEmail(c, req.Email)
		markAuthAuditFailureFromError(c, err)
		// 默认行为（login_shape_disguise_enabled 关闭）：
		// 「账号不存在」与「密码错误（无论账号是否绑定 2FA）」统一返回 401 INVALID_CREDENTIALS，
		// 不弹 2FA 验证框；authService.Login 已对不存在的邮箱执行等成本 bcrypt 比对，避免被探测账号是否存在。
		// 仅当显式开启 login_shape_disguise_enabled 且全站启用 TOTP 登录时，才下发伪装挑战，见 auth_login_disguise.go。
		if errors.Is(err, service.ErrInvalidCredentials) &&
			h.loginShapeDisguiseActive(c.Request.Context()) &&
			h.settingSvc.LoginTOTPEnabled(c.Request.Context()) {
			// 响应是 200 挑战，但审计必须仍按「凭据错误」记失败，否则撞库/爆破
			// 在审计里会显示成成功登录。
			h.respondDisguisedLoginChallenge(c, req.Email, req.Password)
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	_ = token // token 由 authService.Login 返回但此处由 respondWithTokenPair 重新生成

	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 双因子因子策略（唯一入口：TotpService.LoginRequiredFactors）：
	//   - 只绑 TOTP    → 密码 + TOTP（现状不变，全局 TOTP 开关语义沿用）；
	//   - 只绑 Passkey → 密码 + Passkey；
	//   - 两者都绑     → 密码 + TOTP + Passkey（顺序固定先 TOTP 后 Passkey）。
	// 策略读取失败一律 fail-closed，不允许静默降级成单因子/无因子。
	if h.totpService != nil {
		factors, err := h.totpService.LoginRequiredFactors(c.Request.Context(), user)
		if err != nil {
			slog.Error("load login two-factor policy failed", "user_id", user.ID, "error", err)
			response.ErrorFrom(c, service.ErrTwoFactorPolicyUnavailable)
			return
		}
		if len(factors) > 0 {
			// 5 分钟因子凭证：同一账号在同一客户端（IP+UA）上刚刚通过过某个
			// 因子，窗口内该因子免重复验证。凭证过期/绑定不匹配/Redis 不可用时
			// 一律退回该因子的挑战，不存在 fail-open 分支。
			satisfied := consumeAllTwoFactorGrants(c, h.totpService, user.ID, factors)
			missing := service.MissingFactors(factors, satisfied)
			if len(missing) == 0 {
				// 审计动作单独命名，便于统计「有多少登录是靠 5 分钟凭证跳过的」。
				middleware2.SetAuditAction(c, service.AuditActionLogin2FAGrantUsed)
				markAuthAuditSuccess(c, user.ID, user.Email)
				h.recordSuccessfulLogin(c, user.ID)
				h.respondWithTokenPair(c, user)
				return
			}
			// 浏览器确实带了某因子凭证但被拒：动作改为 grant_rejected，
			// 并保留挑战下发的 requires_2fa 标记。没带凭证属于正常首次挑战。
			for _, factor := range missing {
				if readTwoFactorGrantCookie(c, factor) != "" {
					middleware2.SetAuditAction(c, service.AuditActionLogin2FAGrantRejected)
					break
				}
			}

			// 审计：2FA 挑战下发（此时尚未签发会话，2FA 绕过尝试靠这条反查）。
			markAuthAuditChallenge(c, user.ID, user.Email)
			tempToken, err := h.totpService.CreateLoginSessionForFactors(
				c.Request.Context(), user.ID, req.Email, factors, satisfied)
			if err != nil {
				slog.Warn("create login challenge session failed", "error", err)
				response.InternalError(c, "Failed to create 2FA session")
				return
			}
			// 邮箱掩码用请求里的邮箱计算，避免大小写/别名差异。
			h.respondLoginFactorChallenge(c, tempToken, user, req.Email, factors, satisfied, missing)
			return
		}
	}

	h.recordSuccessfulLogin(c, user.ID)

	h.respondWithTokenPair(c, user)
}

// TotpLoginResponse represents the response when 2FA is required
type TotpLoginResponse struct {
	Requires2FA     bool   `json:"requires_2fa"`
	TempToken       string `json:"temp_token,omitempty"`
	UserEmailMasked string `json:"user_email_masked,omitempty"`
	// RequiredFactors 本次登录必须全部完成的因子（顺序：totp,passkey）。
	RequiredFactors []string `json:"required_factors,omitempty"`
	// PassedFactors 已经完成的因子（第一步用 5 分钟凭证跳过，或前一步已通过）。
	PassedFactors []string `json:"passed_factors,omitempty"`
	// NextFactor 下一步要验证的因子：totp 或 passkey，前端据此弹对应对话框。
	NextFactor string `json:"next_factor,omitempty"`
	// PasskeySessionToken / PasskeyOptions 仅当 NextFactor=passkey 时下发：
	// 前端用 navigator.credentials.get 完成 assertion 后调用
	// POST /auth/login/2fa/passkey/finish。
	PasskeySessionToken string `json:"passkey_session_token,omitempty"`
	PasskeyOptions      any    `json:"passkey_options,omitempty"`
}

// respondLoginFactorChallenge 下发「按因子挑战」的第二步响应：
// 缺失因子里的第一个就是下一步。若下一步是 Passkey，同时生成 assertion
// 挑战（challenge 存 Redis，5 分钟一次性），前端拿 options 调
// navigator.credentials.get。
//
// 失败一律 fail-closed：Passkey 服务不可用 / 生成挑战失败时不签发任何令牌，
// 也不会退回「只验 TOTP」。
func (h *AuthHandler) respondLoginFactorChallenge(
	c *gin.Context,
	tempToken string,
	user *service.User,
	email string,
	required, passed, missing []service.Factor,
) {
	if len(missing) == 0 {
		response.InternalError(c, "Missing two-factor challenge")
		return
	}
	next := missing[0]
	resp := TotpLoginResponse{
		Requires2FA:     true,
		TempToken:       tempToken,
		UserEmailMasked: service.MaskEmail(email),
		RequiredFactors: service.FactorStrings(required),
		PassedFactors:   service.FactorStrings(passed),
		NextFactor:      next.String(),
	}
	middleware2.SetAuditExtra(c, map[string]any{
		"two_factor_required_factors": strings.Join(service.FactorStrings(required), ","),
		"two_factor_next_factor":      next.String(),
	})
	if next == service.FactorPasskey {
		if user == nil || h.passkeys == nil {
			response.ErrorFrom(c, service.ErrTwoFactorPasskeyUnavailable)
			return
		}
		assertion, sessionToken, err := h.passkeys.BeginLoginForUser(
			c.Request.Context(), user.ID, service.PasskeySessionKindLogin2FA)
		if err != nil {
			slog.Warn("begin login passkey assertion failed", "user_id", user.ID, "error", err)
			response.ErrorFrom(c, err)
			return
		}
		resp.PasskeySessionToken = sessionToken
		resp.PasskeyOptions = assertion
	}
	response.Success(c, resp)
}

// loginChallengeRateLimitKey 为「凭据无效」的统一挑战生成限速键。
//
// 这类挑战没有真实用户 ID，但第二步同样允许提交动态码，必须限速，否则
// 「先猜密码、再提交随机动态码看错误码」就变成密码有效性探测。键取邮箱的
// 稳定散列并强制放在高位区间（>= 2^40），避免和真实自增用户 ID 撞车——
// ReserveAccountTOTPAttempt 只接受正数用户 ID。
func loginChallengeRateLimitKey(email string) int64 {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	key := int64(binary.BigEndian.Uint64(sum[:8]) & 0x3fffffffffffffff)
	const minDecoyKey = int64(1) << 40
	if key < minDecoyKey {
		key |= minDecoyKey
	}
	return key
}

// Login2FARequest represents the 2FA login request
type Login2FARequest struct {
	TempToken string `json:"temp_token" binding:"required"`
	TotpCode  string `json:"totp_code" binding:"required,len=6"`
	// 统一登录协议：第二步同时回传邮箱+密码，服务端按需重新校验。
	// 老客户端（OAuth 回调流程）只传 temp_token+totp_code，仍然兼容。
	Email    string `json:"email"`
	Password string `json:"password"`
}

// respondLogin2FACredentialOrCodeFailure 统一第二步「凭据错误 / 动态码错误」的
// 对外响应：一律 INVALID_CREDENTIALS，真实原因只写服务端日志与审计。
//
// 真实 2FA 账号「密码正确、动态码错误」若返回 TOTP_INVALID_CODE，而「密码错误」
// 返回 INVALID_CREDENTIALS，第二步就重新变成密码有效性 oracle；两类失败必须
// 不可区分。
func respondLogin2FACredentialOrCodeFailure(c *gin.Context, err error) {
	if err != nil {
		slog.Warn("login_2fa_credential_or_code_rejected",
			"reason", infraerrors.Reason(err),
			"error", err)
	}
	markAuthAuditFailureFromError(c, err)
	response.ErrorFrom(c, service.ErrInvalidCredentials)
}

// isLogin2FACredentialOrCodeFailure 判断错误是否属于「第二步凭据/动态码不正确」。
// 只覆盖码本身无效/已被重放的错误：限速、TOTP 服务不可用等基础设施错误仍按
// 原错误码返回，不能把 5xx/429 误报成「密码错误」。
func isLogin2FACredentialOrCodeFailure(err error) bool {
	return errors.Is(err, service.ErrTotpInvalidCode) ||
		errors.Is(err, service.ErrTotpCodeReplayed) ||
		errors.Is(err, service.ErrCryptoTotpReplayed)
}

// Login2FA completes the login with 2FA verification
// POST /api/v1/auth/login/2fa
func (h *AuthHandler) Login2FA(c *gin.Context) {
	// 二次验证响应会签发令牌 / 下发新的 2FA 凭证 Cookie，禁止任何缓存。
	c.Header("Cache-Control", "no-store")
	var req Login2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	slog.Debug("login_2fa_request",
		"temp_token_len", len(req.TempToken),
		"totp_code_len", len(req.TotpCode))

	// Get the login session
	session, err := h.totpService.GetLoginSession(c.Request.Context(), req.TempToken)
	if err != nil || session == nil {
		tokenPrefix := ""
		if len(req.TempToken) >= 8 {
			tokenPrefix = req.TempToken[:8]
		}
		slog.Debug("login_2fa_session_invalid",
			"temp_token_prefix", tokenPrefix,
			"error", err)
		response.BadRequest(c, "Invalid or expired 2FA session")
		return
	}

	slog.Debug("login_2fa_session_found",
		"user_id", session.UserID,
		"email", session.Email)
	// 审计：2FA 验证请求也登记被验证账号的邮箱指纹（不含明文）。
	markAuthAuditEmail(c, session.Email)

	// 开关打开期间下发的伪装挑战会话：与真实会话同序、同错误码、同限速、会话同样不因失败而销毁，
	// 永远不会放行，见 auth_login_disguise.go。开关关闭时不会产生这种会话，这里不可达。
	if session.Decoy {
		h.respondDecoyLogin2FA(c, session, &req)
		return
	}

	// 兼容旧版滚动升级期间尚未过期的无用户 ID 诱饵会话（UserID <= 0）：
	// 必然失败并统一返回 INVALID_CREDENTIALS，同时限速并清理会话。
	if session.UserID <= 0 {
		decoyKey := loginChallengeRateLimitKey(session.Email)
		if _, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, decoyKey); !ok {
			return
		}
		// 等成本校验：不存在账号时也跑一次 bcrypt，响应时间不随账号是否存在变化。
		h.authService.EqualizePasswordTiming(req.Password)
		_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)
		response.ErrorFrom(c, service.ErrInvalidCredentials)
		return
	}

	// 统一登录协议：新前端在第二步同时回传邮箱+密码，这里重新校验一次凭据，
	// 让「凭据错误」在第二步统一以 INVALID_CREDENTIALS 返回（老客户端不传密码，
	// 走第一步已经验证过的会话，保持兼容）。
	//
	// 密码复核固定使用 temp_token 会话里的邮箱，请求里的 email 只做一致性校验。
	// 否则持有自己账号会话的人可以替换 email，拿任意邮箱做密码校验，绕过按邮箱
	// 维度做的限速与审计。不一致时与「凭据错误」返回完全相同的错误。
	if req.Password != "" {
		if reqEmail := strings.TrimSpace(req.Email); reqEmail != "" &&
			!strings.EqualFold(reqEmail, strings.TrimSpace(session.Email)) {
			slog.Warn("login_2fa_email_mismatch", "user_id", session.UserID)
			markAuthAuditFailureFromError(c, service.ErrInvalidCredentials)
			response.ErrorFrom(c, service.ErrInvalidCredentials)
			return
		}
		if _, _, err := h.authService.Login(c.Request.Context(), strings.TrimSpace(session.Email), req.Password); err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				respondLogin2FACredentialOrCodeFailure(c, err)
				return
			}
			response.ErrorFrom(c, err)
			return
		}
	}

	// 开关打开（默认关闭）时，真实会话先过与伪装会话共用的「账号 + 客户端 IP」桶，再走下面的账号桶；
	// 开关关闭时 ipBucket 为 0，这里什么都不做。见 auth_login_disguise.go「限速桶的取舍」。
	ipBucket := h.realSessionIPBucket(c, session.UserID)
	if ipBucket != 0 {
		if _, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, ipBucket); !ok {
			return
		}
	}

	// 账号维度限速：同一账号 60 秒滚动窗口内最多 5 次 2FA 校验尝试。
	// 超限 429 + Retry-After；限流存储故障 fail-closed（只影响 2FA 校验）。
	attemptCtx, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, session.UserID)
	if !ok {
		return
	}

	// Verify without consuming the shared replay marker yet. The login flow
	// still has account-mode and OAuth binding checks that can fail after code
	// validation; consuming here would make a valid retry report "already used".
	counter, err := h.totpService.VerifyLoginCode(attemptCtx, session.UserID, req.TotpCode)
	if err != nil {
		slog.Debug("login_2fa_verify_failed",
			"user_id", session.UserID,
			"error", err)
		// 动态码错误与密码错误对外必须同码：真实原因只落日志/审计。
		if isLogin2FACredentialOrCodeFailure(err) {
			respondLogin2FACredentialOrCodeFailure(c, err)
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	// Get the user (before session deletion so we can check backend mode)
	user, err := h.userService.GetByID(c.Request.Context(), session.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if session.PendingOAuthBind != nil {
		pendingSvc, err := h.pendingIdentityService()
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}

		pendingSession, err := pendingSvc.GetBrowserSession(
			c.Request.Context(),
			session.PendingOAuthBind.PendingSessionToken,
			session.PendingOAuthBind.BrowserSessionKey,
		)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}

		decision, err := h.ensurePendingOAuthAdoptionDecision(c, pendingSession.ID, oauthAdoptionDecisionRequest{})
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if err := applyPendingOAuthBinding(
			c.Request.Context(),
			h.entClient(),
			h.authService,
			h.userService,
			pendingSession,
			decision,
			&user.ID,
			true,
			true,
		); err != nil {
			response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to bind pending oauth identity").WithCause(err))
			return
		}
		if _, err := pendingSvc.ConsumeBrowserSession(
			c.Request.Context(),
			pendingSession.SessionToken,
			pendingSession.BrowserSessionKey,
		); err != nil {
			response.ErrorFrom(c, err)
			return
		}

		secureCookie := h.isRequestHTTPS(c)
		clearOAuthPendingSessionCookie(c, secureCookie)
		clearOAuthPendingBrowserCookie(c, secureCookie)
		h.recordSuccessfulLogin(c, user.ID)

		user, err = h.userService.GetByID(c.Request.Context(), session.UserID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	// Consume only after every login/binding check has passed.
	if err := h.totpService.ConsumeLoginCode(c.Request.Context(), session.UserID, counter, req.TotpCode); err != nil {
		// 重放的动态码同样属于「码不正确」，对外与密码错误同码。
		if isLogin2FACredentialOrCodeFailure(err) {
			respondLogin2FACredentialOrCodeFailure(c, err)
			return
		}
		response.ErrorFrom(c, err)
		return
	}

	// 成功验证后清空账号维度计数（成功即清）。
	resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, session.UserID)
	if ipBucket != 0 {
		resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, ipBucket)
	}

	// 因子编排：TOTP 通过后，若账号还绑定了 Passkey，必须继续完成 Passkey
	// 断言才签发令牌。漏一个因子一律 fail-closed（不下发令牌、不降级）。
	if session.PendingOAuthBind == nil && len(session.RequiredFactors) > 0 {
		updated, err := h.totpService.MarkLoginFactorPassed(
			c.Request.Context(), req.TempToken, service.FactorTOTP)
		if err != nil {
			slog.Warn("mark login factor passed failed", "user_id", session.UserID, "error", err)
			response.ErrorFrom(c, service.ErrTwoFactorPolicyUnavailable)
			return
		}
		// TOTP 验证成功也要下发该因子的 5 分钟不透明凭证（scope=login）：
		// 完成 Passkey 期间/之后同客户端的登录与密码校验可免重复 TOTP。
		issueTwoFactorGrant(c, h.totpService, session.UserID, service.TwoFactorGrantScopeLogin, service.FactorTOTP)
		missing := service.LoginSessionMissingFactors(updated)
		if len(missing) > 0 {
			// 还缺 Passkey：保持登录会话（不删除），返回下一因子的挑战。
			if user, err = h.userService.GetByID(c.Request.Context(), session.UserID); err != nil {
				response.ErrorFrom(c, err)
				return
			}
			h.respondLoginFactorChallenge(
				c, req.TempToken, user, session.Email, updated.RequiredFactors, updated.PassedFactors, missing)
			return
		}
		_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)
		h.recordSuccessfulLogin(c, user.ID)
		h.respondWithTokenPair(c, user)
		return
	}

	// 历史流程（OAuth pending bind 兑换 / 没有因子状态的旧会话）：保持原行为，
	// 只校验 TOTP。新登录一律走上面的因子编排。
	_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)

	if session.PendingOAuthBind == nil {
		h.recordSuccessfulLogin(c, user.ID)
	}

	// 任一次动态码校验成功都要下发 TOTP 因子的 5 分钟不透明凭证：之后同客户端
	// 5 分钟内的密码登录 / 密码校验不再要求动态码。Cookie 由服务端 HttpOnly
	// 下发，响应 body 不回显 token。
	issueTwoFactorGrant(c, h.totpService, user.ID, service.TwoFactorGrantScopeLogin, service.FactorTOTP)

	h.respondWithTokenPair(c, user)
}

// Login2FAPasskeyFinishRequest 是密码登录第二步 Passkey 因子完成请求。
type Login2FAPasskeyFinishRequest struct {
	TempToken    string          `json:"temp_token" binding:"required"`
	SessionToken string          `json:"session_token" binding:"required"`
	Credential   json.RawMessage `json:"credential" binding:"required"`
}

// Login2FAPasskeyFinish 完成密码登录的 Passkey 因子（双因子策略第二步）。
//
// 前置条件：Login2FA 已通过 TOTP 并返回 next_factor=passkey + passkey_options，
// 且登录会话里的 TOTP 因子已完成。校验通过后检查 RequiredFactors 是否全部完成，
// 只有全部完成才签发登录令牌（fail-closed）。
//
// POST /api/v1/auth/login/2fa/passkey/finish
func (h *AuthHandler) Login2FAPasskeyFinish(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req Login2FAPasskeyFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Credential) == 0 {
		response.BadRequest(c, "Invalid passkey response")
		return
	}
	if h.totpService == nil || h.passkeys == nil {
		response.ErrorFrom(c, service.ErrTwoFactorPasskeyUnavailable)
		return
	}
	session, err := h.totpService.GetLoginSession(c.Request.Context(), req.TempToken)
	// 伪装挑战会话（开关打开期间下发）先于 UserID<=0 的拒绝分支处理：它要与真实会话表现一致。
	if err == nil && session != nil && session.Decoy {
		h.respondDecoyPasskeyFinish(c, session, &req)
		return
	}
	if err != nil || session == nil || session.UserID <= 0 {
		response.BadRequest(c, "Invalid or expired 2FA session")
		return
	}
	// 只有「密码登录 + 需要 Passkey 因子」的会话能走本接口：
	// OAuth pending bind 会话 / 没要求 Passkey 的会话一律拒绝（fail-closed）。
	if session.PendingOAuthBind != nil || !service.HasFactor(session.RequiredFactors, service.FactorPasskey) {
		response.BadRequest(c, "No passkey step is required for this login session")
		return
	}
	// 开关打开（默认关闭）时先过与伪装会话共用的「账号 + 客户端 IP」桶；关闭时 ipBucket 为 0，什么都不做。
	ipBucket := h.realSessionIPBucket(c, session.UserID)
	if ipBucket != 0 {
		if _, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, ipBucket); !ok {
			return
		}
	}
	attemptCtx, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, session.UserID)
	if !ok {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	user, err := h.passkeys.FinishLoginForUser(
		attemptCtx,
		session.UserID,
		service.PasskeySessionKindLogin2FA,
		req.SessionToken,
		credentialRequest,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err := h.totpService.MarkLoginFactorPassed(
		c.Request.Context(), req.TempToken, service.FactorPasskey)
	if err != nil {
		response.ErrorFrom(c, service.ErrTwoFactorPolicyUnavailable)
		return
	}
	// TOTP 因子必须在同一会话里已经完成；任何缺失都不签发令牌。
	if missing := service.LoginSessionMissingFactors(updated); len(missing) > 0 {
		_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)
		markAuthAuditFailureFromError(c, service.ErrInvalidCredentials)
		response.ErrorFrom(c, infraerrors.Forbidden(
			"TWO_FACTOR_FACTORS_INCOMPLETE", "all required two-factor steps must be completed"))
		return
	}
	resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, session.UserID)
	if ipBucket != 0 {
		resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, ipBucket)
	}
	_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)
	issueTwoFactorGrant(c, h.totpService, user.ID, service.TwoFactorGrantScopeLogin, service.FactorPasskey)
	middleware2.SetAuditAction(c, service.AuditActionLogin2FA)
	h.recordSuccessfulLogin(c, user.ID)
	h.respondWithTokenPair(c, user)
}

func (h *AuthHandler) recordSuccessfulLogin(c *gin.Context, userID int64) {
	if service.CryptoAdminTestMode() {
		return
	}
	// 审计：密码登录 / 2FA 验证通过时写入操作者主体（邮箱指纹由调用方按需补充）。
	markAuthAuditSuccess(c, userID, "")
	h.authService.RecordSuccessfulLogin(c.Request.Context(), userID)
}

// currentUserResponse is the /auth/me payload. It is a named type so the short
// reuse window can cache it without re-marshalling on every poll.
type currentUserResponse struct {
	userProfileResponse
	RunMode string `json:"run_mode"`
}

// GetCurrentUser handles getting current authenticated user
// GET /api/v1/auth/me
func (h *AuthHandler) GetCurrentUser(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	if cached, ok := h.currentUserCache.get(subject.UserID); ok {
		response.Success(c, cached)
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	identities, err := h.userService.GetProfileIdentitySummaries(c.Request.Context(), subject.UserID, user)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	runMode := config.RunModeStandard
	if h.cfg != nil {
		runMode = h.cfg.RunMode
	}

	payload := &currentUserResponse{
		userProfileResponse: userProfileResponseFromService(user, identities),
		RunMode:             runMode,
	}
	h.currentUserCache.put(subject.UserID, payload)
	response.Success(c, payload)
}

// ValidatePromoCodeRequest 验证优惠码请求
type ValidatePromoCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

// ValidatePromoCodeResponse 验证优惠码响应
type ValidatePromoCodeResponse struct {
	Valid       bool    `json:"valid"`
	BonusAmount float64 `json:"bonus_amount,omitempty"`
	ErrorCode   string  `json:"error_code,omitempty"`
	Message     string  `json:"message,omitempty"`
}

// ValidatePromoCode 验证优惠码（公开接口，注册前调用）
// POST /api/v1/auth/validate-promo-code
func (h *AuthHandler) ValidatePromoCode(c *gin.Context) {
	// 检查优惠码功能是否启用
	if h.settingSvc != nil && !h.settingSvc.IsPromoCodeEnabled(c.Request.Context()) {
		response.Success(c, ValidatePromoCodeResponse{
			Valid:     false,
			ErrorCode: "PROMO_CODE_DISABLED",
		})
		return
	}

	var req ValidatePromoCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	promoCode, err := h.promoService.ValidatePromoCode(c.Request.Context(), req.Code)
	if err != nil {
		// 根据错误类型返回对应的错误码
		errorCode := "PROMO_CODE_INVALID"
		switch err {
		case service.ErrPromoCodeNotFound:
			errorCode = "PROMO_CODE_NOT_FOUND"
		case service.ErrPromoCodeExpired:
			errorCode = "PROMO_CODE_EXPIRED"
		case service.ErrPromoCodeDisabled:
			errorCode = "PROMO_CODE_DISABLED"
		case service.ErrPromoCodeMaxUsed:
			errorCode = "PROMO_CODE_MAX_USED"
		case service.ErrPromoCodeAlreadyUsed:
			errorCode = "PROMO_CODE_ALREADY_USED"
		}

		response.Success(c, ValidatePromoCodeResponse{
			Valid:     false,
			ErrorCode: errorCode,
		})
		return
	}

	if promoCode == nil {
		response.Success(c, ValidatePromoCodeResponse{
			Valid:     false,
			ErrorCode: "PROMO_CODE_INVALID",
		})
		return
	}

	response.Success(c, ValidatePromoCodeResponse{
		Valid:       true,
		BonusAmount: promoCode.BonusAmount,
	})
}

// ValidateInvitationCodeRequest 验证邀请码请求
type ValidateInvitationCodeRequest struct {
	Code string `json:"code" binding:"required"`
}

// ValidateInvitationCodeResponse 验证邀请码响应
type ValidateInvitationCodeResponse struct {
	Valid     bool   `json:"valid"`
	ErrorCode string `json:"error_code,omitempty"`
}

// ValidateInvitationCode 验证邀请码（公开接口，注册前调用）
// POST /api/v1/auth/validate-invitation-code
func (h *AuthHandler) ValidateInvitationCode(c *gin.Context) {
	// 检查邀请码功能是否启用
	if h.settingSvc == nil || !h.settingSvc.IsInvitationCodeEnabled(c.Request.Context()) {
		response.Success(c, ValidateInvitationCodeResponse{
			Valid:     false,
			ErrorCode: "INVITATION_CODE_DISABLED",
		})
		return
	}

	var req ValidateInvitationCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 验证邀请码
	redeemCode, err := h.redeemService.GetByCode(c.Request.Context(), req.Code)
	if err != nil {
		response.Success(c, ValidateInvitationCodeResponse{
			Valid:     false,
			ErrorCode: "INVITATION_CODE_NOT_FOUND",
		})
		return
	}

	// 检查类型和状态
	if redeemCode.Type != service.RedeemTypeInvitation {
		response.Success(c, ValidateInvitationCodeResponse{
			Valid:     false,
			ErrorCode: "INVITATION_CODE_INVALID",
		})
		return
	}

	if redeemCode.Status != service.StatusUnused {
		response.Success(c, ValidateInvitationCodeResponse{
			Valid:     false,
			ErrorCode: "INVITATION_CODE_USED",
		})
		return
	}

	response.Success(c, ValidateInvitationCodeResponse{
		Valid: true,
	})
}

// ForgotPasswordRequest 忘记密码请求
type ForgotPasswordRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

// ForgotPasswordResponse 忘记密码响应
type ForgotPasswordResponse struct {
	Message string `json:"message"`
}

// ForgotPassword 请求密码重置
// POST /api/v1/auth/forgot-password
func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	proof := captchaProof(req.TurnstileToken, req.TencentCaptchaTicket, req.TencentCaptchaRandstr)
	if err := h.authService.VerifyCaptcha(c.Request.Context(), proof, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	frontendBaseURL := strings.TrimSpace(h.settingSvc.GetFrontendURL(c.Request.Context()))
	if frontendBaseURL == "" {
		slog.Error("frontend_url not configured in settings or config; cannot build password reset link")
		response.InternalError(c, "Password reset is not configured")
		return
	}

	// Request password reset (async)
	// Note: This returns success even if email doesn't exist (to prevent enumeration)
	if err := h.authService.RequestPasswordResetAsync(c.Request.Context(), req.Email, frontendBaseURL, c.GetHeader("Accept-Language")); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, ForgotPasswordResponse{
		Message: "If your email is registered, you will receive a password reset link shortly.",
	})
}

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

// ResetPasswordResponse 重置密码响应
type ResetPasswordResponse struct {
	Message string `json:"message"`
}

// ResetPassword 重置密码
// POST /api/v1/auth/reset-password
func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// Reset password
	if err := h.authService.ResetPassword(c.Request.Context(), req.Email, req.Token, req.NewPassword); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, ResetPasswordResponse{
		Message: "Your password has been reset successfully. You can now log in with your new password.",
	})
}

// ==================== Token Refresh Endpoints ====================

// RefreshTokenRequest 刷新Token请求
type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// RefreshTokenResponse 刷新Token响应
type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // Access Token有效期（秒）
	TokenType    string `json:"token_type"`
}

// RefreshToken 刷新Token
// POST /api/v1/auth/refresh
func (h *AuthHandler) RefreshToken(c *gin.Context) {
	var req RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 后端模式只允许管理员：在消费令牌之前拦（guard 在签发/消费之前调用），被拒用户的令牌原样保留，
	// 也不会先轮换再报错。响应带明确的业务码 BACKEND_MODE_ADMIN_ONLY，前端据此清登录态。
	result, err := h.authService.RefreshTokenPairGuarded(c.Request.Context(), req.RefreshToken, func(user *service.User) error {
		if h.settingSvc != nil && !user.IsAdmin() && h.settingSvc.IsBackendModeEnabled(c.Request.Context()) {
			return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Only admin login is allowed.")
		}
		return nil
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 刷新时发生了指纹重绑（只是 IP 变了所以放行）：成功的刷新默认不进审计，这一条值得留下——
	// 只带前后两端的粗粒度标签，不含 IP 与完整 UA。审计行限频（同一用户 + 会话家族每个窗口一条，
	// 由服务层判定 AuditRebind），计数照常累加。
	if result.AuditRebind {
		middleware2.SetAuditAction(c, service.AuditActionSessionBindingRebind)
		middleware2.SetAuditActor(c, result.UserID, "")
		middleware2.SetAuditExtra(c, map[string]any{"binding_from": result.BindingFrom, "binding_to": result.BindingTo})
		middleware2.MarkAuditAlways(c)
	}

	response.Success(c, RefreshTokenResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		TokenType:    "Bearer",
	})
}

// LogoutRequest 登出请求
type LogoutRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"` // 可选：撤销指定的Refresh Token
}

// LogoutResponse 登出响应
type LogoutResponse struct {
	Message string `json:"message"`
}

// Logout 用户登出
// POST /api/v1/auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	var req LogoutRequest
	// 允许空请求体（向后兼容）
	_ = c.ShouldBindJSON(&req)
	if fields := strings.Fields(c.GetHeader("Authorization")); len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		if err := h.authService.RevokeCryptoGrantSessionForAccessToken(c.Request.Context(), fields[1]); err != nil &&
			!errors.Is(err, service.ErrInvalidToken) && !errors.Is(err, service.ErrTokenExpired) && !errors.Is(err, service.ErrTokenTooLarge) {
			response.ErrorFrom(c, service.ErrServiceUnavailable)
			return
		}
	}

	// 如果提供了Refresh Token，撤销它
	if req.RefreshToken != "" {
		if err := h.authService.RevokeRefreshToken(c.Request.Context(), req.RefreshToken); err != nil {
			slog.Debug("failed to revoke refresh token", "error", err)
			if !errors.Is(err, service.ErrRefreshTokenInvalid) && !errors.Is(err, service.ErrRefreshTokenNotFound) {
				response.ErrorFrom(c, service.ErrServiceUnavailable)
				return
			}
		}
	}
	h.consumePendingOAuthSessionOnLogout(c)
	clearOAuthLogoutCookies(c, h.isRequestHTTPS(c))
	// 登出后这个浏览器不应该再持有「5 分钟内免 2FA」的凭证。
	clearAllTwoFactorGrantCookies(c)

	response.Success(c, LogoutResponse{
		Message: "Logged out successfully",
	})
}

// presentRequestCookieNames 返回当前请求实际携带的 Cookie 名称集合。
// 登出/撤销会话等清理路径据此按需下发 Set-Cookie 删除头，避免一次性输出大量无关 Cookie
// 导致上游响应头超过 Nginx 默认 4KB proxy_buffer_size 引发 502。
func presentRequestCookieNames(c *gin.Context) map[string]struct{} {
	if c == nil || c.Request == nil {
		return nil
	}
	cookies := c.Request.Cookies()
	if len(cookies) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(cookies))
	for _, ck := range cookies {
		if ck != nil && ck.Name != "" {
			present[ck.Name] = struct{}{}
		}
	}
	return present
}

// RevokeAllSessionsResponse 撤销所有会话响应
type RevokeAllSessionsResponse struct {
	Message string `json:"message"`
}

// RevokeAllSessions 撤销当前用户的所有会话
// POST /api/v1/auth/revoke-all-sessions
func (h *AuthHandler) RevokeAllSessions(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	if err := h.authService.RevokeAllUserTokens(c.Request.Context(), subject.UserID); err != nil {
		slog.Error("failed to revoke all sessions", "user_id", subject.UserID, "error", err)
		response.InternalError(c, "Failed to revoke sessions")
		return
	}

	// 强制下线/撤销全部会话必须同时清掉该用户全部 2FA 凭证：否则被撤销的
	// 会话在 5 分钟窗口内还能用旧凭证跳过动态码重新登录。撤销失败不改变
	// 已完成的会话撤销结果，但必须留日志，窗口最多只剩 5 分钟。
	if h.totpService != nil {
		if err := h.totpService.RevokeTwoFactorGrantsForUser(c.Request.Context(), subject.UserID); err != nil {
			slog.Warn("revoke two-factor grants after session revocation failed",
				"user_id", subject.UserID, "error", err)
		}
	}
	clearAllTwoFactorGrantCookies(c)

	response.Success(c, RevokeAllSessionsResponse{
		Message: "All sessions have been revoked. Please log in again.",
	})
}
