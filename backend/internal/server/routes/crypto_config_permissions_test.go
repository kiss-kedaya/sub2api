package routes

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (r *cryptoRouteSettingsRepo) GetValueUncached(ctx context.Context, key string) (string, error) {
	return r.GetValue(ctx, key)
}

func (r *cryptoRouteSettingsRepo) CompareAndSwapValue(_ context.Context, key string, expected *string, value string) (bool, error) {
	current, exists := r.values[key]
	if (expected == nil && exists) || (expected != nil && (!exists || current != *expected)) {
		return false, nil
	}
	r.values[key] = value
	return true, nil
}

func (r *cryptoRouteSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestCryptoConfigPublicAndPrivateRoutesSeparateSecretsAndConsumeCodeOnce(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	owner := activeCryptoAdmin()
	owner.ID = 1
	h := newCryptoRouteHarness(t, owner)
	const secret = "https://fixture-user:fixture-password@rpc.example.test/private?key=fixture-key"
	require.NoError(t, h.settings.SetRawSetting(t.Context(), cryptopay.SettingKeyCryptoConfig, `{"rpc":{"bsc":"`+secret+`"}}`))
	public := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	require.Equal(t, "{}", gjson.Get(public.Body.String(), "data.rpc").Raw)
	require.Equal(t, `["bsc"]`, gjson.Get(public.Body.String(), "data.rpc_configured").Raw)
	require.NotContains(t, public.Body.String(), "https://")
	require.NotContains(t, public.Body.String(), "fixture-password")
	require.Equal(t, "no-store", public.Header().Get("Cache-Control"))
	for _, code := range []string{"", wrongCryptoRouteCode(t, 0)} {
		denied := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", code, nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, denied, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
		require.NotContains(t, denied.Body.String(), secret)
	}
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	private := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, private.Code, private.Body.String())
	require.Equal(t, secret, gjson.Get(private.Body.String(), "data.rpc.bsc").String())
	replay := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config/private", code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, replay, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")
	require.NotContains(t, replay.Body.String(), secret)
	// A replayed/unneeded header does not turn a public read into a step-up
	// operation or make the public endpoint expose the private representation.
	public = cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config", code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	require.NotContains(t, public.Body.String(), secret)
}

func TestCryptoConfigSensitivePutConsumesFreshCodeExactlyOnceInHandler(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	owner := activeCryptoAdmin()
	owner.ID = 1
	h := newCryptoRouteHarness(t, owner)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	const path = "/api/v1/admin/crypto/config"
	const rpcPatch = `{"rpc":{"bsc":"https://first.example.test"}}`
	first := cryptoRouteRequestBody(h.router, http.MethodPut, path, rpcPatch, code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	// The same code cannot authorize another effective sensitive change.
	replayed := cryptoRouteRequestBody(h.router, http.MethodPut, path, `{"rpc":{"bsc":"https://second.example.test"}}`, code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, replayed, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")
	raw, err := h.settings.GetRawSetting(t.Context(), cryptopay.SettingKeyCryptoConfig)
	require.NoError(t, err)
	require.Equal(t, "https://first.example.test", gjson.Get(raw, "rpc.bsc").String())
	// An unrelated safe patch needs no new code and cannot erase the RPC value
	// hidden by GET /config.
	safe := cryptoRouteRequestBody(h.router, http.MethodPut, path, `{"scan_range":{"bsc":500}}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, safe.Code, safe.Body.String())
	raw, err = h.settings.GetRawSetting(t.Context(), cryptopay.SettingKeyCryptoConfig)
	require.NoError(t, err)
	require.Equal(t, "https://first.example.test", gjson.Get(raw, "rpc.bsc").String())
}

func TestCryptoConfigNoCodeExemptionsDoNotOpenOtherWritesOrUnboundPrivateAccess(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	owner := activeCryptoAdmin()
	owner.ID, owner.TotpEnabled, owner.TotpSecretEncrypted = 1, false, nil
	h := newCryptoRouteHarness(t, owner)
	safe := cryptoRouteRequestBody(h.router, http.MethodPut, "/api/v1/admin/crypto/config", `{"credit_max_amount":5000}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, safe.Code, safe.Body.String())
	for _, path := range []string{"/api/v1/admin/crypto/config/private", "/api/v1/admin/crypto/deposits.csv"} {
		private := cryptoRouteRequest(h.router, http.MethodGet, path, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, private, http.StatusForbidden, "CRYPTO_TOTP_NOT_SETUP")
	}
	for _, endpoint := range []struct{ path, body string }{
		{"/api/v1/admin/crypto/config", `{"enabled":true}`},
		{"/api/v1/admin/crypto/config", `{"credit_max_amount":5000,"rpc":{"bsc":"https://new.example.test"}}`},
		{"/api/v1/admin/crypto/sweep/run", `{}`},
	} {
		method := http.MethodPost
		if endpoint.path == "/api/v1/admin/crypto/config" {
			method = http.MethodPut
		}
		denied := cryptoRouteRequestBody(h.router, method, endpoint.path, endpoint.body, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, denied, http.StatusForbidden, "CRYPTO_TOTP_NOT_SETUP")
	}
}

func TestCryptoLowSensitivityReadsStillRequireOperatorJWTAndSecureTransport(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, path := range []string{"/api/v1/admin/crypto/config", "/api/v1/admin/crypto/hot-wallet", "/api/v1/admin/crypto/hot-wallet/address"} {
		for _, failure := range []string{"no JWT", "machine key", "unconfigured admin", "public HTTP"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				owner := activeCryptoAdmin()
				owner.ID = 1
				h := newCryptoRouteHarness(t, owner)
				peer, host := "127.0.0.1:1234", "localhost"
				switch failure {
				case "no JWT":
					h.identity.authenticated = false
				case "machine key":
					h.identity.method = service.AuditAuthMethodAdminAPIKey
				case "unconfigured admin":
					owner.ID = 42
				case "public HTTP":
					peer, host = "203.0.113.1:1234", "example.test"
				}
				denied := cryptoRouteRequest(h.router, http.MethodGet, path, "", nil, peer, host)
				require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
				require.NotContains(t, denied.Body.String(), "https://")
			})
		}
		owner := activeCryptoAdmin()
		owner.ID = 1
		h := newCryptoRouteHarness(t, owner)
		got := cryptoRouteRequest(h.router, http.MethodGet, path, "", nil, "127.0.0.1:1234", "localhost")
		// The fixture has no active wallet. Public wallet reads reach their
		// handler and fail on that fixture, not on a missing authentication code.
		if path == "/api/v1/admin/crypto/config" {
			require.Equal(t, http.StatusOK, got.Code, got.Body.String())
		} else {
			require.Equal(t, http.StatusBadRequest, got.Code, got.Body.String())
		}
		require.NotContains(t, got.Body.String(), "CRYPTO_TOTP_REQUIRED")
		require.Zero(t, h.cache.attempts)
	}
}
