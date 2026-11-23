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
