package routes

import (
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

const (
	cryptoScanRunsPath  = "/api/v1/admin/crypto/scan/runs"
	cryptoScanStartPath = "/api/v1/admin/crypto/scan/run?async=1"
	cryptoScanTestPeer  = "127.0.0.1:1234"
)

// cryptoScanRunsHarness builds a router whose handler has a (zero-value)
// payment service, so requests reach the scan handlers instead of the nil
// service shortcut.
func cryptoScanRunsHarness(t *testing.T) *cryptoRouteHarness {
	t.Helper()
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	return newCryptoRouteHarness(t, activeCryptoAdmin(), &walletRouteNotification{})
}

func TestCryptoScanRunCancelNeedsAFreshSecondFactor(t *testing.T) {
	for name, code := range map[string]string{"wrong code": wrongCryptoRouteCode(t, 0), "no code": ""} {
		t.Run(name, func(t *testing.T) {
			h := cryptoScanRunsHarness(t)
			response := cryptoRouteRequest(h.router, http.MethodPost, cryptoScanRunsPath+"/scan-x/cancel", code, nil, cryptoScanTestPeer, "localhost")
			requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
		})
	}
}

func TestCryptoScanRunCancelWithAValidCodeReachesTheHandler(t *testing.T) {
	h := cryptoScanRunsHarness(t)
	code, err := totp.GenerateCode(cryptoRouteTotpSecret, time.Now())
	require.NoError(t, err)
	response := cryptoRouteRequest(h.router, http.MethodPost, cryptoScanRunsPath+"/scan-missing/cancel", code, nil, cryptoScanTestPeer, "localhost")
	// The handler's own 404, not the router's: the route exists and the guard accepted one code.
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "scan run not found")
}

func TestCryptoScanRunReadsNeedNoSecondFactor(t *testing.T) {
	h := cryptoScanRunsHarness(t)
	list := cryptoRouteRequest(h.router, http.MethodGet, cryptoScanRunsPath, "", nil, cryptoScanTestPeer, "localhost")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	one := cryptoRouteRequest(h.router, http.MethodGet, cryptoScanRunsPath+"/scan-missing", "", nil, cryptoScanTestPeer, "localhost")
	require.Equal(t, http.StatusNotFound, one.Code, one.Body.String())
	require.Contains(t, one.Body.String(), "scan run not found")
}

func TestCryptoAsyncScanStartKeepsTheForwardCatchUpExemption(t *testing.T) {
	h := cryptoScanRunsHarness(t)
	response := cryptoRouteRequestBody(h.router, http.MethodPost, cryptoScanStartPath, `{"from_block":0}`, "", nil, cryptoScanTestPeer, "localhost")
	// Not 403: the guard let the forward catch-up through without a code. The
	// zero-value service has no running scanner, so the handler refuses to start.
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "Payment scanner unavailable")
	require.NotContains(t, response.Body.String(), "CRYPTO_TOTP_REQUIRED")
}

func TestCryptoAsyncScanRewindStillNeedsTheSecondFactor(t *testing.T) {
	h := cryptoScanRunsHarness(t)
	response := cryptoRouteRequestBody(h.router, http.MethodPost, cryptoScanStartPath, `{"from_block":12345}`, "", nil, cryptoScanTestPeer, "localhost")
	requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
}

func TestCryptoScanRunWritesAreBlockedOnAReadOnlyTestInstance(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	for name, tc := range map[string]struct{ path, body string }{
		"async start": {cryptoScanStartPath, `{"from_block":0}`},
		"cancel":      {cryptoScanRunsPath + "/scan-x/cancel", `{}`},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCryptoRouteHarness(t, activeCryptoAdmin(), &walletRouteNotification{})
			response := cryptoRouteRequestBody(h.router, http.MethodPost, tc.path, tc.body, "", nil, cryptoScanTestPeer, "localhost")
			requireCryptoReason(t, response, http.StatusForbidden, "CRYPTO_TEST_MODE_READ_ONLY")
		})
	}
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), &walletRouteNotification{})
	read := cryptoRouteRequest(h.router, http.MethodGet, cryptoScanRunsPath, "", nil, cryptoScanTestPeer, "localhost")
	require.Equal(t, http.StatusOK, read.Code, "reads stay available on a read-only test instance: %s", read.Body.String())
}
