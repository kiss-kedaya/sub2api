package routes

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayPathAliasAuthSeen struct {
	Method   string
	Path     string
	RawQuery string
	Header   string
	Body     string
}

// gatewayPathAliasAuthProbe 用 401 证明请求确实命中了网关路由并进入了鉴权流程，
// 同时记录鉴权中间件看到的路径/query/header/body。
type gatewayPathAliasAuthProbe struct {
	seen []gatewayPathAliasAuthSeen
}

func (p *gatewayPathAliasAuthProbe) middleware() servermiddleware.APIKeyAuthMiddleware {
	return servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		p.seen = append(p.seen, gatewayPathAliasAuthSeen{
			Method:   c.Request.Method,
			Path:     c.Request.URL.Path,
			RawQuery: c.Request.URL.RawQuery,
			Header:   c.GetHeader("X-Client-Base-Url"),
			Body:     string(body),
		})
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"type": "invalid_request_error", "message": "Invalid API key"},
		})
	})
}

func (p *gatewayPathAliasAuthProbe) last(t *testing.T) gatewayPathAliasAuthSeen {
	t.Helper()
	require.NotEmpty(t, p.seen, "请求没有进入网关鉴权中间件，说明路由未命中")
	return p.seen[len(p.seen)-1]
}

func (p *gatewayPathAliasAuthProbe) reset() { p.seen = p.seen[:0] }

func newGatewayPathAliasRoutesRouter(t *testing.T) (*gin.Engine, *gatewayPathAliasAuthProbe) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	probe := &gatewayPathAliasAuthProbe{}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxBodySize:     1024 * 1024,
			TextMaxBodySize: 1024 * 1024,
		},
	}
	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
			AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
		},
		probe.middleware(),
		nil, nil, nil, nil, nil,
		cfg,
	)
	return router, probe
}

// TestGatewayPathAliasZeroToManyV1PrefixesHitAuthentication 覆盖 0/1/2/3/6 个 /v1
// 前缀：全部必须命中同一个网关路由并进入鉴权流程（401），而不是 404。
func TestGatewayPathAliasZeroToManyV1PrefixesHitAuthentication(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	body := `{"model":"claude-opus-5","max_tokens":16,"stream":false}`
	prefixSets := []string{
		"",                   // 裸路径：客户端 Base URL 完全没带 /v1
		"/v1",                // 规范写法
		"/v1/v1",             // 常见坑：Base URL 带 /v1，客户端又拼了一次
		"/v1/v1/v1",          // 多写两次
		"/v1/v1/v1/v1/v1/v1", // 连续 6 个 /v1
	}

	endpoints := []struct {
		method    string
		suffix    string
		canonical string
	}{
		{http.MethodPost, "/messages", "/v1/messages"},
		{http.MethodPost, "/messages/count_tokens", "/v1/messages/count_tokens"},
		{http.MethodPost, "/responses", "/v1/responses"},
		{http.MethodPost, "/chat/completions", "/v1/chat/completions"},
		{http.MethodPost, "/embeddings", "/v1/embeddings"},
		{http.MethodGet, "/models", "/v1/models"},
		{http.MethodGet, "/models/claude-opus-5", "/v1/models/claude-opus-5"},
	}

	for _, endpoint := range endpoints {
		for _, prefix := range prefixSets {
			probe.reset()
			requestPath := prefix + endpoint.suffix + "?beta=1&trace=%2Fabc"
			var requestBody io.Reader
			if endpoint.method == http.MethodPost {
				requestBody = strings.NewReader(body)
			}
			req := httptest.NewRequest(endpoint.method, requestPath, requestBody)
			req.Header.Set("X-Client-Base-Url", "https://kedaya.ai"+prefix)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handlerUnderTest.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code,
				"method=%s path=%q 应命中网关路由进入鉴权并返回 401（而不是 404）", endpoint.method, requestPath)
			seen := probe.last(t)
			require.Equal(t, endpoint.method, seen.Method)
			require.Equal(t, endpoint.canonical, seen.Path,
				"path=%q 应被规范化为 %q", requestPath, endpoint.canonical)
			require.Equal(t, "beta=1&trace=%2Fabc", seen.RawQuery, "query string 必须原样保留")
			require.Equal(t, "https://kedaya.ai"+prefix, seen.Header, "header 必须原样保留")
			if endpoint.method == http.MethodPost {
				require.Equal(t, body, seen.Body, "body 必须原样保留")
			}
		}
	}
}

