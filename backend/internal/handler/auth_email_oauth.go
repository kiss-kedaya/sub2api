package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauth"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
	"github.com/tidwall/gjson"
)

const (
	emailOAuthCookiePath       = "/api/v1/auth/oauth"
	emailOAuthStateCookieName  = "email_oauth_state"
	emailOAuthRedirectCookie   = "email_oauth_redirect"
	emailOAuthProviderCookie   = "email_oauth_provider"
	emailOAuthAffiliateCookie  = "email_oauth_affiliate"
	emailOAuthIntentCookieName = "email_oauth_intent"
	emailOAuthBindUserCookie   = "email_oauth_bind_user"
	emailOAuthCookieMaxAgeSec  = 10 * 60
	emailOAuthDefaultRedirect  = "/dashboard"
)

type emailOAuthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope,omitempty"`
}

type emailOAuthProfile struct {
	Subject       string
	Email         string
	EmailVerified bool
	Username      string
	DisplayName   string
	AvatarURL     string
	Metadata      map[string]any
}

func (h *AuthHandler) GitHubOAuthStart(c *gin.Context) { h.emailOAuthStart(c, "github") }
func (h *AuthHandler) GoogleOAuthStart(c *gin.Context) { h.emailOAuthStart(c, "google") }

func (h *AuthHandler) GitHubOAuthCallback(c *gin.Context) { h.emailOAuthCallback(c, "github") }
func (h *AuthHandler) GoogleOAuthCallback(c *gin.Context) { h.emailOAuthCallback(c, "google") }
func (h *AuthHandler) CompleteGitHubOAuthRegistration(c *gin.Context) {
	h.completeEmailOAuthRegistration(c, "github")
}
func (h *AuthHandler) CompleteGoogleOAuthRegistration(c *gin.Context) {
	h.completeEmailOAuthRegistration(c, "google")
}

func (h *AuthHandler) emailOAuthStart(c *gin.Context, provider string) {
	if !h.requireActionCaptchaForOAuthLoginStart(c) {
		return
	}
	cfg, err := h.getEmailOAuthConfig(c.Request.Context(), provider)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	state, err := oauth.GenerateState()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_STATE_GEN_FAILED", "failed to generate oauth state").WithCause(err))
		return
	}
	redirectTo := sanitizeFrontendRedirectPath(c.Query("redirect"))
	if redirectTo == "" {
		redirectTo = emailOAuthDefaultRedirect
	}

	secureCookie := h.isRequestHTTPS(c)
	emailOAuthSetCookie(c, emailOAuthStateCookieName, encodeCookieValue(state), secureCookie)
	emailOAuthSetCookie(c, emailOAuthRedirectCookie, encodeCookieValue(redirectTo), secureCookie)
	emailOAuthSetCookie(c, emailOAuthProviderCookie, encodeCookieValue(provider), secureCookie)
	intent := normalizeOAuthIntent(c.Query("intent"))
	emailOAuthSetCookie(c, emailOAuthIntentCookieName, encodeCookieValue(intent), secureCookie)
	if intent == oauthIntentBindCurrentUser {
		bindCookieValue, err := h.buildOAuthBindUserCookieFromContext(c)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		emailOAuthSetCookie(c, emailOAuthBindUserCookie, encodeCookieValue(bindCookieValue), secureCookie)
	} else {
		emailOAuthClearCookie(c, emailOAuthBindUserCookie, secureCookie)
	}
	captureOAuthPromoCode(c, secureCookie)
	if affCode := strings.TrimSpace(firstNonEmpty(c.Query("aff_code"), c.Query("aff"))); affCode != "" {
		emailOAuthSetCookie(c, emailOAuthAffiliateCookie, encodeCookieValue(affCode), secureCookie)
	} else {
		emailOAuthClearCookie(c, emailOAuthAffiliateCookie, secureCookie)
	}

	authURL, err := buildEmailOAuthAuthorizeURL(cfg, state)
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OAUTH_BUILD_URL_FAILED", "failed to build oauth authorization url").WithCause(err))
		return
	}
	respondOAuthStart(c, authURL)
}

