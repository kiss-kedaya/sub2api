package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const adminAPIKeyPlaintextFixture = "sk-abcdefgh1234"

func adminKeyListStub() *stubAdminService {
	stub := newStubAdminService()
	stub.apiKeys = []service.APIKey{{
		ID:     10,
		UserID: 1,
		Key:    adminAPIKeyPlaintextFixture,
		Name:   "prod",
		Status: service.StatusActive,
	}}
	return stub
}

func serveAdminKeyList(t *testing.T, handler gin.HandlerFunc, path string) httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/admin/users/:id/api-keys", handler)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return *rec
}

// 管理员查看用户 key 列表：只允许 key_prefix / key_masked，任何明文字段都不许出现。
func TestAdminUserAPIKeysNeverReturnPlaintextKey(t *testing.T) {
	h := NewUserHandler(adminKeyListStub(), nil, nil, nil, nil, nil, nil)
	rec := serveAdminKeyList(t, h.GetUserAPIKeys, "/api/v1/admin/users/1/api-keys")

	body := rec.Body.String()
	require.NotContains(t, body, adminAPIKeyPlaintextFixture)
	require.NotContains(t, body, `"key":`)
	require.Contains(t, body, `"key_prefix":"sk-abcde"`)
	require.Contains(t, body, `"key_masked":"sk-abcde****1234"`)

	var decoded struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &decoded))
	require.Len(t, decoded.Data.Items, 1)
	_, hasKey := decoded.Data.Items[0]["key"]
	require.False(t, hasKey, "admin user key list must not expose a key field: %s", body)
}

// 管理员查看分组 key 列表：同一脱敏边界。
func TestAdminGroupAPIKeysNeverReturnPlaintextKey(t *testing.T) {
	h := NewGroupHandler(adminKeyListStub(), nil, nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/admin/groups/:id/api-keys", h.GetGroupAPIKeys)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/2/api-keys", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.False(t, strings.Contains(body, adminAPIKeyPlaintextFixture), body)
	require.False(t, strings.Contains(body, `"key":`), body)
	require.True(t, strings.Contains(body, `"key_masked":"sk-abcde****1234"`), body)
}
