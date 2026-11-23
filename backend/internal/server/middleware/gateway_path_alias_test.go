package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayPathAliasSeen struct {
	Method     string
	Path       string
	RawPath    string
	RawQuery   string
	Header     string
	Body       string
	HasAlias   bool
	AliasRule  PathAliasRule
	AliasOrig  string
	AliasDupes int
	AliasPref  string
}

// newGatewayPathAliasTestRouter 按真实网关路由的形状注册路由：
// 根（/v1/*、/v1beta/*、根别名）、/antigravity、/backend-api/codex 三处前缀，
// 外加一批必须原样透传的非网关路径。
func newGatewayPathAliasTestRouter() (*gin.Engine, *[]gatewayPathAliasSeen) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	seen := make([]gatewayPathAliasSeen, 0, 8)
	record := func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		alias, hasAlias := gatewayPathAliasInfoFromContext(c.Request.Context())
		seen = append(seen, gatewayPathAliasSeen{
			Method:     c.Request.Method,
			Path:       c.Request.URL.Path,
			RawPath:    c.Request.URL.RawPath,
			RawQuery:   c.Request.URL.RawQuery,
			Header:     c.GetHeader("X-Test-Header"),
			Body:       string(body),
			HasAlias:   hasAlias,
			AliasRule:  alias.Rule,
			AliasOrig:  alias.Original,
			AliasDupes: alias.DuplicatePrefixes,
			AliasPref:  alias.Prefix,
		})
		c.Status(http.StatusOK)
	}
	// 根前缀与 /v1 版本段
	router.GET("/messages", record)
	router.POST("/messages", record)
	router.POST("/messages/count_tokens", record)
	router.POST("/responses", record)
	router.POST("/chat/completions", record)
	router.GET("/models", record)
	router.GET("/models/:model", record)
	router.POST("/v1/messages", record)
	router.POST("/v1/messages/count_tokens", record)
	router.POST("/v1/chat/completions", record)
	router.POST("/v1/responses", record)
	router.POST("/v1/responses/*subpath", record)
	router.OPTIONS("/v1/messages", record)
	router.GET("/v1/models", record)
	router.GET("/v1/models/:model", record)
	router.DELETE("/v1/images/batches/:id", record)
	router.GET("/v1/usage", record)
	router.GET("/v1/sub2api/billing", record)
	router.GET("/v1beta/models", record)
	router.GET("/v1beta/models/:model", record)
	router.POST("/v1beta/models/*modelAction", record)
	router.GET("/v1beta/cachedContents/:cacheID", record)
	// /antigravity 前缀（版本段与非版本段并存）
	router.GET("/antigravity/models", record)
	router.POST("/antigravity/v1/messages", record)
	router.POST("/antigravity/v1/messages/count_tokens", record)
	router.GET("/antigravity/v1/models", record)
	router.GET("/antigravity/v1beta/models", record)
	router.POST("/antigravity/v1beta/models/*modelAction", record)
	// /backend-api/codex 前缀（版本段与非版本段并存）
	router.GET("/backend-api/codex/models", record)
	router.POST("/backend-api/codex/realtime/calls", record)
	router.POST("/backend-api/codex/responses", record)
	router.POST("/backend-api/codex/v1/responses", record)
	// 反证用：这些路径必须原样透传，不能被规范化。
	trap := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) {
			seen = append(seen, gatewayPathAliasSeen{
				Method: c.Request.Method,
				Path:   c.Request.URL.Path,
			})
			c.Status(status)
		}
	}
	router.GET("/api/v1/admin/users", trap(http.StatusTeapot))
	router.GET("/api/v1/admin/dashboard", trap(http.StatusTeapot))
	router.GET("/assets/index-abc.js", trap(http.StatusTeapot))
	router.GET("/health", trap(http.StatusTeapot))
	router.GET("/usage", trap(http.StatusTeapot))
	router.GET("/admin/dashboard", trap(http.StatusTeapot))
	return router, &seen
}

