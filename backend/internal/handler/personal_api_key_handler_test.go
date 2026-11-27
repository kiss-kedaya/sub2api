//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubPersonalKeyRepoHandler struct {
	byUser map[int64]*service.UserPersonalAPIKey
}

func (r *stubPersonalKeyRepoHandler) GetByUserID(_ context.Context, userID int64) (*service.UserPersonalAPIKey, error) {
	item, ok := r.byUser[userID]
	if !ok {
		return nil, service.ErrUserPersonalAPIKeyNotFound
	}
	cp := *item
	return &cp, nil
}
func (r *stubPersonalKeyRepoHandler) GetByHash(_ context.Context, keyHash string) (*service.UserPersonalAPIKey, error) {
	for _, item := range r.byUser {
		if item.KeyHash == keyHash {
			cp := *item
			return &cp, nil
		}
	}
	return nil, service.ErrUserPersonalAPIKeyNotFound
}
func (r *stubPersonalKeyRepoHandler) Upsert(_ context.Context, key *service.UserPersonalAPIKey) (bool, error) {
	_, existed := r.byUser[key.UserID]
	cp := *key
	r.byUser[key.UserID] = &cp
	return existed, nil
}
func (r *stubPersonalKeyRepoHandler) DeleteByUserID(_ context.Context, userID int64) (bool, error) {
	_, existed := r.byUser[userID]
	delete(r.byUser, userID)
	return existed, nil
}
func (r *stubPersonalKeyRepoHandler) TouchLastUsed(_ context.Context, _ int64, _, _ string, _ time.Time) error {
	return nil
}

func newPersonalAPIKeyTestRouter(h *APIKeyHandler, userID int64) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		c.Next()
	})
	r.GET("/api/v1/user/personal-api-key", h.GetPersonalAPIKey)
	r.POST("/api/v1/user/personal-api-key", h.CreateOrRegeneratePersonalAPIKey)
	r.DELETE("/api/v1/user/personal-api-key", h.DeletePersonalAPIKey)
	return r
}

func TestPersonalAPIKeyHandler_FailClosedWithoutVerificationService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &stubPersonalKeyRepoHandler{byUser: map[int64]*service.UserPersonalAPIKey{}}
	personalSvc := service.NewUserPersonalAPIKeyService(repo, nil)
	h := NewAPIKeyHandler(nil, nil)
	h.SetPersonalAPIKeyService(personalSvc)
	// 未注入 verificationService 必须 fail-closed 返回 503，严禁静默绕过身份校验。

	r := newPersonalAPIKeyTestRouter(h, 88)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/user/personal-api-key", bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Empty(t, repo.byUser)
}

func TestPersonalAPIKeyHandler_CreateRegenerateDelete_WithStrictVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &stubPersonalKeyRepoHandler{byUser: map[int64]*service.UserPersonalAPIKey{}}
	personalSvc := service.NewUserPersonalAPIKeyService(repo, nil)

	user := &service.User{ID: 88, Email: "ops@example.com", Status: service.StatusActive, SignupSource: "email"}
	require.NoError(t, user.SetPassword("my-secret-pass"))
	verifySvc := service.NewAPIKeyVerificationService(
		&handlerVerifyUserRepo{user: user},
		nil,
		nil,
		nil,
		nil,
		newHandlerVerifyStore(),
	)

	h := NewAPIKeyHandler(nil, nil)
	h.SetPersonalAPIKeyService(personalSvc)
	h.SetVerificationService(verifySvc)
	r := newPersonalAPIKeyTestRouter(h, 88)

	// 1. 初始查询不存在。
	w0 := httptest.NewRecorder()
	r.ServeHTTP(w0, httptest.NewRequest(http.MethodGet, "/api/v1/user/personal-api-key", nil))
	require.Equal(t, http.StatusOK, w0.Code)
	require.Equal(t, "no-store", w0.Header().Get("Cache-Control"))
	require.Contains(t, w0.Body.String(), `"exists":false`)

	// 2. 有密码因子但未传 verification，即使无 2FA 也必须被 VerifyStrict 拒绝（400 VERIFY_REQUIRED）。
	wNoVerify := httptest.NewRecorder()
	r.ServeHTTP(wNoVerify, httptest.NewRequest(http.MethodPost, "/api/v1/user/personal-api-key", bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusBadRequest, wNoVerify.Code)
	require.Equal(t, "VERIFY_REQUIRED", handlerVerificationErrorReason(t, wNoVerify))

	// 3. 带正确密码首次生成：返回一次明文 key，regenerated=false。
	validBody := `{"verification":{"method":"password","password":"my-secret-pass"}}`
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest(http.MethodPost, "/api/v1/user/personal-api-key", bytes.NewBufferString(validBody)))
	require.Equal(t, http.StatusOK, w1.Code)
	require.Equal(t, "no-store", w1.Header().Get("Cache-Control"))
	var createResp struct {
		Code int `json:"code"`
		Data struct {
			Key         string                           `json:"key"`
			Regenerated bool                             `json:"regenerated"`
			Status      service.UserPersonalAPIKeyStatus `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &createResp))
	require.True(t, service.IsUserPersonalAPIKeyFormat(createResp.Data.Key))
	require.False(t, createResp.Data.Regenerated)
	require.True(t, createResp.Data.Status.Exists)
	require.NotEqual(t, createResp.Data.Key, createResp.Data.Status.KeyPrefix)

	// 4. 再次 GET 不含明文。
	wGet := httptest.NewRecorder()
	r.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/api/v1/user/personal-api-key", nil))
	require.Equal(t, http.StatusOK, wGet.Code)
	require.NotContains(t, wGet.Body.String(), createResp.Data.Key)
	require.Contains(t, wGet.Body.String(), createResp.Data.Status.KeyPrefix)

	// 5. 再次 POST 标记 regenerated=true 并生成不同明文。
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodPost, "/api/v1/user/personal-api-key", bytes.NewBufferString(validBody)))
	require.Equal(t, http.StatusOK, w2.Code)
	var regenResp struct {
		Data struct {
			Key         string `json:"key"`
			Regenerated bool   `json:"regenerated"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &regenResp))
	require.True(t, regenResp.Data.Regenerated)
	require.NotEqual(t, createResp.Data.Key, regenResp.Data.Key)

	// 6. DELETE 后状态回到 exists=false。
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, httptest.NewRequest(http.MethodDelete, "/api/v1/user/personal-api-key", nil))
	require.Equal(t, http.StatusOK, wDel.Code)
	require.Contains(t, wDel.Body.String(), `"deleted":true`)

	wAfter := httptest.NewRecorder()
	r.ServeHTTP(wAfter, httptest.NewRequest(http.MethodGet, "/api/v1/user/personal-api-key", nil))
	require.Equal(t, http.StatusOK, wAfter.Code)
	require.Contains(t, wAfter.Body.String(), `"exists":false`)
}

