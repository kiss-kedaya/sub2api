package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// 创建 Key / 单条轮换 / 批量轮换（无限画布的「创建并进入」「轮换并进入」走的也是这三个接口）
// 都会下发明文，后端按账号实际绑定的因子要求身份校验：
//
//	有 TOTP    → 动态码，且一个码只能用一次
//	有 Passkey → 一次性票据，同样只能用一次
//	没有因子   → 放行（只能一次性显示明文）
//
// 这里用真实的 handler + 校验服务把三类账号各走一遍：创建、轮换、批量轮换都成立，重放被拒。

const accountTypeTOTPSecret = "JBSWY3DPEHPK3PXP"

type accountTypeTOTPEncryptor struct{}

func (accountTypeTOTPEncryptor) Encrypt(plaintext string) (string, error)  { return plaintext, nil }
func (accountTypeTOTPEncryptor) Decrypt(ciphertext string) (string, error) { return ciphertext, nil }

// accountTypePasskeyRepo 只回答「这个账号绑了几枚 Passkey」。
type accountTypePasskeyRepo struct {
	service.PasskeyRepository
	count int
}

func (r accountTypePasskeyRepo) ListByUserID(context.Context, int64) ([]service.PasskeyCredentialRecord, error) {
	records := make([]service.PasskeyCredentialRecord, 0, r.count)
	for i := 0; i < r.count; i++ {
		records = append(records, service.PasskeyCredentialRecord{ID: int64(i + 1), UserID: 7, Name: "fixture-passkey"})
	}
	return records, nil
}

type accountTypePasskeySessions struct{ service.PasskeySessionStore }

type accountTypeEngine struct {
	router *gin.Engine
	keys   *plaintextGuardKeyRepo
	store  *handlerVerifyStore
}

func newAccountTypeEngine(t *testing.T, user *service.User, passkeyCount int, idempotencyRepo service.IdempotencyRepository, identities ...service.UserAuthIdentityRecord) *accountTypeEngine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	keys := newPlaintextGuardKeyRepo()
	apiKeyService := service.NewAPIKeyService(keys, availableGroupsUserRepoStub{}, nil, nil, nil, nil, &config.Config{})
	store := newHandlerVerifyStore()
	users := &handlerVerifyUserRepo{user: user, identities: identities}
	totpService := service.NewTotpService(users, accountTypeTOTPEncryptor{}, store, nil, nil, nil)
	passkeys, err := service.NewPasskeyService(&config.Config{WebAuthn: config.WebAuthnConfig{
		Enabled:       true,
		RPDisplayName: "Sub2API",
		RPID:          "sub2api.example.com",
		RPOrigins:     []string{"https://sub2api.example.com"},
	}}, accountTypePasskeyRepo{count: passkeyCount}, accountTypePasskeySessions{}, users)
	require.NoError(t, err)
	verification := service.NewAPIKeyVerificationService(users, keys, nil, totpService, passkeys, store)
	h := NewAPIKeyHandler(apiKeyService, nil)
	h.SetVerificationService(verification)

	if idempotencyRepo != nil {
		previous := service.DefaultIdempotencyCoordinator()
		service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(idempotencyRepo, service.DefaultIdempotencyConfig()))
		t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	}

	router := gin.New()
	r := router.Group("/api/v1")
	r.Use(withUserSubject(7))
	r.GET("/user/security/verification-factors", h.GetVerificationFactors)
	r.POST("/keys", h.Create)
	r.PUT("/keys/:id", h.Update)
	r.POST("/keys/batch-rotate", h.BatchRotate)
	return &accountTypeEngine{router: router, keys: keys, store: store}
}

// accountTypeUser 默认是第三方登录注册的账号（登录密码不可用），这样 TOTP / Passkey 是它唯一的因子。
func accountTypeUser(t *testing.T, withTOTP bool) *service.User {
	t.Helper()
	user := &service.User{ID: 7, Email: "owner@example.com", Status: service.StatusActive, SignupSource: "github"}
	require.NoError(t, user.SetPassword("random-generated-secret"))
	if withTOTP {
		secret := accountTypeTOTPSecret
		user.TotpEnabled = true
		user.TotpSecretEncrypted = &secret
	}
	return user
}

// totpCodeAtStep 生成「距现在 offset 个 30 秒步」的码；三个相邻步的码互不相同才有意义（相同的概率是百万分之一）。
func totpCodesForThreeSteps(t *testing.T) (previous, current, next string) {
	t.Helper()
	gen := func(offset time.Duration) string {
		code, err := totp.GenerateCode(accountTypeTOTPSecret, time.Now().Add(offset))
		require.NoError(t, err)
		return code
	}
	previous, current, next = gen(-30*time.Second), gen(0), gen(30*time.Second)
	if previous == current || current == next || previous == next {
		t.Skip("相邻时间步恰好生成了相同的码（百万分之几），无法区分")
	}
	return previous, current, next
}

