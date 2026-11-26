package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 「创建 API Key 时是否需要验证身份」开关（PUT /user/security/api-key-create-verification）的审计契约：
//   - 请求体可能带 verification（密码 / 动态码 / 通行密钥票据），整体不入库；
//   - 固定动作名；
//   - extra 只允许改前 / 改后标签、是否放宽，以及因子名 / 结果 / 机器码，凭据字段一律进不来。

const createVerificationAuditRoute = "PUT /api/v1/user/security/api-key-create-verification"

func TestAuditCreateVerificationRouteContract(t *testing.T) {
	require.Contains(t, auditBodyOmittedRoutes, createVerificationAuditRoute,
		"the request body may carry a password / TOTP code / passkey ticket")
	require.Equal(t, service.AuditActionAPIKeyCreateVerificationSet, auditActionOverrides[createVerificationAuditRoute])
	require.Equal(t, "api_key.create_verification.set", service.AuditActionAPIKeyCreateVerificationSet)

	for _, key := range []string{
		"api_key_create_verification_from",
		"api_key_create_verification_to",
		"api_key_create_verification_relaxed",
		"api_key_verification",
		"api_key_verification_reason",
		"api_key_auth_factor",
	} {
		require.Contains(t, auditExtraAllowedKeys, key)
	}
	for _, key := range []string{"password", "totp_code", "passkey_session_token", "verification", "mode"} {
		require.NotContains(t, auditExtraAllowedKeys, key, "a credential-bearing or free-form key must never be allowlisted: %s", key)
	}
}

func TestAuditCreateVerificationExtraKeepsLabelsAndDropsCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetAuditExtra(c, map[string]any{
		"api_key_create_verification_from":    "auto",
		"api_key_create_verification_to":      "off",
		"api_key_create_verification_relaxed": true,
		"api_key_verification":                "verified",
		"api_key_auth_factor":                 "password",
		// 下面这些必须被白名单挡掉。
		"password":                "hunter2-canary",
		"totp_code":               "123456",
		"passkey_session_token":   "ticket-canary",
		"verification":            map[string]any{"password": "hunter2-canary"},
		"api_key_create_secret_x": "hunter2-canary",
	})
	value, ok := c.Get(auditCtxKeyExtra)
	require.True(t, ok)
	fields, ok := value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"api_key_create_verification_from":    "auto",
		"api_key_create_verification_to":      "off",
		"api_key_create_verification_relaxed": true,
		"api_key_verification":                "verified",
		"api_key_auth_factor":                 "password",
	}, fields)
}

// 端到端：带凭据的请求走过真实审计中间件，落库的只有固定动作名 + 标签，请求体被省略，
// 而且 handler 仍然读得到完整的请求体（省略审计不能影响业务）。
func TestAuditCreateVerificationNeverPersistsRequestCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const canary = "audit-create-verification-canary"
	body := `{"mode":"off","verification":{"method":"password","password":"` + canary + `-pw","totp_code":"` + canary + `-otp","passkey_session_token":"` + canary + `-ticket"}}`

	capture := &auditCaptureRepository{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 7})
		c.Set(string(ContextKeyUserRole), service.RoleUser)
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	var received string
	router.PUT("/api/v1/user/security/api-key-create-verification", func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		received = string(raw)
		SetAuditExtra(c, map[string]any{
			"api_key_verification":                "verified",
			"api_key_auth_factor":                 "password",
			"api_key_create_verification_from":    "auto",
			"api_key_create_verification_to":      "off",
			"api_key_create_verification_relaxed": true,
		})
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/user/security/api-key-create-verification", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	audit.Stop()

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, body, received, "omitting the audit body must not consume the handler's request body")
	require.Len(t, capture.logs, 1)
	entry := capture.logs[0]
	require.Equal(t, service.AuditActionAPIKeyCreateVerificationSet, entry.Action)
	require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
	require.Equal(t, "off", entry.Extra["api_key_create_verification_to"])
	require.Equal(t, "auto", entry.Extra["api_key_create_verification_from"])
	require.Equal(t, true, entry.Extra["api_key_create_verification_relaxed"])
	require.Equal(t, "password", entry.Extra["api_key_auth_factor"])
	require.NotContains(t, entry.RequestBody, canary)
	for key, value := range entry.Extra {
		if text, ok := value.(string); ok {
			require.NotContains(t, text, canary, key)
		}
	}
}