func TestPersonalAPIKeyHandler_ZeroFactorOAuthUserAllowedWithoutPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &stubPersonalKeyRepoHandler{byUser: map[int64]*service.UserPersonalAPIKey{}}
	personalSvc := service.NewUserPersonalAPIKeyService(repo, nil)

	// 纯第三方 OAuth 账号（无本地密码绑定记录、未开 TOTP、无 Passkey）允许免校验载荷创建。
	oauthUser := &service.User{ID: 99, Email: "gh@example.com", Status: service.StatusActive, SignupSource: "github"}
	require.NoError(t, oauthUser.SetPassword("random-oauth-generated"))
	verifySvc := service.NewAPIKeyVerificationService(
		&handlerVerifyUserRepo{user: oauthUser},
		nil,
		nil,
		nil,
		nil,
		newHandlerVerifyStore(),
	)

	h := NewAPIKeyHandler(nil, nil)
	h.SetPersonalAPIKeyService(personalSvc)
	h.SetVerificationService(verifySvc)
	r := newPersonalAPIKeyTestRouter(h, 99)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/user/personal-api-key", bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"key":"uak_`)
}

func TestKeyVerificationGuard_PersonalAPIKeyCreateAndOwnershipCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	keys := &handlerSecretRepo{keys: map[int64]*service.APIKey{
		1: {ID: 1, UserID: 88, KeyPrefix: "sk-owner", Status: service.StatusAPIKeyActive},
		2: {ID: 2, UserID: 99, KeyPrefix: "sk-other", Status: service.StatusAPIKeyActive},
	}}
	verifySvc := service.NewAPIKeyVerificationService(nil, keys, nil, nil, nil, nil)
	h := NewAPIKeyHandler(nil, nil)
	h.SetVerificationService(verifySvc)

	newCtx := func(method, target string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, target, strings.NewReader(`{}`))
		c.Set("auth_method", service.AuditAuthMethodUserAPIKey)
		return c
	}

	// 1. 创建路径（apiKeyID=0）免交互验证。
	cCreate := newCtx(http.MethodPost, "/api/v1/keys")
	method, err := h.keyVerificationGuard(cCreate, 88, 0, nil)
	require.NoError(t, err)
	require.Equal(t, service.AuditAuthMethodUserAPIKey, method)
	require.True(t, cCreate.GetBool(service.ApiKeyVerificationGuardPassedKey))

	// 2. 轮换本人 Key（apiKeyID=1）通过归属校验。
	cOwn := newCtx(http.MethodPut, "/api/v1/keys/1")
	method, err = h.keyVerificationGuard(cOwn, 88, 1, nil)
	require.NoError(t, err)
	require.Equal(t, service.AuditAuthMethodUserAPIKey, method)
	require.True(t, cOwn.GetBool(service.ApiKeyVerificationGuardPassedKey))

	// 3. 轮换他人 Key（apiKeyID=2）必须被拒绝（ErrAPIKeyNotFound），不标 passed。
	cForeign := newCtx(http.MethodPut, "/api/v1/keys/2")
	_, err = h.keyVerificationGuard(cForeign, 88, 2, nil)
	require.ErrorIs(t, err, service.ErrAPIKeyNotFound)
	require.False(t, cForeign.GetBool(service.ApiKeyVerificationGuardPassedKey))

	// 4. apiKeyRepo 未装配时必须 fail-closed 返回 ErrAPIKeyVerificationUnavailable。
	hNilRepo := NewAPIKeyHandler(nil, nil)
	hNilRepo.SetVerificationService(service.NewAPIKeyVerificationService(nil, nil, nil, nil, nil, nil))
	cNil := newCtx(http.MethodPut, "/api/v1/keys/1")
	_, err = hNilRepo.keyVerificationGuard(cNil, 88, 1, nil)
	require.ErrorIs(t, err, service.ErrAPIKeyVerificationUnavailable)
}
