package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 「创建 API Key 时是否需要验证身份」的用户开关（PUT /user/security/api-key-create-verification）的 HTTP 层：
//   - GET verification-factors 回「有效」要求 + 模式 + 能不能配置；
//   - PUT 只在变宽松时要求三选一校验，失败不写库，429 带 Retry-After；
//   - 审计只记因子名 / 结果 / 改前改后标签，绝不带任何凭据；
//   - 创建 / 批量轮换 / 明文编辑三个入口都遵守 off。

const (
	cvhPath       = "/api/v1/user/security/api-key-create-verification"
	cvhFactors    = "/api/v1/user/security/verification-factors"
	cvhPassword   = "correct-password"
	cvhWrongToken = "wrong-password"
)

// handlerCreateVerificationPrefs 是 service.UserSecurityPreferenceRepository 的内存实现（语义同迁移 343 的表）。
type handlerCreateVerificationPrefs struct {
	mu       sync.Mutex
	rows     map[int64]string
	getErr   error
	casErr   error
	conflict bool // 比较并交换永远「被别人抢先」
	casCalls int
}

func (p *handlerCreateVerificationPrefs) GetAPIKeyCreateVerificationMode(_ context.Context, userID int64) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.getErr != nil {
		return "", p.getErr
	}
	return p.rows[userID], nil
}

func (p *handlerCreateVerificationPrefs) CompareAndSetAPIKeyCreateVerificationMode(_ context.Context, userID int64, expected, next string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.casCalls++
	if p.casErr != nil {
		return false, p.casErr
	}
	if p.conflict || p.rows[userID] != expected {
		return false, nil
	}
	if next == "" {
		delete(p.rows, userID)
	} else {
		p.rows[userID] = next
	}
	return true, nil
}

func (p *handlerCreateVerificationPrefs) stored(userID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if value := p.rows[userID]; value != "" {
		return value
	}
	return service.APIKeyCreateVerificationAuto
}

// cvhAuditRecorder 在每个请求结束后抓下 handler 写进 gin.Context 的 audit_extra
// （真实的 SetAuditExtra 已经按白名单过滤过：不在白名单里的字段根本进不来）。
type cvhAuditRecorder struct {
	mu   sync.Mutex
	last map[string]any
}

func (r *cvhAuditRecorder) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		extra := map[string]any{}
		if value, ok := c.Get("audit_extra"); ok {
			if fields, ok := value.(map[string]any); ok {
				for key, item := range fields {
					extra[key] = item
				}
			}
		}
		r.mu.Lock()
		r.last = extra
		r.mu.Unlock()
	}
}

func (r *cvhAuditRecorder) extra() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

type cvhEngine struct {
	router *gin.Engine
	keys   *plaintextGuardKeyRepo
	store  *handlerVerifyStore
	prefs  *handlerCreateVerificationPrefs
	budget *fakePasswordBudget
	audit  *cvhAuditRecorder
}