func (h *AuthHandler) emailOAuthCallback(c *gin.Context, provider string) {
	cfg, cfgErr := h.getEmailOAuthConfig(c.Request.Context(), provider)
	if cfgErr != nil {
		response.ErrorFrom(c, cfgErr)
		return
	}
	frontendCallback := strings.TrimSpace(cfg.FrontendRedirectURL)
	if frontendCallback == "" {
		frontendCallback = "/auth/oauth/callback"
	}
	if providerErr := strings.TrimSpace(c.Query("error")); providerErr != "" {
		redirectOAuthError(c, frontendCallback, "provider_error", providerErr, c.Query("error_description"))
		return
	}
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		redirectOAuthError(c, frontendCallback, "missing_params", "missing code/state", "")
		return
	}

	secureCookie := h.isRequestHTTPS(c)
	defer func() {
		emailOAuthClearCookie(c, emailOAuthStateCookieName, secureCookie)
		emailOAuthClearCookie(c, emailOAuthRedirectCookie, secureCookie)
		emailOAuthClearCookie(c, emailOAuthProviderCookie, secureCookie)
		emailOAuthClearCookie(c, emailOAuthAffiliateCookie, secureCookie)
		emailOAuthClearCookie(c, emailOAuthIntentCookieName, secureCookie)
		emailOAuthClearCookie(c, emailOAuthBindUserCookie, secureCookie)
		clearOAuthPromoCodeCookie(c, secureCookie)
	}()
	expectedState, err := readCookieDecoded(c, emailOAuthStateCookieName)
	if err != nil || expectedState == "" || expectedState != state {
		redirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth state", "")
		return
	}
	expectedProvider, _ := readCookieDecoded(c, emailOAuthProviderCookie)
	if !strings.EqualFold(strings.TrimSpace(expectedProvider), provider) {
		redirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth provider", "")
		return
	}
	redirectTo, _ := readCookieDecoded(c, emailOAuthRedirectCookie)
	redirectTo = sanitizeFrontendRedirectPath(redirectTo)
	if redirectTo == "" {
		redirectTo = emailOAuthDefaultRedirect
	}
	intent, _ := readCookieDecoded(c, emailOAuthIntentCookieName)
	intent = normalizeOAuthIntent(intent)

	tokenResp, err := exchangeEmailOAuthCode(c.Request.Context(), cfg, code)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "token_exchange_failed", "failed to exchange oauth code", singleLine(err.Error()))
		return
	}
	profile, err := fetchEmailOAuthProfile(c.Request.Context(), provider, cfg, tokenResp)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "userinfo_failed", "failed to fetch verified email", singleLine(err.Error()))
		return
	}
	if intent == oauthIntentBindCurrentUser {
		h.emailOAuthCallbackBindWithProfile(c, provider, frontendCallback, redirectTo, profile)
		return
	}
	h.emailOAuthCallbackWithProfile(c, provider, cfg, frontendCallback, redirectTo, profile)
}

// emailOAuthCallbackBindWithProfile 处理已登录用户主动绑定 GitHub / Google 的回调：
// 不登录也不注册，只把本次 OAuth 身份写入 pending 会话，等前端兑换接口
// （/auth/oauth/pending/exchange）在登录态下把身份绑到当前用户。
func (h *AuthHandler) emailOAuthCallbackBindWithProfile(
	c *gin.Context,
	provider string,
	frontendCallback string,
	redirectTo string,
	profile *emailOAuthProfile,
) {
	bindMeta, err := h.readOAuthBindUserMetaFromCookie(c, emailOAuthBindUserCookie)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "invalid_state", "invalid oauth bind target", "")
		return
	}
	targetUserID := bindMeta.UserID
	browserSessionKey, err := generateOAuthPendingBrowserSession()
	if err != nil {
		redirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth bind", "")
		return
	}
	secureCookie := h.isRequestHTTPS(c)
	setOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)
	clearOAuthPendingSessionCookie(c, secureCookie)

	email := strings.TrimSpace(strings.ToLower(profile.Email))
	upstreamClaims := map[string]any{
		"email":            email,
		"email_verified":   profile.EmailVerified,
		"username":         strings.TrimSpace(profile.Username),
		"provider":         provider,
		"provider_key":     provider,
		"provider_subject": strings.TrimSpace(profile.Subject),
	}
	for key, value := range profile.Metadata {
		if _, exists := upstreamClaims[key]; !exists {
			upstreamClaims[key] = value
		}
	}
	markAuthAuditEmail(c, profile.Email)
	if err := h.createOAuthPendingSession(c, oauthPendingSessionPayload{
		Intent:                 oauthIntentBindCurrentUser,
		Identity:               service.PendingAuthIdentityKey{ProviderType: provider, ProviderKey: provider, ProviderSubject: strings.TrimSpace(profile.Subject)},
		TargetUserID:           &targetUserID,
		BindTokenIssuedAt:      bindMeta.TokenIssuedAt,
		BindTokenVersion:       bindMeta.TokenVersion,
		HasBindTokenVersion:    bindMeta.HasTokenVersion,
		ResolvedEmail:          email,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse: map[string]any{
			"provider": provider,
			"binding":  true,
			"redirect": redirectTo,
		},
	}); err != nil {
		markAuthAuditFailureFromError(c, err)
		redirectOAuthError(c, frontendCallback, "session_error", "failed to continue oauth bind", "")
		return
	}
	markAuthAuditPending(c, "binding_started")
	redirectToFrontendCallback(c, frontendCallback)
}

