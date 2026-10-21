package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CryptoAdminTestModeGuard limits a shared-production-database review instance
// to authentication and crypto administration. It is intentionally a
// process-local route allowlist; shared settings cannot disable it.
func CryptoAdminTestModeGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !service.CryptoAdminTestMode() || cryptoTestModeRouteAllowed(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		response.ErrorWithDetails(c, http.StatusForbidden, "test instance route is read-only", "CRYPTO_TEST_MODE_READ_ONLY", nil)
		c.Abort()
	}
}

func cryptoTestModeRouteAllowed(method, path string) bool {
	if method == http.MethodOptions {
		return true
	}
	if (method == http.MethodGet || method == http.MethodHead) &&
		(path == "/health" || path == "/readyz" || path == "/setup/status") {
		return true
	}
	if method == http.MethodPost && (path == "/api/v1/auth/login" || path == "/api/v1/auth/login/2fa") {
		return true
	}
	if method == http.MethodGet && (path == "/api/v1/auth/me" || path == "/api/v1/settings/public" ||
		path == "/api/v1/admin/crypto/security/status") {
		return true
	}
	if method == http.MethodGet {
		switch path {
		case "/api/v1/admin/crypto/config", "/api/v1/admin/crypto/addresses",
			"/api/v1/admin/crypto/sweep/plan", "/api/v1/admin/crypto/sweep/records",
			"/api/v1/admin/crypto/deposits", "/api/v1/admin/crypto/wallet/status",
			"/api/v1/admin/crypto/pool", "/api/v1/admin/crypto/hot-wallet",
			"/api/v1/admin/crypto/hot-wallet/address", "/api/v1/admin/crypto/rpc/candidates":
			return true
		}
	}
	if strings.HasPrefix(path, "/api/") || cryptoTestModeGatewayPath(path) {
		return false
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	return path == "/" || path == "/login" || path == "/admin/login" || path == "/admin/crypto" ||
		strings.HasPrefix(path, "/assets/") || path == "/site-logo" || path == "/favicon.ico" || path == "/robots.txt"
}

func cryptoTestModeGatewayPath(path string) bool {
	for _, prefix := range []string{
		"/v1/", "/v1beta/", "/responses", "/chat/completions", "/messages", "/models",
		"/videos", "/images", "/embeddings", "/completions", "/moderations", "/audio/", "/realtime/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
