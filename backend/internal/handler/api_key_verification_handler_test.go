package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---- 测试替身 -------------------------------------------------------------

type handlerVerifyUserRepo struct {
	service.UserRepository
	user *service.User
	// identities：第三方注册账号判「是否设置过本地密码」时查的认证身份（默认没有）。
	identities []service.UserAuthIdentityRecord
}

func (r *handlerVerifyUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, service.ErrUserNotFound
	}
	return r.user, nil
}

func (r *handlerVerifyUserRepo) ListUserAuthIdentities(context.Context, int64) ([]service.UserAuthIdentityRecord, error) {
	return r.identities, nil
}

// handlerVerifyStore 同时实现 TotpCache（嵌入占位）与 APIKeyVerificationStore。
type handlerVerifyStore struct {
	service.TotpCache

	mu      sync.Mutex
	grants  map[string]*service.APIKeyRevealGrantRecord
	tickets map[string]*service.APIKeyPasskeyVerificationRecord
	// usedCodes 复刻 Redis SETNX 的 TOTP 重放标记（按用户 + 摘要）。
	usedCodes map[string]bool
}

func newHandlerVerifyStore() *handlerVerifyStore {
	return &handlerVerifyStore{
		grants:    map[string]*service.APIKeyRevealGrantRecord{},
		tickets:   map[string]*service.APIKeyPasskeyVerificationRecord{},
		usedCodes: map[string]bool{},
	}
}

// TotpService 校验动态码用到的缓存方法：失败计数用不到（始终未超限），重放标记按 SETNX 语义。
func (s *handlerVerifyStore) GetVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }

func (s *handlerVerifyStore) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}

func (s *handlerVerifyStore) ClearVerifyAttempts(context.Context, int64) error { return nil }

func (s *handlerVerifyStore) MarkLoginCodeUsed(_ context.Context, userID int64, digest string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strconv.FormatInt(userID, 10) + ":" + digest
	if s.usedCodes[key] {
		return false, nil
	}
	s.usedCodes[key] = true
	return true, nil
}

func (s *handlerVerifyStore) SaveAPIKeyRevealGrant(_ context.Context, digest string, record *service.APIKeyRevealGrantRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *record
	s.grants[digest] = &copied
	return nil
}

func (s *handlerVerifyStore) ConsumeAPIKeyRevealGrant(_ context.Context, digest string) (*service.APIKeyRevealGrantRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.grants[digest]
	if !ok {
		return nil, nil
	}
	delete(s.grants, digest)
	copied := *record
	return &copied, nil
}

func (s *handlerVerifyStore) SaveAPIKeyPasskeyTicket(_ context.Context, digest string, record *service.APIKeyPasskeyVerificationRecord, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := *record
	s.tickets[digest] = &copied
	return nil
}

func (s *handlerVerifyStore) ConsumeAPIKeyPasskeyTicket(_ context.Context, digest string) (*service.APIKeyPasskeyVerificationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.tickets[digest]
	if !ok {
		return nil, nil
	}
	delete(s.tickets, digest)
	copied := *record
	return &copied, nil
}

// ---- 装配 -----------------------------------------------------------------

type handlerVerificationEngine struct {
	router      *gin.Engine
	handler     *APIKeyHandler
	createRepo  *apiKeyCreateReceiptRepo
	secretStore *handlerSecretStore
	store       *handlerVerifyStore
	user        *service.User
}

func newHandlerVerificationEngine(t *testing.T, user *service.User) *handlerVerificationEngine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	keys := &handlerSecretRepo{keys: map[int64]*service.APIKey{
		1: {ID: 1, UserID: 7, KeyPrefix: "sk-owner1", Status: service.StatusAPIKeyActive},
		9: {ID: 9, UserID: 8, KeyPrefix: "sk-other", Status: service.StatusAPIKeyActive},
	}}
	secretStore := newHandlerSecretStore()
	createRepo := &apiKeyCreateReceiptRepo{}

	apiKeyService := service.NewAPIKeyService(createRepo, availableGroupsUserRepoStub{}, nil, nil, nil, nil, &config.Config{})
	secretService := service.NewAPIKeySecretService(keys, secretStore)
	verifyStore := newHandlerVerifyStore()
	verificationService := service.NewAPIKeyVerificationService(
		&handlerVerifyUserRepo{user: user}, keys, secretStore, nil, nil, verifyStore)

	h := NewAPIKeyHandler(apiKeyService, secretService)
	h.SetVerificationService(verificationService)

	router := gin.New()
	r := router.Group("/api/v1")
	r.Use(withUserSubject(7))
	r.GET("/user/security/verification-factors", h.GetVerificationFactors)
	r.POST("/keys", h.Create)
	r.PUT("/keys/:id", h.Update)
	r.POST("/keys/batch-rotate", h.BatchRotate)
	r.PUT("/user/api-keys/:id/secret-seal", middleware2.RequestBodyLimit(service.APIKeySecretMaxBodyBytes), h.PutSecretSeal)
	r.GET("/user/api-keys/:id/secret-seal", h.GetSecretSeal)
	r.POST("/user/api-keys/:id/reveal-authorize", h.AuthorizeReveal)
	return &handlerVerificationEngine{
		router:      router,
		handler:     h,
		createRepo:  createRepo,
		secretStore: secretStore,
		store:       verifyStore,
		user:        user,
	}
}

