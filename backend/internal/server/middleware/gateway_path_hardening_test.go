package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 路径别名发生在鉴权之前，匿名请求就能触发。版本段多到不可能是填错 Base URL 的路径
// 必须原样透传（不改写、不诊断），否则重复 /v1 上千次就是 O(N²) 的 CPU 放大
// （8KB 路径约 0.4s、63KB 约 20s）。
func TestGatewayPathAliasPassesThroughExcessiveVersionSegments(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"重复 /v1 + 不存在的端点", http.MethodGet, strings.Repeat("/v1", 5000) + "/zzz"},
		{"重复 /v1 + 真实端点", http.MethodPost, strings.Repeat("/v1", 5000) + "/messages"},
		{"/v1 与 /v1beta 交替", http.MethodGet, strings.Repeat("/v1/v1beta", 5000) + "/zzz"},
		{"codex 前缀", http.MethodPost, "/backend-api/codex" + strings.Repeat("/v1", 5000) + "/responses"},
		{"antigravity 前缀", http.MethodPost, "/antigravity" + strings.Repeat("/v1", 5000) + "/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, seen := newGatewayPathAliasTestRouter()
			handler := GatewayPathAlias(router)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			w := httptest.NewRecorder()

			start := time.Now()
			handler.ServeHTTP(w, req)
			elapsed := time.Since(start)

			// 修复前 5000 段要 1~2 秒；修复后是微秒级，100ms 给慢 CI 留足余量。
			require.Less(t, elapsed, 100*time.Millisecond, "超限路径必须在常数时间内放行")
			require.Equal(t, http.StatusNotFound, w.Code)
			require.Empty(t, *seen, "超限路径不能被改写到任何 handler")
			require.NotContains(t, w.Body.String(), "gateway_path_not_found", "超限路径不诊断，也不把几 KB 的路径回显进响应")
		})
	}
}

// 上限本身：恰好 maxGatewayVersionSegments 个版本段仍然折叠，多一个就原样透传。
func TestGatewayPathAliasVersionSegmentLimitBoundary(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	atLimit := strings.Repeat("/v1", maxGatewayVersionSegments) + "/messages"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, atLimit, strings.NewReader(`{}`)))
	require.Equal(t, http.StatusOK, w.Code, "恰好 %d 个版本段仍然折叠", maxGatewayVersionSegments)
	require.Len(t, *seen, 1)
	require.Equal(t, "/v1/messages", (*seen)[0].Path)

	*seen = (*seen)[:0]
	overLimit := strings.Repeat("/v1", maxGatewayVersionSegments+1) + "/messages"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, overLimit, strings.NewReader(`{}`)))
	require.Equal(t, http.StatusNotFound, w.Code, "多一个版本段就原样透传")
	require.Empty(t, *seen)
}

// 版本段个数上限只是兜底。折叠/剥离的循环本身也必须是线性的：每个段只扫一次，候选不重复
// 拼整串。这里直接调循环、绕过上限，把上限拿掉以后也不能退回平方级。
func TestStripGatewayVersionPathIsLinear(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	index := newGatewayRouteIndex(router)

	const n = 20000
	repeat := func(segment string, count int) []string {
		out := make([]string, count)
		for i := range out {
			out[i] = segment
		}
		return out
	}
	alternating := make([]string, 0, n)
	for i := 0; i < n/2; i++ {
		alternating = append(alternating, "v1", "v1beta")
	}
	cases := map[string][]string{
		"重复 /v1":           append(repeat("v1", n), "zzz"),
		"/v1 与 /v1beta 交替": append(alternating, "zzz"),
		"codex 前缀":         append(append([]string{"backend-api", "codex"}, repeat("v1", n)...), "responses"),
		"antigravity 前缀":   append(append([]string{"antigravity"}, repeat("v1", n)...), "messages"),
	}
	for name, segments := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_, _ = index.stripGatewayVersionPath(http.MethodPost, segments)
			elapsed := time.Since(start)
			// 平方级实现在 2 万段上要十秒以上；线性实现是毫秒级。
			require.Less(t, elapsed, 500*time.Millisecond, "%d 段必须线性完成", len(segments))
		})
	}
}

