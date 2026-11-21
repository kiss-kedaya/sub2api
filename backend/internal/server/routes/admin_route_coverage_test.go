//go:build unit

package routes

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// buildFullAdminRouteTree 注册生产环境的完整管理面路由树。
//
// 条件注册的 handler 全部用零值占位，保证「handler 是否装配」不会让覆盖测试
// 漏掉整族路由。占位 handler 只在被请求时才会解引用，覆盖测试只做
// 路由枚举与门控行为断言，不进入业务实现。
func buildFullAdminRouteTree(t *testing.T, stepUp middleware.StepUpAuthMiddleware) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := &handler.Handlers{
		ModelPlaza: &handler.ModelPlazaHandler{},
		Gateway:    &handler.GatewayHandler{},
		Crypto:     &handler.CryptoHandler{},
		Admin: &handler.AdminHandlers{
			UpstreamChannel: &adminhandler.UpstreamChannelHandler{},
			IPRisk:          &adminhandler.IPRiskHandler{},
			RequestCapture:  &adminhandler.RequestCaptureHandler{},
			UpstreamMonitor: &adminhandler.UpstreamMonitorHandler{},
			AccountOps:      &adminhandler.AccountOpsHandler{},
			Mihomo:          &adminhandler.MihomoHandler{},
			Serverless:      &adminhandler.ServerlessHandler{},
			ExcelGrant:      &adminhandler.ExcelGrantHandler{},
			OpsAutomation:   &adminhandler.OpsAutomationHandler{},
			CredentialGuard: &adminhandler.CredentialGuardHandler{},
			PromptAudit:     &securityaudit.PromptAdminHandler{},
		},
	}
	RegisterAdminRoutes(
		r.Group("/api/v1"), h,
		adminRoutesTestAuth(service.AuditAuthMethodJWT),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		stepUpRouteSettings(map[string]string{}),
		nil,
	)
	RegisterPaymentRoutes(
		r.Group("/api/v1"),
		&handler.PaymentHandler{}, &handler.PaymentWebhookHandler{}, &adminhandler.PaymentHandler{},
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		adminRoutesTestAuth(service.AuditAuthMethodJWT),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUp,
		nil, stepUpRouteSettings(map[string]string{}), nil,
	)
	return r
}

