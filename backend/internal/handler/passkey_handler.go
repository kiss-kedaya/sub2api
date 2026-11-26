package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type PasskeyHandler struct {
	passkeys    *service.PasskeyService
	authService *service.AuthService
	settingSvc  *service.SettingService
	// totpService 提供按因子的 5 分钟 step-up 授权（Passkey 因子）与凭证签发，
	// 同时驱动 Passkey 创建/删除的 2FA 门禁（账号开启 TOTP 时必须先过 step-up）。
	totpService *service.TotpService
	// userService 提供账号信息，供 Passkey 创建/删除门禁判断因子绑定情况。
	userService *service.UserService
	// twoFactorLimiter 按账号维度限制 2FA 校验尝试（60s 滚动窗口 5 次）。
	twoFactorLimiter twoFactorRateLimiter
}

func NewPasskeyHandler(
	passkeys *service.PasskeyService,
	authService *service.AuthService,
	settingService *service.SettingService,
) *PasskeyHandler {
	return &PasskeyHandler{
		passkeys:    passkeys,
		authService: authService,
		settingSvc:  settingService,
	}
}

// SetStepUpDependencies 注入 Passkey 创建/删除门禁所需的 TOTP 服务与用户查询。
// 由 wire 装配（ProvidePasskeyHandler）；为 nil 时门禁放行，保持既有测试可直接构造。
func (h *PasskeyHandler) SetStepUpDependencies(totpService *service.TotpService, userService *service.UserService) {
	if h == nil {
		return
	}
	h.totpService = totpService
	h.userService = userService
}

// SetTotpService 注入 Passkey step-up 所需的 TOTP/因子策略服务。
func (h *PasskeyHandler) SetTotpService(totpService *service.TotpService) {
	if h == nil {
		return
	}
	h.totpService = totpService
}

// SetTwoFactorRateLimiter 注入账号维度 2FA 限速器（由 router 装配）。
func (h *PasskeyHandler) SetTwoFactorRateLimiter(limiter twoFactorRateLimiter) {
	if h == nil {
		return
	}
	h.twoFactorLimiter = limiter
}

type passkeyOptionsResponse struct {
	SessionToken string `json:"session_token"`
	Options      any    `json:"options"`
}

type passkeyFinishRequest struct {
	SessionToken string          `json:"session_token" binding:"required"`
	Name         string          `json:"name,omitempty"`
	Credential   json.RawMessage `json:"credential" binding:"required"`
}

type passkeyBeginLoginRequest struct {
	// TurnstileToken 承载阿里云验证码的 captchaVerifyParam（复用既有请求字段名）
	TurnstileToken        string `json:"turnstile_token"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr"`
}

type passkeyRenameRequest struct {
	Name string `json:"name" binding:"required"`
}

// passkeyPasswordRequest carries the account password that gates passkey
// enrollment and revocation. Binding errors are tolerated: a missing or
// malformed body yields an empty password, which the service rejects with a
// precise PASSWORD_REQUIRED error.
type passkeyPasswordRequest struct {
	Password string `json:"password"`
}

func bindPasskeyPassword(c *gin.Context) string {
	var req passkeyPasswordRequest
	_ = c.ShouldBindJSON(&req)
	return req.Password
}

const passkeyFinishBodyMaxBytes = 64 * 1024

