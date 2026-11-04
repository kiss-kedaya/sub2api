package routes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

const chainStatusPath = "/api/v1/admin/crypto/chains/status"

func chainStatusHarness(t *testing.T) *cryptoRouteHarness {
	t.Helper()
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	// A notifier makes the harness build a (zero-value) crypto service, so the
	// request reaches the real handler instead of its nil-service branch.
	return newCryptoRouteHarness(t, activeCryptoAdmin(), &walletRouteNotification{})
}

func TestChainStatusRouteIsASingleReadOnlyGET(t *testing.T) {
	h := chainStatusHarness(t)
	var methods []string
	for _, route := range h.router.Routes() {
		if route.Path == chainStatusPath {
			methods = append(methods, route.Method)
		}
	}
	require.Equal(t, []string{http.MethodGet}, methods)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := cryptoRouteRequest(h.router, method, chainStatusPath, "", nil, "127.0.0.1:1234", "localhost")
		require.Equal(t, http.StatusNotFound, r.Code, method)
	}
}

// A plain GET needs the administrator session and a secure transport, nothing
// more: no fresh code, and no special case for this path.
func TestChainStatusGETDoesNotRequireTwoFactorAndNothingAroundItChanged(t *testing.T) {
	h := chainStatusHarness(t)
	ok := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.Contains(t, ok.Header().Get("Cache-Control"), "no-store")
	require.Contains(t, ok.Body.String(), `"data":{"generated_at"`)
	require.Contains(t, ok.Body.String(), `"chains":[]`)

	// The header is not consulted, so polling with a stale or wrong code neither
	// fails nor burns the verification attempt budget (it would trip at 5).
	for range 8 {
		r := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	}

	// The neighbours behave exactly as before: ordinary reads need no code, the
	// sensitive reads still do.
	plain := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/config", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
	for _, path := range []string{"/api/v1/admin/crypto/config/private", "/api/v1/admin/crypto/deposits.csv"} {
		r := cryptoRouteRequest(h.router, http.MethodGet, path, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_TOTP_REQUIRED")
	}
}

// Only the group guard decides who gets in: these are the same refusals every
// other route in the group gives.
func TestChainStatusRouteIsInsideTheCryptoAdminSecurityGroup(t *testing.T) {
	t.Run("not authenticated", func(t *testing.T) {
		h := chainStatusHarness(t)
		h.identity.authenticated = false
		r := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_ADMIN_REQUIRED")
	})
	t.Run("administrator who is not a payment operator", func(t *testing.T) {
		h := chainStatusHarness(t)
		h.user.ID = 42
		r := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_OPERATOR_REQUIRED")
	})
	t.Run("crypto disabled on this instance", func(t *testing.T) {
		h := chainStatusHarness(t)
		t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
		r := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, "", nil, "127.0.0.1:1234", "localhost")
		requireCryptoReason(t, r, http.StatusServiceUnavailable, "CRYPTO_DISABLED")
	})
	t.Run("insecure transport", func(t *testing.T) {
		h := chainStatusHarness(t)
		r := cryptoRouteRequest(h.router, http.MethodGet, chainStatusPath, "", map[string]string{"X-Forwarded-Proto": "https"}, "203.0.113.9:443", "kedaya.ai")
		requireCryptoReason(t, r, http.StatusForbidden, "CRYPTO_HTTPS_REQUIRED")
	})
}
