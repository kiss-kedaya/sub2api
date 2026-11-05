package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The read-only review instance must be able to load the owner of each
// address, otherwise the sweep page shows a column of dashes there.
func TestCryptoAdminTestModeGuardAllowsOnlyTheGetOfAddressOwners(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(servermiddleware.CryptoAdminTestModeGuard())
	for _, path := range []string{
		"/api/v1/admin/crypto/address-owners",
		"/api/v1/admin/crypto/address-owners/export",
		"/api/v1/admin/crypto/addresses/owners",
		"/api/v1/admin/crypto/sweep/records",
	} {
		router.Any(path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/v1/admin/crypto/address-owners", http.StatusNoContent},
		{http.MethodGet, "/api/v1/admin/crypto/sweep/records", http.StatusNoContent},
		// Same level as /sweep/records: GET only, so HEAD stays closed for both.
		{http.MethodHead, "/api/v1/admin/crypto/address-owners", http.StatusForbidden},
		{http.MethodHead, "/api/v1/admin/crypto/sweep/records", http.StatusForbidden},
		{http.MethodPost, "/api/v1/admin/crypto/address-owners", http.StatusForbidden},
		{http.MethodPut, "/api/v1/admin/crypto/address-owners", http.StatusForbidden},
		{http.MethodDelete, "/api/v1/admin/crypto/address-owners", http.StatusForbidden},
		// Only the exact path is listed; look-alikes stay closed.
		{http.MethodGet, "/api/v1/admin/crypto/address-owners/export", http.StatusForbidden},
		{http.MethodGet, "/api/v1/admin/crypto/addresses/owners", http.StatusForbidden},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.want, res.Code, res.Body.String())
			if tc.want == http.StatusForbidden {
				require.Contains(t, res.Body.String(), "CRYPTO_TEST_MODE_READ_ONLY")
			}
		})
	}
}