// BeginLogin starts a usernameless, discoverable-credential login ceremony.
func (h *PasskeyHandler) BeginLogin(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	var req passkeyBeginLoginRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.authService.VerifyActionCaptchaIfEnabled(c.Request.Context(), service.CaptchaProof{
		TurnstileToken: req.TurnstileToken,
		TencentTicket:  req.TencentCaptchaTicket,
		TencentRandstr: req.TencentCaptchaRandstr,
	}, ip.GetClientIP(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	assertion, token, err := h.passkeys.BeginLogin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, passkeyOptionsResponse{SessionToken: token, Options: assertion})
}

// FinishLogin validates a passkey assertion and creates a normal Sub2API token
// session. User verification is mandatory, so a successful passkey assertion
// already supplies phishing-resistant multi-factor authentication and does not
// enter the separate TOTP challenge flow.
func (h *PasskeyHandler) FinishLogin(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	req, ok := bindPasskeyFinishRequest(c)
	if !ok {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	user, err := h.passkeys.FinishLogin(c.Request.Context(), req.SessionToken, credentialRequest)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err = h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// passkey 登录不能绕过登录二次验证：与密码登录 / 第三方 OAuth 登录同一口径
	// （SettingService.LoginRequiresTOTP）。当前前端没有 passkey → 2FA 的挑战流程，
	// 宁可 fail-closed 也不能直接发 token。
	if h.settingSvc != nil && h.settingSvc.LoginRequiresTOTP(c.Request.Context(), user) {
		response.Forbidden(c, "This account has two-factor authentication enabled; sign in with your password and 2FA code instead of a passkey")
		return
	}
	middleware2.MarkAuthAuditActor(c, user.ID, user.Email)
	middleware2.MarkAuthAuditResult(c, service.AuditResultSuccess, "")
	c.Set("auth_method", service.AuditAuthMethodPasskey)
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	respondWithTokenPair(c, h.authService, user)
}

func (h *PasskeyHandler) BeginRegistration(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	// 创建 Passkey 是可长期接管账号的敏感操作：账号开启 TOTP 时必须先过 step-up，
	// 未开启 TOTP 的账号不额外要求（与「校验当前密码」类入口同一口径）。
	if !h.requirePasskeyMutationStepUp(c, subject.UserID) {
		return
	}
	creation, token, err := h.passkeys.BeginRegistration(c.Request.Context(), subject.UserID, bindPasskeyPassword(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, passkeyOptionsResponse{SessionToken: token, Options: creation})
}

func (h *PasskeyHandler) FinishRegistration(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	req, valid := bindPasskeyFinishRequest(c)
	if !valid {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	credential, err := h.passkeys.FinishRegistration(
		c.Request.Context(),
		subject.UserID,
		req.SessionToken,
		req.Name,
		credentialRequest,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, credential)
}

func (h *PasskeyHandler) List(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	credentials, err := h.passkeys.List(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, credentials)
}

func (h *PasskeyHandler) Rename(c *gin.Context) {
	subject, credentialID, ok := passkeyMutationTarget(c)
	if !ok {
		return
	}
	var req passkeyRenameRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		response.BadRequest(c, "Passkey name is required")
		return
	}
	if err := h.passkeys.Rename(c.Request.Context(), subject.UserID, credentialID, req.Name); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

func (h *PasskeyHandler) Delete(c *gin.Context) {
	subject, credentialID, ok := passkeyMutationTarget(c)
	if !ok {
		return
	}
	// 删除 Passkey 同样是敏感操作：开启 TOTP 的账号必须先过 step-up。
	if !h.requirePasskeyMutationStepUp(c, subject.UserID) {
		return
	}
	if err := h.passkeys.Delete(c.Request.Context(), subject.UserID, credentialID, bindPasskeyPassword(c)); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"success": true})
}

// requirePasskeyMutationStepUp 是 Passkey 创建/删除的 2FA 门禁：
//   - 账号未绑定任何 2FA 因子：直接放行（不额外要求）；
//   - 已绑定 TOTP / Passkey 因子：任一已绑定因子持有有效的 5 分钟 grant 即可；
//     一个都没有 → 403 STEP_UP_REQUIRED，前端全局 step-up 对话框完成
//     /user/totp/step-up 或 /user/step-up/passkey/finish 之一后自动重试本次请求。
//
// totpService 或 userService 未装配时保持旧行为（放行），避免非 wire 装配的调用方
// （测试、脚本）被意外阻断。
func (h *PasskeyHandler) requirePasskeyMutationStepUp(c *gin.Context, userID int64) bool {
	if h == nil || h.totpService == nil || h.userService == nil || userID <= 0 {
		return true
	}
	user, err := h.userService.GetByID(c.Request.Context(), userID)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	return requirePasswordVerificationGrant(c, h.totpService, user)
}

// passkeyStepUpResponse 是 Passkey step-up 验证成功的响应。
// 结构与 TOTP step-up 保持一致（verified + 绝对/剩余秒数），前端共用倒计时。
type passkeyStepUpResponse struct {
	Verified  bool   `json:"verified"`
	Factor    string `json:"factor"`
	ExpiresAt int64  `json:"expires_at"`
	ExpiresIn int64  `json:"expires_in"`
}

// StepUpBegin 为已登录会话生成 Passkey assertion 挑战（敏感操作 step-up）。
// POST /api/v1/user/step-up/passkey/begin
func (h *PasskeyHandler) StepUpBegin(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.totpService == nil {
		response.ErrorFrom(c, service.ErrTwoFactorPasskeyUnavailable)
		return
	}
	assertion, token, err := h.passkeys.BeginLoginForUser(
		c.Request.Context(), subject.UserID, service.PasskeySessionKindStepUp)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, passkeyOptionsResponse{SessionToken: token, Options: assertion})
}

// StepUpFinish 校验 Passkey assertion，成功后：
//  1. 为当前会话写入 Passkey 因子的 5 分钟 step-up 授权（与 TOTP 授权同格式，
//     但按因子隔离）；
//  2. 下发 Passkey 因子的 5 分钟不透明凭证 Cookie（scope=password），供
//     「校验当前密码」类入口复用；
//  3. 返回与 TOTP step-up 对齐的到期时间。
//
// POST /api/v1/user/step-up/passkey/finish
func (h *PasskeyHandler) StepUpFinish(c *gin.Context) {
	if !h.requirePasskeysEnabled(c) {
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	req, valid := bindPasskeyFinishRequest(c)
	if !valid {
		return
	}
	attemptCtx, ok := checkTwoFactorRateLimit(c, h.twoFactorLimiter, subject.UserID)
	if !ok {
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	user, err := h.passkeys.FinishLoginForUser(
		attemptCtx,
		subject.UserID,
		service.PasskeySessionKindStepUp,
		req.SessionToken,
		credentialRequest,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	resetTwoFactorRateLimit(c.Request.Context(), h.twoFactorLimiter, subject.UserID)

	sessionKey := middleware2.StepUpSessionKey(c, subject.UserID)
	ttl, err := h.totpService.GrantStepUpForFactor(
		c.Request.Context(), subject.UserID, sessionKey, service.FactorPasskey)
	if err != nil {
		response.ErrorFrom(c, service.ErrTotpUnavailable)
		return
	}
	markRequestFactor(c, service.FactorPasskey)
	issueTwoFactorGrant(c, h.totpService, user.ID, service.TwoFactorGrantScopePassword, service.FactorPasskey)
	response.Success(c, passkeyStepUpResponse{
		Verified:  true,
		Factor:    service.FactorPasskey.String(),
		ExpiresAt: time.Now().Add(ttl).Unix(),
		ExpiresIn: int64(ttl / time.Second),
	})
}

func (h *PasskeyHandler) requirePasskeysEnabled(c *gin.Context) bool {
	if h.settingSvc == nil {
		response.ErrorFrom(c, service.ErrPasskeysDisabled)
		return false
	}
	enabled, err := h.settingSvc.PasskeyEnabled(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	if !enabled {
		response.ErrorFrom(c, service.ErrPasskeysDisabled)
	}
	return enabled
}

func (h *PasskeyHandler) ensureBackendModeAllowsUser(ctx context.Context, user *service.User) error {
	if err := ensureLoginUserActive(user); err != nil {
		return err
	}
	if h.settingSvc == nil || !h.settingSvc.IsBackendModeEnabled(ctx) || user.IsAdmin() {
		return nil
	}
	return infraerrors.Forbidden("BACKEND_MODE_ADMIN_ONLY", "Backend mode is active. Only admin login is allowed.")
}

func bindPasskeyFinishRequest(c *gin.Context) (*passkeyFinishRequest, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, passkeyFinishBodyMaxBytes)
	var req passkeyFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Credential) == 0 {
		response.BadRequest(c, "Invalid passkey response")
		return nil, false
	}
	return &req, true
}

func cloneRequestWithJSON(original *http.Request, payload []byte) *http.Request {
	request := original.Clone(original.Context())
	request.Body = io.NopCloser(bytes.NewReader(payload))
	request.ContentLength = int64(len(payload))
	request.Header = original.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	return request
}

func passkeyMutationTarget(c *gin.Context) (middleware2.AuthSubject, int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return middleware2.AuthSubject{}, 0, false
	}
	credentialID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || credentialID <= 0 {
		response.BadRequest(c, "Invalid passkey ID")
		return middleware2.AuthSubject{}, 0, false
	}
	return subject, credentialID, true
}
