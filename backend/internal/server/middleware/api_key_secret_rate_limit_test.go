package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// GET /user/api-keys/:id/secret-seal 是密文的读取口：每用户 20 次/分钟，
// 防止它被当成暴力验证登录密码的辅助通道。
func TestPanelRateLimiterSecretSealReadLimitsPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allower := &fakePanelAllower{counts: map[string]int64{}}
	limiter := &PanelRateLimiter{limiter: allower}

	router := gin.New()
	subject := func(userID int64) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set(string(ContextKeyUser), AuthSubject{UserID: userID})
			c.Next()
		}
	}
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	router.GET("/u7/seal", subject(7), limiter.SecretSealRead(), ok)
	router.GET("/u8/seal", subject(8), limiter.SecretSealRead(), ok)

	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u7/seal", nil))
		require.Equal(t, http.StatusOK, rec.Code, "request %d must pass", i+1)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u7/seal", nil))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))

	// 另一个用户有独立额度。
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u8/seal", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPanelRateLimiterSecretSealReadNilSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var limiter *PanelRateLimiter
	router := gin.New()
	router.GET("/seal", func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 7})
		c.Next()
	}, limiter.SecretSealRead(), func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/seal", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

// 审计口径锁定：查看密文必须留痕且只带 Key 标识；封存请求体整体不入库。
func TestAPIKeySecretSealAuditMapping(t *testing.T) {
	fetchRoute := "GET /api/v1/user/api-keys/:id/secret-seal"
	sealRoute := "PUT /api/v1/user/api-keys/:id/secret-seal"
	deleteRoute := "DELETE /api/v1/user/api-keys/:id/secret-seal"

	require.Equal(t, service.AuditActionAPIKeySecretFetch, auditSensitiveReads[fetchRoute])
	require.Equal(t, service.AuditActionAPIKeySecretSeal, auditActionOverrides[sealRoute])
	require.Equal(t, service.AuditActionAPIKeySecretDelete, auditActionOverrides[deleteRoute])
	require.Contains(t, auditBodyOmittedRoutes, sealRoute)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetAuditExtra(c, map[string]any{
		"api_key_id":     int64(42),
		"api_key_prefix": "sk-abc12",
		// 下面这些不在白名单，必须被丢弃。
		"sealed":         "c2VhbGVk",
		"kdf":            map[string]any{"salt": "MDEyMzQ1Njc="},
		"api_key_secret": "leak",
	})
	value, ok := c.Get(auditCtxKeyExtra)
	require.True(t, ok)
	extra, ok := value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, int64(42), extra["api_key_id"])
	require.Equal(t, "sk-abc12", extra["api_key_prefix"])
	require.NotContains(t, extra, "sealed")
	require.NotContains(t, extra, "kdf")
	require.NotContains(t, extra, "api_key_secret")
}
