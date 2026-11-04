package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Manual deposit confirmation credits balances, so it needs a stable audit
// action rather than whatever deriveAuditAction would invent from the path.
func TestCryptoManualDepositConfirmAuditHasStableActionAndNoOneTimeCode(t *testing.T) {
	const route = "POST /api/v1/admin/crypto/deposits/confirm"
	require.Equal(t, "admin.crypto.deposit.manual_confirm", auditActionOverrides[route])
	require.NotEqual(t, deriveAuditAction("POST", "/api/v1/admin/crypto/deposits/confirm"), auditActionOverrides[route],
		"the override must replace the derived name, otherwise it is not doing anything")
	// The body carries ids and a free-text note, not credentials, so it stays
	// in the audit row for traceability.
	require.NotContains(t, auditBodyOmittedRoutes, route)

	gin.SetMode(gin.TestMode)
	capture := &auditCaptureRepository{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 41})
		c.Set(string(ContextKeyUserRole), service.RoleAdmin)
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.POST("/api/v1/admin/crypto/deposits/confirm", func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/crypto/deposits/confirm",
		bytes.NewBufferString(`{"ids":[302,321],"note":"checked on the explorer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crypto-TOTP", "654321")
	router.ServeHTTP(httptest.NewRecorder(), req)
	audit.Stop()

	require.Len(t, capture.logs, 1)
	entry := capture.logs[0]
	require.Equal(t, "admin.crypto.deposit.manual_confirm", entry.Action)
	require.NotNil(t, entry.ActorUserID)
	require.EqualValues(t, 41, *entry.ActorUserID)
	require.Contains(t, entry.RequestBody, "302")
	require.Contains(t, entry.RequestBody, "321")
	require.Contains(t, entry.RequestBody, "checked on the explorer")
	require.Equal(t, "x-crypto-totp <redacted>", entry.CredentialMasked)
	require.NotContains(t, entry.RequestBody, "654321")
	require.NotContains(t, entry.CredentialMasked, "654321")
}

// A review instance that is read-only must record nothing for this route,
// because it never reaches the handler.
func TestCryptoManualDepositConfirmIsBlockedInReadOnlyTestMode(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "")
	require.False(t, cryptoTestModeRouteAllowed(http.MethodPost, "/api/v1/admin/crypto/deposits/confirm"))
	require.True(t, cryptoTestModeRouteAllowed(http.MethodGet, "/api/v1/admin/crypto/deposits"),
		"listing deposits stays available to the reviewer")
}
