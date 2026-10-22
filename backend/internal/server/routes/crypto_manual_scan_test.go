package routes

import (
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

func TestCryptoManualScanRequiresFreshCodeAndRejectsReplay(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	path := "/api/v1/admin/crypto/scan/run"
	missing := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, missing, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	invalid := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, invalid, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	valid := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusServiceUnavailable, valid.Code, valid.Body.String()) // harness has no scanner
	require.Contains(t, valid.Header().Get("Cache-Control"), "no-store")
	replay := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, code, nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, replay, http.StatusConflict, "CRYPTO_TOTP_REPLAYED")
}

func TestCryptoManualScanIsReadOnlyInRestrictedReview(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	r := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/scan/run", `{}`, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")
}
