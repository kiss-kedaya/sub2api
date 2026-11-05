package routes

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// addressOwnersNoopNotifier only exists so the harness builds a non-nil
// cryptopay.Service; the owners route never notifies anyone.
type addressOwnersNoopNotifier struct{}

func (addressOwnersNoopNotifier) PushToAdmins(context.Context, string, string, string) error {
	return nil
}

const addressOwnersPath = "/api/v1/admin/crypto/address-owners"

func ownersRouteAddress(digit byte) string { return "0x" + strings.Repeat(string(digit), 40) }

func TestCryptoAddressOwnersIsInsideTheAdminSecurityGroup(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, failure := range []string{"no JWT", "machine key", "unconfigured admin", "public HTTP", "normal user", "inactive admin"} {
		t.Run(failure, func(t *testing.T) {
			owner := activeCryptoAdmin()
			h := newCryptoRouteHarness(t, owner, addressOwnersNoopNotifier{})
			peer, host := "127.0.0.1:1234", "localhost"
			switch failure {
			case "no JWT":
				h.identity.authenticated = false
			case "machine key":
				h.identity.method = "admin_api_key"
			case "unconfigured admin":
				owner.ID = 42
			case "public HTTP":
				peer, host = "203.0.113.1:1234", "example.test"
			case "normal user":
				owner.Role = "user"
			case "inactive admin":
				owner.Status = "disabled"
			}
			denied := cryptoRouteRequest(h.router, http.MethodGet, addressOwnersPath+"?address="+ownersRouteAddress('a'), "", nil, peer, host)
			require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
			require.NotContains(t, denied.Body.String(), "owners")
		})
	}
}

func TestCryptoAddressOwnersDoesNotAskForASecondFactor(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), addressOwnersNoopNotifier{})

	plain := cryptoRouteRequest(h.router, http.MethodGet, addressOwnersPath, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, plain.Code, plain.Body.String())
	require.JSONEq(t, `{"data":{"owners":{}}}`, plain.Body.String())
	require.Contains(t, plain.Header().Get("Cache-Control"), "no-store", "user identities must never be cached")

	// A code, even a wrong one, is neither required nor inspected nor consumed.
	withCode := cryptoRouteRequest(h.router, http.MethodGet, addressOwnersPath+"?address="+ownersRouteAddress('a')+"x", wrongCryptoRouteCode(t, 0), nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, withCode.Code, withCode.Body.String())
	require.NotContains(t, withCode.Body.String(), "CRYPTO_TOTP_REQUIRED")
	require.Zero(t, h.cache.attempts)
}

func TestCryptoAddressOwnersRejectsMoreThanOneHundredDistinctAddresses(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), addressOwnersNoopNotifier{})
	var query []string
	for i := 0; i < 101; i++ {
		query = append(query, fmt.Sprintf("address=0x%040x", i+1))
	}
	tooMany := cryptoRouteRequest(h.router, http.MethodGet, addressOwnersPath+"?"+strings.Join(query, "&"), "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusBadRequest, tooMany.Code, tooMany.Body.String())
	require.Contains(t, tooMany.Body.String(), "too many addresses")
}

func TestCryptoAddressOwnersIsReadOnlyAndHasNoLookalikeRoute(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), addressOwnersNoopNotifier{})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := cryptoRouteRequest(h.router, method, addressOwnersPath, "", nil, "127.0.0.1:1234", "localhost")
		require.Equal(t, http.StatusNotFound, r.Code, "%s must not be routed: %s", method, r.Body.String())
	}
	// The path is deliberately not under /addresses/, which already carries
	// /addresses/:id/... routes.
	var owners, lookalike int
	for _, route := range h.router.Routes() {
		switch {
		case route.Path == addressOwnersPath && route.Method == http.MethodGet:
			owners++
		case strings.HasPrefix(route.Path, "/api/v1/admin/crypto/addresses/owners"):
			lookalike++
		}
	}
	require.Equal(t, 1, owners)
	require.Zero(t, lookalike)
}

func TestCryptoAddressOwnersReachesTheHandlerInReadOnlyReview(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), addressOwnersNoopNotifier{})
	r := cryptoRouteRequest(h.router, http.MethodGet, addressOwnersPath, "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	require.NotContains(t, r.Body.String(), "CRYPTO_TEST_MODE_READ_ONLY")
}

func TestCryptoSweepRecordsAcceptPagingWithoutASecondFactor(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	h := newCryptoRouteHarness(t, activeCryptoAdmin(), addressOwnersNoopNotifier{})
	// A malformed cursor is rejected by the handler before any database use,
	// which also proves the request got past the security group without a code.
	r := cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/sweep/records?limit=999", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusBadRequest, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "invalid limit")
	require.NotContains(t, r.Body.String(), "CRYPTO_TOTP_REQUIRED")
	r = cryptoRouteRequest(h.router, http.MethodGet, "/api/v1/admin/crypto/sweep/records?before_id=0", "", nil, "127.0.0.1:1234", "localhost")
	require.Equal(t, http.StatusBadRequest, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), "invalid before_id")
}
