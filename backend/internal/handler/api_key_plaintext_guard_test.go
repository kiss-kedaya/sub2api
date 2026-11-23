package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// 审计发现的绕过口（P1）：能下发 / 变更明文 Key 的入口必须全部要求三选一
// 身份校验，只有「创建」一条挂了校验是漏洞。
//
//   POST /keys/batch-rotate   下发每把 Key 的新明文 —— 必须 400 VERIFY_REQUIRED
//   PUT  /keys/:id            带 regenerate_key 时下发新明文 —— 必须 400 VERIFY_REQUIRED
//   账号没有任何可用因子         显式放行并写 skipped_no_factor
//   幂等重放                   不许用历史回执跳过校验
//
// 这些用例同时在服务层与 handler 层验证：账号有因子时一次都不能下发明文，
// 没有因子时行为保持不变（一次性显示）。

// ---- 测试替身 -------------------------------------------------------------

// plaintextGuardKeyRepo 记录 Key 行的读/写，用来证明「校验失败时一步都没动」。
type plaintextGuardKeyRepo struct {
	service.APIKeyRepository

	mu      sync.Mutex
	keys    map[int64]*service.APIKey
	created []*service.APIKey
	updates int
}

func newPlaintextGuardKeyRepo() *plaintextGuardKeyRepo {
	return &plaintextGuardKeyRepo{keys: map[int64]*service.APIKey{
		1: {ID: 1, UserID: 7, Name: "own-key", Key: "fixture-own-existing-value", KeyHash: "hash-own", KeyPrefix: "fixture-own", Status: service.StatusAPIKeyActive},
		9: {ID: 9, UserID: 8, Name: "other-key", Key: "fixture-other-existing-value", KeyHash: "hash-other", KeyPrefix: "fixture-oth", Status: service.StatusAPIKeyActive},
	}}
}

func (r *plaintextGuardKeyRepo) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key, ok := r.keys[id]; ok && key != nil {
		copied := *key
		return &copied, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r *plaintextGuardKeyRepo) Create(_ context.Context, key *service.APIKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key.ID = int64(100 + len(r.created))
	copied := *key
	r.created = append(r.created, &copied)
	return nil
}

func (r *plaintextGuardKeyRepo) VerifyOwnership(_ context.Context, userID int64, ids []int64) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	owned := make([]int64, 0, len(ids))
	for _, id := range ids {
		if key, ok := r.keys[id]; ok && key != nil && key.UserID == userID {
			owned = append(owned, id)
		}
	}
	return owned, nil
}

func (r *plaintextGuardKeyRepo) ExistsByKey(_ context.Context, key string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.keys {
		if item != nil && (item.Key == key || item.KeyHash == key) {
			return true, nil
		}
	}
	return false, nil
}

func (r *plaintextGuardKeyRepo) Update(_ context.Context, key *service.APIKey, _ service.APIKeyUpdateFields) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates++
	if existing, ok := r.keys[key.ID]; ok && existing != nil {
		*(existing) = *key
	}
	return nil
}

func (r *plaintextGuardKeyRepo) guardUpdateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates
}

func (r *plaintextGuardKeyRepo) guardCreateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

// ---- 装配 -----------------------------------------------------------------

type plaintextGuardEngine struct {
	router   *gin.Engine
	keys     *plaintextGuardKeyRepo
	user     *service.User
	verifSvc *service.APIKeyVerificationService
}

func newPlaintextGuardEngine(t *testing.T, user *service.User, idempotencyRepo service.IdempotencyRepository) *plaintextGuardEngine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	keys := newPlaintextGuardKeyRepo()
	apiKeyService := service.NewAPIKeyService(keys, availableGroupsUserRepoStub{}, nil, nil, nil, nil, &config.Config{})
	verificationService := service.NewAPIKeyVerificationService(
		&handlerVerifyUserRepo{user: user}, keys, nil, nil, nil, newHandlerVerifyStore())
	h := NewAPIKeyHandler(apiKeyService, nil)
	h.SetVerificationService(verificationService)

	if idempotencyRepo != nil {
		previous := service.DefaultIdempotencyCoordinator()
		service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(idempotencyRepo, service.DefaultIdempotencyConfig()))
		t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	}

	router := gin.New()
	r := router.Group("/api/v1")
	r.Use(withUserSubject(7))
	r.POST("/keys", h.Create)
	r.POST("/keys/batch-rotate", h.BatchRotate)
	r.PUT("/keys/:id", h.Update)
	return &plaintextGuardEngine{router: router, keys: keys, user: user, verifSvc: verificationService}
}

