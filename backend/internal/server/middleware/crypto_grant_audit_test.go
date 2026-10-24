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

func TestCryptoGrantAuditOmitsVerificationBodyAndEntireCredential(t *testing.T) {
	const path = "/api/v1/admin/crypto/security/verify"
	gin.SetMode(gin.TestMode)
	capture := &auditCaptureRepository{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.POST(path, func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"totp_code":"654321"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Crypto-Grant", "cpg1.private-fixture-credential.private-signature")
	router.ServeHTTP(httptest.NewRecorder(), request)
	audit.Stop()
	require.Len(t, capture.logs, 1)
	require.Equal(t, "admin.crypto.security.verify", capture.logs[0].Action)
	require.Equal(t, "<credential-bearing body omitted>", capture.logs[0].RequestBody)
	require.Equal(t, "x-crypto-grant <redacted>", capture.logs[0].CredentialMasked)
}