// newCVHEngine 用真实的 handler + 校验服务 + 内存偏好存储装配出整条链路。
func newCVHEngine(t *testing.T, user *service.User, passkeyCount int, stored string, idempotencyRepo service.IdempotencyRepository) *cvhEngine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	keys := newPlaintextGuardKeyRepo()
	apiKeyService := service.NewAPIKeyService(keys, availableGroupsUserRepoStub{}, nil, nil, nil, nil, &config.Config{})
	store := newHandlerVerifyStore()
	users := &handlerVerifyUserRepo{user: user}
	totpService := service.NewTotpService(users, accountTypeTOTPEncryptor{}, store, nil, nil, nil)
	var passkeys *service.PasskeyService
	if passkeyCount > 0 {
		var err error
		passkeys, err = service.NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
			Enabled:       true,
			RPDisplayName: "Sub2API",
			RPID:          "sub2api.example.com",
			RPOrigins:     []string{"https://sub2api.example.com"},
		}}, accountTypePasskeyRepo{count: passkeyCount}, accountTypePasskeySessions{}, users)
		require.NoError(t, err)
	}
	verification := service.NewAPIKeyVerificationService(users, keys, nil, totpService, passkeys, store)
	prefs := &handlerCreateVerificationPrefs{rows: map[int64]string{}}
	if stored != "" && stored != service.APIKeyCreateVerificationAuto {
		prefs.rows[user.ID] = stored
	}
	verification.SetCreateVerificationPreferences(prefs)
	budget := newFakePasswordBudget(5)
	verification.SetPasswordAttemptLimiter(budget)
	h := NewAPIKeyHandler(apiKeyService, nil)
	h.SetVerificationService(verification)

	if idempotencyRepo != nil {
		previous := service.DefaultIdempotencyCoordinator()
		service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(idempotencyRepo, service.DefaultIdempotencyConfig()))
		t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	}

	audit := &cvhAuditRecorder{}
	router := gin.New()
	r := router.Group("/api/v1")
	r.Use(audit.middleware())
	r.Use(withUserSubject(7))
	r.GET("/user/security/verification-factors", h.GetVerificationFactors)
	r.PUT("/user/security/api-key-create-verification", h.SetAPIKeyCreateVerification)
	r.POST("/keys", h.Create)
	r.PUT("/keys/:id", h.Update)
	r.POST("/keys/batch-rotate", h.BatchRotate)
	return &cvhEngine{router: router, keys: keys, store: store, prefs: prefs, budget: budget, audit: audit}
}

func cvhPasswordUser(t *testing.T) *service.User { return handlerVerificationUser(t) }

// cvhTOTPUser 是开了 TOTP 的邮箱账号（有登录密码）：默认规则下创建 Key 必须校验。
func cvhTOTPUser(t *testing.T) *service.User { return handlerVerificationTOTPUser(t) }

// cvhOAuthUser 是没设过本地密码、没开 2FA 的第三方登录账号：一个可用因子都没有。
func cvhOAuthUser(t *testing.T) *service.User {
	t.Helper()
	user := handlerVerificationUser(t)
	user.SignupSource = "github"
	return user
}

func cvhPut(e *cvhEngine, body string) *httptest.ResponseRecorder {
	return doJSON(e.router, http.MethodPut, cvhPath, body, nil)
}

func cvhPasswordBody(mode, password string) string {
	return `{"mode":"` + mode + `","verification":{"method":"password","password":"` + password + `"}}`
}