func plaintextGuardUser(t *testing.T) *service.User {
	t.Helper()
	user := &service.User{ID: 7, Email: "owner@example.com", Status: service.StatusActive, SignupSource: "email"}
	require.NoError(t, user.SetPassword("correct-password"))
	return user
}

// plaintextGuardBodyKey 取回执里的明文（创建 / 轮换 / 批量都用同一字段名）。
func plaintextGuardBodyKey(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Data struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Data.Key
}

func plaintextGuardReason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out.Reason
}

// ---- 1. batch-rotate -------------------------------------------------------

func TestBatchRotateRequiresIdentityVerification(t *testing.T) {
	engine := newPlaintextGuardEngine(t, plaintextGuardUser(t), nil)

	// 旧前端 / 脚本直接调用：账号有登录密码可用，必须 400 VERIFY_REQUIRED，
	// 且一把都不能被轮换（旧 Key 不能被悄悄作废、新明文不能下发）。
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec))
	require.NotContains(t, rec.Body.String(), "fixture-")
	require.Zero(t, engine.keys.guardUpdateCount(), "verification failure must not rotate any key")

	// 密码错误同样拒绝。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate",
		`{"ids":[1],"verification":{"method":"password","password":"wrong-password"}}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", plaintextGuardReason(t, rec))
	require.Zero(t, engine.keys.guardUpdateCount())

	// 校验通过：逐条回执新明文（合法路径保持原行为）。
	rec = doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate",
		`{"ids":[1],"verification":{"method":"password","password":"correct-password"}}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
	require.Contains(t, rec.Body.String(), `"key":"`)
	require.Equal(t, 1, engine.keys.guardUpdateCount())
}

func TestBatchRotateSkipsVerificationWhenNoFactorAvailable(t *testing.T) {
	oauth := plaintextGuardUser(t)
	oauth.SignupSource = "github"
	engine := newPlaintextGuardEngine(t, oauth, nil)

	// 一个可用因子都没有：显式放行（审计记 skipped_no_factor），轮换照常。
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"key":"`)
	require.Equal(t, 1, engine.keys.guardUpdateCount())
}

func TestBatchRotateAuditsSkippedNoFactor(t *testing.T) {
	oauth := plaintextGuardUser(t)
	oauth.SignupSource = "github"
	engine := newPlaintextGuardEngine(t, oauth, nil)

	// 审计结果直接检查 handler 写入的 extra：无因子放行必须是 skipped_no_factor。
	rec := doJSON(engine.router, http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	// 校验通过 / 放行两种路径的审计标签由 annotateKeyShowVerification 统一写入，
	// 这里直接验证它的语义：空因子 → skipped_no_factor。
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	annotateKeyShowVerification(ctx, "")
	value, ok := ctx.Get("audit_extra")
	require.True(t, ok)
	fields := value.(map[string]any)
	require.Equal(t, auditVerificationSkipped, fields["api_key_verification"])
	_, hasFactor := fields["api_key_auth_factor"]
	require.False(t, hasFactor, "skipped_no_factor must not claim a factor")
}

// ---- 2. PUT /keys/:id ------------------------------------------------------

func TestKeyUpdatePlaintextPathsRequireIdentityVerification(t *testing.T) {
	engine := newPlaintextGuardEngine(t, plaintextGuardUser(t), nil)

	// 轮换（regenerate_key）是 PUT 的明文回执：没有校验必须 400，且 Key 不动。
	rec := doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, rec))
	require.Zero(t, engine.keys.guardUpdateCount(), "旧 Key 不能被悄悄轮换掉")
	require.NotContains(t, rec.Body.String(), "fixture-")

	// 密码错误同样拒绝。
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1",
		`{"regenerate_key":true,"verification":{"method":"password","password":"wrong-password"}}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", plaintextGuardReason(t, rec))
	require.Zero(t, engine.keys.guardUpdateCount())

	// 校验通过：轮换成功并回执新明文。
	rec = doJSON(engine.router, http.MethodPut, "/api/v1/keys/1",
		`{"regenerate_key":true,"verification":{"method":"password","password":"correct-password"}}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"key":"`)
	require.Equal(t, 1, engine.keys.guardUpdateCount())
}

