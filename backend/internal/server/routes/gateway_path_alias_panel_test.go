//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/stretchr/testify/require"
)

// TestGatewayPathAliasDoesNotRewriteRealAdminRoutes 用真实的管理面路由树（和网关路由挂在同一个
// 引擎上，生产也是这样）验证：别名层只改写网关路径，不会把 /api/v1/v1/admin/usage、
// /api/admin/usage 这类路径折叠/补齐到真实的管理路由上。这些路由的路径里都带 usage、models
// 之类的网关端点词，修复前全部会被改写后命中管理 handler。
func TestGatewayPathAliasDoesNotRewriteRealAdminRoutes(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	probe := &gatewayPathAliasAuthProbe{}
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
			AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
		},
		probe.middleware(),
		nil, nil, nil, nil, nil,
		&config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}},
	)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	for _, path := range []string{
		"/api/v1/v1/admin/usage",
		"/api/v1/v1/v1/admin/usage/stats",
		"/api/admin/usage",
		"/api/admin/usage/stats",
		"/api/v1/v1/admin/accounts/42/usage",
		"/api/admin/accounts/42/models",
		"/api/v1/v1/admin/dashboard/models",
		"/api/v1/v1/admin/channel-monitor-v2/models",
	} {
		probe.reset()
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

		require.Equal(t, http.StatusNotFound, w.Code, "path=%q 不能被别名到管理路由", path)
		require.Empty(t, probe.seen, "path=%q 不应进入网关鉴权", path)
	}

	// 网关路径照常别名：同一个引擎里的 /v1/v1/usage 折叠后进入网关鉴权（401）。
	probe.reset()
	w := httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/v1/usage", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, "/v1/usage", probe.last(t).Path)
}