func TestIsGatewayVersionSegment(t *testing.T) {
	cases := map[string]bool{
		"v1":       true,
		"v1beta":   true,
		"v1alpha":  true,
		"v1a":      true,
		"v10":      false,
		"v12":      false,
		"v2":       false,
		"v1Beta":   false,
		"V1":       false,
		"v":        false,
		"messages": false,
		"1":        false,
	}
	for segment, want := range cases {
		require.Equal(t, want, isGatewayVersionSegment(segment), "segment=%q", segment)
	}
}

func TestGatewayRouteIndexSupportsExpectedPrefixes(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	index := newGatewayRouteIndex(router)

	prefixes := supportedGatewayPrefixes(index)
	for _, want := range []string{
		"v1",          // /v1/messages
		"v1beta",      // /v1beta/models
		"antigravity", // /antigravity/v1/messages
		"backend-api", // /backend-api/codex/*
		"codex",       // /backend-api/codex/*
	} {
		require.Contains(t, prefixes, want, "受支持前缀应包含 %q（实际 %v）", want, prefixes)
	}
	// 协议端点不是前缀：/antigravity/models 只有 models 一条路由。
	require.NotContains(t, prefixes, "antigravity/models")

	// 版本根：哪些前缀下面真的挂着“版本段 + 端点”路由。
	// 根前缀是 ""（/v1/messages、/v1beta/models …），/antigravity、/backend-api/codex 各自独立。
	require.Greater(t, index.gatewayVersionRootCount(""), 0)
	require.Greater(t, index.gatewayVersionRootCount("/antigravity"), 0)
	require.Greater(t, index.gatewayVersionRootCount("/backend-api/codex"), 0)
	// /antigravity/v1beta 本身不是版本根（那是别人多写一层的形态）。
	require.Zero(t, index.gatewayVersionRootCount("/antigravity/v1beta"))
}

