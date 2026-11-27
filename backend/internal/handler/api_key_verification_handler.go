package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// API Key 三选一身份校验（登录密码 / TOTP / Passkey）的 HTTP 层。
//
//   GET  /api/v1/user/security/verification-factors      该账号实际可用的因子 + 创建 Key 是否必须校验（有效值）
//   PUT  /api/v1/user/security/api-key-create-verification  用户自己决定创建 Key 时要不要验证身份（on/off/auto）
//   POST /api/v1/user/security/passkey/verify/begin      Passkey 校验挑战
//   POST /api/v1/user/security/passkey/verify/finish     Passkey 校验，签发一次性票据
//   POST /api/v1/user/api-keys/:id/reveal-authorize      二次查看授权，签发 5 分钟 grant
//   PUT  /api/v1/user/api-keys/:id/secret-seal           （改造）封存前必须校验登录密码
//   GET  /api/v1/user/api-keys/:id/secret-seal           （改造）必须携带 reveal grant
//
// 审计只记录因子名与结果，绝不记录密码 / 动态码 / Passkey 断言 / 票据。

// auditVerificationOutcome 是审计里 api_key_verification 的取值。
const (
	auditVerificationVerified = "verified"
	// auditVerificationSkipped 创建 / 轮换时没有做身份校验：账号没有任何可用因子，或只有登录密码
	// （没开 TOTP、没绑 Passkey，默认不强制）。字符串保持原样，审计看板按它过滤。
	auditVerificationSkipped = "skipped_no_factor"
	// auditVerificationSkippedByUser 创建 / 轮换时没有做身份校验，是因为用户自己把「创建 API Key 时需要
	// 验证身份」关掉了（设置为 off）。与 skipped_no_factor 分开记，事后能区分「用户自己关的」和「默认不强制」。
	auditVerificationSkippedByUser = "skipped_by_user_setting"
	auditVerificationRejected      = "rejected"

	// apiKeyVerificationSkippedByUserKey 是 keyVerificationGuard 写在 gin.Context 上的标记：本次放行是因为
	// 用户关掉了开关。annotateKeyShowVerification（签名固定）据此选审计标签。
	apiKeyVerificationSkippedByUserKey = "api_key_verification_skipped_by_user"
)

// apiKeyVerificationRequest 是创建 / 二次查看请求里的 verification 对象。
type apiKeyVerificationRequest struct {
	Method string `json:"method"`
	// Password 仅 method=password 时使用；服务端只做 bcrypt 比对，不落库。
	Password string `json:"password"`
	// TOTPCode 仅 method=totp 时使用。
	TOTPCode string `json:"totp_code"`
	// PasskeySessionToken 仅 method=passkey 时使用：由 passkey/verify/finish 签发。
	PasskeySessionToken string `json:"passkey_session_token"`
}

// toService 转成服务层入参（归一化在服务层做，保持 fail-closed）。
func (r *apiKeyVerificationRequest) toService() *service.APIKeyVerificationInput {
	if r == nil {
		return nil
	}
	return &service.APIKeyVerificationInput{
		Method:              r.Method,
		Password:            r.Password,
		TOTPCode:            r.TOTPCode,
		PasskeySessionToken: r.PasskeySessionToken,
	}
}

// passkeyVerifyBeginResponse 是 Passkey 校验挑战响应。
type passkeyVerifyBeginResponse struct {
	SessionToken string `json:"session_token"`
	Options      any    `json:"options"`
}

// passkeyVerifyFinishResponse 是 Passkey 校验票据响应。
type passkeyVerifyFinishResponse struct {
	PasskeySessionToken string `json:"passkey_session_token"`
	Factor              string `json:"factor"`
	ExpiresIn           int64  `json:"expires_in"`
}

// revealAuthorizeResponse 是二次查看授权响应；grant 只回一次，不写日志/审计。
type revealAuthorizeResponse struct {
	Grant     string `json:"grant"`
	Factor    string `json:"factor"`
	ExpiresIn int64  `json:"expires_in"`
}

// requireVerificationService 取校验服务；装配缺失时 fail-closed。
func (h *APIKeyHandler) requireVerificationService(c *gin.Context) *service.APIKeyVerificationService {
	if h == nil || h.verificationService == nil {
		response.ErrorFrom(c, service.ErrAPIKeyVerificationUnavailable)
		return nil
	}
	return h.verificationService
}