func handlerVerificationUser(t *testing.T) *service.User {
	t.Helper()
	user := &service.User{ID: 7, Email: "user@example.com", Status: service.StatusActive, SignupSource: "email"}
	require.NoError(t, user.SetPassword("correct-password"))
	return user
}

// handlerVerificationTOTPUser 是开了 TOTP 的邮箱账号：创建 Key 只对开了 TOTP 或绑了 Passkey 的账号强制校验。
func handlerVerificationTOTPUser(t *testing.T) *service.User {
	t.Helper()
	user := handlerVerificationUser(t)
	user.TotpEnabled = true
	return user
}

func handlerVerificationErrorReason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Reason
}

func handlerVerificationSealBody(t *testing.T, password, authFactor string) string {
	t.Helper()
	kdf, err := json.Marshal(map[string]any{
		"algo":       service.APIKeySecretKDFAlgoPBKDF2SHA256,
		"iterations": 310000,
		"salt":       "MDEyMzQ1Njc4OWFiY2RlZg==",
	})
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"version":     1,
		"kdf":         json.RawMessage(kdf),
		"sealed":      "c2VhbGVkLXNlY3JldC1maXh0dXJlLTAwMDAwMDAwMDA=",
		"auth_factor": authFactor,
		"password":    password,
	})
	require.NoError(t, err)
	return string(body)
}

