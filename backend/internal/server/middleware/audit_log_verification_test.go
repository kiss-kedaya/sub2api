package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// API Key 三选一身份校验（密码 / TOTP / Passkey）的审计契约：
//   - 请求体几乎全部是凭据的接口整体不入库（auditBodyOmittedRoutes）；
//   - 只允许附加因子名 / 结果 / 机器码三个标量字段；
//   - 通用 body 脱敏也必须覆盖 verification 里的 password / totp_code / token。
func TestAuditVerificationRoutesOmitCredentialBodies(t *testing.T) {
	for _, route := range []string{
		"PUT /api/v1/user/api-keys/:id/secret-seal",
		"POST /api/v1/user/api-keys/:id/reveal-authorize",
		"POST /api/v1/user/security/passkey/verify/finish",
	} {
		_, ok := auditBodyOmittedRoutes[route]
		require.True(t, ok, "credential-bearing route must omit request body: %s", route)
	}

	for _, key := range []string{
		"api_key_auth_factor",
		"api_key_verification",
		"api_key_verification_reason",
	} {
		_, ok := auditExtraAllowedKeys[key]
		require.True(t, ok, "verification audit extra key must be allowlisted: %s", key)
	}

	// 反向断言：这些名字不允许被附加（避免以后把密码/票据塞进审计）。
	for _, key := range []string{"password", "totp_code", "passkey_session_token", "reveal_grant"} {
		_, ok := auditExtraAllowedKeys[key]
		require.False(t, ok, "secret-bearing key must never be allowlisted in audit extra: %s", key)
	}

	// 动作映射：Passkey 校验与二次查看授权有独立动作名。
	require.Equal(t, service.AuditActionAPIKeyPasskeyVerify,
		auditActionOverrides["POST /api/v1/user/security/passkey/verify/finish"])
	require.Equal(t, service.AuditActionAPIKeyRevealAuthorize,
		auditActionOverrides["POST /api/v1/user/api-keys/:id/reveal-authorize"])
}

func TestAuditBodyRedactionMasksVerificationSecrets(t *testing.T) {
	body := []byte(`{
		"name":"k",
		"verification":{
			"method":"password",
			"password":"correct-horse-battery-staple",
			"totp_code":"123456",
			"passkey_session_token":"opaque-passkey-ticket-fixture"
		}
	}`)
	redacted := service.RedactAuditBody(body, "application/json")
	require.NotContains(t, redacted, "correct-horse-battery-staple")
	require.NotContains(t, redacted, "123456")
	require.NotContains(t, redacted, "opaque-passkey-ticket-fixture")
	require.Contains(t, redacted, `"method":"password"`)
}