func (h *AuthHandler) emailOAuthCallbackWithProfile(
	c *gin.Context,
	provider string,
	cfg config.EmailOAuthProviderConfig,
	frontendCallback string,
	redirectTo string,
	profile *emailOAuthProfile,
) {
	input := service.EmailOAuthIdentityInput{
		ProviderType:     provider,
		ProviderKey:      provider,
		ProviderSubject:  profile.Subject,
		Email:            profile.Email,
		EmailVerified:    profile.EmailVerified,
		Username:         profile.Username,
		DisplayName:      profile.DisplayName,
		AvatarURL:        profile.AvatarURL,
		UpstreamMetadata: profile.Metadata,
	}
	affiliateCode := h.emailOAuthAffiliateCode(c)
	// 审计：只登记尝试账号的邮箱指纹，绝不落明文。
	markAuthAuditEmail(c, profile.Email)
	if shouldCreate, err := h.emailOAuthShouldCreatePendingRegistration(c.Request.Context(), input); err != nil {
		markAuthAuditFailureFromError(c, err)
		redirectOAuthError(c, frontendCallback, infraerrors.Reason(err), infraerrors.Message(err), "")
		return
	} else if shouldCreate {
		if pendingErr := h.createEmailOAuthRegistrationPendingSession(c, provider, frontendCallback, redirectTo, profile); pendingErr != nil {
			markAuthAuditFailureFromError(c, pendingErr)
			redirectOAuthError(c, frontendCallback, infraerrors.Reason(pendingErr), infraerrors.Message(pendingErr), "")
			return
		}
		markAuthAuditPending(c, "registration_required")
		redirectToFrontendCallback(c, frontendCallback)
		return
	}

	tokenPair, user, err := h.authService.LoginOrRegisterVerifiedEmailOAuthWithSignupCodes(
		c.Request.Context(),
		input,
		"",
		affiliateCode,
		readOAuthPromoCode(c),
	)
	if err != nil {
		if errors.Is(err, service.ErrLoginTOTPRequired) {
			if user == nil {
				redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "two-factor verification is unavailable", "")
				return
			}
			h.redirectEmailOAuthPending2FAChallenge(c, user, provider, input, frontendCallback, redirectTo)
			return
		}
		if errors.Is(err, service.ErrOAuthInvitationRequired) {
			if pendingErr := h.createEmailOAuthRegistrationPendingSession(c, provider, frontendCallback, redirectTo, profile); pendingErr != nil {
				redirectOAuthError(c, frontendCallback, infraerrors.Reason(pendingErr), infraerrors.Message(pendingErr), "")
				return
			}
			markAuthAuditPending(c, "invitation_required")
			redirectToFrontendCallback(c, frontendCallback)
			return
		}
		redirectOAuthError(c, frontendCallback, infraerrors.Reason(err), infraerrors.Message(err), "")
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		if tokenPair != nil {
			_ = h.authService.RevokeRefreshToken(c.Request.Context(), tokenPair.RefreshToken)
		}
		redirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}

	markAuthAuditSuccess(c, user.ID, user.Email)
	h.redirectEmailOAuthSession(c, provider, frontendCallback, redirectTo, tokenPair, user)
}

// emailOAuthCodeExchangeEnabled 报告是否启用「OAuth 回调只下发一次性 code」。
// 默认开启；security.oauth_code_exchange_enabled=false 或 Redis 未配置时
// 退回旧的 fragment 令牌方案（保留一个版本的兼容窗口，便于回滚）。
func (h *AuthHandler) emailOAuthCodeExchangeEnabled() bool {
	if h == nil || h.cfg == nil || h.authService == nil {
		return false
	}
	return h.cfg.Security.OAuthCodeExchangeEnabled && h.authService.OAuthCodeExchangeAvailable()
}

