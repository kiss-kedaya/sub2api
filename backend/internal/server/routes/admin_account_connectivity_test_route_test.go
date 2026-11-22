//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 管理端「测试设置 / 测试全部模型」对每个模型都调
// POST /api/v1/admin/accounts/:id/test（见 frontend/src/utils/accountModelTest.ts）。
// 该接口只用库内凭据探测上游连通性与模型可用性，响应里不含上游凭据，因此不应
// 要求 step-up：没有 grant 的真人会话必须直接拿到 200，而不是 403
// "This operation requires recent two-factor verification"。
//
// 这里用 mock handler 固定返回值，断言的是运行时门控没有被执行，而不是业务实现。
// 同一套门控下的凭据回读 / 导出 / 账号写操作必须继续返回 403。
func TestAccountConnectivityTestRouteSkipsStepUp(t *testing.T) {
	const testPath = "/api/v1/admin/accounts/:id/test"

	require.False(t, adminroutepolicy.IsStepUpRequired(http.MethodPost, testPath),
		"账号连通性测试不得要求 step-up")

	// 真实路由树里必须有这条路由（防止策略与路由漂移）。
	router := buildFullAdminRouteTree(t, stepUpProbe())
	registered := map[string]struct{}{}
	for _, route := range adminRoutesOf(router) {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	require.Contains(t, registered, "POST "+testPath, "生产路由树缺少账号测试路由")

	// 真实路由树里请求该接口：守卫不得拦（空桩 handler panic 由 Recovery 吸收，
	// 这里只断言没有走到 step-up 的 403 分支）。
	wReal := httptest.NewRecorder()
	router.ServeHTTP(wReal, httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil))
	require.NotEqual(t, http.StatusForbidden, wReal.Code, "真实路由树仍对该接口执行 step-up 门控")
	require.NotContains(t, wReal.Body.String(), "STEP_UP_REQUIRED")

	// 运行时门控断言：同一守卫下挂 mock handler。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	admin := r.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(adminRoutesTestAuth(service.AuditAuthMethodJWT)))
	admin.Use(adminStepUpRouteGuard(stepUpProbe()))
	mockHandler := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "account_id": c.Param("id")})
	}
	admin.POST("/accounts/:id/test", mockHandler)
	// 边界路由：只放开「测试」，凭据读取/导出与账号写操作保持 step-up。
	admin.GET("/accounts/:id/credential", mockHandler)
	admin.GET("/accounts/data", mockHandler)
	admin.POST("/accounts", mockHandler)
	admin.PUT("/accounts/:id", mockHandler)
	admin.DELETE("/accounts/:id", mockHandler)

	// 无 grant 会话调测试接口：200，不再 403 STEP_UP_REQUIRED。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "STEP_UP_REQUIRED")

	// 边界：凭据回读/导出/创建/编辑/删除仍然要求 step-up。
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/accounts/1/credential"},
		{http.MethodGet, "/api/v1/admin/accounts/data"},
		{http.MethodPost, "/api/v1/admin/accounts"},
		{http.MethodPut, "/api/v1/admin/accounts/1"},
		{http.MethodDelete, "/api/v1/admin/accounts/1"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, http.StatusForbidden, w.Code, "%s %s 必须仍被 step-up 拦截", route.method, route.path)
		require.Contains(t, w.Body.String(), "STEP_UP_REQUIRED", "%s %s 必须仍被 step-up 拦截", route.method, route.path)
	}
}