// TestGatewayPathAliasFoldsExcludedBarePaths 确认"裸路径不开别名"的排除项只作用于
// 裸路径补全：显式 /v1/v1/usage 这类重复前缀仍然要折叠并命中网关路由。
func TestGatewayPathAliasFoldsExcludedBarePaths(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	cases := []string{"/v1/v1/usage", "/v1/v1/sub2api/billing"}
	for _, requestPath := range cases {
		probe.reset()
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code, "path=%q 应命中网关路由", requestPath)
		require.Equal(t, strings.Replace(requestPath, "/v1/v1", "/v1", 1), probe.last(t).Path,
			"path=%q 应折叠成单个 /v1 前缀", requestPath)
	}
}

// TestGatewayPathAliasVariantsAreEquivalent 证明折叠/补前缀后与直接请求规范路径
// 完全等价：同一个 handler、同一个 401 响应体。
func TestGatewayPathAliasVariantsAreEquivalent(t *testing.T) {
	router, _ := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	variants := []string{
		"/v1/messages",
		"/v1/v1/messages",
		"/v1/v1/v1/v1/v1/v1/messages",
		"/messages",
	}
	var baselineCode int
	var baselineBody string
	for _, variant := range variants {
		req := httptest.NewRequest(http.MethodPost, variant, strings.NewReader(`{"model":"claude-opus-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code, "variant=%q", variant)
		if baselineBody == "" {
			baselineCode = w.Code
			baselineBody = w.Body.String()
			continue
		}
		require.Equal(t, baselineCode, w.Code, "variant=%q 状态码应与规范路径一致", variant)
		require.Equal(t, baselineBody, w.Body.String(), "variant=%q 响应体应与规范路径完全一致", variant)
	}
}

// TestGatewayPathAliasDoesNotRewriteNonGatewayPaths 反证：非网关路径（管理 API、
// 静态资源、健康检查、SPA 页面、Gemini 原生前缀）不会被重写。
func TestGatewayPathAliasDoesNotRewriteNonGatewayPaths(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	// 额外注册一些非网关路径，模拟管理 API / 静态资源 / SPA 页面。
	router.GET("/api/v1/admin/users", func(c *gin.Context) { c.String(http.StatusOK, "admin-api") })
	router.GET("/assets/index-abc.js", func(c *gin.Context) { c.String(http.StatusOK, "static-js") })
	router.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "health") })
	router.GET("/usage", func(c *gin.Context) { c.String(http.StatusOK, "spa-usage-page") })
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	cases := []struct {
		path    string
		want    string
		comment string
	}{
		{"/api/v1/admin/users", "admin-api", "管理 API 不能被当成裸网关路径补 /v1"},
		{"/assets/index-abc.js", "static-js", "静态资源不能被改写"},
		{"/health", "health", "探针不能被改写"},
		{"/usage", "spa-usage-page", "SPA 的 /usage 页面必须继续可用（不补 /v1）"},
	}
	for _, tc := range cases {
		probe.reset()
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "%s: path=%q", tc.comment, tc.path)
		require.Equal(t, tc.want, w.Body.String(), "%s: path=%q", tc.comment, tc.path)
		require.Empty(t, probe.seen, "%s: path=%q 不应进入网关鉴权", tc.comment, tc.path)
	}

	// 单 /v1 的未知子路径与 Gemini 原生前缀都不能被折叠/补齐。
	for _, path := range []string{"/v1/definitely-not-a-route", "/v1beta/definitely-not-a-route"} {
		probe.reset()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "path=%q", path)
		require.Empty(t, probe.seen, "path=%q 不应进入网关鉴权", path)
	}
}

// TestGatewayPathAliasUnmatchedDuplicatedPrefixReturnsJSON 折叠后仍不存在的
// /v1/v1/* 子路径要返回带 Base URL 提示的 JSON，而不是空 404。
func TestGatewayPathAliasUnmatchedDuplicatedPrefixReturnsJSON(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
	req.Host = "kedaya.ai"
	w := httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, probe.seen, "未知子路径不应进入鉴权")
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.NotEmpty(t, w.Body.Bytes(), "不能返回空 404")

	var payload struct {
		Error struct {
			Code           string `json:"code"`
			Hint           string `json:"hint"`
			OriginalPath   string `json:"original_path"`
			NormalizedPath string `json:"normalized_path"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "gateway_path_not_found", payload.Error.Code)
	require.Contains(t, payload.Error.Hint, "Base URL")
	require.Contains(t, payload.Error.Hint, "https://kedaya.ai")
	require.Equal(t, "/v1/v1/v1/definitely-not-a-route", payload.Error.OriginalPath)
	require.Equal(t, "/v1/definitely-not-a-route", payload.Error.NormalizedPath)
}

// TestGatewayPathAliasPrefixedDuplicatedVersionSegmentsHitAuthentication 是本次修复的
// 主用例：带前缀路由（/antigravity、/backend-api/codex）重复写版本段时必须折叠成
// 规范路径并进入鉴权（401），不能直接 404。
func TestGatewayPathAliasPrefixedDuplicatedVersionSegmentsHitAuthentication(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	body := `{"model":"claude-opus-5","max_tokens":16,"stream":false}`
	cases := []struct {
		method    string
		path      string
		canonical string
	}{
		// 题设 P1：/antigravity/v1/messages 正常 401，重复写版本段原先 404
		{http.MethodPost, "/antigravity/v1/messages", "/antigravity/v1/messages"},
		{http.MethodPost, "/antigravity/v1/v1/messages", "/antigravity/v1/messages"},
		{http.MethodPost, "/antigravity/v1/v1/v1/messages", "/antigravity/v1/messages"},
		{http.MethodPost, "/antigravity/v1/v1/messages/count_tokens", "/antigravity/v1/messages/count_tokens"},
		{http.MethodPost, "/antigravity/v1/v1/v1/messages/count_tokens", "/antigravity/v1/messages/count_tokens"},
		{http.MethodGet, "/antigravity/v1/models", "/antigravity/v1/models"},
		{http.MethodGet, "/antigravity/v1/v1/models", "/antigravity/v1/models"},
		{http.MethodGet, "/antigravity/v1/usage", "/antigravity/v1/usage"},
		{http.MethodGet, "/antigravity/v1/v1/usage", "/antigravity/v1/usage"},
		// /backend-api/codex 前缀：规范写法本身就是 /backend-api/codex/responses，
		// 没有“重复版本段”可折叠，必须原样透传给 gin（不是 Base URL 填错的情形）。
		{http.MethodPost, "/backend-api/codex/responses", "/backend-api/codex/responses"},
		{http.MethodGet, "/backend-api/codex/models", "/backend-api/codex/models"},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.path, "/", "_"), func(t *testing.T) {
			probe.reset()
			requestPath := tc.path + "?beta=1&trace=%2Fabc"
			var requestBody io.Reader
			if tc.method == http.MethodPost {
				requestBody = strings.NewReader(body)
			}
			req := httptest.NewRequest(tc.method, requestPath, requestBody)
			req.Header.Set("X-Client-Base-Url", "https://kedaya.ai"+tc.path)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			handlerUnderTest.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code,
				"method=%s path=%q 应命中网关路由进入鉴权并返回 401（而不是 404）", tc.method, tc.path)
			seen := probe.last(t)
			require.Equal(t, tc.method, seen.Method, "path=%q", tc.path)
			require.Equal(t, tc.canonical, seen.Path, "path=%q 应规范化为 %q", tc.path, tc.canonical)
			require.Equal(t, "beta=1&trace=%2Fabc", seen.RawQuery, "query string 必须原样保留")
			require.Equal(t, "https://kedaya.ai"+tc.path, seen.Header, "header 必须原样保留")
			if tc.method == http.MethodPost {
				require.Equal(t, body, seen.Body, "body 必须原样保留")
			}
		})
	}
}

