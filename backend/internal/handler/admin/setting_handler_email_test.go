//go:build unit

package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func bindSMTPRequest[T any](t *testing.T, body string) T {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	var payload T
	require.NoError(t, c.ShouldBindJSON(&payload))
	return payload
}

func TestTestSMTPRequestUseTLSOmissionFallsBackToSavedSetting(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{}`)

	require.Nil(t, req.SMTPUseTLS)
	require.True(t, resolveSMTPUseTLS(req.SMTPUseTLS, &service.SMTPConfig{UseTLS: true}))
}

func TestTestSMTPRequestExplicitFalseOverridesSavedUseTLS(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{"smtp_use_tls":false}`)

	require.NotNil(t, req.SMTPUseTLS)
	require.False(t, resolveSMTPUseTLS(req.SMTPUseTLS, &service.SMTPConfig{UseTLS: true}))
}

func TestTestSMTPRequestExplicitTrueOverridesMissingSavedUseTLS(t *testing.T) {
	req := bindSMTPRequest[TestSMTPRequest](t, `{"smtp_use_tls":true}`)

	require.NotNil(t, req.SMTPUseTLS)
	require.True(t, resolveSMTPUseTLS(req.SMTPUseTLS, nil))
}

func TestSendTestEmailRequestPreservesUseTLSOmissionSemantics(t *testing.T) {
	omitted := bindSMTPRequest[SendTestEmailRequest](t, `{"email":"admin@example.com"}`)
	explicitFalse := bindSMTPRequest[SendTestEmailRequest](t, `{"email":"admin@example.com","smtp_use_tls":false}`)
	saved := &service.SMTPConfig{UseTLS: true}

	require.True(t, resolveSMTPUseTLS(omitted.SMTPUseTLS, saved))
	require.False(t, resolveSMTPUseTLS(explicitFalse.SMTPUseTLS, saved))
}

// 对外错误必须是固定分类：不能把 host/port、也不能把「策略拒绝 / 解析失败 /
// 拒绝连接」的差异带出去，否则等于给了一个内网 host:port 状态预言机。
func TestSMTPTestFailureMessageDoesNotLeakTargetDetails(t *testing.T) {
	for _, err := range []error{
		service.ErrSMTPTargetBlocked,
		service.ErrEmailNotConfigured,
	} {
		message := smtpTestPublicMessage(err)
		require.Equal(t, "SMTP connection test failed: unable to connect or authenticate", message)
		require.NotContains(t, message, "127.0.0.1")
		require.NotContains(t, message, "target not allowed")
	}
}