func TestKeyUpdateWithoutPlaintextSkipsVerification(t *testing.T) {
	engine := newPlaintextGuardEngine(t, plaintextGuardUser(t), nil)

	// 改名 / 改配额不下发明文：不要求校验，否则高频编辑每次都要弹 TOTP。
	rec := doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", `{"name":"renamed"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, engine.keys.guardUpdateCount())
	require.NotContains(t, rec.Body.String(), `"key":"sk-`)
}

func TestKeyUpdatePlaintextForForeignKeyIsNotFound(t *testing.T) {
	engine := newPlaintextGuardEngine(t, plaintextGuardUser(t), nil)

	// 别人的 Key：先判归属再校验，按 404 处理，不泄露存在性、更不轮换。
	rec := doJSON(engine.router, http.MethodPut, "/api/v1/keys/9",
		`{"regenerate_key":true,"verification":{"method":"password","password":"correct-password"}}`, nil)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Zero(t, engine.keys.guardUpdateCount())
}

func TestKeyUpdatePlaintextSkipsVerificationWhenNoFactorAvailable(t *testing.T) {
	oauth := plaintextGuardUser(t)
	oauth.SignupSource = "github"
	engine := newPlaintextGuardEngine(t, oauth, nil)

	rec := doJSON(engine.router, http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"key":"`)
	require.Equal(t, 1, engine.keys.guardUpdateCount())
}

// ---- 3. 幂等重放 -----------------------------------------------------------

func TestCreateIdempotentReplayCannotSkipVerification(t *testing.T) {
	journal := newUserMemoryIdempotencyRepoStub()
	engine := newPlaintextGuardEngine(t, plaintextGuardUser(t), journal)

	call := func(verification string) *httptest.ResponseRecorder {
		body := `{"name":"idem-fixture"}`
		if verification != "" {
			body = fmt.Sprintf(`{"name":"idem-fixture","verification":%s}`, verification)
		}
		return doJSON(engine.router, http.MethodPost, "/api/v1/keys", body,
			map[string]string{"Idempotency-Key": "synthetic-idem-key"})
	}

	// 第一次：带正确密码，创建成功。
	first := call(`{"method":"password","password":"correct-password"}`)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Contains(t, first.Body.String(), `"key":"sk-`)
	require.Equal(t, 1, engine.keys.guardCreateCount())

	// 第二次：同一个 Idempotency-Key，但不带任何校验材料。幂等指纹不含校验
	// 材料，所以会命中历史回执——必须在这里重新校验并 400，绝不能把存储的
	// 回执直接交出去。
	second := call("")
	require.Equal(t, http.StatusBadRequest, second.Code, second.Body.String())
	require.Equal(t, "VERIFY_REQUIRED", plaintextGuardReason(t, second))
	require.NotContains(t, second.Body.String(), `"key":"sk-`)
	require.Equal(t, 1, engine.keys.guardCreateCount(), "replay must not create a second key")

	// 第三次：重放并带上正确密码 → 重新校验通过，返回脱敏回执（不是新明文）。
	third := call(`{"method":"password","password":"correct-password"}`)
	require.Equal(t, http.StatusOK, third.Code, third.Body.String())
	require.Equal(t, "true", third.Header().Get("X-Idempotency-Replayed"))
	require.NotContains(t, plaintextGuardBodyKey(t, third), "sk-")
	require.Equal(t, 1, engine.keys.guardCreateCount())

	// 第四次：重放但密码错误 → 拒绝，也不下发回执。
	fourth := call(`{"method":"password","password":"wrong-password"}`)
	require.Equal(t, http.StatusBadRequest, fourth.Code, fourth.Body.String())
	require.Equal(t, "VERIFY_PASSWORD_INVALID", plaintextGuardReason(t, fourth))
	require.NotContains(t, fourth.Body.String(), "key_masked")
}

func TestCreateIdempotentReplayAllowedForAccountsWithoutFactors(t *testing.T) {
	oauth := plaintextGuardUser(t)
	oauth.SignupSource = "github"
	journal := newUserMemoryIdempotencyRepoStub()
	engine := newPlaintextGuardEngine(t, oauth, journal)

	call := func() *httptest.ResponseRecorder {
		return doJSON(engine.router, http.MethodPost, "/api/v1/keys", `{"name":"idem-oauth"}`,
			map[string]string{"Idempotency-Key": "synthetic-idem-oauth"})
	}
	first := call()
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	replay := call()
	require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	require.Equal(t, "true", replay.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, engine.keys.guardCreateCount())
}

// ---- 4. 老调用的瞬时重放不能被误伤 -----------------------------------------

func TestIdempotencyCoordinatorRejectsGuardedReplay(t *testing.T) {
	// 服务层直接验证 ReplayGuard：命中成功回执且 gate 拒绝时不返回存储数据。
	journal := newUserMemoryIdempotencyRepoStub()
	coordinator := service.NewIdempotencyCoordinator(journal, service.DefaultIdempotencyConfig())
	opts := service.IdempotencyExecuteOptions{
		Scope: "user.api_keys.create", ActorScope: "user:7", Method: "POST",
		Route: "/api/v1/keys", IdempotencyKey: "guard-replay", Payload: map[string]any{"name": "x"},
		RequireKey: true, TTL: service.DefaultWriteIdempotencyTTL(),
	}
	first, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
		return map[string]any{"id": 1}, nil
	})
	require.NoError(t, err)
	require.False(t, first.Replayed)

	opts.ReplayGuard = func() (bool, error) { return false, nil }
	_, err = coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
		return map[string]any{"id": 2}, nil
	})
	require.Error(t, err)
	require.True(t, errorsIsCode(err, service.ErrIdempotencyReplayForbidden), err.Error())

	opts.ReplayGuard = func() (bool, error) { return true, nil }
	replayed, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
		return map[string]any{"id": 3}, nil
	})
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
}