// redirectEmailOAuthSession 把已认证的会话交回前端。
// 默认路径：URL fragment 里只放一次性 code（60 秒、只能兑换一次），前端立刻
// POST /api/v1/auth/oauth/exchange 换取令牌并清理 URL，令牌不再进入浏览历史、
// Referer 或前端日志。兼容路径：创建 code 失败时退回旧的 fragment 令牌方案，
// 避免因为一次性码存储故障把所有人挡在登录门外。
func (h *AuthHandler) redirectEmailOAuthSession(
	c *gin.Context,
	provider string,
	frontendCallback string,
	redirectTo string,
	tokenPair *service.TokenPair,
	user *service.User,
) {
	if h.emailOAuthCodeExchangeEnabled() && user != nil {
		code, err := h.authService.CreateOAuthLoginCode(c.Request.Context(), user.ID, provider, redirectTo)
		if err == nil && code != "" {
			// 回调路径已生成过令牌对；改用 code 后它们不会被下发，立即撤销，
			// 避免在 Redis 里留下永远到不了浏览器的 refresh token。
			if tokenPair != nil {
				_ = h.authService.RevokeRefreshToken(c.Request.Context(), tokenPair.RefreshToken)
			}
			fragment := url.Values{}
			fragment.Set("code", code)
			fragment.Set("redirect", redirectTo)
			redirectWithFragment(c, frontendCallback, fragment)
			return
		}
		slog.Warn("oauth code exchange unavailable, falling back to fragment tokens", "error", err)
	}
	if tokenPair == nil {
		redirectOAuthError(c, frontendCallback, "session_error", "failed to issue session", "")
		return
	}
	fragment := url.Values{}
	fragment.Set("access_token", tokenPair.AccessToken)
	fragment.Set("refresh_token", tokenPair.RefreshToken)
	fragment.Set("expires_in", fmt.Sprintf("%d", tokenPair.ExpiresIn))
	fragment.Set("token_type", "Bearer")
	fragment.Set("redirect", redirectTo)
	redirectWithFragment(c, frontendCallback, fragment)
}

// OAuthCodeExchangeRequest 是一次性登录码兑换请求。
type OAuthCodeExchangeRequest struct {
	Code string `json:"code" binding:"required"`
}

// OAuthCodeExchangeResponse 是一次性登录码兑换结果，字段与登录响应保持一致。
type OAuthCodeExchangeResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Provider     string `json:"provider,omitempty"`
	Redirect     string `json:"redirect,omitempty"`
}

