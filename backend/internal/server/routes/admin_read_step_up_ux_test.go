//go:build unit

package routes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/stretchr/testify/require"
)

// pageLoadReadRoutes 是管理端页面打开/自动刷新时自己发起的请求（不是用户点击）。
// 这些请求命中 403 只允许「静默降级」，绝不允许弹 TOTP：因此注册表必须把它们
// 归类为 secret_free / machine_allowlisted，运行时不得执行 step-up 门控。
//
// 2026-11 事故：AccountsView 打开即请求 proxies/all、accounts/today-stats/batch、
// accounts/:id，前两者被 2026-11 的默认拒绝策略误判成 step-up，页面一加载就弹码，
// 用户取消后整页数据加载失败。
func pageLoadReadRoutes() []adminroutepolicy.Route {
	return []adminroutepolicy.Route{
		// 账号管理页首屏
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/today-stats"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/stats"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/usage"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/models"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/schedulable-timers"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/temp-unschedulable"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/today-stats/batch"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/usage/batch"},
		// 账号列表滚动后自动触发的自定义用量读取（只回用量数字）。
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/:id/custom-usage-query"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/custom-usage-batch"},
		// 代理下拉/列表（密码已掩码）
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/all"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/:id"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/:id/stats"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/:id/accounts"},
		// 上游监控页首屏：渠道列表/详情/密钥列表（读模型已掩码）
		{Method: http.MethodGet, Path: "/api/v1/admin/upstream-monitor/channels"},
		{Method: http.MethodGet, Path: "/api/v1/admin/upstream-monitor/channels/:id"},
		{Method: http.MethodGet, Path: "/api/v1/admin/upstream-monitor/channels/:id/api-keys"},
		// 其它页面首屏的常规只读
		{Method: http.MethodGet, Path: "/api/v1/admin/users"},
		{Method: http.MethodGet, Path: "/api/v1/admin/groups"},
		{Method: http.MethodGet, Path: "/api/v1/admin/settings"},
		{Method: http.MethodGet, Path: "/api/v1/admin/dashboard/stats"},
	}
}

// concreteAdminPath 把注册表里的参数占位符替换成具体值，用于发真实请求。
func concreteAdminPath(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
			segs[i] = "1"
		}
	}
	return strings.Join(segs, "/")
}

// TestAdminPageLoadReadRoutesNeverStepUpGated 是本次故障的防复发主断言：
// 页面加载自动请求必须 secret_free 且运行时门控不被执行。
func TestAdminPageLoadReadRoutesNeverStepUpGated(t *testing.T) {
	var gated []string
	router := buildFullAdminRouteTree(t, stepUpRecordingProbe(&gated))
	registered := map[string]struct{}{}
	for _, route := range adminRoutesOf(router) {
		registered[route.Method+" "+route.Path] = struct{}{}
	}

	for _, route := range pageLoadReadRoutes() {
		key := route.Method + " " + route.Path
		t.Run(key, func(t *testing.T) {
			require.Contains(t, registered, key, "页面加载路由必须真实注册: %s", key)

			resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
			require.True(t, ok, "%s 未归类（运行时按 step-up fail-closed）", key)
			require.NotEqual(t, adminroutepolicy.ClassStepUpRequired, resolved.Class,
				"%s 是页面加载自动请求，不得要求 step-up（rule=%s）", key, resolved.Rule)

			gated = nil
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(route.Method, concreteAdminPath(route.Path), nil))
			require.NotContains(t, gated, concreteAdminPath(route.Path),
				"%s 不得执行 step-up 门控（页面加载会弹 TOTP）", key)
			require.NotContains(t, w.Body.String(), "STEP_UP_REQUIRED")
		})
	}
}

// TestAdminGetStepUpRoutesHaveExplicitJustification 落实「新增只读 GET 若被归为
// step-up 必须显式说明理由」：遍历真实注册的 GET 路由，凡是 step_up_required 的
// 规则都必须在 adminroutepolicy 的只读理由表里登记过，且理由非空。
func TestAdminGetStepUpRoutesHaveExplicitJustification(t *testing.T) {
	router := buildFullAdminRouteTree(t, stepUpProbe())
	var undocumented []string
	justified := 0
	for _, route := range adminRoutesOf(router) {
		if route.Method != http.MethodGet {
			continue
		}
		resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
		require.True(t, ok, "GET %s 未归类", route.Path)
		if resolved.Class != adminroutepolicy.ClassStepUpRequired {
			continue
		}
		reason, found := adminroutepolicy.StepUpReadRuleJustification(resolved.Rule)
		if !found || reason == "" {
			undocumented = append(undocumented, route.Path+" (rule="+resolved.Rule+")")
			continue
		}
		justified++
	}
	sort.Strings(undocumented)
	require.Empty(t, undocumented,
		"以下只读 GET 要求 step-up 但没有登记理由（在 policy.go 的 stepUpReadRuleJustifications 补一句具体秘密字段）：\n%s",
		strings.Join(undocumented, "\n"))
	require.Positive(t, justified, "注册表里应当仍存在有理由的只读 step-up 路由")
}

