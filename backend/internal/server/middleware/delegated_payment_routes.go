package middleware

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// DelegatedPaymentRoutesGuard rejects payment requests that reach a gateway
// whose payment and wallet operations run on a separate backend.
func DelegatedPaymentRoutesGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isDelegatedPaymentRequest(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		response.ErrorWithDetails(c, http.StatusServiceUnavailable, "payments are unavailable on this gateway", "PAYMENTS_DELEGATED", nil)
		c.Abort()
	}
}

func isDelegatedPaymentRequest(method, path string) bool {
	path, ok := strings.CutPrefix(path, "/api/v1/")
	if !ok {
		return false
	}
	// The general settings update can also write payment configuration.
	if path == "admin/settings" {
		return method == http.MethodPut
	}
	// Keep the compatibility callback and payment OAuth alongside the
	// payment and crypto groups. Match only whole path segments.
	for _, prefix := range [...]string{
		"crypto",
		"admin/crypto",
		"payment",
		"admin/payment",
		"payments/callback",
		"auth/oauth/wechat/payment",
	} {
		rest, matched := strings.CutPrefix(path, prefix)
		if matched && (rest == "" || rest[0] == '/') {
			return true
		}
	}
	return false
}