// OAuthCodeExchange 用 OAuth 回调下发的一次性 code 换取令牌对。
// POST /api/v1/auth/oauth/exchange
//
// 安全属性：
//   - code 60 秒过期，Redis GET+DEL 原子消费，重复兑换返回 OAUTH_CODE_INVALID；
//   - 令牌在此请求的会话指纹（IP/UA）下签发，回调页与兑换请求必须来自同一浏览器；
//   - 不在响应里返回用户资料，前端拿到令牌后再走 /auth/me（保持既有登录流程）。
func (h *AuthHandler) OAuthCodeExchange(c *gin.Context) {
	var req OAuthCodeExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	ctx := c.Request.Context()
	codeData, err := h.authService.ConsumeOAuthLoginCode(ctx, req.Code)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	user, err := h.authService.GetActiveUserForLogin(ctx, codeData.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.ensureBackendModeAllowsUser(ctx, user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	tokenPair, err := h.authService.GenerateTokenPair(ctx, user, "")
	if err != nil {
		slog.Error("failed to issue session for oauth code exchange", "error", err)
		response.ErrorFrom(c, service.ErrServiceUnavailable)
		return
	}
	response.Success(c, OAuthCodeExchangeResponse{
		AccessToken:  tokenPair.AccessToken,
		RefreshToken: tokenPair.RefreshToken,
		ExpiresIn:    tokenPair.ExpiresIn,
		TokenType:    "Bearer",
		Provider:     codeData.Provider,
		Redirect:     codeData.RedirectTo,
	})
}

// redirectEmailOAuthPending2FAChallenge 在已有账号触发 2FA（TOTP / Passkey / 双因子）时，
// 不向 auth_identities/users 提前落库，也不签发任何令牌，而是把待绑定身份写入 pending 会话并跳回前端回调页，
// 由前端通过 POST /auth/oauth/pending/exchange 拉取完整多因子挑战（含 Passkey options），
// 等全部必需因子验证通过后再原子完成绑定并签发会话。
func (h *AuthHandler) redirectEmailOAuthPending2FAChallenge(
	c *gin.Context,
	user *service.User,
	provider string,
	input service.EmailOAuthIdentityInput,
	frontendCallback string,
	redirectTo string,
) {
	if h == nil || user == nil || h.totpService == nil {
		redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "two-factor verification is unavailable", "")
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		redirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	factors, err := h.totpService.LoginRequiredFactors(c.Request.Context(), user)
	if err != nil || len(factors) == 0 {
		redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "two-factor verification is unavailable", "")
		return
	}
	if h.entClient() != nil && strings.TrimSpace(input.ProviderType) != "" && strings.TrimSpace(input.ProviderSubject) != "" {
		browserSessionKey, genErr := generateOAuthPendingBrowserSession()
		if genErr != nil {
			redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "failed to create two-factor session", "")
			return
		}
		secureCookie := h.isRequestHTTPS(c)
		setOAuthPendingBrowserCookie(c, browserSessionKey, secureCookie)

		providerType := strings.TrimSpace(input.ProviderType)
		providerKey := strings.TrimSpace(input.ProviderKey)
		if providerKey == "" {
			providerKey = providerType
		}
		upstreamClaims := map[string]any{
			"email":            strings.TrimSpace(strings.ToLower(input.Email)),
			"email_verified":   input.EmailVerified,
			"username":         strings.TrimSpace(input.Username),
			"provider":         provider,
			"provider_key":     providerKey,
			"provider_subject": strings.TrimSpace(input.ProviderSubject),
		}
		if strings.TrimSpace(input.DisplayName) != "" {
			upstreamClaims["suggested_display_name"] = strings.TrimSpace(input.DisplayName)
		}
		if strings.TrimSpace(input.AvatarURL) != "" {
			upstreamClaims["suggested_avatar_url"] = strings.TrimSpace(input.AvatarURL)
		}
		for key, value := range input.UpstreamMetadata {
			if _, exists := upstreamClaims[key]; !exists {
				upstreamClaims[key] = value
			}
		}
		targetUserID := user.ID
		if createErr := h.createOAuthPendingSession(c, oauthPendingSessionPayload{
			Intent:                 oauthIntentLogin,
			Identity:               service.PendingAuthIdentityKey{ProviderType: providerType, ProviderKey: providerKey, ProviderSubject: strings.TrimSpace(input.ProviderSubject)},
			TargetUserID:           &targetUserID,
			PendingBindAfter2FA:    true,
			ResolvedEmail:          user.Email,
			RedirectTo:             redirectTo,
			BrowserSessionKey:      browserSessionKey,
			UpstreamIdentityClaims: upstreamClaims,
			CompletionResponse: map[string]any{
				"provider": provider,
				"redirect": redirectTo,
			},
		}); createErr != nil {
			clearOAuthPendingSessionCookie(c, secureCookie)
			clearOAuthPendingBrowserCookie(c, secureCookie)
			redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "failed to create two-factor session", "")
			return
		}
		markAuthAuditChallenge(c, user.ID, user.Email)
		redirectToFrontendCallback(c, frontendCallback)
		return
	}
	h.redirectEmailOAuthTOTPChallenge(c, user, provider, frontendCallback, redirectTo)
}

// redirectEmailOAuthTOTPChallenge 为已启用 2FA 的已有账号签发一次性 2FA 登录会话，
// 前端拿到 requires_2fa + temp_token 后走 /auth/login/2fa 完成验证再拿 token。
// 这里不签发任何 access/refresh token，避免 OAuth 路径绕过登录二次验证。
func (h *AuthHandler) redirectEmailOAuthTOTPChallenge(
	c *gin.Context,
	user *service.User,
	provider string,
	frontendCallback string,
	redirectTo string,
) {
	if h.totpService == nil || user == nil {
		redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "two-factor verification is unavailable", "")
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		redirectOAuthError(c, frontendCallback, "login_blocked", infraerrors.Reason(err), infraerrors.Message(err))
		return
	}
	factors, err := h.totpService.LoginRequiredFactors(c.Request.Context(), user)
	if err != nil || len(factors) == 0 {
		redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "two-factor verification is unavailable", "")
		return
	}
	tempToken, err := h.totpService.CreateLoginSessionForFactors(c.Request.Context(), user.ID, user.Email, factors, nil)
	if err != nil {
		redirectOAuthError(c, frontendCallback, "two_factor_unavailable", "failed to create two-factor session", "")
		return
	}
	// 审计：OAuth 登录被 2FA 拦下（挑战下发，未签发会话）。
	markAuthAuditChallenge(c, user.ID, user.Email)
	fragment := url.Values{}
	fragment.Set("requires_2fa", "1")
	fragment.Set("temp_token", tempToken)
	fragment.Set("user_email_masked", service.MaskEmail(user.Email))
	fragment.Set("required_factors", strings.Join(service.FactorStrings(factors), ","))
	fragment.Set("next_factor", factors[0].String())
	fragment.Set("provider", provider)
	fragment.Set("redirect", redirectTo)
	redirectWithFragment(c, frontendCallback, fragment)
}