// ginChainProbe 记录请求有没有走进 gin 的全局中间件链，以及 Honeypot 有没有出手。
type ginChainProbe struct {
	ran      int
	rejected bool
	reason   IngressRejectReason
}

// newGatewayPathAliasChainRouter 模拟 SetupRouter 的全局中间件：先记一笔“走进了 gin 链”，
// 再让带 X-Test-Banned 的请求像被 ipRisk 封禁的 IP 一样直接 403，最后是真实的 Honeypot。
func newGatewayPathAliasChainRouter(probe *ginChainProbe) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		probe.ran++
		c.Next()
		probe.reason, probe.rejected = GetIngressRejectReason(c)
	})
	router.Use(func(c *gin.Context) {
		if c.GetHeader("X-Test-Banned") != "" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	})
	router.Use(Honeypot(nil))
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

// A-P2-07：折叠后仍不存在的 /v1/v1/* 不能在 gin 之外直接 404，否则 Honeypot、ipRisk、
// 访问日志对这类请求全部失效；扫描器拿 /v1/v1/ 当前缀就能绕过封禁。
func TestGatewayPathAliasUnmatchedPathRunsThroughGinMiddlewareChain(t *testing.T) {
	t.Run("扫描器路径被 Honeypot 拦下", func(t *testing.T) {
		probe := &ginChainProbe{}
		handler := GatewayPathAlias(newGatewayPathAliasChainRouter(probe))

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/v1/../../.env", nil))

		require.Equal(t, 1, probe.ran, "请求必须走进 gin 的全局中间件链")
		require.True(t, probe.rejected, "Honeypot 必须对 /v1/v1/../../.env 出手")
		require.Equal(t, IngressRejectHoneypot, probe.reason)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Zero(t, w.Body.Len(), "Honeypot 的 404 是空体，不是 gateway_path_not_found 诊断")
	})

	t.Run("被封 IP 拿不到诊断", func(t *testing.T) {
		probe := &ginChainProbe{}
		handler := GatewayPathAlias(newGatewayPathAliasChainRouter(probe))

		req := httptest.NewRequest(http.MethodPost, "/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
		req.Header.Set("X-Test-Banned", "1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.Equal(t, http.StatusForbidden, w.Code, "前面的全局中间件拒绝了请求，诊断不能抢先响应")
		require.NotContains(t, w.Body.String(), "gateway_path_not_found")
	})

	t.Run("正常客户端仍然拿到诊断 JSON", func(t *testing.T) {
		probe := &ginChainProbe{}
		handler := GatewayPathAlias(newGatewayPathAliasChainRouter(probe))

		req := httptest.NewRequest(http.MethodPost, "/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
		req.Host = "kedaya.ai"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.Equal(t, 1, probe.ran, "诊断是 gin 404 链末端输出的，必须先走完全局中间件")
		require.False(t, probe.rejected)
		require.Equal(t, http.StatusNotFound, w.Code)
		require.Contains(t, w.Header().Get("Content-Type"), "application/json")
		require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		var payload struct {
			Error struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.Equal(t, "gateway_path_not_found", payload.Error.Code)
		require.Contains(t, payload.Error.Hint, "https://kedaya.ai")
	})
}

// 别名层给引擎追加诊断中间件时不能吞掉应用自己的 NoRoute 处理器，也不能碰命中路由的请求。
func TestGatewayPathAliasKeepsNoRouteHandlerAndMatchedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/messages", func(c *gin.Context) { c.String(http.StatusOK, "messages") })
	router.NoRoute(func(c *gin.Context) { c.String(http.StatusTeapot, "custom-no-route") })
	handler := GatewayPathAlias(router)

	// 普通 404（不是 /v1/v1/*）仍然落到应用自己的 NoRoute。
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/definitely-not-a-route", nil))
	require.Equal(t, http.StatusTeapot, w.Code)
	require.Equal(t, "custom-no-route", w.Body.String())

	// 超限路径同样原样进 gin，由应用的 NoRoute 处理（不诊断）。
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, strings.Repeat("/v1", 100)+"/zzz", nil))
	require.Equal(t, http.StatusTeapot, w.Code)

	// 命中路由的请求（含折叠后命中的）完全不受影响。
	for _, path := range []string{"/v1/messages", "/v1/v1/messages"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		require.Equal(t, http.StatusOK, w.Code, "path=%q", path)
		require.Equal(t, "messages", w.Body.String(), "path=%q", path)
	}
}

// 面板/管理 API 路径里带 usage、models 这类端点词，但不是网关协议路由：别名索引不收它们，
// 它们的前缀（api、admin、user）也不能混进受支持前缀与版本族。
func TestGatewayRouteIndexOnlyCollectsGatewayRoutes(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	index := newGatewayRouteIndex(router)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/usage"},
		{http.MethodGet, "/api/v1/user/usage"},
		{http.MethodGet, "/api/v1/admin/accounts/42/usage"},
		{http.MethodPost, "/api/v1/admin/models/metadata/sync"},
	} {
		require.False(t, matchGatewayRoute(index, tc.method, tc.path),
			"面板路由 %s %s 不能进别名索引", tc.method, tc.path)
	}

	prefixes := supportedGatewayPrefixes(index)
	for _, panelSegment := range []string{"api", "admin", "user"} {
		require.NotContains(t, prefixes, panelSegment, "面板路径段 %q 不能成为受支持前缀", panelSegment)
	}
	require.NotContains(t, index.versionedPrefix, "api/v1", "面板根不能成为带版本段的前缀族")
	require.NotContains(t, index.versionPrefix, "api", "面板根不能成为版本根")

	// 网关路由本身一条不少。
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/messages"},
		{http.MethodGet, "/v1/usage"},
		{http.MethodGet, "/antigravity/v1/models"},
	} {
		require.True(t, matchGatewayRoute(index, tc.method, tc.path), "网关路由 %s %s 必须仍在索引里", tc.method, tc.path)
	}
}

