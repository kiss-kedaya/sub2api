//go:build unit

package routes

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestScopedAdminKeyManagementRoutesRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	stepUp := middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.AbortWithStatusJSON(403, gin.H{"reason": "STEP_UP_REQUIRED"}) })
	RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{Setting: adminhandler.NewSettingHandler(nil, nil, nil, nil, nil, nil, nil)}}, middleware.AdminAuthMiddleware(func(c *gin.Context) { c.Set("auth_method", "jwt"); c.Next() }), middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), stepUp, nil, nil)
	// 旧版全权密钥的 regenerate 已退役（不再接受新建/轮换），不参与 step-up 断言；
	// 其退役响应由 handler 测试 TestRegenerateLegacyAdminAPIKeyIsRetired 覆盖。
	for _, test := range []struct{ method, path string }{
		{"DELETE", "/api/v1/admin/settings/admin-api-key"},
		{"POST", "/api/v1/admin/settings/admin-api-keys"},
		{"PUT", "/api/v1/admin/settings/admin-api-keys/key-1"},
		{"POST", "/api/v1/admin/settings/admin-api-keys/key-1/rotate"},
		{"DELETE", "/api/v1/admin/settings/admin-api-keys/key-1"},
		{"DELETE", "/api/v1/admin/settings/admin-api-keys/key-1/record"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		require.Equal(t, 403, w.Code, test.method+" "+test.path)
		require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED")
	}
}