func (h *AuthHandler) emailOAuthShouldCreatePendingRegistration(ctx context.Context, input service.EmailOAuthIdentityInput) (bool, error) {
	client := h.entClient()
	if client == nil {
		return false, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	identityUser, err := h.findOAuthIdentityUser(ctx, service.PendingAuthIdentityKey{
		ProviderType:    strings.TrimSpace(input.ProviderType),
		ProviderKey:     strings.TrimSpace(input.ProviderKey),
		ProviderSubject: strings.TrimSpace(input.ProviderSubject),
	})
	if err != nil {
		return false, err
	}
	email := strings.TrimSpace(strings.ToLower(input.Email))
	if identityUser != nil {
		if !strings.EqualFold(strings.TrimSpace(identityUser.Email), email) {
			return false, infraerrors.Conflict("AUTH_IDENTITY_EMAIL_MISMATCH", "oauth identity belongs to a different email")
		}
		return false, nil
	}
	if _, err := findUserByNormalizedEmail(ctx, client, email); err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (h *AuthHandler) emailOAuthAffiliateCode(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if code, err := readCookieDecoded(c, emailOAuthAffiliateCookie); err == nil {
		return strings.TrimSpace(code)
	}
	return ""
}

func (h *AuthHandler) createEmailOAuthRegistrationPendingSession(
	c *gin.Context,
	provider string,
	frontendCallback string,
	redirectTo string,
	profile *emailOAuthProfile,
) error {
	if h == nil || profile == nil {
		return infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready")
	}
	browserSessionKey, err := generateOAuthPendingBrowserSession()
	if err != nil {
		return infraerrors.InternalServer("PENDING_AUTH_SESSION_CREATE_FAILED", "failed to create pending auth session").WithCause(err)
	}
	setOAuthPendingBrowserCookie(c, browserSessionKey, h.isRequestHTTPS(c))

	email := strings.TrimSpace(strings.ToLower(profile.Email))
	username := strings.TrimSpace(profile.Username)
	affiliateCode := h.emailOAuthAffiliateCode(c)
	upstreamClaims := map[string]any{
		"email":            email,
		"email_verified":   profile.EmailVerified,
		"username":         username,
		"provider":         provider,
		"provider_key":     provider,
		"provider_subject": strings.TrimSpace(profile.Subject),
	}
	if strings.TrimSpace(profile.DisplayName) != "" {
		upstreamClaims["suggested_display_name"] = strings.TrimSpace(profile.DisplayName)
	}
	if strings.TrimSpace(profile.AvatarURL) != "" {
		upstreamClaims["suggested_avatar_url"] = strings.TrimSpace(profile.AvatarURL)
	}
	if affiliateCode != "" {
		upstreamClaims["aff_code"] = affiliateCode
	}
	for key, value := range profile.Metadata {
		if _, exists := upstreamClaims[key]; !exists {
			upstreamClaims[key] = value
		}
	}

	invitationRequired := h != nil && h.settingSvc != nil && h.settingSvc.IsInvitationCodeEnabled(c.Request.Context())
	pendingError := "registration_completion_required"
	choiceReason := "registration_completion_required"
	if invitationRequired {
		pendingError = "invitation_required"
		choiceReason = "invitation_required"
	}
	completionResponse := map[string]any{
		"step":                      oauthPendingChoiceStep,
		"error":                     pendingError,
		"choice_reason":             choiceReason,
		"adoption_required":         false,
		"create_account_allowed":    true,
		"existing_account_bindable": false,
		"force_email_on_signup":     true,
		"invitation_required":       invitationRequired,
		"email":                     email,
		"resolved_email":            email,
		"provider":                  provider,
		"redirect":                  redirectTo,
	}
	if strings.TrimSpace(frontendCallback) != "" {
		completionResponse["frontend_callback"] = strings.TrimSpace(frontendCallback)
	}

	return h.createOAuthPendingSession(c, oauthPendingSessionPayload{
		Intent:                 oauthIntentLogin,
		Identity:               service.PendingAuthIdentityKey{ProviderType: provider, ProviderKey: provider, ProviderSubject: strings.TrimSpace(profile.Subject)},
		ResolvedEmail:          email,
		RedirectTo:             redirectTo,
		BrowserSessionKey:      browserSessionKey,
		UpstreamIdentityClaims: upstreamClaims,
		CompletionResponse:     completionResponse,
	})
}

type completeEmailOAuthRequest struct {
	Password       string `json:"password" binding:"required,min=6"`
	InvitationCode string `json:"invitation_code,omitempty"`
	AffCode        string `json:"aff_code,omitempty"`
}

func (h *AuthHandler) completeEmailOAuthRegistration(c *gin.Context, provider string) {
	var req completeEmailOAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	_, session, clearCookies, err := readPendingOAuthBrowserSession(c, h)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensurePendingOAuthCompleteRegistrationSession(session); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(session.ProviderType), provider) {
		response.BadRequest(c, "Pending oauth session provider mismatch")
		return
	}
	if err := h.ensureBackendModeAllowsNewUserLogin(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	affiliateCode := strings.TrimSpace(req.AffCode)
	if affiliateCode == "" {
		affiliateCode = pendingSessionStringValue(session.UpstreamIdentityClaims, "aff_code")
	}

	tokenPair, user, err := h.authService.RegisterVerifiedOAuthEmailAccount(
		c.Request.Context(),
		strings.TrimSpace(session.ResolvedEmail),
		req.Password,
		strings.TrimSpace(req.InvitationCode),
		strings.TrimSpace(session.ProviderType),
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	client := h.entClient()
	if client == nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("PENDING_AUTH_NOT_READY", "pending auth service is not ready"))
		return
	}
	tx, err := client.Tx(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to consume pending oauth session").WithCause(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(c.Request.Context(), tx)
	sessionForBinding := *session
	sessionForBinding.UpstreamIdentityClaims = clonePendingMap(session.UpstreamIdentityClaims)
	if strings.TrimSpace(req.InvitationCode) != "" {
		sessionForBinding.UpstreamIdentityClaims["invitation_code"] = strings.TrimSpace(req.InvitationCode)
	}
	decision, err := h.ensurePendingOAuthAdoptionDecision(c, session.ID, oauthAdoptionDecisionRequest{})
	if err != nil {
		_ = tx.Rollback()
		_ = h.authService.RollbackOAuthEmailAccountCreation(c.Request.Context(), user.ID, strings.TrimSpace(req.InvitationCode))
		response.ErrorFrom(c, err)
		return
	}
	if err := applyPendingOAuthBinding(txCtx, client, h.authService, h.userService, &sessionForBinding, decision, &user.ID, true, false); err != nil {
		_ = tx.Rollback()
		_ = h.authService.RollbackOAuthEmailAccountCreation(c.Request.Context(), user.ID, strings.TrimSpace(req.InvitationCode))
		respondPendingOAuthBindingApplyError(c, err)
		return
	}
	if err := h.authService.FinalizeOAuthEmailAccount(
		txCtx,
		user,
		strings.TrimSpace(req.InvitationCode),
		strings.TrimSpace(session.ProviderType),
		affiliateCode,
	); err != nil {
		_ = tx.Rollback()
		_ = h.authService.RollbackOAuthEmailAccountCreation(c.Request.Context(), user.ID, strings.TrimSpace(req.InvitationCode))
		response.ErrorFrom(c, err)
		return
	}
	if err := consumePendingOAuthBrowserSessionTx(c.Request.Context(), tx, session); err != nil {
		_ = tx.Rollback()
		_ = h.authService.RollbackOAuthEmailAccountCreation(c.Request.Context(), user.ID, strings.TrimSpace(req.InvitationCode))
		clearCookies()
		response.ErrorFrom(c, err)
		return
	}
	if err := tx.Commit(); err != nil {
		_ = h.authService.RollbackOAuthEmailAccountCreation(c.Request.Context(), user.ID, strings.TrimSpace(req.InvitationCode))
		response.ErrorFrom(c, infraerrors.InternalServer("PENDING_AUTH_BIND_APPLY_FAILED", "failed to consume pending oauth session").WithCause(err))
		return
	}
	h.authService.ApplyOAuthSignupPromoCode(c.Request.Context(), user.ID, pendingOAuthPromoCode(session))
	markAuthAuditSuccess(c, user.ID, user.Email)
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	clearCookies()
	writeOAuthTokenPairResponse(c, tokenPair)
}

func (h *AuthHandler) getEmailOAuthConfig(ctx context.Context, provider string) (config.EmailOAuthProviderConfig, error) {
	if h != nil && h.settingSvc != nil {
		return h.settingSvc.GetEmailOAuthProviderConfig(ctx, provider)
	}
	return config.EmailOAuthProviderConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
}

func buildEmailOAuthAuthorizeURL(cfg config.EmailOAuthProviderConfig, state string) (string, error) {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("parse authorize_url: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURL)
	q.Set("state", state)
	if strings.TrimSpace(cfg.Scopes) != "" {
		q.Set("scope", cfg.Scopes)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func exchangeEmailOAuthCode(ctx context.Context, cfg config.EmailOAuthProviderConfig, code string) (*emailOAuthTokenResponse, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetFormData(map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     cfg.ClientID,
			"client_secret": cfg.ClientSecret,
			"code":          code,
			"redirect_uri":  cfg.RedirectURL,
		}).
		Post(cfg.TokenURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, truncateLogValue(resp.String(), 1024))
	}
	var tokenResp emailOAuthTokenResponse
	if err := json.Unmarshal(resp.Bytes(), &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, errors.New("missing access_token")
	}
	return &tokenResp, nil
}

func fetchEmailOAuthProfile(ctx context.Context, provider string, cfg config.EmailOAuthProviderConfig, token *emailOAuthTokenResponse) (*emailOAuthProfile, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetBearerAuthToken(token.AccessToken).
		SetHeader("Accept", "application/json").
		Get(cfg.UserInfoURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("userinfo endpoint status %d: %s", resp.StatusCode, truncateLogValue(resp.String(), 1024))
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return parseGitHubOAuthProfile(ctx, cfg, token, resp.String())
	case "google":
		return parseGoogleOAuthProfile(resp.String())
	default:
		return nil, errors.New("unsupported oauth provider")
	}
}

func parseGitHubOAuthProfile(ctx context.Context, cfg config.EmailOAuthProviderConfig, token *emailOAuthTokenResponse, body string) (*emailOAuthProfile, error) {
	subject := strings.TrimSpace(gjson.Get(body, "id").String())
	if subject == "" {
		return nil, errors.New("github user id is missing")
	}
	email := ""
	emailsURL := strings.TrimSpace(cfg.EmailsURL)
	if emailsURL == "" {
		return nil, errors.New("github verified email is missing")
	}
	verifiedEmail, err := fetchGitHubPrimaryVerifiedEmail(ctx, emailsURL, token.AccessToken)
	if err != nil {
		return nil, err
	}
	email = verifiedEmail
	if email == "" {
		return nil, errors.New("github verified email is missing")
	}
	login := strings.TrimSpace(gjson.Get(body, "login").String())
	name := strings.TrimSpace(gjson.Get(body, "name").String())
	return &emailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      firstNonEmpty(login, name, "github_"+subject),
		DisplayName:   firstNonEmpty(name, login),
		AvatarURL:     strings.TrimSpace(gjson.Get(body, "avatar_url").String()),
		Metadata: map[string]any{
			"login": login,
		},
	}, nil
}

func fetchGitHubPrimaryVerifiedEmail(ctx context.Context, emailsURL string, accessToken string) (string, error) {
	resp, err := req.C().
		R().
		SetContext(ctx).
		SetBearerAuthToken(accessToken).
		SetHeader("Accept", "application/json").
		Get(emailsURL)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github emails endpoint status %d: %s", resp.StatusCode, truncateLogValue(resp.String(), 1024))
	}
	items := gjson.Parse(resp.String()).Array()
	for _, item := range items {
		if item.Get("primary").Bool() && item.Get("verified").Bool() {
			if email := strings.TrimSpace(item.Get("email").String()); email != "" {
				return email, nil
			}
		}
	}
	for _, item := range items {
		if item.Get("verified").Bool() {
			if email := strings.TrimSpace(item.Get("email").String()); email != "" {
				return email, nil
			}
		}
	}
	return "", errors.New("github verified email is missing")
}

func parseGoogleOAuthProfile(body string) (*emailOAuthProfile, error) {
	subject := strings.TrimSpace(gjson.Get(body, "sub").String())
	email := strings.TrimSpace(gjson.Get(body, "email").String())
	verified := gjson.Get(body, "email_verified").Bool()
	if subject == "" {
		return nil, errors.New("google subject is missing")
	}
	if email == "" || !verified {
		return nil, errors.New("google verified email is missing")
	}
	name := strings.TrimSpace(gjson.Get(body, "name").String())
	return &emailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      firstNonEmpty(strings.TrimSpace(gjson.Get(body, "given_name").String()), name, email),
		DisplayName:   name,
		AvatarURL:     strings.TrimSpace(gjson.Get(body, "picture").String()),
		Metadata: map[string]any{
			"email_verified": true,
		},
	}, nil
}

func emailOAuthSetCookie(c *gin.Context, name, value string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     emailOAuthCookiePath,
		MaxAge:   emailOAuthCookieMaxAgeSec,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func emailOAuthClearCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     emailOAuthCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