func totpBody(prefix, code string) string {
	return `{` + prefix + `"verification":{"method":"totp","totp_code":"` + code + `"}}`
}

func (e *accountTypeEngine) mintPasskeyTicket(t *testing.T, token string) {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	require.NoError(t, e.store.SaveAPIKeyPasskeyTicket(t.Context(), hex.EncodeToString(sum[:]), &service.APIKeyPasskeyVerificationRecord{
		UserID:    7,
		Factor:    service.APIKeyFactorPasskey,
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
	}, 5*time.Minute))
}

func passkeyBody(prefix, token string) string {
	return `{` + prefix + `"verification":{"method":"passkey","passkey_session_token":"` + token + `"}}`
}

func TestKeyCreateAndRotateForATOTPAccount(t *testing.T) {
	engine := newAccountTypeEngine(t, accountTypeUser(t, true), 0, nil)
	previous, current, next := totpCodesForThreeSteps(t)

	rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Contains(t, rec.Body.String(), `"available_methods":["totp"]`)
	require.Contains(t, rec.Body.String(), `"password_available":false`)

	// 没带校验：创建 / 轮换 / 批量轮换一律 VERIFY_REQUIRED，一把都不动。
	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/keys", `{"name":"canvas"}`},
		{http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`},
		{http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`},
	} {
		rec = doJSON(engine.router, call.method, call.path, call.body, nil)
		require.Equal(t, http.StatusBadRequest, rec.Code, call.path)
		require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec), call.path)
	}
	// 该账号的登录密码不可用：显式选密码被拒，不会因为「没有因子」被放行。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"canvas","verification":{"method":"password","password":"random-generated-secret"}}`, nil)
	require.Equal(t, "VERIFY_FACTOR_UNAVAILABLE", plaintextGuardReason(t, rec))
	require.Zero(t, engine.keys.guardCreateCount())
	require.Zero(t, engine.keys.guardUpdateCount())

	// 创建：带正确的动态码成功，明文只回这一次。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", totpBody(`"name":"canvas",`, current), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	require.Equal(t, 1, engine.keys.guardCreateCount())

	// 同一个码再用一次（重放）：拒绝，且没有第二把 Key。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", totpBody(`"name":"canvas-again",`, current), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "VERIFY_TOTP_INVALID", plaintextGuardReason(t, rec))
	require.Equal(t, 1, engine.keys.guardCreateCount())

	// 轮换：换一个新码成功；同一个码重放被拒，旧 Key 不会被再换一次。
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", totpBody(`"regenerate_key":true,`, next), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	require.Equal(t, 1, engine.keys.guardUpdateCount())
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", totpBody(`"regenerate_key":true,`, next), nil)
	require.Equal(t, "VERIFY_TOTP_INVALID", plaintextGuardReason(t, rec))
	require.Equal(t, 1, engine.keys.guardUpdateCount())

	// 批量轮换同理：再用一个没用过的码。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", totpBody(`"ids":[1],`, previous), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
	require.Equal(t, 2, engine.keys.guardUpdateCount())
}