func TestGatewayPathAliasPreservesRequestShape(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	body := `{"model":"claude-opus-5","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/v1/v1/messages?beta=1&model=claude%2Fopus", strings.NewReader(body))
	req.Header.Set("X-Test-Header", "keep-me")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	got := (*seen)[0]
	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "/v1/messages", got.Path, "重复 /v1 应折叠成一个")
	require.Equal(t, "", got.RawPath)
	require.Equal(t, "beta=1&model=claude%2Fopus", got.RawQuery, "query string 必须原样保留")
	require.Equal(t, "keep-me", got.Header, "header 必须原样保留")
	require.Equal(t, body, got.Body, "body 必须原样保留")
	require.True(t, got.HasAlias, "handler 应能读到路径规范化信息")
	require.Equal(t, PathAliasRuleDuplicateVersionPrefix, got.AliasRule)
	require.Equal(t, "/v1/v1/v1/messages", got.AliasOrig)
	require.Equal(t, 3, got.AliasDupes)
	require.Equal(t, "/v1", got.AliasPref)
}

func TestGatewayPathAliasFoldsRepeatedVersionSegmentsAfterPrefix(t *testing.T) {
	cases := []struct {
		path       string
		normalized string
		prefix     string
		dupes      int
		body       string
	}{
		// 根 + /v1：历史行为，一条都不能变。
		{"/v1/messages", "", "", 0, `{"model":"claude-opus-5"}`},
		{"/v1/v1/messages", "/v1/messages", "/v1", 2, `{"model":"claude-opus-5"}`},
		{"/v1/v1/v1/messages", "/v1/messages", "/v1", 3, `{"model":"claude-opus-5"}`},
		// /antigravity 前缀：题设 P1 复现点。
		{"/antigravity/v1/messages", "", "", 0, `{"model":"gemini-3-pro"}`},
		{"/antigravity/v1/v1/messages", "/antigravity/v1/messages", "/antigravity/v1", 2, `{"model":"gemini-3-pro"}`},
		{"/antigravity/v1/v1/v1/messages", "/antigravity/v1/messages", "/antigravity/v1", 3, `{"model":"gemini-3-pro"}`},
		{"/antigravity/v1beta/v1beta/models", "/antigravity/v1beta/models", "/antigravity/v1beta", 2, ``},
		{"/antigravity/v1beta/models", "", "", 0, ``},
		// /backend-api/codex 前缀。
		{"/backend-api/codex/v1/v1/responses", "/backend-api/codex/v1/responses", "/backend-api/codex/v1", 2, `{"model":"gpt-5"}`},
		{"/backend-api/codex/v1/v1/v1/responses", "/backend-api/codex/v1/responses", "/backend-api/codex/v1", 3, `{"model":"gpt-5"}`},

		{"/backend-api/codex/v1/responses", "", "", 0, `{"model":"gpt-5"}`},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.path, "/", "_"), func(t *testing.T) {
			router, seen := newGatewayPathAliasTestRouter()
			handler := GatewayPathAlias(router)

			requestPath := tc.path + "?trace=%2Fabc"
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(tc.body))
			} else {
				req = httptest.NewRequest(http.MethodGet, requestPath, nil)
			}
			req.Header.Set("X-Test-Header", "keep-me")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, "path=%q", tc.path)
			require.Len(t, *seen, 1, "path=%q 应命中一个 handler", tc.path)
			got := (*seen)[0]
			wantPath := tc.path
			if tc.normalized != "" {
				wantPath = tc.normalized
			}
			require.Equal(t, wantPath, got.Path, "path=%q", tc.path)
			require.Equal(t, "trace=%2Fabc", got.RawQuery, "path=%q query string 必须原样保留", tc.path)
			require.Equal(t, "keep-me", got.Header, "path=%q header 必须原样保留", tc.path)
			if tc.body != "" {
				require.Equal(t, tc.body, got.Body, "path=%q body 必须原样保留", tc.path)
			}
			if tc.normalized == "" {
				require.False(t, got.HasAlias, "path=%q 本身就是规范写法，不能注入规范化信息", tc.path)
				return
			}
			require.True(t, got.HasAlias, "path=%q 应能读到规范化信息", tc.path)
			require.Equal(t, tc.prefix, got.AliasPref, "path=%q", tc.path)
			require.Equal(t, tc.dupes, got.AliasDupes, "path=%q", tc.path)
			require.Equal(t, tc.path, got.AliasOrig, "path=%q", tc.path)
			require.Equal(t, PathAliasRuleDuplicateVersionPrefix, got.AliasRule, "path=%q", tc.path)
		})
	}
}

func TestGatewayPathAliasNeverFoldsV1BetaIntoV1(t *testing.T) {
	// 反例：/v1beta 不是 /v1 的重复写，任何形式的 /v1beta 路径都不能被折叠成 /v1。
	for _, tc := range []struct {
		method string
		class  string
		path   string
	}{
		{http.MethodGet, "合规路径", "/v1beta/models"},
		{http.MethodGet, "合规路径", "/antigravity/v1beta/models"},
		{http.MethodGet, "合规路径", "/v1beta/cachedContents/v1"}, // cacheID 恰好叫 v1，也不许动
		{http.MethodGet, "未知子路径", "/v1/v1beta/models"},
		{http.MethodGet, "未知子路径", "/v1beta/v1/models"},
		{http.MethodGet, "未知子路径", "/antigravity/v1/v1beta/models"},
	} {
		router, seen := newGatewayPathAliasTestRouter()
		handler := GatewayPathAlias(router)
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		for _, s := range *seen {
			require.Equal(t, tc.path, s.Path, "%s: path=%q 不能被改写", tc.class, tc.path)
			require.False(t, s.HasAlias, "%s: path=%q 不应注入规范化信息", tc.class, tc.path)
		}
		if len(*seen) == 0 {
			require.Equal(t, http.StatusNotFound, w.Code, "%s: path=%q", tc.class, tc.path)
		}
	}
}

func TestGatewayPathAliasAddsMissingVersionPrefix(t *testing.T) {
	cases := []struct {
		method     string
		path       string
		normalized string
		prefix     string
	}{
		// 根前缀裸路径（历史行为）
		{http.MethodPost, "/messages?x=1", "/v1/messages", ""},
		{http.MethodPost, "/responses", "/v1/responses", ""},
		{http.MethodGet, "/models", "/v1/models", ""},
		// 带前缀的裸路径
		{http.MethodPost, "/antigravity/messages", "/antigravity/v1/messages", "/antigravity"},
		{http.MethodGet, "/antigravity/models", "/antigravity/v1/models", "/antigravity"},
	}
	for _, tc := range cases {
		router, seen := newGatewayPathAliasTestRouter()
		handler := GatewayPathAlias(router)

		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"claude-opus-5"}`))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "path=%q", tc.path)
		require.Len(t, *seen, 1, "path=%q", tc.path)
		got := (*seen)[0]
		require.Equal(t, tc.normalized, got.Path, "path=%q", tc.path)
		require.Equal(t, PathAliasRuleMissingVersionPrefix, got.AliasRule, "path=%q", tc.path)
		require.Equal(t, tc.prefix, got.AliasPref, "path=%q", tc.path)
		require.Equal(t, 0, got.AliasDupes, "path=%q", tc.path)
		require.Equal(t, strings.SplitN(tc.path, "?", 2)[0], got.AliasOrig, "path=%q", tc.path)
	}
}