// GetVerificationFactors 返回当前账号可用的身份校验因子。
// GET /api/v1/user/security/verification-factors
func (h *APIKeyHandler) GetVerificationFactors(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requireVerificationService(c)
	if svc == nil {
		return
	}
	factors, err := svc.Factors(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, factors)
}

// setAPIKeyCreateVerificationRequest 是 PUT /user/security/api-key-create-verification 的请求体。
type setAPIKeyCreateVerificationRequest struct {
	// Mode 目标模式：on（有可用因子就必须校验）/ off（不要求校验）/ auto（恢复默认规则）。
	Mode string `json:"mode"`
	// Verification 降低保护强度（on > auto > off 的任何一步下降）时必填的三选一身份校验，
	// 结构与创建 Key 的 verification 相同。强度上升或持平不需要。
	Verification *apiKeyVerificationRequest `json:"verification"`
}

// setAPIKeyCreateVerificationBodyMaxBytes 请求体上限：mode + 一份校验材料，远用不到 8KB。
const setAPIKeyCreateVerificationBodyMaxBytes = 8 << 10

// SetAPIKeyCreateVerification 用户自己决定「创建 API Key 时是否需要验证身份」。
// PUT /api/v1/user/security/api-key-create-verification
//
// 只在降低保护强度（on > auto > off 的任何一步下降，含「没有记录」→ off）时要求三选一身份校验，
// 错误码复用 VERIFY_REQUIRED / VERIFY_*_INVALID / VERIFY_PASSWORD_RATE_LIMITED（429 带 Retry-After）；
// 任何失败都不写库。审计只记因子名与结果，以及改前 / 改后的模式标签和「强度是否下降」，绝不记录任何凭据。
// 成功时回包与 GET verification-factors 同形（改后的视图由服务层算好，写库成功之后不再读库，
// 所以不会出现「已经改成功、回包时读库抖一下返回 5xx」），前端直接用它刷新开关状态。
func (h *APIKeyHandler) SetAPIKeyCreateVerification(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requireVerificationService(c)
	if svc == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, setAPIKeyCreateVerificationBodyMaxBytes)
	var req setAPIKeyCreateVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 不回显解析错误：请求体里可能带着校验材料。
		setVerificationAudit(c, nil, auditVerificationRejected, "INVALID_REQUEST")
		response.BadRequest(c, "Invalid request")
		return
	}
	change, err := svc.SetCreateVerificationMode(c.Request.Context(), subject.UserID, req.Mode, req.Verification.toService())
	if err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, infraerrors.Reason(err))
		// 失败时也留下「想改成什么」（只收合法标签，绝不把请求里的任意字符串写进审计）。
		if target, valid := service.NormalizeAPIKeyCreateVerificationMode(req.Mode); valid {
			middleware2.SetAuditExtra(c, map[string]any{"api_key_create_verification_to": target})
		}
		respondVerificationError(c, err)
		return
	}
	if change.Factor != "" {
		setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: change.Factor}, auditVerificationVerified, "")
	}
	middleware2.SetAuditExtra(c, map[string]any{
		"api_key_create_verification_from":    change.From,
		"api_key_create_verification_to":      change.To,
		"api_key_create_verification_relaxed": change.Relaxed,
	})
	c.Header("Cache-Control", "no-store")
	response.Success(c, change.Factors)
}

// BeginKeyPasskeyVerification 为 API Key 创建 / 二次查看生成 Passkey assertion。
// POST /api/v1/user/security/passkey/verify/begin
func (h *APIKeyHandler) BeginKeyPasskeyVerification(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requireVerificationService(c)
	if svc == nil {
		return
	}
	assertion, sessionToken, err := svc.BeginPasskeyVerification(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, passkeyVerifyBeginResponse{SessionToken: sessionToken, Options: assertion})
}