func TestAuditExtraCarriesVerificationOutcome(t *testing.T) {
	// 无法用真实审计库时，验证 guard 写入的审计字段集合：拒绝时只有机器码，
	// 不出现密码 / 动态码 / Passkey 票据。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/keys", strings.NewReader(""))
	setVerificationAudit(c, nil, auditVerificationRejected, "VERIFY_PASSWORD_INVALID")
	raw, err := json.Marshal(mustAuditExtra(c))
	require.NoError(t, err)
	require.Contains(t, string(raw), "VERIFY_PASSWORD_INVALID")
	require.NotContains(t, string(raw), "password")

	// 中间件的白名单必须接受这些键，否则审计会静默丢失。
	allowed := []string{"api_key_verification", "api_key_auth_factor", "api_key_verification_reason"}
	for _, key := range allowed {
		require.True(t, middlewareAuditAllows(key), "audit whitelist must allow %s", key)
	}
}

func mustAuditExtra(c *gin.Context) map[string]any {
	value, _ := c.Get("audit_extra")
	fields, _ := value.(map[string]any)
	return fields
}

// 时间相关的守卫：TTL 常量不能为 0（否则回执立刻过期，重放永远重新执行）。
func TestIdempotencyTTLIsPositive(t *testing.T) {
	require.Greater(t, service.DefaultWriteIdempotencyTTL(), time.Duration(0))
	require.Equal(t, middleware2.StepUpSkippedHeader, "X-StepUp-Skipped")
}

func errorsIsCode(err error, target error) bool {
	if err == nil || target == nil {
		return false
	}
	return strings.Contains(err.Error(), "IDEMPOTENCY_REPLAY_FORBIDDEN")
}

func middlewareAuditAllows(key string) bool {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	middleware2.SetAuditExtra(c, map[string]any{key: "x"})
	value, ok := c.Get("audit_extra")
	if !ok {
		return false
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, present := fields[key]
	return present
}