func TestGatewayPathAliasDoesNotAddVersionPrefixToUnknownRoutes(t *testing.T) {
	// 目标路由不存在时不能凭空补版本段：真实业务段保持原语义，继续 404。
	for _, path := range []string{
		"/antigravity/v1/definitely-not-a-route",
		"/antigravity/v1/v1/definitely-not-a-route",
		"/backend-api/codex/alpha",       // 真实路由是 /backend-api/codex/alpha/search
		"/v1beta/definitely-not-a-route", // 前缀下没有可补的 /v1 路由
		"/antigravity/definitely-not-a-route",
		"/definitely-not-a-route",
	} {
		router, seen := newGatewayPathAliasTestRouter()
		handler := GatewayPathAlias(router)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		require.Equal(t, http.StatusNotFound, w.Code, "path=%q", path)
		require.Empty(t, *seen, "path=%q 不应命中任何 handler", path)
	}
}

func TestGatewayPathAliasPreservesHTTPMethod(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodDelete, "/v1/v1/images/batches/42", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	require.Equal(t, http.MethodDelete, (*seen)[0].Method)
	require.Equal(t, "/v1/images/batches/42", (*seen)[0].Path)
}

func TestGatewayPathAliasDoesNotTouchUnrelatedPaths(t *testing.T) {
	// 这些路径要么本来就规范，要么不属于网关协议根路径；必须原样透传。
	cases := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/v1/admin/users", http.StatusTeapot},
		{http.MethodGet, "/api/v1/admin/dashboard", http.StatusTeapot},
		{http.MethodGet, "/assets/index-abc.js", http.StatusTeapot},
		{http.MethodGet, "/health", http.StatusTeapot},
		{http.MethodGet, "/usage", http.StatusTeapot},
		{http.MethodGet, "/admin/dashboard", http.StatusTeapot},
		{http.MethodGet, "/v1beta/models", http.StatusOK},
		{http.MethodGet, "/antigravity/v1beta/models", http.StatusOK},
		{http.MethodGet, "/antigravity/v1/models", http.StatusOK},
		// 单 /v1 的未注册子路径不折叠，交给 gin 原逻辑（404）。
		{http.MethodGet, "/v1/definitely-not-a-route", http.StatusNotFound},
		// 裸路径但不存在对应网关路由时不补前缀，交给 gin 原逻辑（404）。
		{http.MethodGet, "/definitely-not-a-route", http.StatusNotFound},
	}
	for _, tc := range cases {
		router, seen := newGatewayPathAliasTestRouter()
		handler := GatewayPathAlias(router)
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code, "path=%q", tc.path)
		if len(*seen) > 0 {
			require.Equal(t, tc.path, (*seen)[0].Path, "path=%q 不能被重写", tc.path)
			require.False(t, (*seen)[0].HasAlias, "path=%q 不应注入规范化信息", tc.path)
		}
	}
}