// 经过别名层的面板请求既不折叠重复版本段，也不补漏写的版本段：鉴权虽然照常生效，
// 但别名层不该替面板路径改写 URL。
func TestGatewayPathAliasDoesNotAliasPanelRoutes(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/v1/admin/usage"},                 // 重复版本段折叠
		{http.MethodGet, "/api/v1/v1/v1/user/usage"},               // 多个重复版本段
		{http.MethodGet, "/api/admin/usage"},                       // 漏写版本段补齐
		{http.MethodGet, "/api/user/usage"},                        // 漏写版本段补齐
		{http.MethodGet, "/api/v1/v1/admin/accounts/42/usage"},     // 带路径参数
		{http.MethodPost, "/api/v1/v1/admin/models/metadata/sync"}, // 非 GET
		{http.MethodPost, "/api/admin/models/metadata/sync"},       // 非 GET + 补齐
	} {
		router, seen := newGatewayPathAliasTestRouter()
		handler := GatewayPathAlias(router)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))

		require.Equal(t, http.StatusNotFound, w.Code, "path=%q 不能被别名到面板路由", tc.path)
		require.Empty(t, *seen, "path=%q 不能进入面板 handler", tc.path)
	}

	// 规范写法的面板路由本身原样可达，别名层一个字节都不动。
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/usage?window=7d", nil))
	require.Equal(t, http.StatusTeapot, w.Code)
	require.Len(t, *seen, 1)
	require.Equal(t, "/api/v1/admin/usage", (*seen)[0].Path)
	require.False(t, (*seen)[0].HasAlias)
}