// FinishKeyPasskeyVerification 校验 Passkey assertion，成功后签发一次性票据。
// POST /api/v1/user/security/passkey/verify/finish
func (h *APIKeyHandler) FinishKeyPasskeyVerification(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	svc := h.requireVerificationService(c)
	if svc == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, passkeyFinishBodyMaxBytes)
	var req passkeyFinishRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Credential) == 0 || strings.TrimSpace(req.SessionToken) == "" {
		setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_PASSKEY_FAILED")
		response.ErrorFrom(c, service.ErrAPIKeyPasskeyFailed)
		return
	}
	credentialRequest := cloneRequestWithJSON(c.Request, req.Credential)
	ticket, err := svc.FinishPasskeyVerification(
		c.Request.Context(), subject.UserID, req.SessionToken, credentialRequest)
	if err != nil {
		// 底层细分错误码（挑战过期 / origin / RP ID …）只写日志给运维排障，
		// 对用户统一 VERIFY_PASSKEY_FAILED，前端据此展示「Passkey 验证失败」。
		slog.Warn("api key passkey verification failed",
			"user_id", subject.UserID, "reason", infraerrors.Reason(err), "error", err)
		setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_PASSKEY_FAILED")
		response.ErrorFrom(c, service.ErrAPIKeyPasskeyFailed)
		return
	}
	setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: service.APIKeyFactorPasskey}, auditVerificationVerified, "")
	c.Header("Cache-Control", "no-store")
	response.Success(c, passkeyVerifyFinishResponse{
		PasskeySessionToken: ticket.Token,
		Factor:              service.APIKeyFactorPasskey,
		ExpiresIn:           ticket.ExpiresIn,
	})
}

// AuthorizeReveal 为「再次查看 Key 明文」做身份校验并签发 5 分钟一次性 grant。
// POST /api/v1/user/api-keys/:id/reveal-authorize
func (h *APIKeyHandler) AuthorizeReveal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	keyID, ok := parseAPIKeySecretID(c)
	if !ok {
		return
	}
	svc := h.requireVerificationService(c)
	if svc == nil {
		return
	}
	var req apiKeyVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_REQUIRED")
		response.ErrorFrom(c, service.ErrAPIKeyVerificationRequired)
		return
	}
	input := req.toService()
	if input == nil || !service.APIKeyVerificationMethodValid(input.Method) {
		setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_FACTOR_UNAVAILABLE")
		response.ErrorFrom(c, service.ErrAPIKeyFactorUnavailable)
		return
	}
	grant, err := svc.AuthorizeReveal(c.Request.Context(), subject.UserID, keyID, *input)
	if err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, infraerrors.Reason(err))
		respondVerificationError(c, err)
		return
	}
	setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: grant.Factor}, auditVerificationVerified, "")
	c.Header("Cache-Control", "no-store")
	response.Success(c, revealAuthorizeResponse{
		Grant:     grant.Token,
		Factor:    grant.Factor,
		ExpiresIn: grant.ExpiresIn,
	})
}

// respondVerificationError 写身份校验失败的响应；账号维度的密码失败预算用尽（429）时
// 补 Retry-After，前端据此提示多久后可以再试。
func respondVerificationError(c *gin.Context, err error) {
	if seconds := service.RetryAfterSecondsFromError(err); seconds > 0 {
		c.Header("Retry-After", strconv.Itoa(seconds))
	}
	response.ErrorFrom(c, err)
}

// setVerificationAudit 给当前审计记录补因子与结果：
// 只写因子名 / 结果 / 机器码，绝不写密码、动态码、断言或 grant 明文。
func setVerificationAudit(c *gin.Context, result *service.APIKeyVerificationResult, outcome, reason string) {
	if c == nil {
		return
	}
	fields := map[string]any{"api_key_verification": outcome}
	factor := ""
	if result != nil {
		factor = result.Factor
	}
	if factor != "" {
		fields["api_key_auth_factor"] = factor
	}
	if reason != "" {
		fields["api_key_verification_reason"] = reason
	}
	middleware2.SetAuditExtra(c, fields)
}

// annotateKeyShowVerification 给创建 Key 的审计补因子（成功路径）。
func annotateKeyShowVerification(c *gin.Context, method string) {
	if c == nil {
		return
	}
	if method == "" {
		outcome := auditVerificationSkipped
		if c.GetBool(apiKeyVerificationSkippedByUserKey) {
			outcome = auditVerificationSkippedByUser
		}
		setVerificationAudit(c, nil, outcome, "")
		return
	}
	setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: method}, auditVerificationVerified, "")
}

