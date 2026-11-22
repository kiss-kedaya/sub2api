//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// lowRiskReleasedRoutes 是 2026-11「2FA 触发必要性矩阵」复核后放开的一批管理面
// 动作：只做探测/预览/聚合或返回掩码读模型，响应不含凭据/密码/密钥/卡密明文，
// 不动钱、不改权限、不改安全配置。
//
// 放开理由逐条记录在 adminroutepolicy 的 Reason 常量与 docs/security/2fa-trigger-matrix.md。
// 这份清单是运行时回归断言：任何一条被重新归类为 step_up_required，页面按钮就会
// 重新弹 TOTP，这里必须变红。
func lowRiskReleasedRoutes() []adminroutepolicy.Route {
	return []adminroutepolicy.Route{
		// 卡密读模型掩码化后的只读修正
		{Method: http.MethodGet, Path: "/api/v1/admin/redeem-codes"},
		{Method: http.MethodGet, Path: "/api/v1/admin/redeem-codes/:id"},
		{Method: http.MethodGet, Path: "/api/v1/admin/redeem-codes/stats"},
		{Method: http.MethodGet, Path: "/api/v1/admin/promo-codes/:id/usages"},
		// 代理探测
		{Method: http.MethodPost, Path: "/api/v1/admin/proxies/:id/test"},
		{Method: http.MethodPost, Path: "/api/v1/admin/proxies/:id/quality-check"},
		// 账号预检 / 预览 / 用量 / 模型目录探测
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/check-mixed-channel"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/schedulable-timers/preview"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/ollama-cloud-usage/refresh"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/models/sync-upstream"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/models/sync-upstream-preview"},
		// 上游渠道公告元数据同步
		{Method: http.MethodPost, Path: "/api/v1/admin/upstream-monitor/channels/:id/announcements/sync"},
		// 设置页的测试/预检动作（真实发信、保存配置仍 step-up）
		{Method: http.MethodPost, Path: "/api/v1/admin/settings/test-smtp"},
		{Method: http.MethodPost, Path: "/api/v1/admin/settings/web-search-emulation/test"},
		{Method: http.MethodPost, Path: "/api/v1/admin/settings/ip-risk/check"},
	}
}

// sensitiveBoundaryRoutes 是「明确保留」的边界：凭据回读/导出/备份、密钥管理、
// 资金与权限变更、删号、2FA/安全设置、发信、危险主机生命周期。
func sensitiveBoundaryRoutes() []adminroutepolicy.Route {
	return []adminroutepolicy.Route{
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/credential"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/data"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/data"},
		{Method: http.MethodGet, Path: "/api/v1/admin/backups/s3-config"},
		{Method: http.MethodGet, Path: "/api/v1/admin/backups/:id/download-url"},
		{Method: http.MethodGet, Path: "/api/v1/admin/debug-dumps/:name"},
		{Method: http.MethodGet, Path: "/api/v1/admin/users/:id/api-keys"},
		{Method: http.MethodGet, Path: "/api/v1/admin/redeem-codes/export"},
		{Method: http.MethodGet, Path: "/api/v1/admin/promo-codes"},
		{Method: http.MethodPut, Path: "/api/v1/admin/settings"},
		{Method: http.MethodPost, Path: "/api/v1/admin/settings/send-test-email"},
		{Method: http.MethodPost, Path: "/api/v1/admin/settings/admin-api-keys"},
		{Method: http.MethodPost, Path: "/api/v1/admin/users/:id/balance"},
		{Method: http.MethodDelete, Path: "/api/v1/admin/users/:id"},
		{Method: http.MethodPost, Path: "/api/v1/admin/subscriptions/assign"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/refresh"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/model-whitelist-auto-sync/run"},
		{Method: http.MethodPut, Path: "/api/v1/admin/payment/config"},
		{Method: http.MethodPost, Path: "/api/v1/admin/payment/orders/:id/refund"},
		{Method: http.MethodPost, Path: "/api/v1/admin/system/update"},
	}
}

// TestLowRiskReleasedAdminRoutesAreSecretFree 断言放开清单在集中注册表里真实存在、
// 归类为带理由的 secret_free，且运行时判定不再要求 step-up。
func TestLowRiskReleasedAdminRoutesAreSecretFree(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	registered := map[string]struct{}{}
	for _, route := range adminRoutesOf(router) {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range lowRiskReleasedRoutes() {
		key := route.Method + " " + route.Path
		require.Contains(t, registered, key, "放开清单引用了不存在的路由: %s", key)
		resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
		require.True(t, ok, "%s 未归类", key)
		require.Equal(t, adminroutepolicy.ClassSecretFree, resolved.Class,
			"%s 必须归为 secret_free，实际 %s/%s", key, resolved.Class, resolved.Reason)
		require.NotEmpty(t, resolved.Reason, "%s 缺少分类理由", key)
		require.False(t, adminroutepolicy.IsStepUpRequired(route.Method, route.Path),
			"%s 不应再要求 step-up", key)
	}
}

// TestLowRiskReleasedAdminRoutesBypassStepUpGate 用合成路由验证运行时门控真的
// 不再命中这些路由：与 adminStepUpRouteGuard 使用完全同源的 Resolve。
func TestLowRiskReleasedAdminRoutesBypassStepUpGate(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	routes := adminRoutesOf(router)

	var gated []string
	probe := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		gated = append(gated, c.Request.Method+" "+c.FullPath())
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"reason": "STEP_UP_REQUIRED"})
	})
	guard := adminStepUpRouteGuard(probe)

	synthetic := gin.New()
	for _, route := range routes {
		synthetic.Match([]string{route.Method}, route.Path, guard, func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
	}

	for _, route := range lowRiskReleasedRoutes() {
		key := route.Method + " " + route.Path
		t.Run(key, func(t *testing.T) {
			gated = nil
			w := httptest.NewRecorder()
			synthetic.ServeHTTP(w, httptest.NewRequest(route.Method, concreteAdminPath(route.Path), nil))
			require.NotContains(t, gated, key, "%s 不得再执行 step-up 门控", key)
			require.Equal(t, http.StatusNoContent, w.Code, "%s 应直接进入业务层", key)
		})
	}
}

// TestSensitiveBoundariesStillStepUpGated 防止「放开低风险」顺带把秘密读/资金/
// 权限/安全配置一起放开。
func TestSensitiveBoundariesStillStepUpGated(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	routes := adminRoutesOf(router)

	var gated []string
	probe := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		gated = append(gated, c.Request.Method+" "+c.FullPath())
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"reason": "STEP_UP_REQUIRED"})
	})
	guard := adminStepUpRouteGuard(probe)

	synthetic := gin.New()
	for _, route := range routes {
		synthetic.Match([]string{route.Method}, route.Path, guard, func(c *gin.Context) {
			c.Status(http.StatusNoContent)
		})
	}

	for _, route := range sensitiveBoundaryRoutes() {
		key := route.Method + " " + route.Path
		t.Run(key, func(t *testing.T) {
			gated = nil
			w := httptest.NewRecorder()
			synthetic.ServeHTTP(w, httptest.NewRequest(route.Method, concreteAdminPath(route.Path), nil))
			require.Contains(t, gated, key, "%s 必须继续保持 step-up 门控", key)
			require.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}