// TestAdminGetStepUpRoutesStaySecretOnly 冻结当前「允许要求 step-up 的只读 GET」
// 清单：只有返回明文秘密的读才允许留在这里。新增项必须先改这里并说明理由。
func TestAdminGetStepUpRoutesStaySecretOnly(t *testing.T) {
	expected := map[string]struct{}{
		"GET /api/v1/admin/accounts/:id/credential":  {},
		"GET /api/v1/admin/accounts/data":            {},
		"GET /api/v1/admin/backups/:id/download-url": {},
		"GET /api/v1/admin/backups/s3-config":        {},
		"GET /api/v1/admin/debug-dumps/:name":        {},
		"GET /api/v1/admin/groups/:id/api-keys":      {},
		"GET /api/v1/admin/proxies/data":             {},
		"GET /api/v1/admin/users/:id/api-keys":       {},
		"GET /api/v1/admin/promo-codes":              {},
		"GET /api/v1/admin/promo-codes/:id":          {},
		"GET /api/v1/admin/promo-codes/:id/usages":   {},
		"GET /api/v1/admin/redeem-codes":             {},
		"GET /api/v1/admin/redeem-codes/:id":         {},
		"GET /api/v1/admin/redeem-codes/export":      {},
		"GET /api/v1/admin/redeem-codes/stats":       {},
	}

	router := buildFullAdminRouteTree(t, stepUpProbe())
	actual := map[string]struct{}{}
	for _, route := range adminRoutesOf(router) {
		if route.Method != http.MethodGet {
			continue
		}
		resolved, _ := adminroutepolicy.Resolve(route.Method, route.Path)
		if resolved.Class == adminroutepolicy.ClassStepUpRequired {
			actual["GET "+route.Path] = struct{}{}
		}
	}
	var unexpected []string
	for key := range actual {
		if _, ok := expected[key]; !ok {
			unexpected = append(unexpected, key)
		}
	}
	sort.Strings(unexpected)
	require.Empty(t, unexpected,
		"新增了要求 step-up 的只读 GET；确认它确实返回明文秘密并把理由写进 stepUpReadRuleJustifications 与这里：\n%s",
		strings.Join(unexpected, "\n"))
	for key := range expected {
		require.Contains(t, actual, key, "只读 step-up 白名单里的路由已不存在，请同步清理: %s", key)
	}
}

// TestAdminRoutePolicyRuntimeClassificationDump 是排障用的「dump 运行时分类」工具：
// 设置 ADMIN_ROUTE_POLICY_DUMP=1 后运行，会按注册表规则打印每一条管理面路由的
// 运行时分类（与 adminStepUpRouteGuard 使用的 Resolve 完全同源），可用
// ADMIN_ROUTE_POLICY_DUMP_FILTER 过滤路径子串。
//
//	ADMIN_ROUTE_POLICY_DUMP=1 go test -tags=unit ./internal/server/routes \
//	  -run TestAdminRoutePolicyRuntimeClassificationDump -v
func TestAdminRoutePolicyRuntimeClassificationDump(t *testing.T) {
	if os.Getenv("ADMIN_ROUTE_POLICY_DUMP") != "1" {
		t.Skip("set ADMIN_ROUTE_POLICY_DUMP=1 to dump runtime admin route classification")
	}
	filter := os.Getenv("ADMIN_ROUTE_POLICY_DUMP_FILTER")
	router := buildFullAdminRouteTree(t, stepUpProbe())
	rows := make([]string, 0, 700)
	for _, route := range adminRoutesOf(router) {
		if filter != "" && !strings.Contains(route.Path, filter) {
			continue
		}
		resolved, ok := adminroutepolicy.Resolve(route.Method, route.Path)
		if !ok {
			rows = append(rows, fmt.Sprintf("UNCLASSIFIED\t-\t-\t%s\t%s", route.Method, route.Path))
			continue
		}
		rows = append(rows, fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
			resolved.Class, resolved.Reason, resolved.Rule, route.Method, route.Path))
	}
	sort.Strings(rows)
	for _, row := range rows {
		t.Log(row)
	}
	t.Logf("dumped %d admin routes (filter=%q)", len(rows), filter)
}

// TestStepUpReadGuardStillProtectsSecrets 保持真正返秘密的读仍被门控拦截，
// 防止在放宽代理/批量统计时顺带把秘密读也放开。
func TestStepUpReadGuardStillProtectsSecrets(t *testing.T) {
	var gated []string
	router := buildFullAdminRouteTree(t, stepUpRecordingProbe(&gated))
	for _, route := range []adminroutepolicy.Route{
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/:id/credential"},
		{Method: http.MethodGet, Path: "/api/v1/admin/accounts/data"},
		{Method: http.MethodGet, Path: "/api/v1/admin/proxies/data"},
		{Method: http.MethodGet, Path: "/api/v1/admin/backups/s3-config"},
		{Method: http.MethodGet, Path: "/api/v1/admin/backups/:id/download-url"},
		{Method: http.MethodGet, Path: "/api/v1/admin/debug-dumps/:name"},
		{Method: http.MethodGet, Path: "/api/v1/admin/users/:id/api-keys"},
		{Method: http.MethodGet, Path: "/api/v1/admin/groups/:id/api-keys"},
		{Method: http.MethodPost, Path: "/api/v1/admin/accounts/batch"},
		{Method: http.MethodPost, Path: "/api/v1/admin/upstream-monitor/channels/:id/api-keys/:key_id/reveal"},
		{Method: http.MethodPut, Path: "/api/v1/admin/proxies/:id"},
	} {
		key := route.Method + " " + route.Path
		t.Run(key, func(t *testing.T) {
			gated = nil
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(route.Method, concreteAdminPath(route.Path), nil))
			require.Contains(t, gated, concreteAdminPath(route.Path), "%s 必须执行 step-up 门控", key)
			require.Equal(t, http.StatusForbidden, w.Code)
		})
	}
}