// TestGatewayPathAliasNeverFoldsV1BetaIntoV1 反例：/v1beta 不是 /v1 的重复写，
// 任何前缀下的 v1beta 段都不能被折叠掉或折成 /v1。
func TestGatewayPathAliasNeverFoldsV1BetaIntoV1(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/antigravity/v1beta/models/*modelAction"},
		{http.MethodPost, "/v1beta/v1beta/models/gemini-3-pro:generateContent"},
		{http.MethodPost, "/antigravity/v1beta/v1beta/models/gemini-3-pro:generateContent"},
		{http.MethodGet, "/antigravity/v1/v1beta/models"},
		{http.MethodGet, "/v1/v1beta/models"},
	} {
		probe.reset()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)

		for _, seen := range probe.seen {
			require.NotContains(t, seen.Path, "/v1/models", "path=%q 不能被折成 /v1 路径", tc.path)
			require.NotEqual(t, "/v1beta/models", strings.TrimPrefix(seen.Path, ""), "path=%q", tc.path)
		}
		require.NotContains(t, w.Body.String(), "gateway_path_not_found", "path=%q 不应触发诊断", tc.path)
	}
}

// TestGatewayPathAliasCodexPrefixNormalizedFormsPassThrough 确认 /backend-api/codex 前缀
// 下的规范写法不被改写：它本身就带端点、没有版本段，属于合规路径（应为 401 而不是 404）。
// 多写版本段的写法（/backend-api/codex/v1/responses、/v1/v1/responses …）属于
// “真实路由不带版本段的前缀族”规则：整段剥掉后命中 /backend-api/codex/responses，
// 同样是 401（见 TestGatewayPathAliasCodexVersionlessPrefixStripsAnyVersionSegments）。
func TestGatewayPathAliasCodexPrefixNormalizedFormsPassThrough(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/backend-api/codex/responses"},
		{http.MethodGet, "/backend-api/codex/models"},
	}
	for _, tc := range cases {
		probe.reset()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, req)

		require.Equal(t, http.StatusUnauthorized, w.Code, "path=%q 应命中网关鉴权", tc.path)
		require.Equal(t, tc.path, probe.last(t).Path, "path=%q 不能被改写", tc.path)
	}
}

// TestGatewayPathAliasCodexVersionlessPrefixStripsAnyVersionSegments 对应需求 R1：
// /backend-api/codex 的真实路由不带版本段（/backend-api/codex/responses），所以客户端
// 多写的任意个版本段都整段剥掉，必须进鉴权（401）而不是 404。
func TestGatewayPathAliasCodexVersionlessPrefixStripsAnyVersionSegments(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	body := `{"model":"gpt-5","stream":true}`
	cases := []struct {
		method    string
		path      string
		canonical string
	}{
		{http.MethodPost, "/backend-api/codex/responses", "/backend-api/codex/responses"},
		{http.MethodPost, "/backend-api/codex/v1/responses", "/backend-api/codex/responses"},
		{http.MethodPost, "/backend-api/codex/v1/v1/responses", "/backend-api/codex/responses"},
		{http.MethodPost, "/backend-api/codex/v1/v1/v1/responses", "/backend-api/codex/responses"},
		{http.MethodPost, "/backend-api/codex/v1beta/responses", "/backend-api/codex/responses"},
		{http.MethodGet, "/backend-api/codex/v1/models", "/backend-api/codex/models"},
		{http.MethodGet, "/backend-api/codex/v1beta/v1beta/models", "/backend-api/codex/models"},
		// /responses 的 catch-all 子路径同样保留。
		{http.MethodPost, "/backend-api/codex/v1/responses/input_items", "/backend-api/codex/responses/input_items"},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.path, "/", "_"), func(t *testing.T) {
			probe.reset()
			requestPath := tc.path + "?trace=%2Fabc&stream=true"
			var requestBody io.Reader
			if tc.method == http.MethodPost {
				requestBody = strings.NewReader(body)
			}
			req := httptest.NewRequest(tc.method, requestPath, requestBody)
			req.Header.Set("X-Client-Base-Url", "https://kedaya.ai"+tc.path)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handlerUnderTest.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code,
				"method=%s path=%q 应进入鉴权返回 401（而不是 404）", tc.method, tc.path)
			seen := probe.last(t)
			require.Equal(t, tc.method, seen.Method, "HTTP method 必须保留")
			require.Equal(t, tc.canonical, seen.Path, "path=%q 应规范化为 %q", tc.path, tc.canonical)
			require.Equal(t, "trace=%2Fabc&stream=true", seen.RawQuery, "query string 必须原样保留")
			require.Equal(t, "https://kedaya.ai"+tc.path, seen.Header, "header 必须原样保留")
			if tc.method == http.MethodPost {
				require.Equal(t, body, seen.Body, "body 必须原样保留")
			}
		})
	}
}

// TestGatewayPathAliasAntigravityExactRouteNotRewritten 对应需求 R2 的“原路径命中就不动”：
// /antigravity/models 是真实路由，必须原样透传（401），不能补成 /antigravity/v1/models。
// 真实路由表里 /antigravity 下没有裸的 /antigravity/models/:model，所以 /antigravity/models/xxx
// 保持 404（保守规则：只有补版本段能唯一命中时才补）。
func TestGatewayPathAliasAntigravityExactRouteNotRewritten(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	// /antigravity/models 是真实路由，命中后由 regional 中间件先返回 401（而不是 404，
	// 也不是被补成 /antigravity/v1/models）。这里只需要断言“没有被重写成带版本段的
	// 路径”：如果被重写，鉴权探针会看到 /antigravity/v1/models。
	probe.reset()
	w := httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/antigravity/models?x=1", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code, "/antigravity/models 是真实路由，应命中路由而不是 404")
	for _, seen := range probe.seen {
		require.Equal(t, "/antigravity/models", seen.Path, "/antigravity/models 不能被改写")
		require.Equal(t, "x=1", seen.RawQuery, "query string 必须原样保留")
	}

	// R2（补版本段）：生产路由表里没有 /antigravity/models/:model，但唯一存在
	// /antigravity/v1beta/models/:model，所以 /antigravity/models/xxx 补上版本段后
	// 命中 /antigravity/v1beta/models/xxx，必须进鉴权（401）而不是 404。
	// 注意 /antigravity：v1 下只有 /antigravity/v1/models（无 :model），v1beta 下才有
	// :model，所以这里只命中一条模板。
	probe.reset()
	w = httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/antigravity/models/gemini-3-pro", nil))
	require.Equal(t, http.StatusUnauthorized, w.Code,
		"/antigravity/models/gemini-3-pro 应补版本段后命中 /antigravity/v1beta/models/:model 并进入鉴权")
	for _, seen := range probe.seen {
		require.Equal(t, "/antigravity/v1beta/models/gemini-3-pro", seen.Path, "应补成唯一命中的版本族")
	}

	// 反例：命中多条模板时保持原样 404。生产路由表里 /antigravity/v1beta/models 与
	// /antigravity/v1beta/models/:model 都存在，且没有裸的 /antigravity/models/:model，
	// 这里构造 /antigravity/v1beta/models 之外再验证“未知端点不补”。
	probe.reset()
	w = httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/antigravity/definitely-not-a-route", nil))
	require.Equal(t, http.StatusNotFound, w.Code, "未知端点不能凭空补版本段")
	require.Empty(t, probe.seen)
}

// TestGatewayPathAliasDiagnosticsAndSafetyBoundariesUnchanged 覆盖反例：诊断 JSON 格式、
// 非网关路径、/v1beta 不折成 /v1、/api/v1/* 不改写。
func TestGatewayPathAliasDiagnosticsAndSafetyBoundariesUnchanged(t *testing.T) {
	router, probe := newGatewayPathAliasRoutesRouter(t)
	router.GET("/api/v1/admin/users", func(c *gin.Context) { c.String(http.StatusOK, "admin-api") })
	handlerUnderTest := servermiddleware.GatewayPathAlias(router)

	// 反例 1：/v1beta 不能被折成 /v1（不同族绝不折叠）。
	for _, path := range []string{"/v1beta/models", "/antigravity/v1beta/models"} {
		probe.reset()
		w := httptest.NewRecorder()
		handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusUnauthorized, w.Code, "path=%q 应命中网关路由", path)
		for _, seen := range probe.seen {
			require.Equal(t, path, seen.Path, "path=%q 不能被改写", path)
		}
	}
	// 反例 2：/api/v1/* 一律不改写。
	probe.reset()
	w := httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/v1/admin/x", nil))
	require.Equal(t, http.StatusNotFound, w.Code, "/api/v1/v1/admin/x 不能命中任何网关路由")
	probe.reset()
	w = httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "admin-api", w.Body.String(), "/api/v1/* 必须原样透传")
	require.Empty(t, probe.seen, "/api/v1/* 不能进网关鉴权")

	// 反例 3：未知路径仍 404，且诊断 JSON 格式不变。
	probe.reset()
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
	req.Host = "kedaya.ai"
	handlerUnderTest.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	var payload struct {
		Error struct {
			Type           string `json:"type"`
			Code           string `json:"code"`
			Message        string `json:"message"`
			Hint           string `json:"hint"`
			OriginalPath   string `json:"original_path"`
			NormalizedPath string `json:"normalized_path"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, "invalid_request_error", payload.Error.Type)
	require.Equal(t, "gateway_path_not_found", payload.Error.Code)
	require.Contains(t, payload.Error.Hint, "Base URL")
	require.Equal(t, "/v1/v1/v1/definitely-not-a-route", payload.Error.OriginalPath)
	require.Equal(t, "/v1/definitely-not-a-route", payload.Error.NormalizedPath)

	// 反例 4：codex 前缀下的未知端点不会被凭空补/剥成别的路由。
	probe.reset()
	w = httptest.NewRecorder()
	handlerUnderTest.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/backend-api/codex/v1/definitely-not-a-route", strings.NewReader(`{}`)))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, probe.seen, "未知端点不能进入鉴权")
}