func cvhFactorsData(t *testing.T, rec *httptest.ResponseRecorder) service.APIKeyVerificationFactors {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Data service.APIKeyVerificationFactors `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Data
}

func cvhReason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Reason
}

// ---- GET：回的是「有效」值 ---------------------------------------------------

func TestVerificationFactorsEndpointReportsTheEffectiveSwitch(t *testing.T) {
	cases := []struct {
		name             string
		user             *service.User
		stored           string
		wantRequired     bool
		wantMode         string
		wantConfigurable bool
	}{
		{"password only, never touched", cvhPasswordUser(t), "", false, "auto", true},
		{"password only, on", cvhPasswordUser(t), "on", true, "on", true},
		{"password only, off", cvhPasswordUser(t), "off", false, "off", true},
		{"totp, never touched", cvhTOTPUser(t), "", true, "auto", true},
		{"totp, off", cvhTOTPUser(t), "off", false, "off", true},
		{"totp, on", cvhTOTPUser(t), "on", true, "on", true},
		{"oauth without any factor, never touched", cvhOAuthUser(t), "", false, "auto", false},
		{"oauth without any factor, stale on cannot lock anyone", cvhOAuthUser(t), "on", false, "on", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCVHEngine(t, tc.user, 0, tc.stored, nil)
			rec := doJSON(engine.router, http.MethodGet, cvhFactors, "", nil)
			factors := cvhFactorsData(t, rec)
			require.Equal(t, tc.wantRequired, factors.CreateVerificationRequired)
			require.Equal(t, tc.wantMode, factors.CreateVerificationMode)
			require.Equal(t, tc.wantConfigurable, factors.CreateVerificationConfigurable)
			require.Contains(t, rec.Body.String(), `"create_verification_mode":"`+tc.wantMode+`"`)
			require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		})
	}
}

// ---- PUT：鉴权规则 ------------------------------------------------------------

func TestSetCreateVerificationRelaxingRequiresIdentityVerification(t *testing.T) {
	engine := newCVHEngine(t, cvhTOTPUser(t), 0, "", nil)

	// 1) 开了 2FA 的账号默认就是强制的：关掉就是放宽，没带校验 → 400 VERIFY_REQUIRED，不写库。
	rec := cvhPut(engine, `{"mode":"off"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", cvhReason(t, rec))
	require.Contains(t, rec.Body.String(), "turn this protection off")
	require.Equal(t, "auto", engine.prefs.stored(7))
	require.Zero(t, engine.prefs.casCalls, "a rejected change must not touch the store")
	audit := engine.audit.extra()
	require.Equal(t, "rejected", audit["api_key_verification"])
	require.Equal(t, "VERIFY_REQUIRED", audit["api_key_verification_reason"])
	require.Equal(t, "off", audit["api_key_create_verification_to"], "a rejected attempt still records what it asked for")

	// 2) 密码错误 → VERIFY_PASSWORD_INVALID，不写库。
	rec = cvhPut(engine, cvhPasswordBody("off", cvhWrongToken))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", cvhReason(t, rec))
	require.Equal(t, "auto", engine.prefs.stored(7))
	require.Zero(t, engine.prefs.casCalls)

	// 3) 未绑定的因子 → VERIFY_FACTOR_UNAVAILABLE。
	rec = cvhPut(engine, `{"mode":"off","verification":{"method":"passkey","passkey_session_token":"ticket"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_FACTOR_UNAVAILABLE", cvhReason(t, rec))
	require.Equal(t, "auto", engine.prefs.stored(7))

	// 4) 密码正确 → 200，回包是新的有效状态，审计记因子名 + 改前改后标签。
	rec = cvhPut(engine, cvhPasswordBody("off", cvhPassword))
	factors := cvhFactorsData(t, rec)
	require.False(t, factors.CreateVerificationRequired)
	require.Equal(t, "off", factors.CreateVerificationMode)
	require.Equal(t, "off", engine.prefs.stored(7))
	audit = engine.audit.extra()
	require.Equal(t, "verified", audit["api_key_verification"])
	require.Equal(t, "password", audit["api_key_auth_factor"])
	require.Equal(t, "auto", audit["api_key_create_verification_from"])
	require.Equal(t, "off", audit["api_key_create_verification_to"])
	require.Equal(t, true, audit["api_key_create_verification_relaxed"])
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestSetCreateVerificationTighteningNeedsNoVerification(t *testing.T) {
	engine := newCVHEngine(t, cvhTOTPUser(t), 0, "off", nil)

	// off → on：变严，不需要校验，也不要求带 verification 字段。
	rec := cvhPut(engine, `{"mode":"on"}`)
	factors := cvhFactorsData(t, rec)
	require.True(t, factors.CreateVerificationRequired)
	require.Equal(t, "on", engine.prefs.stored(7))
	audit := engine.audit.extra()
	require.Equal(t, "off", audit["api_key_create_verification_from"])
	require.Equal(t, "on", audit["api_key_create_verification_to"])
	require.Equal(t, false, audit["api_key_create_verification_relaxed"])
	require.NotContains(t, audit, "api_key_verification", "nothing was verified, so nothing claims it was")
	require.NotContains(t, audit, "api_key_auth_factor")

	// on → auto（开了 2FA 的账号 auto 同样强制）：持平，不需要校验。
	rec = cvhPut(engine, `{"mode":"auto"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "auto", engine.prefs.stored(7))
	require.Empty(t, engine.prefs.rows, "auto deletes the row")

	// 重复提交同一个值：幂等成功，不写库。
	calls := engine.prefs.casCalls
	rec = cvhPut(engine, `{"mode":"auto"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, calls, engine.prefs.casCalls)
}

// 只有登录密码的账号：on → auto 是放宽（输密码的要求没了），auto → off 不是。
func TestSetCreateVerificationComparesEffectiveRequirementForPasswordOnlyAccounts(t *testing.T) {
	engine := newCVHEngine(t, cvhPasswordUser(t), 0, "on", nil)
	rec := cvhPut(engine, `{"mode":"auto"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", cvhReason(t, rec))
	require.Equal(t, "on", engine.prefs.stored(7))

	rec = cvhPut(engine, cvhPasswordBody("auto", cvhPassword))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "auto", engine.prefs.stored(7))

	rec = cvhPut(engine, `{"mode":"off"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "off", engine.prefs.stored(7))
	require.Equal(t, false, engine.audit.extra()["api_key_create_verification_relaxed"])
}

// 没有任何可用因子的账号：on / off 没有意义（409，不落库），auto 只用来清残留。
func TestSetCreateVerificationForAccountWithoutAnyFactor(t *testing.T) {
	engine := newCVHEngine(t, cvhOAuthUser(t), 0, "", nil)
	for _, mode := range []string{"on", "off"} {
		rec := cvhPut(engine, `{"mode":"`+mode+`"}`)
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		require.Equal(t, "API_KEY_CREATE_VERIFICATION_NOT_CONFIGURABLE", cvhReason(t, rec))
		require.Equal(t, "auto", engine.prefs.stored(7))
	}
	rec := cvhPut(engine, `{"mode":"auto"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.False(t, cvhFactorsData(t, rec).CreateVerificationConfigurable)

	// 创建照样放行（这类账号只能一次性显示明文），不受任何残留设置影响。
	engine = newCVHEngine(t, cvhOAuthUser(t), 0, "on", nil)
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"one-time"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestSetCreateVerificationRejectsBadRequests(t *testing.T) {
	engine := newCVHEngine(t, cvhPasswordUser(t), 0, "", nil)
	for name, body := range map[string]string{
		"unknown mode":   `{"mode":"maybe"}`,
		"missing mode":   `{}`,
		"empty mode":     `{"mode":""}`,
		"numeric mode":   `{"mode":1}`,
		"not json":       `mode=on`,
		"empty body":     ``,
		"array body":     `["on"]`,
		"null mode":      `{"mode":null}`,
		"oversized body": `{"mode":"on","pad":"` + strings.Repeat("x", 16<<10) + `"}`,
	} {
		rec := cvhPut(engine, body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", name, rec.Body.String())
		require.Equal(t, "auto", engine.prefs.stored(7), name)
	}
	rec := cvhPut(engine, `{"mode":"maybe"}`)
	require.Equal(t, "API_KEY_CREATE_VERIFICATION_MODE_INVALID", cvhReason(t, rec))
	// 失败时审计只收合法标签：想改成什么（maybe）不是合法标签，不会被写进去。
	require.NotContains(t, engine.audit.extra(), "api_key_create_verification_to")
	// 解析错误不回显请求体（里面可能带着校验材料）。
	rec = cvhPut(engine, `{"mode":"on","verification":{"password":"hunter2-secret"`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, rec.Body.String(), "hunter2-secret")
}

func TestSetCreateVerificationPasswordBudgetGivesRetryAfter(t *testing.T) {
	engine := newCVHEngine(t, cvhTOTPUser(t), 0, "", nil)
	for i := 0; i < 5; i++ {
		rec := cvhPut(engine, cvhPasswordBody("off", cvhWrongToken))
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Equal(t, "VERIFY_PASSWORD_INVALID", cvhReason(t, rec))
	}
	// 预算用尽：即使这次的密码是对的，也只拿到 429 + Retry-After，且设置原样不动。
	rec := cvhPut(engine, cvhPasswordBody("off", cvhPassword))
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_RATE_LIMITED", cvhReason(t, rec))
	require.Equal(t, "42", rec.Header().Get("Retry-After"))
	require.Equal(t, "auto", engine.prefs.stored(7))
	require.Equal(t, 5, engine.budget.usedBy(7), "the same per-user budget the other entries use")
	require.Equal(t, "VERIFY_PASSWORD_RATE_LIMITED", engine.audit.extra()["api_key_verification_reason"])

	// 同一份预算：创建 Key 带正确密码也是 429。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"budget","verification":{"method":"password","password":"`+cvhPassword+`"}}`, nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
}

func TestSetCreateVerificationStorageFailuresFailClosed(t *testing.T) {
	engine := newCVHEngine(t, cvhTOTPUser(t), 0, "", nil)

	engine.prefs.casErr = context.DeadlineExceeded
	rec := cvhPut(engine, cvhPasswordBody("off", cvhPassword))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Equal(t, "auto", engine.prefs.stored(7))
	engine.prefs.casErr = nil

	engine.prefs.getErr = context.DeadlineExceeded
	rec = cvhPut(engine, cvhPasswordBody("off", cvhPassword))
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	// 读不到设置时，创建（不带校验）也不能悄悄放行。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"no-peek"}`, nil)
	require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest, rec.Body.String())
	require.Zero(t, engine.keys.guardCreateCount())
	engine.prefs.getErr = nil

	// 一直被并发抢先：409，不写库。
	engine.prefs.conflict = true
	rec = cvhPut(engine, cvhPasswordBody("off", cvhPassword))
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, "API_KEY_CREATE_VERIFICATION_CONFLICT", cvhReason(t, rec))
	require.Equal(t, "auto", engine.prefs.stored(7))
}

func TestSetCreateVerificationRequiresAuthenticationAndWiring(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 没有登录主体 → 401。
	h := NewAPIKeyHandler(nil, nil)
	router := gin.New()
	router.PUT("/x", h.SetAPIKeyCreateVerification)
	rec := doJSON(router, http.MethodPut, "/x", `{"mode":"on"}`, nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())

	// 校验服务没装配 → fail-closed（503），不是悄悄成功。
	router = gin.New()
	router.Use(withUserSubject(7))
	router.PUT("/x", h.SetAPIKeyCreateVerification)
	rec = doJSON(router, http.MethodPut, "/x", `{"mode":"on"}`, nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_UNAVAILABLE", cvhReason(t, rec))
}

// TOTP 动态码 / Passkey 票据同样能授权放宽，而且各自只能用一次。
func TestSetCreateVerificationRelaxWithTOTPCodeAndPasskeyTicket(t *testing.T) {
	// 第三方登录 + TOTP（没有登录密码）：只能用动态码。
	totpUser := accountTypeUser(t, true)
	engine := newCVHEngine(t, totpUser, 0, "on", nil)
	_, current, next := totpCodesForThreeSteps(t)
	rec := cvhPut(engine, `{"mode":"off","verification":{"method":"totp","totp_code":"`+current+`"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "off", engine.prefs.stored(7))
	require.Equal(t, "totp", engine.audit.extra()["api_key_auth_factor"])
	require.NotContains(t, rec.Body.String(), current)

	// 收紧回去，再拿同一个码来放宽：被拒（一码一用）；下一个时间步的新码可以。
	rec = cvhPut(engine, `{"mode":"on"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = cvhPut(engine, `{"mode":"off","verification":{"method":"totp","totp_code":"`+current+`"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_TOTP_INVALID", cvhReason(t, rec))
	require.Equal(t, "on", engine.prefs.stored(7))
	rec = cvhPut(engine, `{"mode":"off","verification":{"method":"totp","totp_code":"`+next+`"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 绑了 Passkey 的账号：用一次性票据。
	passkeyUser := accountTypeUser(t, false)
	engine = newCVHEngine(t, passkeyUser, 1, "", nil)
	mint := func(token string) {
		sum := sha256.Sum256([]byte(token))
		require.NoError(t, engine.store.SaveAPIKeyPasskeyTicket(t.Context(), hex.EncodeToString(sum[:]), &service.APIKeyPasskeyVerificationRecord{
			UserID:    7,
			Factor:    service.APIKeyFactorPasskey,
			ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		}, 5*time.Minute))
	}
	mint("cv-ticket-1")
	rec = cvhPut(engine, `{"mode":"off","verification":{"method":"passkey","passkey_session_token":"cv-ticket-1"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "passkey", engine.audit.extra()["api_key_auth_factor"])
	rec = cvhPut(engine, `{"mode":"on"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = cvhPut(engine, `{"mode":"off","verification":{"method":"passkey","passkey_session_token":"cv-ticket-1"}}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSKEY_FAILED", cvhReason(t, rec), "a ticket is one-shot")
	require.Equal(t, "on", engine.prefs.stored(7))
}

// ---- 审计里没有任何凭据 --------------------------------------------------------

func TestSetCreateVerificationAuditNeverContainsCredentials(t *testing.T) {
	engine := newCVHEngine(t, cvhTOTPUser(t), 0, "", nil)
	secrets := []string{"hunter2-super-secret", "987654", "opaque-ticket-fixture"}
	bodies := []string{
		// 成功、失败、凭据混着带的请求都查一遍。
		`{"mode":"off","verification":{"method":"password","password":"hunter2-super-secret","totp_code":"987654","passkey_session_token":"opaque-ticket-fixture"}}`,
		`{"mode":"off","verification":{"method":"totp","password":"hunter2-super-secret","totp_code":"987654","passkey_session_token":"opaque-ticket-fixture"}}`,
		`{"mode":"nonsense","verification":{"method":"password","password":"hunter2-super-secret","totp_code":"987654","passkey_session_token":"opaque-ticket-fixture"}}`,
		`{"mode":"off","verification":{"method":"password","password":"` + cvhPassword + `","totp_code":"987654","passkey_session_token":"opaque-ticket-fixture"}}`,
	}
	allowed := map[string]bool{
		"api_key_verification": true, "api_key_verification_reason": true, "api_key_auth_factor": true,
		"api_key_create_verification_from": true, "api_key_create_verification_to": true, "api_key_create_verification_relaxed": true,
	}
	for i, body := range bodies {
		rec := cvhPut(engine, body)
		raw, err := json.Marshal(engine.audit.extra())
		require.NoError(t, err)
		for _, secret := range secrets {
			require.NotContains(t, string(raw), secret, "body %d audit", i)
			require.NotContains(t, rec.Body.String(), secret, "body %d response", i)
		}
		require.NotContains(t, string(raw), cvhPassword, "body %d audit", i)
		for key := range engine.audit.extra() {
			require.True(t, allowed[key], "unexpected audit field %q", key)
		}
	}
}

// ---- 三个下发 / 变更明文的入口都遵守开关 ----------------------------------------

type cvhEntry struct {
	name   string
	method string
	path   string
	body   func(verification string) string
	ok     func(t *testing.T, e *cvhEngine)
}

func cvhEntries() []cvhEntry {
	withVerification := func(base, verification string) string {
		if verification == "" {
			return base
		}
		return strings.Replace(base, "}", `,"verification":`+verification+"}", 1)
	}
	return []cvhEntry{
		{
			name: "create", method: http.MethodPost, path: "/api/v1/keys",
			body: func(v string) string { return withVerification(`{"name":"switch-fixture"}`, v) },
			ok:   func(t *testing.T, e *cvhEngine) { require.Equal(t, 1, e.keys.guardCreateCount()) },
		},
		{
			name: "batch rotate", method: http.MethodPost, path: "/api/v1/keys/batch-rotate",
			body: func(v string) string { return withVerification(`{"ids":[1]}`, v) },
			ok:   func(t *testing.T, e *cvhEngine) { require.Equal(t, 1, e.keys.guardUpdateCount()) },
		},
		{
			name: "rotate by edit", method: http.MethodPut, path: "/api/v1/keys/1",
			body: func(v string) string { return withVerification(`{"regenerate_key":true}`, v) },
			ok:   func(t *testing.T, e *cvhEngine) { require.Equal(t, 1, e.keys.guardUpdateCount()) },
		},
	}
}

func (e *cvhEngine) untouched(t *testing.T) {
	t.Helper()
	require.Zero(t, e.keys.guardCreateCount())
	require.Zero(t, e.keys.guardUpdateCount())
}

// off：开了 2FA 的账号也不再被要求校验；审计单独记 skipped_by_user_setting。
func TestEveryPlaintextEntryHonoursTheOffSwitch(t *testing.T) {
	for _, entry := range cvhEntries() {
		t.Run(entry.name, func(t *testing.T) {
			engine := newCVHEngine(t, cvhTOTPUser(t), 0, "off", nil)
			rec := doJSON(engine.router, entry.method, entry.path, entry.body(""), nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"key":"`)
			entry.ok(t, engine)
			require.Equal(t, "skipped_by_user_setting", engine.audit.extra()["api_key_verification"])
			require.NotContains(t, engine.audit.extra(), "api_key_auth_factor")

			// 主动带了校验材料时仍然真验证：错的直接拒绝，不因为 off 就放过。
			engine = newCVHEngine(t, cvhTOTPUser(t), 0, "off", nil)
			rec = doJSON(engine.router, entry.method, entry.path,
				entry.body(`{"method":"password","password":"`+cvhWrongToken+`"}`), nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "VERIFY_PASSWORD_INVALID", cvhReason(t, rec))
			engine.untouched(t)

			// 带了对的：通过，并且记录实际用的因子。
			rec = doJSON(engine.router, entry.method, entry.path,
				entry.body(`{"method":"password","password":"`+cvhPassword+`"}`), nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, "verified", engine.audit.extra()["api_key_verification"])
			require.Equal(t, "password", engine.audit.extra()["api_key_auth_factor"])
		})
	}
}

// on：只有登录密码的账号也必须校验；没带校验 → VERIFY_REQUIRED（旧页面看到的文案不变）。
func TestEveryPlaintextEntryHonoursTheOnSwitch(t *testing.T) {
	for _, entry := range cvhEntries() {
		t.Run(entry.name, func(t *testing.T) {
			engine := newCVHEngine(t, cvhPasswordUser(t), 0, "on", nil)
			rec := doJSON(engine.router, entry.method, entry.path, entry.body(""), nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "VERIFY_REQUIRED", cvhReason(t, rec))
			// 更新前就打开、一直没刷新的旧页面只会原样显示这句：文案要双语并带强制刷新指引。
			require.Contains(t, rec.Body.String(), "Ctrl+F5")
			require.Contains(t, rec.Body.String(), "强制刷新")
			engine.untouched(t)

			rec = doJSON(engine.router, entry.method, entry.path,
				entry.body(`{"method":"password","password":"`+cvhPassword+`"}`), nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			entry.ok(t, engine)
			require.Equal(t, "verified", engine.audit.extra()["api_key_verification"])
		})
	}
}

// auto（没设置过）：与改造前完全一致——开了 2FA 强制、仅有密码不强制。
func TestEveryPlaintextEntryKeepsTheLegacyRuleWhenTheSwitchWasNeverTouched(t *testing.T) {
	for _, entry := range cvhEntries() {
		t.Run(entry.name, func(t *testing.T) {
			engine := newCVHEngine(t, cvhTOTPUser(t), 0, "", nil)
			rec := doJSON(engine.router, entry.method, entry.path, entry.body(""), nil)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Equal(t, "VERIFY_REQUIRED", cvhReason(t, rec))
			engine.untouched(t)

			engine = newCVHEngine(t, cvhPasswordUser(t), 0, "", nil)
			rec = doJSON(engine.router, entry.method, entry.path, entry.body(""), nil)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			entry.ok(t, engine)
			require.Equal(t, "skipped_no_factor", engine.audit.extra()["api_key_verification"],
				"password-only default keeps the old audit label; only an explicit off is a different one")
		})
	}
}

// 幂等重放不能借历史回执绕过校验：用户后来把开关打开，同一个 Idempotency-Key 重放就必须重新校验。
func TestCreateIdempotentReplayRechecksTheSwitchAfterItIsTurnedOn(t *testing.T) {
	journal := newUserMemoryIdempotencyRepoStub()
	engine := newCVHEngine(t, cvhPasswordUser(t), 0, "off", journal)
	headers := map[string]string{"Idempotency-Key": "cv-switch-idem-key"}
	body := `{"name":"idem-switch"}`

	first := doJSON(engine.router, http.MethodPost, "/api/v1/keys", body, headers)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, 1, engine.keys.guardCreateCount())

	rec := cvhPut(engine, `{"mode":"on"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	replay := doJSON(engine.router, http.MethodPost, "/api/v1/keys", body, headers)
	require.Equal(t, http.StatusBadRequest, replay.Code, replay.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", cvhReason(t, replay))
	require.Equal(t, 1, engine.keys.guardCreateCount(), "the replay must not create or return a second key")
	require.NotContains(t, replay.Body.String(), `"key":"sk-`)
}
