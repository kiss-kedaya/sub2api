package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountCustomUsageRoutesRequireAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Account: adminhandler.NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)}}
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatus(401)
		} else {
			c.AbortWithStatus(403)
		}
	})
	RegisterAdminRoutes(router.Group("/api/v1"), h, auth, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }), nil, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/accounts/1/custom-usage-config"}, {http.MethodPut, "/api/v1/admin/accounts/1/custom-usage-config"}, {http.MethodPost, "/api/v1/admin/accounts/1/custom-usage-query"}, {http.MethodPost, "/api/v1/admin/accounts/custom-usage-batch"},
	} {
		for _, token := range []string{"", "non-admin"} {
			t.Run(route.method+route.path+token, func(t *testing.T) {
				r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
				r.Header.Set("Authorization", token)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, r)
				expected := 401
				if token != "" {
					expected = 403
				}
				require.Equal(t, expected, w.Code)
			})
		}
	}
}
