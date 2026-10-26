package routes

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const privateKeyExportTestPath = "/api/v1/admin/crypto/addresses/17/export-private-key"

func TestCryptoAddressPrivateKeyExportEnforcesRegisteredSecurityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		want         int
		change       func(*cryptoRouteHarness)
	}{
		{name: "missing code", want: 403, reason: "CRYPTO_TOTP_REQUIRED"},
		{name: "unauthenticated", want: 403, reason: "CRYPTO_ADMIN_REQUIRED", change: func(h *cryptoRouteHarness) { h.identity.authenticated = false }},
		{name: "machine credential", want: 403, reason: "CRYPTO_ADMIN_REQUIRED", change: func(h *cryptoRouteHarness) { h.identity.method = service.AuditAuthMethodAdminAPIKey }},
		{name: "observer", want: 403, reason: "CRYPTO_OPERATOR_REQUIRED", change: func(h *cryptoRouteHarness) { h.user.ID = 42 }},
		{name: "disabled operator", want: 403, reason: "CRYPTO_ADMIN_REQUIRED", change: func(h *cryptoRouteHarness) { h.user.Status = service.StatusDisabled }},
		{name: "demoted operator", want: 403, reason: "CRYPTO_ADMIN_REQUIRED", change: func(h *cryptoRouteHarness) { h.user.Role = service.RoleUser }},
		{name: "no authenticator", want: 403, reason: "CRYPTO_TOTP_NOT_SETUP", change: func(h *cryptoRouteHarness) { h.user.TotpEnabled = false; h.user.TotpSecretEncrypted = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			h := newCryptoRouteHarness(t, activeCryptoAdmin())
			if tc.change != nil {
				tc.change(h)
			}
			result := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, "", nil, "127.0.0.1:1234", "localhost")
			requireCryptoReason(t, result, tc.want, tc.reason)
			require.Contains(t, result.Header().Get("Cache-Control"), "no-store")
			require.False(t, gjson.Get(result.Body.String(), "private_key").Exists())
		})
	}
}

func TestCryptoAddressPrivateKeyExportCodeReplayAndNotifications(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	push := &walletRouteNotification{}
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), push)
	intent := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/operation-intent", `{"action":"export_private_key"}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, intent.Code, intent.Body.String())
	require.Len(t, push.messages, 1)
	require.Contains(t, push.messages[0], "导出单地址私钥")
	invalid := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, invalid, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	require.Len(t, push.messages, 1)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	valid := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, code, nil, "127.0.0.1:1234", "localhost")
	// No wallet is configured in this harness. 503 proves authentication and
	// its security notification ran before reaching the registered export.
	require.Equal(t, http.StatusServiceUnavailable, valid.Code)
	require.Len(t, push.messages, 2)
	require.Contains(t, push.messages[1], "服务器已验证当前支付授权")
	replay := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, replay, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")
	require.Len(t, push.messages, 2)
}

func TestCryptoGrantAddressPrivateKeyExportKeepsExpiryAndSessionBinding(t *testing.T) {
	for _, test := range []string{"valid", "wrong session", "expired", "notification failed"} {
		t.Run(test, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			push := &walletRouteNotification{}
			h := newCryptoRouteHarness(t, activeCryptoAdmin(), push)
			code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
			require.NoError(t, err)
			issued := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/verify", `{"totp_code":"`+code+`"}`, "", nil, "127.0.0.1:1234", "localhost")
			require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
			grant := gjson.Get(issued.Body.String(), "data.credential").String()
			require.NotEmpty(t, grant)
			switch test {
			case "wrong session":
				h.identity.sessionID = "different-session"
			case "expired":
				h.mini.FastForward(6 * time.Minute)
			case "notification failed":
				push.fail = true
			}
			result := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, "", map[string]string{"X-Crypto-Grant": grant}, "127.0.0.1:1234", "localhost")
			switch test {
			case "wrong session", "expired":
				requireCryptoReason(t, result, http.StatusForbidden, "CRYPTO_GRANT_REQUIRED")
				require.Empty(t, push.messages)
			case "notification failed":
				requireCryptoReason(t, result, http.StatusServiceUnavailable, "CRYPTO_NOTIFICATION_UNAVAILABLE")
			default:
				require.Equal(t, http.StatusServiceUnavailable, result.Code)
				require.Contains(t, result.Body.String(), "Private key export unavailable")
				require.Len(t, push.messages, 1)
			}
		})
	}
}

func TestCryptoAddressPrivateKeyExportKeepsTransportAndReadOnlyGuards(t *testing.T) {
	for _, tc := range []struct{ name, reason string }{
		{"public http", "CRYPTO_HTTPS_REQUIRED"},
		{"read only", "CRYPTO_TEST_MODE_READ_ONLY"},
		{"disabled instance", "CRYPTO_DISABLED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			h := newCryptoRouteHarness(t, activeCryptoAdmin())
			peer, host := "127.0.0.1:1234", "localhost"
			switch tc.name {
			case "public http":
				t.Setenv("CRYPTO_ADMIN_ALLOW_HTTP", "false")
				peer, host = "203.0.113.1:1234", "example.test"
			case "read only":
				t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
				t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
			case "disabled instance":
				t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
			}
			result := cryptoRouteRequest(h.router, http.MethodPost, privateKeyExportTestPath, "", nil, peer, host)
			require.GreaterOrEqual(t, result.Code, 400)
			require.Contains(t, result.Body.String(), tc.reason)
		})
	}
}
