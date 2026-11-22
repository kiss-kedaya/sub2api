package handler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"sync"

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
	// twoFactorLimiter 按账号维度限制 2FA 校验尝试（60s 滚动窗口 5 次）。
	twoFactorLimiter twoFactorRateLimiter

	dingTalkClientInstance *DingTalkClient
	dingTalkClientMu       sync.Mutex

	// currentUserCache reuses one /auth/me payload for a few seconds; the route
	// is the busiest API call in the product.
	currentUserCache authCurrentUserCache
}

// NewAuthHandler creates a new AuthHandler
func NewAuthHandler(cfg *config.Config, authService *service.AuthService, userService *service.UserService, settingService *service.SettingService, promoService *service.PromoService, redeemService *service.RedeemService, totpService *service.TotpService, userAttributeService *service.UserAttributeService) *AuthHandler {
	return &AuthHandler{
		cfg:                  cfg,
		authService:          authService,
		userService:          userService,
		settingSvc:           settingService,
		promoService:         promoService,
		redeemService:        redeemService,
		totpService:          totpService,
		userAttributeService: userAttributeService,
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

// respondWithTokenPair 生成 Token 对并返回认证响应
// 如果 Token 对生成失败，回退到只返回 Access Token（向后兼容）
// SetTwoFactorRateLimiter 注入账号维度 2FA 限速器（由 router 装配）。
func (h *AuthHandler) SetTwoFactorRateLimiter(limiter twoFactorRateLimiter) {
	h.twoFactorLimiter = limiter
}

func (h *AuthHandler) respondWithTokenPair(c *gin.Context, user *service.User) {
	respondWithTokenPair(c, h.authService, user)
}

func respondWithTokenPair(c *gin.Context, authService *service.AuthService, user *service.User) {
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tokenPair, err := authService.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		slog.Error("failed to generate token pair", "error", err, "user_id", user.ID)
		// 回退到只返回Access Token
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
		// 统一登录响应（安全 + 体验）：
		// 「账号不存在」「密码错误」与下面「密码正确但需要 2FA」返回完全相同的挑战结构，
		// 响应体里没有任何可以区分两种情况的字段，避免登录接口被当作账号枚举 /
		// 密码有效性探测的 oracle。凭据错误延后到 /auth/login/2fa 统一返回
		// INVALID_CREDENTIALS（见 Login2FA）。
		// TOTP 服务不可用时无法下发挑战，退回旧行为（仅测试/异常部署会出现）。
		if errors.Is(err, service.ErrInvalidCredentials) && h.totpService != nil {
			// 响应是 200 挑战，但审计必须仍按「凭据错误」记失败，否则撞库/爆破
			// 在审计里会显示成成功登录。
			markAuthAuditFailureFromError(c, err)
			h.createLoginChallenge(c, 0, req.Email)
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

	// Check if TOTP 2FA is enabled for this user.
	// 统一走 SettingService.LoginRequiresTOTP：功能开关读取失败时 fail-closed
	// （要求动态码），与第三方 OAuth / passkey / pending OAuth 兑换同一口径。
	// 这里不能用 IsTotpEnabled —— 它在设置读取失败时返回 false，会让密码登录
	// 在设置存储抖动时静默跳过已启用账号的二次验证。
	if h.totpService != nil && h.settingSvc.LoginRequiresTOTP(c.Request.Context(), user) {
		// 五分钟 2FA 凭证：同一账号在同一客户端（IP+UA）上刚刚通过了动态码，
		// 窗口内只输密码即可直接签发会话——老板要的「5 分钟内登录不再需要二次
		// 验证」。凭证过期/绑定不匹配/Redis 不可用时一律退回下面的动态码挑战，
		// 所以这里不存在 fail-open 分支。
		if grant := consumeTwoFactorGrant(c, h.totpService, user.ID); grant != nil {
			// 审计动作单独命名，便于统计「有多少登录是靠 5 分钟凭证跳过的」。
			middleware2.SetAuditAction(c, service.AuditActionLogin2FAGrantUsed)
			markAuthAuditSuccess(c, user.ID, user.Email)
			h.recordSuccessfulLogin(c, user.ID)
			h.respondWithTokenPair(c, user)
			return
		}
		// 浏览器确实带了凭证但被拒：动作改为 grant_rejected，并保留挑战下发的
		// requires_2fa 标记。没带凭证属于正常首次挑战，不记成拒绝。
		if readTwoFactorGrantCookie(c) != "" {
			middleware2.SetAuditAction(c, service.AuditActionLogin2FAGrantRejected)
		}

		// 审计：2FA 挑战下发（此时尚未签发会话，2FA 绕过尝试靠这条反查）。
		markAuthAuditChallenge(c, user.ID, user.Email)
		// 与「凭据错误」下发同一结构（见上面的错误分支），客户端无法据此判断
		// 账号是否存在、密码是否正确。邮箱掩码用请求里的邮箱计算，两条路径一致。
		h.createLoginChallenge(c, user.ID, req.Email)
		return
	}

	h.recordSuccessfulLogin(c, user.ID)

	h.respondWithTokenPair(c, user)
}

// TotpLoginResponse represents the response when 2FA is required
type TotpLoginResponse struct {
	Requires2FA     bool   `json:"requires_2fa"`
	TempToken       string `json:"temp_token,omitempty"`
	UserEmailMasked string `json:"user_email_masked,omitempty"`
}

// createLoginChallenge 下发统一的「第二步校验」挑战，并保证响应体与
// 「凭据错误」路径逐字节一致（除随机 temp_token 取值外）。
//
// userID > 0  ：第一步密码已验证通过，等待动态码；
// userID <= 0 ：第一步没有拿到有效凭据（账号不存在 / 密码错误），第二步只会
// 统一返回 INVALID_CREDENTIALS，不暴露账号是否存在。
//
// 邮箱掩码一律按请求里的邮箱计算，不读库里的真实邮箱，避免大小写/别名差异
// 让两条路径的响应出现字节差异。
func (h *AuthHandler) createLoginChallenge(c *gin.Context, userID int64, email string) {
	if h.totpService == nil {
		response.InternalError(c, "Failed to create 2FA session")
		return
	}
	tempToken, err := h.totpService.CreateLoginSession(c.Request.Context(), userID, email)
	if err != nil {
		slog.Warn("create login challenge session failed", "error", err)
		response.InternalError(c, "Failed to create 2FA session")
		return
	}
	response.Success(c, TotpLoginResponse{
		Requires2FA:     true,
		TempToken:       tempToken,
		UserEmailMasked: service.MaskEmail(email),
	})
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

	// 第一步下发的「凭据无效」统一挑战：第二步必然失败，统一返回
	// INVALID_CREDENTIALS（与真实 2FA 账号「密码已验证、动态码错误」区分开，
	// 也不暴露账号是否存在）。账号维度限速用邮箱散列做键，避免被拿来当
	// 密码有效性探测的快速通道。
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
	if req.Password != "" {
		verifyEmail := session.Email
		if strings.TrimSpace(req.Email) != "" {
			verifyEmail = strings.TrimSpace(req.Email)
		}
		if _, _, err := h.authService.Login(c.Request.Context(), verifyEmail, req.Password); err != nil {
			if errors.Is(err, service.ErrInvalidCredentials) {
				response.ErrorFrom(c, service.ErrInvalidCredentials)
				return
			}
			response.ErrorFrom(c, err)
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

		secureCookie := isRequestHTTPS(c)
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
		response.ErrorFrom(c, err)
		return
	}

	// 成功验证后清空账号维度计数（成功即清）。
	resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, session.UserID)

	// Delete the login session (only after all checks pass)
	_ = h.totpService.DeleteLoginSession(c.Request.Context(), req.TempToken)

	if session.PendingOAuthBind == nil {
		h.recordSuccessfulLogin(c, user.ID)
	}

	// 任一次动态码校验成功都要下发 5 分钟不透明凭证：之后同客户端 5 分钟内的
	// 密码登录 / 密码校验不再要求动态码。Cookie 由服务端 HttpOnly 下发，
	// 响应 body 不回显 token。
	issueTwoFactorGrant(c, h.totpService, user.ID, service.TwoFactorGrantScopeLogin)

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

	result, err := h.authService.RefreshTokenPair(c.Request.Context(), req.RefreshToken)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Backend mode: block non-admin token refresh
	if h.settingSvc.IsBackendModeEnabled(c.Request.Context()) && result.UserRole != "admin" {
		response.Forbidden(c, "Backend mode is active. Only admin login is allowed.")
		return
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
	clearOAuthLogoutCookies(c)
	// 登出后这个浏览器不应该再持有「5 分钟内免 2FA」的凭证。
	clearTwoFactorGrantCookie(c)

	response.Success(c, LogoutResponse{
		Message: "Logged out successfully",
	})
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
	clearTwoFactorGrantCookie(c)

	response.Success(c, RevokeAllSessionsResponse{
		Message: "All sessions have been revoked. Please log in again.",
	})
}