func TestKeyCreateAndRotateForAPasskeyAccount(t *testing.T) {
	engine := newAccountTypeEngine(t, accountTypeUser(t, false), 1, nil)

	rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Contains(t, rec.Body.String(), `"available_methods":["passkey"]`)

	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"canvas"}`, nil)
	require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec))
	// 没有这张票据（或不是这个账号签发的）：拒绝。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", passkeyBody(`"name":"canvas",`, "never-issued"), nil)
	require.Equal(t, "VERIFY_PASSKEY_FAILED", plaintextGuardReason(t, rec))
	require.Zero(t, engine.keys.guardCreateCount())

	// 创建：Passkey 校验换来的一次性票据。
	engine.mintPasskeyTicket(t, "ticket-create")
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", passkeyBody(`"name":"canvas",`, "ticket-create"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	// 票据只能用一次。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", passkeyBody(`"name":"canvas-again",`, "ticket-create"), nil)
	require.Equal(t, "VERIFY_PASSKEY_FAILED", plaintextGuardReason(t, rec))
	require.Equal(t, 1, engine.keys.guardCreateCount())

	// 轮换与批量轮换各用各的票据。
	engine.mintPasskeyTicket(t, "ticket-rotate")
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", passkeyBody(`"regenerate_key":true,`, "ticket-rotate"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	engine.mintPasskeyTicket(t, "ticket-batch")
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", passkeyBody(`"ids":[1],`, "ticket-batch"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
	require.Equal(t, 2, engine.keys.guardUpdateCount())
}

func TestKeyCreateAndRotateForAnAccountWithoutAnyFactor(t *testing.T) {
	engine := newAccountTypeEngine(t, accountTypeUser(t, false), 0, nil)

	rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Contains(t, rec.Body.String(), `"available_methods":[]`)

	// 没有任何因子：显式放行，三个入口都照常工作（只能一次性显示明文）。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"canvas"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, engine.keys.guardCreateCount())
	require.Equal(t, 2, engine.keys.guardUpdateCount())

	// 但显式选一个这个账号没有的因子，依然被拒（不会因为「没有任何因子」被放行）。
	_, current, _ := totpCodesForThreeSteps(t)
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", totpBody(`"name":"canvas",`, current), nil)
	require.Equal(t, "VERIFY_FACTOR_UNAVAILABLE", plaintextGuardReason(t, rec))
}

// 审计 F16：第三方登录注册、之后在资料页绑定邮箱并设置了本地密码的账号，原先因为「按注册来源判断」
// 永远没有密码因子。现在按「是否真的设置过本地密码」判断：这类账号创建 / 轮换要用密码校验；
// 而只有系统随机口令的第三方账号（身份表里只有创建 / 登录时写的邮箱身份）仍然没有因子、照常放行。
func TestKeyCreateForAThirdPartyAccountThatSetALocalPassword(t *testing.T) {
	bound := service.UserAuthIdentityRecord{ProviderType: "email", ProviderKey: "email", Metadata: map[string]any{"source": "auth_service_email_bind"}}
	created := service.UserAuthIdentityRecord{ProviderType: "email", ProviderKey: "email", Metadata: map[string]any{"source": "user_repo_create"}}

	t.Run("bound an email with a password", func(t *testing.T) {
		user := accountTypeUser(t, false)
		require.NoError(t, user.SetPassword("chosen-password"))
		engine := newAccountTypeEngine(t, user, 0, nil, created, bound)

		rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
		require.Contains(t, rec.Body.String(), `"password_available":true`)
		require.Contains(t, rec.Body.String(), `"seal_available":true`)
		require.Contains(t, rec.Body.String(), `"available_methods":["password"]`)

		// 现在有了密码因子：创建 / 轮换不带校验被拒，带对密码才通过。
		rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"canvas"}`, nil)
		require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec))
		rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`, nil)
		require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec))
		rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
			`{"name":"canvas","verification":{"method":"password","password":"wrong-password"}}`, nil)
		require.Equal(t, "VERIFY_PASSWORD_INVALID", plaintextGuardReason(t, rec))
		require.Zero(t, engine.keys.guardCreateCount())
		rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
			`{"name":"canvas","verification":{"method":"password","password":"chosen-password"}}`, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	})

	t.Run("only the system-generated password", func(t *testing.T) {
		engine := newAccountTypeEngine(t, accountTypeUser(t, false), 0, nil, created)

		rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
		require.Contains(t, rec.Body.String(), `"password_available":false`)
		require.Contains(t, rec.Body.String(), `"available_methods":[]`)
		rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"canvas"}`, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.NotEmpty(t, plaintextGuardBodyKey(t, rec))
	})
}

// 创建走幂等：重放同一个 Idempotency-Key 必须重新校验。TOTP 账号的码只能用一次，
// 所以重放要换一个新码；用旧码重放被拒，且不会多建一把 Key、也拿不到回执。
func TestCreateIdempotentReplayForATOTPAccountNeedsAFreshCode(t *testing.T) {
	engine := newAccountTypeEngine(t, accountTypeUser(t, true), 0, newUserMemoryIdempotencyRepoStub())
	_, current, next := totpCodesForThreeSteps(t)
	call := func(code string) *httptest.ResponseRecorder {
		return doJSON(engine.router, http.MethodPost, "/api/v1/keys", totpBody(`"name":"idem-totp",`, code),
			map[string]string{"Idempotency-Key": "synthetic-idem-totp"})
	}

	first := call(current)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.NotEmpty(t, plaintextGuardBodyKey(t, first))

	// 网络抖动后原样重试：码已经被第一次用掉了。
	stale := call(current)
	require.Equal(t, http.StatusBadRequest, stale.Code, stale.Body.String())
	require.Equal(t, "VERIFY_TOTP_INVALID", plaintextGuardReason(t, stale))
	require.NotContains(t, stale.Body.String(), `"key":"sk-`)

	// 换新码重放：通过校验，拿到的是脱敏回执（不是新明文），也没有多建 Key。
	replay := call(next)
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("X-Idempotency-Replayed"))
	require.NotContains(t, plaintextGuardBodyKey(t, replay), "sk-")
	require.Equal(t, 1, engine.keys.guardCreateCount())
}
