package routes

import (
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

func TestCryptoForwardManualScanDoesNotRequireFreshCode(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	path := "/api/v1/admin/crypto/scan/run"
	response := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String()) // handler was reached; harness has no scanner
	require.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	// An optional TOTP header is not inspected or consumed by this idempotent
	// cursor-forward operation.
	optional := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{}`, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusServiceUnavailable, optional.Code, optional.Body.String())

	rewind := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{"from_block":12345}`, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, rewind, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	verified := cryptoRouteRequestBody(h.router, http.MethodPost, path, `{"from_block":12345}`, code, nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusServiceUnavailable, verified.Code, verified.Body.String())
}

func TestCryptoManualScanIsReadOnlyInRestrictedReview(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin())
	r := cryptoRouteRequestBody(h.router, http.MethodPost, "/api/v1/admin/crypto/scan/run", `{}`, "", nil, "127.0.0.1:1234", "localhost")
	requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")
}