func adminRoutesOf(r *gin.Engine) []adminroutepolicy.Route {
	var out []adminroutepolicy.Route
	for _, ri := range r.Routes() {
		if strings.HasPrefix(ri.Path, "/api/v1/admin") {
			out = append(out, adminroutepolicy.Route{Method: ri.Method, Path: ri.Path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// TestAdminRouteCoverageClassification 是防复发的主闸门：管理面路由树里的每一条
// 路由都必须被集中注册表显式归类。新加一条路由却忘了归类，CI 直接红。
func TestAdminRouteCoverageClassification(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	routes := adminRoutesOf(router)

	require.GreaterOrEqual(t, len(routes), 600,
		"管理面路由树数量异常，覆盖测试可能没有注册到完整路由（实际 %d）", len(routes))

	unclassified := adminroutepolicy.UnclassifiedAdminRoutes(routes)
	require.Empty(t, unclassified,
		"以下管理面路由未在 adminroutepolicy 注册表归类（machine_allowlisted / step_up_required / secret_free），"+
			"请先在 backend/internal/pkg/adminroutepolicy/policy.go 显式归类后再加路由：\n%s",
		strings.Join(unclassified, "\n"))

	counts := map[adminroutepolicy.Class]int{}
	for _, route := range routes {
		resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
		require.True(t, ok, "%s %s 未归类", route.Method, route.Path)
		require.NotEmpty(t, resolved.Reason, "%s %s 缺少分类理由", route.Method, route.Path)
		require.NotEmpty(t, resolved.Rule, "%s %s 缺少规则来源", route.Method, route.Path)
		if resolved.Class == adminroutepolicy.ClassStepUpRequired {
			require.NotEmpty(t, resolved.Resource, "%s %s 属于 step-up 但未标注敏感资源", route.Method, route.Path)
		}
		counts[resolved.Class]++
	}
	require.Positive(t, counts[adminroutepolicy.ClassMachineAllowlisted])
	require.Positive(t, counts[adminroutepolicy.ClassStepUpRequired])
	require.Positive(t, counts[adminroutepolicy.ClassSecretFree])

	// 导出归类矩阵，CI 日志里能直接看到三类各多少条。
	t.Logf("admin route classification matrix: machine_allowlisted=%d step_up_required=%d secret_free=%d total=%d",
		counts[adminroutepolicy.ClassMachineAllowlisted],
		counts[adminroutepolicy.ClassStepUpRequired],
		counts[adminroutepolicy.ClassSecretFree],
		len(routes))
}

// TestAdminRouteMachineAllowlistIsSecretFree 机器凭据白名单只能落在明确的
// secret_free 家族里，且不得触碰任何敏感资源。白名单泛化 = 2FA 后门复活。
func TestAdminRouteMachineAllowlistIsSecretFree(t *testing.T) {
	allowlist := adminroutepolicy.MachineAllowlist()
	require.NotEmpty(t, allowlist)

	router := buildFullAdminRouteTree(t, stepUpProbe())
	registered := map[string]struct{}{}
	for _, route := range adminRoutesOf(router) {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	for _, route := range allowlist {
		key := route.Method + " " + route.Path
		require.Contains(t, registered, key, "机器凭据白名单引用了不存在的路由: %s", key)

		resource, sensitive := adminroutepolicy.SensitiveResourceFor(route.Path)
		require.False(t, sensitive, "机器凭据白名单不得指向敏感资源 %s: %s", resource, key)

		reason, ok := adminroutepolicy.SecretFreeReason(route.Method, route.Path)
		require.True(t, ok, "机器凭据白名单必须由 secret_free 家族兜底: %s", key)
		require.NotEmpty(t, reason, "机器凭据白名单缺少 secret_free 理由: %s", key)
	}
}

// TestAdminRouteCoverageFailsForUnclassifiedRoute 证明机制本身有效：
// 故意塞一条未归类路由，覆盖校验必须失败。
func TestAdminRouteCoverageFailsForUnclassifiedRoute(t *testing.T) {
	routes := []adminroutepolicy.Route{
		{Method: http.MethodGet, Path: "/api/v1/admin/zz-new-route-without-policy"},
	}
	unclassified := adminroutepolicy.UnclassifiedAdminRoutes(routes)
	require.Equal(t, []string{"GET /api/v1/admin/zz-new-route-without-policy"}, unclassified,
		"未归类路由必须被覆盖校验拒绝")
}

// TestAdminRouteStepUpGuardMatchesRegistry 用合成路由（不触达业务 handler）验证：
//   - step_up_required 路由必须真正执行 step-up 门控（含 GET 敏感读）；
//   - secret_free / machine_allowlisted 路由不得被误伤。
func TestAdminRouteStepUpGuardMatchesRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
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

	concrete := func(path string) string {
		segs := strings.Split(path, "/")
		for i, seg := range segs {
			if strings.HasPrefix(seg, ":") {
				segs[i] = "1"
			} else if strings.HasPrefix(seg, "*") {
				segs[i] = "x"
			}
		}
		return strings.Join(segs, "/")
	}

	for _, route := range routes {
		resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
		require.True(t, ok)
		w := httptest.NewRecorder()
		synthetic.ServeHTTP(w, httptest.NewRequest(route.Method, concrete(route.Path), nil))
		if resolved.Class == adminroutepolicy.ClassStepUpRequired {
			require.Contains(t, gated, route.Method+" "+route.Path,
				"敏感路由未执行 step-up 门控: %s %s", route.Method, route.Path)
			require.Equal(t, http.StatusForbidden, w.Code)
			continue
		}
		require.Equal(t, http.StatusNoContent, w.Code,
			"非敏感路由被 step-up 误伤: %s %s", route.Method, route.Path)
	}
}

// TestAdminAdminAPIKeyRoutesAreSensitive 管理面自己的密钥管理接口必须属于
// 敏感资源：这类接口能创建新的机器身份，绝不能让机器凭据调用。
func TestAdminAdminAPIKeyRoutesAreSensitive(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/admin/settings/admin-api-keys"},
		{http.MethodPut, "/api/v1/admin/settings/admin-api-keys/x"},
		{http.MethodDelete, "/api/v1/admin/settings/admin-api-keys/x"},
		{http.MethodPut, "/api/v1/admin/settings"},
		{http.MethodDelete, "/api/v1/admin/settings/admin-api-key"},
	} {
		resolved, ok := adminroutepolicy.Resolve(route.method, route.path)
		require.True(t, ok, "%s %s 未归类", route.method, route.path)
		require.Equal(t, adminroutepolicy.ClassStepUpRequired, resolved.Class,
			"%s %s 必须要求 step-up", route.method, route.path)
	}
}