func TestGatewayPathAliasUnmatchedDuplicatedPrefixReturnsDiagnostic(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
	req.Host = "kedaya.ai"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, *seen, "未覆盖的子路径不应该进入任何 handler")
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
	require.Contains(t, payload.Error.Hint, "https://kedaya.ai")
	require.Equal(t, "/v1/v1/definitely-not-a-route", payload.Error.OriginalPath)
	require.Equal(t, "/v1/definitely-not-a-route", payload.Error.NormalizedPath)
}

func TestGatewayPathAliasSanitizesHostInDiagnostic(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
	req.Host = "evil.example<script>alert(1)</script>"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "<script>")
	require.Contains(t, w.Body.String(), "your-gateway-host")
}

func TestGatewayPathAliasNonGetPostMethodsAreNotIntercepted(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	// OPTIONS 预检即使多写了 /v1，只要折叠后的规范路径有对应方法的路由就正常重写。
	req := httptest.NewRequest(http.MethodOptions, "/v1/v1/messages", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	require.Equal(t, "/v1/messages", (*seen)[0].Path)

	// 折叠后仍无对应路由时，OPTIONS/HEAD 等方法原样透传给 gin（保护 CORS 预检行为），
	// 不返回 JSON 诊断。
	for _, method := range []string{http.MethodOptions, http.MethodHead} {
		req := httptest.NewRequest(method, "/v1/v1/definitely-not-a-route", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s", method)
		require.NotContains(t, w.Body.String(), "gateway_path_not_found", "method=%s 不应触发 JSON 诊断", method)
	}
}

// TestGatewayPathAliasRootDuplicatedPrefixContinuesToWork 复核：根前缀下的重复版本段
// 如果折叠后的路径存在，就必须折叠并命中（/messages/count_tokens → /v1/messages/count_tokens）。
func TestGatewayPathAliasRootDuplicatedPrefixContinuesToWork(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodPost, "/v1/v1/messages/count_tokens", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	require.Equal(t, "/v1/messages/count_tokens", (*seen)[0].Path)
}

// TestGatewayPathAliasRouteIndexDebug 打印索引摘要，出问题时用 -v 看。
func TestGatewayPathAliasRouteIndexDebug(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	index := newGatewayRouteIndex(router)
	t.Log(gatewayIndexDebugString(index))
}

// TestGatewayPathAliasRouteClassification 保护 gatewayEndpointSegments 集合：网关协议
// 路由必须被识别成网关路由，面板 API / 静态资源 / SPA 不能被识别成网关路由。
// 新增网关端点时如果忘了登记，这里会失败（再配合路由级用例即可发现漏网）。
func TestGatewayPathAliasRouteClassification(t *testing.T) {
	router, _ := newGatewayPathAliasTestRouter()
	index := newGatewayRouteIndex(router)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/messages"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1beta/models"},
		{http.MethodPost, "/antigravity/v1/messages"},
		{http.MethodGet, "/antigravity/v1beta/models"},
		{http.MethodPost, "/backend-api/codex/v1/responses"},
		{http.MethodGet, "/messages"},
		{http.MethodPost, "/chat/completions"},
	} {
		require.True(t, matchGatewayRoute(index, tc.method, tc.path),
			"网关路由 %s %s 应该被索引收录", tc.method, tc.path)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/assets/index-abc.js"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/dashboard"},
	} {
		require.False(t, matchGatewayRoute(index, tc.method, tc.path),
			"非网关路径 %s %s 不能进入网关索引", tc.method, tc.path)
	}
}
