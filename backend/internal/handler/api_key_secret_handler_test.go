package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// handlerSecretRepo 是 API Key 仓储的内存替身：封存服务只用 GetByID 校验归属，
// 列表接口用 ListByUserID 返回固定页。
type handlerSecretRepo struct {
	service.APIKeyRepository
	keys map[int64]*service.APIKey
	list []service.APIKey
}

func (r *handlerSecretRepo) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	if key, ok := r.keys[id]; ok && key != nil {
		return key, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r *handlerSecretRepo) Update(_ context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	existing, ok := r.keys[key.ID]
	if !ok {
		return service.ErrAPIKeyNotFound
	}
	if fields.Name {
		existing.Name = key.Name
	}
	return nil
}

func (r *handlerSecretRepo) ListByUserID(_ context.Context, _ int64, params pagination.PaginationParams, _ service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return r.list, &pagination.PaginationResult{Total: int64(len(r.list)), Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

type handlerSecretStore struct {
	service.APIKeySecretRepository
	rows map[int64]*service.APIKeySecret
}

func newHandlerSecretStore() *handlerSecretStore {
	return &handlerSecretStore{rows: map[int64]*service.APIKeySecret{}}
}

func (s *handlerSecretStore) Upsert(_ context.Context, secret *service.APIKeySecret) error {
	copied := *secret
	copied.KDF = append(json.RawMessage(nil), secret.KDF...)
	s.rows[secret.APIKeyID] = &copied
	return nil
}

func (s *handlerSecretStore) GetByAPIKeyID(_ context.Context, apiKeyID, userID int64) (*service.APIKeySecret, error) {
	row, ok := s.rows[apiKeyID]
	if !ok || row.UserID != userID {
		return nil, service.ErrAPIKeySecretNotFound
	}
	copied := *row
	copied.KDF = append(json.RawMessage(nil), row.KDF...)
	return &copied, nil
}

func (s *handlerSecretStore) DeleteByAPIKeyID(_ context.Context, apiKeyID, userID int64) error {
	if row, ok := s.rows[apiKeyID]; ok && row.UserID == userID {
		delete(s.rows, apiKeyID)
	}
	return nil
}

func (s *handlerSecretStore) ListPresenceByAPIKeyIDs(_ context.Context, userID int64, ids []int64) (map[int64]struct{}, error) {
	out := map[int64]struct{}{}
	for _, id := range ids {
		if row, ok := s.rows[id]; ok && row.UserID == userID {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

func newSecretSealTestEngine(t *testing.T) (*gin.Engine, *handlerSecretStore, *handlerSecretRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	repo := &handlerSecretRepo{
		keys: map[int64]*service.APIKey{
			1: {ID: 1, UserID: 7, KeyPrefix: "sk-owner1", Status: service.StatusAPIKeyActive},
			2: {ID: 2, UserID: 7, KeyPrefix: "sk-owner2", Status: service.StatusAPIKeyActive},
			9: {ID: 9, UserID: 8, KeyPrefix: "sk-other", Status: service.StatusAPIKeyActive},
		},
		list: []service.APIKey{
			{ID: 1, UserID: 7, KeyPrefix: "sk-owner1", Status: service.StatusAPIKeyActive},
			{ID: 2, UserID: 7, KeyPrefix: "sk-owner2", Status: service.StatusAPIKeyActive},
		},
	}
	store := newHandlerSecretStore()
	cfg := &config.Config{}
	apiKeyService := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	secretService := service.NewAPIKeySecretService(repo, store)
	h := NewAPIKeyHandler(apiKeyService, secretService)

	router := gin.New()
	r := router.Group("/api/v1")
	r.Use(withUserSubject(7))
	r.PUT("/user/api-keys/:id/secret-seal", middleware2.RequestBodyLimit(service.APIKeySecretMaxBodyBytes), h.PutSecretSeal)
	r.PUT("/keys/:id", h.Update)
	r.GET("/user/api-keys/:id/secret-seal", h.GetSecretSeal)
	r.DELETE("/user/api-keys/:id/secret-seal", h.DeleteSecretSeal)
	r.GET("/keys", h.List)
	r.GET("/keys/:id", h.GetByID)
	return router, store, repo
}

func secretSealJSON(t *testing.T, saltBytes, sealedBytes int, iterations int) string {
	t.Helper()
	kdf, err := json.Marshal(map[string]any{
		"algo":       service.APIKeySecretKDFAlgoPBKDF2SHA256,
		"iterations": iterations,
		"salt":       base64.StdEncoding.EncodeToString(make([]byte, saltBytes)),
	})
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"version": 1,
		"kdf":     json.RawMessage(kdf),
		"sealed":  base64.StdEncoding.EncodeToString(make([]byte, sealedBytes)),
	})
	require.NoError(t, err)
	return string(body)
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) response.Response {
	t.Helper()
	var out response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestSecretSealPutGetDeleteRoundTrip(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	body := secretSealJSON(t, 16, 48, 310000)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var putResp struct {
		Code int `json:"code"`
		Data struct {
			OK bool `json:"ok"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &putResp))
	require.Equal(t, 0, putResp.Code)
	require.True(t, putResp.Data.OK)
	require.Contains(t, store.rows, int64(1))

	// GET 原样返回 version/kdf/sealed；没有明文、hash 等其它字段。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	var got struct {
		Data struct {
			Version int             `json:"version"`
			KDF     json.RawMessage `json:"kdf"`
			Sealed  string          `json:"sealed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, 1, got.Data.Version)
	require.NotEmpty(t, got.Data.Sealed)
	require.JSONEq(t, string(store.rows[1].KDF), string(got.Data.KDF))

	// DELETE 后 GET 必须是 404 + API_KEY_SECRET_NOT_FOUND；重复 DELETE 仍然成功。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/user/api-keys/1/secret-seal", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, store.rows)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", nil))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "API_KEY_SECRET_NOT_FOUND", decodeResponse(t, rec).Reason)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/user/api-keys/1/secret-seal", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestSecretSealPutIsIdempotent(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	first := secretSealJSON(t, 16, 40, 310000)
	second := secretSealJSON(t, 24, 64, 600000)

	for _, body := range []string{first, second} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/2/secret-seal", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	require.Len(t, store.rows, 1)
	require.Equal(t, base64.StdEncoding.EncodeToString(make([]byte, 64)), store.rows[2].Sealed)
}

func TestSecretSealRejectsOversizedBody(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	huge := `{"version":1,"kdf":{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"MDEyMzQ1Njc="},"sealed":"` +
		strings.Repeat("A", 9*1024) + `"}`

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", strings.NewReader(huge)))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	require.Empty(t, store.rows)
}

func TestSecretSealRejectsOversizedChunkedBody(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	huge := `{"version":1,"padding":"` + strings.Repeat("A", 9*1024) + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", strings.NewReader(huge))
	req.ContentLength = -1 // 模拟 chunked：没有 Content-Length，靠 MaxBytesReader 兜底

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	require.Empty(t, store.rows)
}

func TestSecretSealPutValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"sealed too short", secretSealJSON(t, 16, 31, 310000)},
		{"salt too short", secretSealJSON(t, 7, 32, 310000)},
		{"iterations too small", secretSealJSON(t, 16, 32, 99999)},
		{"iterations too large", secretSealJSON(t, 16, 32, 2000001)},
		{"not json", "{"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, store, _ := newSecretSealTestEngine(t)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", strings.NewReader(tc.body)))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Empty(t, store.rows)
		})
	}
}

func TestSecretSealForeignKeyLooksMissing(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	body := secretSealJSON(t, 16, 32, 310000)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/9/secret-seal", strings.NewReader(body)))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Empty(t, store.rows)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/9/secret-seal", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/user/api-keys/9/secret-seal", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestSecretSealBadKeyIDIs400(t *testing.T) {
	router, _, _ := newSecretSealTestEngine(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/not-a-number/secret-seal", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestSecretSealListAndDetailHaveHasSealedSecret(t *testing.T) {
	router, store, _ := newSecretSealTestEngine(t)
	body := secretSealJSON(t, 16, 32, 310000)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/2/secret-seal", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 列表：只有 key 2 有密文；列表响应里绝不能出现 sealed 内容。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var list struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Data.Items, 2)
	require.Equal(t, false, list.Data.Items[0]["has_sealed_secret"])
	require.Equal(t, true, list.Data.Items[1]["has_sealed_secret"])
	require.NotContains(t, rec.Body.String(), store.rows[2].Sealed)
	for _, item := range list.Data.Items {
		require.NotContains(t, item, "sealed")
		require.NotContains(t, item, "kdf")
	}

	// 详情同样带布尔值。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/keys/2", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var detail struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &detail))
	require.Equal(t, true, detail.Data["has_sealed_secret"])
	require.NotContains(t, rec.Body.String(), store.rows[2].Sealed)
}

// 普通编辑（非轮换）的回执也必须保留 has_sealed_secret，否则前端一改名就把
// “可查看”按钮状态刷掉。
func TestSecretSealUpdateKeepsHasSealedSecret(t *testing.T) {
	router, _, _ := newSecretSealTestEngine(t)
	body := secretSealJSON(t, 16, 32, 310000)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/2/secret-seal", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/keys/2", strings.NewReader(`{"name":"renamed"}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "renamed", out.Data["name"])
	require.Equal(t, true, out.Data["has_sealed_secret"])
}
