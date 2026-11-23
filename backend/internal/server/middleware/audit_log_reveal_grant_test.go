package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// X-Reveal-Grant 是 #567 引入的「5 分钟一次性」二次查看授权票据：
// 它和密码 / TOTP 同等级别，落进 audit_logs 就等于长期留存一份可用票据。
// 审计只能记「带了哪个 header」，不能记票据内容。
func TestMaskedRequestCredentialMasksRevealGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const grant = "rg1.audit-canary-reveal-grant.audit-canary-reveal-signature"

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", nil)
	ctx.Request.Header.Set("X-Reveal-Grant", grant)

	masked := MaskedRequestCredential(ctx)
	require.Equal(t, "x-reveal-grant <redacted>", masked)
	require.NotContains(t, masked, "audit-canary")
	require.NotContains(t, masked, grant)

	// 没有该 header 时不能被误判成有凭证。
	plain, _ := gin.CreateTestContext(httptest.NewRecorder())
	plain.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", nil)
	require.Empty(t, MaskedRequestCredential(plain))

	// 真值空串（Header.Set 空值）也算「带了」，走完全遮蔽分支而不是留空。
	blank, _ := gin.CreateTestContext(httptest.NewRecorder())
	blank.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/1/secret-seal", nil)
	blank.Request.Header.Set("X-Reveal-Grant", "")
	require.Equal(t, "x-reveal-grant <redacted>", MaskedRequestCredential(blank))
}

// GET /user/api-keys/:id/secret-seal 是把（加密后的）API Key 交给前端解密的敏感读，
// 必须同时满足：进敏感读白名单、动作名稳定、且被登记为不记录请求体。
func TestAPIKeySecretSealFetchRouteIsAudited(t *testing.T) {
	const route = "GET /api/v1/user/api-keys/:id/secret-seal"

	action, ok := auditSensitiveReads[route]
	require.True(t, ok, "secret-seal 读取必须进敏感读白名单：%s", route)
	require.Equal(t, service.AuditActionAPIKeySecretFetch, action)

	_, omitted := auditBodyOmittedRoutes[route]
	require.True(t, omitted, "secret-seal 读取必须登记为不记录请求体：%s", route)

	// 敏感读清单里的每个条目都必须带非空动作名，否则审计里分不清是什么读。
	for readRoute, readAction := range auditSensitiveReads {
		require.NotEmptyf(t, readAction, "敏感读缺少动作名：%s", readRoute)
	}
}

// 端到端：带 X-Reveal-Grant 读密文时，审计记录里只有掩码点位，没有票据内容。
func TestSecretSealFetchAuditNeverPersistsRevealGrant(t *testing.T) {
	const (
		path  = "/api/v1/user/api-keys/:id/secret-seal"
		grant = "rg1.audit-canary-reveal-grant.audit-canary-reveal-signature"
	)
	gin.SetMode(gin.TestMode)

	capture := &auditCaptureRepository{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
		c.Set(string(ContextKeyUserRole), service.RoleUser)
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.GET(path, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"ciphertext": "audit-canary-ciphertext"}})
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys/9/secret-seal", nil)
	request.Header.Set("X-Reveal-Grant", grant)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	audit.Stop()

	require.Equal(t, http.StatusOK, recorder.Code)
	capture.mu.Lock()
	logs := append([]*service.AuditLog(nil), capture.logs...)
	capture.mu.Unlock()
	require.Len(t, logs, 1)

	entry := logs[0]
	require.Equal(t, service.AuditActionAPIKeySecretFetch, entry.Action)
	require.Equal(t, "x-reveal-grant <redacted>", entry.CredentialMasked)
	require.NotContains(t, entry.CredentialMasked, "audit-canary")
	require.NotContains(t, entry.RequestBody, "audit-canary")
	require.NotContains(t, entry.Path, "audit-canary")
}

// 写请求：封存请求体里的密文与 salt 仍然整体不入库（回归保护 #335/#567）。
func TestSecretSealPutBodyStillOmitted(t *testing.T) {
	const path = "/api/v1/user/api-keys/:id/secret-seal"
	gin.SetMode(gin.TestMode)

	capture := &auditCaptureRepository{}
	audit := service.NewAuditLogService(capture, nil)
	audit.Start()

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.PUT(path, func(c *gin.Context) { c.Status(http.StatusOK) })

	body := `{"ciphertext":"audit-canary-ciphertext","salt":"audit-canary-salt","verification":{"password":"audit-canary-password"}}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/user/api-keys/9/secret-seal", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), request)
	audit.Stop()

	require.Len(t, capture.logs, 1)
	require.Equal(t, "<credential-bearing body omitted>", capture.logs[0].RequestBody)
	require.NotContains(t, capture.logs[0].RequestBody, "audit-canary")
}