// keyVerificationGuard 在真正下发 / 变更 Key 明文前做身份校验。
//
// 账号的「有效要求」是必须校验 → 必须带 verification 且校验通过；不要求校验（默认规则下没开 TOTP /
// 没绑 Passkey、用户自己关掉了开关、或一个可用因子都没有的 OAuth-only 账号）→ 放行并在审计里记
// skipped_no_factor / skipped_by_user_setting，这类账号只能一次性显示明文
// （没有登录密码可用于本地派生密钥的账号尤其如此）。
//
// 创建（POST /keys）与批量轮换（POST /keys/batch-rotate）走 VerifyForCreate；
// 会回执明文的编辑（PUT /keys/:id）走 VerifyForKeyEdit（先判归属再校验）。
// apiKeyID<=0 表示「还没有 Key」（创建路径）。
func (h *APIKeyHandler) keyVerificationGuard(
	c *gin.Context,
	userID, apiKeyID int64,
	verification *apiKeyVerificationRequest,
) (string, error) {
	if h == nil || h.verificationService == nil {
		// 非 wire 装配的旧测试 / 嵌入方：跳过校验，保持既有行为。
		return "", nil
	}
	if userID <= 0 {
		return "", service.ErrAPIKeyVerificationUnavailable
	}
	// 个人接口密钥（uak_）本身是在交互式会话中通过严格身份校验后签发的长期管理凭据，
	// 供远程服务器/上游渠道管理无感调用创建/轮换 API Key 接口；此处仍严格 fail-closed 校验归属权（防越权探测），
	// 并将审计因子记为 user_api_key，受面板与 KeyVerification 限流约束。
	if c != nil && c.GetString("auth_method") == service.AuditAuthMethodUserAPIKey {
		if apiKeyID > 0 {
			if err := h.verificationService.VerifyAPIKeyOwnership(c.Request.Context(), userID, apiKeyID); err != nil {
				return "", err
			}
		}
		c.Set(service.ApiKeyVerificationGuardPassedKey, true)
		return service.AuditAuthMethodUserAPIKey, nil
	}
	var (
		result *service.APIKeyVerificationResult
		err    error
	)
	if apiKeyID > 0 {
		result, err = h.verificationService.VerifyForKeyEdit(c.Request.Context(), userID, apiKeyID, verification.toService())
	} else {
		result, err = h.verificationService.VerifyForCreate(c.Request.Context(), userID, verification.toService())
	}
	if err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, infraerrors.Reason(err))
		return "", err
	}
	method := ""
	if result != nil {
		method = result.Factor
		if result.SkippedByUserSetting {
			c.Set(apiKeyVerificationSkippedByUserKey, true)
		}
	}
	// 成功（含 skipped_no_factor / skipped_by_user_setting 的放行）才打标记：幂等中间件只认「本请求
	// 已经过校验」的瞬时重放，历史回执 / 跨请求重放一律重新校验。
	c.Set(service.ApiKeyVerificationGuardPassedKey, true)
	return method, nil
}

// keyUpdateEmitsPlaintext 报告这次编辑是否会回执明文 Key。
//
// PUT /keys/:id 唯一的明文回执是 regenerate_key=true：轮换出新明文、旧值
// 立刻失效。改名 / 配额 / 白名单等编辑只回脱敏视图，不下发明文，因此不要求
// 身份校验（这类高频操作不能每次弹 TOTP）。
func keyUpdateEmitsPlaintext(req UpdateAPIKeyRequest) bool {
	return req.RegenerateKey
}

// keyVerificationGrantHeader 是二次查看密文时携带 grant 的请求头名。
const keyVerificationGrantHeader = "X-Reveal-Grant"

// consumeRevealGrant 校验 grant 并把授权因子写进审计；失败返回空串。
func (h *APIKeyHandler) consumeRevealGrant(c *gin.Context, userID, keyID int64) (string, bool) {
	if h == nil || h.verificationService == nil {
		// 非 wire 装配的旧测试：不启用 grant 门禁（生产装配必定启用）。
		return "", true
	}
	token := strings.TrimSpace(c.GetHeader(keyVerificationGrantHeader))
	factor, err := h.verificationService.ConsumeRevealGrant(c.Request.Context(), token, userID, keyID)
	if err != nil {
		setVerificationAudit(c, nil, auditVerificationRejected, infraerrors.Reason(err))
		response.ErrorFrom(c, err)
		return "", false
	}
	setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: factor}, auditVerificationVerified, "")
	return factor, true
}
