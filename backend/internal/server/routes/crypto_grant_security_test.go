package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func cryptoGrantRouteHarness(t *testing.T) (*cryptoRouteHarness, string) {
	t.Helper()
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	owner := activeCryptoAdmin()
	owner.ID = 1
	h := newCryptoRouteHarness(t, owner)
	require.NoError(t, h.settings.SetRawSetting(t.Context(), cryptopay.SettingKeyCryptoConfig, `{"rpc":{"bsc":"https://public.example.test"}}`))
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	issued := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/verify", `{"totp_code":"`+code+`"}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	require.Contains(t, issued.Header().Get("Cache-Control"), "no-store")
	require.EqualValues(t, 300, gjson.Get(issued.Body.String(), "data.expires_in").Int())
	credential := gjson.Get(issued.Body.String(), "data.credential").String()
	require.NotEmpty(t, credential)
	return h, credential
}

func TestCryptoGrantAuthorizesRepeatedProtectedRequestsButCannotRenewItself(t *testing.T) {
	h, credential := cryptoGrantRouteHarness(t)
	for range 3 {
		result := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
		require.Equal(t, http.StatusOK, result.Code, result.Body.String())
		require.Equal(t, "https://public.example.test", gjson.Get(result.Body.String(), "data.rpc.bsc").String())
	}
	denied := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/verify", `{}`, "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusBadRequest, denied.Code)
	h.mini.FastForward(5 * time.Minute)
	expired := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, expired, http.StatusForbidden, "CRYPTO_GRANT_REQUIRED")
	require.NotContains(t, expired.Body.String(), "public.example.test")
}

func TestCryptoGrantCannotCrossSessionsOrSurviveAuthenticatorChanges(t *testing.T) {
	h, credential := cryptoGrantRouteHarness(t)
	h.identity.sessionID = "another-session"
	result := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, result, http.StatusForbidden, "CRYPTO_GRANT_REQUIRED")
	h.identity.sessionID = "route-test-session"
	secret := "another-ciphertext"
	h.user.TotpSecretEncrypted = &secret
	result = cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, result, http.StatusForbidden, "CRYPTO_GRANT_REQUIRED")
}

func TestCryptoGrantRejectsAmbiguousCredentialHeadersBeforeProtectedRead(t *testing.T) {
	h, credential := cryptoGrantRouteHarness(t)
	for _, tc := range []struct {
		name   string
		values []string
		code   string
	}{
		{"duplicate", []string{credential, credential}, ""},
		{"comma-joined", []string{credential + "," + credential}, ""},
		{"mixed", []string{credential}, "123456"},
		{"empty", []string{""}, ""},
		{"forged", []string{"cpg1.self.issued"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/crypto/config/private", strings.NewReader(`{}`))
			req.RemoteAddr, req.Host = "127.0.0.1:1234", "localhost"
			req.Header.Set("Authorization", "Bearer route-test-token")
			for _, value := range tc.values {
				req.Header.Add("X-Crypto-Grant", value)
			}
			if tc.code != "" {
				req.Header.Set("X-Crypto-TOTP", tc.code)
			}
			result := httptest.NewRecorder()
			h.router.ServeHTTP(result, req)
			requireCryptoReason(t, result, http.StatusForbidden, "CRYPTO_GRANT_REQUIRED")
			require.NotContains(t, result.Body.String(), "public.example.test")
		})
	}
}

func TestCryptoGrantFailsClosedWhenSharedRegistrationIsUnavailable(t *testing.T) {
	h, credential := cryptoGrantRouteHarness(t)
	h.mini.Close()
	result := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, result, http.StatusServiceUnavailable, "CRYPTO_TOTP_REQUIRED")
	require.NotContains(t, result.Body.String(), "public.example.test")
}

func TestCryptoGrantVerificationRetainsIdentityTransportAndStrictBodyGuards(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		edit   func(*cryptoRouteHarness)
		peer   string
		host   string
		status int
	}{
		{name: "missing-code", body: `{}`, status: http.StatusForbidden},
		{name: "unknown-field", body: `{"totp_code":"123456","credential":"forged"}`, status: http.StatusBadRequest},
		{name: "trailing-json", body: `{} {}`, status: http.StatusBadRequest},
		{name: "unauthenticated", body: `{}`, edit: func(h *cryptoRouteHarness) { h.identity.authenticated = false }, status: http.StatusForbidden},
		{name: "machine-key", body: `{}`, edit: func(h *cryptoRouteHarness) { h.identity.method = "admin_api_key" }, status: http.StatusForbidden},
		{name: "not-an-operator", body: `{}`, edit: func(h *cryptoRouteHarness) { h.user.ID = 42 }, status: http.StatusForbidden},
		{name: "public-http", body: `{}`, peer: "203.0.113.1:1234", host: "example.test", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
			t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
			h := newCryptoRouteHarness(t, activeCryptoAdmin())
			if tc.edit != nil {
				tc.edit(h)
			}
			peer, host := tc.peer, tc.host
			if peer == "" {
				peer, host = "127.0.0.1:1234", "localhost"
			}
			result := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/verify", tc.body, "", nil, peer, host)
			require.Equal(t, tc.status, result.Code, result.Body.String())
			require.False(t, gjson.Get(result.Body.String(), "data.credential").Exists())
		})
	}
}

func TestCryptoGrantDoesNotBypassPerOperationSecurityNotifications(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	push := &walletRouteNotification{}
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), push)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	issued := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/security/verify", `{"totp_code":"`+code+`"}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, issued.Code, issued.Body.String())
	credential := gjson.Get(issued.Body.String(), "data.credential").String()
	for count := 1; count <= 2; count++ {
		result := cryptoRouteRequest(h.router, http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
		// The zero wallet returns 503 after the still-mandatory notification.
		require.Equal(t, http.StatusServiceUnavailable, result.Code)
		require.Len(t, push.messages, count)
		require.NotContains(t, result.Body.String(), "CRYPTO_GRANT_REQUIRED")
	}
	push.fail = true
	result := cryptoRouteRequest(h.router, http.MethodPost, "/api/v1/admin/crypto/wallet/export-mnemonics", "", map[string]string{"X-Crypto-Grant": credential}, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, result, http.StatusServiceUnavailable, "CRYPTO_NOTIFICATION_UNAVAILABLE")
}