func doJSON(router *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// ---- 用例 -----------------------------------------------------------------

func TestVerificationFactorsEndpoint(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	rec := doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"password_available":true`)
	require.Contains(t, rec.Body.String(), `"preferred":"password"`)
	require.Contains(t, rec.Body.String(), `"seal_available":true`)
	// 只有登录密码：密码仍然可选（二次查看要用），但创建 Key 不强制校验。
	require.Contains(t, rec.Body.String(), `"create_verification_required":false`)

	// 开了 TOTP：创建 Key 必须先校验，前端据此决定要不要显示校验这一步。
	engine = newHandlerVerificationEngine(t, handlerVerificationTOTPUser(t))
	rec = doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"create_verification_required":true`)

	// OAuth-only：密码不可用，也不能封存。
	oauth := handlerVerificationUser(t)
	oauth.SignupSource = "github"
	engine = newHandlerVerificationEngine(t, oauth)
	rec = doJSON(engine.router, http.MethodGet, "/api/v1/user/security/verification-factors", "", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"password_available":false`)
	require.Contains(t, rec.Body.String(), `"seal_available":false`)
	require.Contains(t, rec.Body.String(), `"available_methods":[]`)
	require.Contains(t, rec.Body.String(), `"create_verification_required":false`)
}

func TestCreateKeyRequiresIdentityVerification(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationTOTPUser(t))

	// 1) 没有 verification：400 VERIFY_REQUIRED，且不会创建 Key。
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"no-verify"}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", handlerVerificationErrorReason(t, rec))
	require.Zero(t, engine.createRepo.calls, "identity verification failure must not create a key")

	// 2) 密码错误：400 VERIFY_PASSWORD_INVALID，同样不允许创建。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"bad-password","verification":{"method":"password","password":"wrong-password"}}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", handlerVerificationErrorReason(t, rec))
	require.Zero(t, engine.createRepo.calls, "wrong password must not create a key")

	// 3) 未绑定的因子：400 VERIFY_FACTOR_UNAVAILABLE。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"totp","verification":{"method":"totp","totp_code":"123456"}}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_FACTOR_UNAVAILABLE", handlerVerificationErrorReason(t, rec))
	require.Zero(t, engine.createRepo.calls)

	// 4) 密码正确：创建成功，明文只回这一次。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"verified","verification":{"method":"password","password":"correct-password"}}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, engine.createRepo.calls)
	require.Contains(t, rec.Body.String(), `"key":"sk-`)
}

func TestCreateKeyOAuthOnlySkipsVerificationButRejectsPassword(t *testing.T) {
	oauth := handlerVerificationUser(t)
	oauth.SignupSource = "github"
	engine := newHandlerVerificationEngine(t, oauth)

	// 一个可用因子都没有：允许创建（只能一次性显示明文）。
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"one-time"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, engine.createRepo.calls)

	// 显式选择密码：账号不知道密码（OAuth 随机口令），服务端拒绝。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys",
		`{"name":"oauth-password","verification":{"method":"password","password":"random"}}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_FACTOR_UNAVAILABLE", handlerVerificationErrorReason(t, rec))
	require.Equal(t, 1, engine.createRepo.calls)
}

func TestSecretSealRequiresVerifiedLoginPassword(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))

	// 错误密码封存：400，且不落库（修复「创建成功、以后解不开」的根因）。
	rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "wrong-password", "password"), nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", handlerVerificationErrorReason(t, rec))
	require.NotContains(t, engine.secretStore.rows, int64(1))

	// 正确密码封存：记录授权因子（二次查看要求同一因子）。
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "correct-password", "password"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, engine.secretStore.rows, int64(1))
	require.Equal(t, service.APIKeyFactorPassword, engine.secretStore.rows[1].AuthFactor)
}

func TestRevealAuthorizeGrantGateOnSecretSeal(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	rec := doJSON(engine.router, http.MethodPut, "/api/v1/user/api-keys/1/secret-seal",
		handlerVerificationSealBody(t, "correct-password", "password"), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 没有 reveal grant 不能取密文。
	rec = doJSON(engine.router, http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", "", nil)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, "REVEAL_GRANT_REQUIRED", handlerVerificationErrorReason(t, rec))

	// 身份校验通过 → 5 分钟一次性 grant。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize",
		`{"method":"password","password":"correct-password"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var grantResp struct {
		Data struct {
			Grant     string `json:"grant"`
			Factor    string `json:"factor"`
			ExpiresIn int64  `json:"expires_in"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &grantResp))
	require.NotEmpty(t, grantResp.Data.Grant)
	require.Equal(t, service.APIKeyFactorPassword, grantResp.Data.Factor)
	require.Equal(t, int64(300), grantResp.Data.ExpiresIn)

	headers := map[string]string{"X-Reveal-Grant": grantResp.Data.Grant}
	rec = doJSON(engine.router, http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", "", headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"sealed"`)

	// grant 一次性：同一 grant 第二次取密文被拒绝。
	rec = doJSON(engine.router, http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", "", headers)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, "REVEAL_GRANT_INVALID", handlerVerificationErrorReason(t, rec))

	// 别人的 Key：reveal-authorize 直接 404，不暴露存在性。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/user/api-keys/9/reveal-authorize",
		`{"method":"password","password":"correct-password"}`, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestRevealRequiresSameFactorAsSealed(t *testing.T) {
	engine := newHandlerVerificationEngine(t, handlerVerificationUser(t))
	engine.secretStore.rows[1] = &service.APIKeySecret{
		APIKeyID:   1,
		UserID:     7,
		Version:    1,
		KDF:        json.RawMessage(`{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"MDEyMzQ1Njc4OWFiY2RlZg=="}`),
		Sealed:     "c2VhbGVk",
		AuthFactor: service.APIKeyFactorTOTP,
	}
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize",
		`{"method":"password","password":"correct-password"}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_FACTOR_MISMATCH", handlerVerificationErrorReason(t, rec))

	// 旧密文（无 auth_factor）不限制因子。
	engine.secretStore.rows[1].AuthFactor = ""
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize",
		`{"method":"password","password":"correct-password"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestVerificationAuditCarriesFactorWithoutSecrets(t *testing.T) {
	// 审计只记录因子与结果：handler 写审计走 SetAuditExtra，其白名单在
	// middleware.auditExtraAllowedKeys；这里直接验证 handler 附加的字段集合。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/keys", bytes.NewReader(nil))
	setVerificationAudit(c, &service.APIKeyVerificationResult{Factor: service.APIKeyFactorPassword}, auditVerificationVerified, "")
	value, ok := c.Get("audit_extra")
	require.True(t, ok)
	fields, ok := value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, auditVerificationVerified, fields["api_key_verification"])
	require.Equal(t, service.APIKeyFactorPassword, fields["api_key_auth_factor"])
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "correct-password")
}
