package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashAuditEmail(t *testing.T) {
	require.Empty(t, HashAuditEmail(""))
	require.Empty(t, HashAuditEmail("   "))

	first := HashAuditEmail("Someone@Example.com")
	require.True(t, strings.HasPrefix(first, "sha256:"))
	require.NotContains(t, first, "@")
	require.NotContains(t, strings.ToLower(first), "someone")
	// 大小写与空白归一化后稳定，便于跨事件串联同一账号。
	require.Equal(t, first, HashAuditEmail("  someone@example.com "))
	// 不同邮箱不同指纹。
	require.NotEqual(t, first, HashAuditEmail("someone.else@example.com"))
}

func TestClassifyAuthFailure(t *testing.T) {
	cases := []struct {
		reason string
		status int
		want   string
	}{
		{"invalid_state", 302, AuditFailureClassOAuthState},
		{"csrf", 302, AuditFailureClassOAuthState},
		{"provider_error", 302, AuditFailureClassOAuthProvider},
		{"token_exchange_failed", 302, AuditFailureClassOAuthToken},
		{"userinfo_failed", 302, AuditFailureClassOAuthUserInfo},
		{"missing_params", 302, AuditFailureClassOAuthState},
		{"TWO_FACTOR_INVALID_CODE", 400, AuditFailureClassTwoFactor},
		{"TOTP_CODE_REUSED", 400, AuditFailureClassTwoFactor},
		{"PENDING_AUTH_SESSION_NOT_FOUND", 400, AuditFailureClassPendingSession},
		{"INVITATION_REQUIRED", 403, AuditFailureClassInvitation},
		{"BACKEND_MODE_ADMIN_ONLY", 403, AuditFailureClassBackendMode},
		{"USER_NOT_ACTIVE", 403, AuditFailureClassAccountState},
		{"INVALID_CREDENTIALS", 401, AuditFailureClassPassword},
		{"", 429, AuditFailureClassRateLimit},
		{"", 500, AuditFailureClassInternal},
		{"weird_unknown", 400, AuditFailureClassOther},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, ClassifyAuthFailure(tc.reason, tc.status),
			"ClassifyAuthFailure(%q, %d)", tc.reason, tc.status)
	}
}

func TestRedactAuditQueryRedactsOAuthOneTimeCredentials(t *testing.T) {
	raw := "code=oauth-code-value&state=oauth-state-value&nonce=n-1&code_verifier=v-1&access_token=t-1&redirect=%2Fdashboard"
	redacted := RedactAuditQuery(raw)
	for _, secret := range []string{"oauth-code-value", "oauth-state-value", "n-1", "v-1", "t-1"} {
		require.NotContains(t, redacted, secret)
	}
	require.Contains(t, redacted, "redirect=")
}

func TestRedactAuditBodyRedactsOAuthOneTimeCredentials(t *testing.T) {
	body := []byte(`{"code":"oauth-code","state":"oauth-state","nonce":"nonce-1","code_verifier":"verifier-1","email_code":"123456","temp_token":"temp-1","email":"someone@example.com"}`)
	redacted := RedactAuditBody(body, "application/json")
	for _, secret := range []string{"oauth-code", "oauth-state", "nonce-1", "verifier-1", "123456", "temp-1"} {
		require.NotContains(t, redacted, secret)
	}
}
