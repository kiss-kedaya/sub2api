package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// passkeyAssertionRoutes 是 routes/auth.go 与 routes/user.go 里全部「名字含 passkey、
// 以 finish 结尾、请求体带 WebAuthn assertion / attestation」的路由（对应请求体里的
// credential 字段：authenticatorData / clientDataJSON / signature / attestationObject）。
// 这些字段名不在键级脱敏清单里，必须整体不入审计库。新增这类路由时加到这里，
// 并同步加入 auditBodyOmittedRoutes。
var passkeyAssertionRoutes = []string{
	"POST /api/v1/auth/passkey/login/finish",
	"POST /api/v1/auth/login/2fa/passkey/finish",
	"POST /api/v1/user/step-up/passkey/finish",
	"POST /api/v1/user/security/passkey/verify/finish",
	"POST /api/v1/user/passkeys/register/finish",
}

func TestPasskeyAssertionRoutesAreInAuditBodyOmittedTable(t *testing.T) {
	for _, route := range passkeyAssertionRoutes {
		t.Run(route, func(t *testing.T) {
			require.Contains(t, auditBodyOmittedRoutes, route,
				"WebAuthn assertion bodies must never be persisted in audit_logs.request_body")
		})
	}
}

// 端到端：带假 assertion 的请求走过审计中间件，落库的 body 只能是省略占位，
// 且 handler 仍能读到完整的原始请求体（省略审计不能影响业务）。
//
// 注意 POST /auth/login/2fa/passkey/finish 同时登记在 auditAuthEvents，认证事件分支
// 本身就会省略 body；它在 omitted 表里的登记由上面的表驱动用例单独把关。
func TestPasskeyAssertionRoutesNeverPersistRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const canary = "audit-passkey-canary"
	body := `{"session_token":"` + canary + `-session","credential":{"id":"` + canary + `-id","rawId":"` + canary + `-rawid",` +
		`"type":"public-key","response":{"authenticatorData":"` + canary + `-authdata","clientDataJSON":"` + canary + `-clientdata",` +
		`"signature":"` + canary + `-signature","attestationObject":"` + canary + `-attestation","userHandle":"` + canary + `-handle"}}}`

	for _, route := range passkeyAssertionRoutes {
		method, path, _ := strings.Cut(route, " ")
		t.Run(route, func(t *testing.T) {
			capture := &auditCaptureRepository{}
			audit := service.NewAuditLogService(capture, nil)
			audit.Start()
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUser), AuthSubject{UserID: 41})
				c.Set(string(ContextKeyUserRole), service.RoleAdmin)
				c.Next()
			})
			router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
			var received string
			router.Handle(method, path, func(c *gin.Context) {
				raw, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				received = string(raw)
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			audit.Stop()

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, body, received, "omitting the audit body must not consume the handler's request body")
			require.Len(t, capture.logs, 1)
			require.Equal(t, "<credential-bearing body omitted>", capture.logs[0].RequestBody)
			require.NotContains(t, capture.logs[0].RequestBody, canary)
		})
	}
}
