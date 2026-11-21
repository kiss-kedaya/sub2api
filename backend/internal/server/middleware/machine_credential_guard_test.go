//go:build unit

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func mountMachineCredentialGuard(router *gin.Engine, path string) {
	router.GET(path, ForbidMachineCredentials(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
}

func TestForbidMachineCredentialsRejectsAdminAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		c.Next()
	})
	mountMachineCredentialGuard(router, "/admin/redeem-codes")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/redeem-codes", nil))
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), MachineCredentialForbiddenCode)
}

func TestForbidMachineCredentialsAllowsHumanSession(t *testing.T) {
	for _, authMethod := range []string{service.AuditAuthMethodJWT, ""} {
		t.Run("auth_method="+authMethod, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			if authMethod != "" {
				router.Use(func(c *gin.Context) {
					c.Set("auth_method", authMethod)
					c.Next()
				})
			}
			mountMachineCredentialGuard(router, "/admin/redeem-codes")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/redeem-codes", nil))
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}
