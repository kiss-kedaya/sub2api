package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/web"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCryptoAdminTestModeGuardAllowsOnlyReviewRoutes(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(servermiddleware.CryptoAdminTestModeGuard())
	for _, path := range []string{
		"/api/v1/auth/login", "/api/v1/auth/login/2fa", "/api/v1/auth/me", "/api/v1/settings/public",
		"/api/v1/admin/crypto/security/status", "/api/v1/admin/crypto/config", "/api/v1/admin/crypto/addresses",
		"/api/v1/admin/crypto/deposits.csv", "/api/v1/admin/users", "/api/v1/admin/crypto/security/setup",
		"/api/v1/admin/crypto/wallet/export-mnemonics", "/v1/models", "/admin/crypto", "/assets/index.js",
		"/metrics", "/api/v1/admin/crypto/future-risk-read",
	} {
		router.Any(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/v1/auth/login", http.StatusNoContent},
		{http.MethodGet, "/api/v1/auth/me", http.StatusNoContent},
		{http.MethodGet, "/api/v1/admin/crypto/config", http.StatusNoContent},
		{http.MethodGet, "/admin/crypto", http.StatusNoContent},
		{http.MethodGet, "/assets/index.js", http.StatusNoContent},
		{http.MethodPost, "/api/v1/admin/users", http.StatusForbidden},
		{http.MethodGet, "/metrics", http.StatusForbidden},
		{http.MethodGet, "/api/v1/admin/crypto/future-risk-read", http.StatusForbidden},
		{http.MethodPost, "/api/v1/admin/crypto/security/setup", http.StatusForbidden},
		{http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", http.StatusForbidden},
		{http.MethodGet, "/api/v1/admin/crypto/deposits.csv", http.StatusForbidden},
		{http.MethodGet, "/v1/models", http.StatusForbidden},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			require.Equal(t, tc.want, res.Code, res.Body.String())
			if tc.want == http.StatusForbidden {
				require.Contains(t, res.Body.String(), "CRYPTO_TEST_MODE_READ_ONLY")
			}
		})
	}
}

func TestCryptoAdminTestModeGuardIsDisabledOutsideTestMode(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(servermiddleware.CryptoAdminTestModeGuard())
	router.POST("/api/v1/admin/users", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", nil))
	require.Equal(t, http.StatusNoContent, res.Code)
}

func TestCryptoAdminTestModeGuardRunsBeforeCanvasHandler(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("private canvas"), 0o600))
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", dir)
	t.Setenv("INFINITE_CANVAS_UPSTREAM", "")
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(servermiddleware.CryptoAdminTestModeGuard())
	router.Use(web.InfiniteCanvasHandler())
	router.GET("/canvas/*path", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/canvas/", nil))
	require.Equal(t, http.StatusForbidden, res.Code, res.Body.String())
	require.Contains(t, res.Body.String(), "CRYPTO_TEST_MODE_READ_ONLY")
	require.NotContains(t, res.Body.String(), "private canvas")
}
