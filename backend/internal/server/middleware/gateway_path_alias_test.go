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
}

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
		})
		c.Status(http.StatusOK)
	}
	router.POST("/v1/messages", record)
	router.POST("/v1/messages/count_tokens", record)
	router.POST("/v1/chat/completions", record)
	router.POST("/v1/responses", record)
	router.OPTIONS("/v1/messages", record)
	router.GET("/v1/models", record)
	router.GET("/v1/models/:model", record)
	router.DELETE("/v1/images/batches/:id", record)

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
	router.GET("/assets/index-abc.js", trap(http.StatusTeapot))
	router.GET("/health", trap(http.StatusTeapot))
	router.GET("/usage", trap(http.StatusTeapot))
	router.GET("/v1beta/models", trap(http.StatusTeapot))
	router.GET("/admin/dashboard", trap(http.StatusTeapot))
	return router, &seen
}

func TestFoldDuplicateVersionPrefix(t *testing.T) {
	cases := []struct {
		path   string
		folded string
		count  int
	}{
		{path: "/v1/messages", folded: "/v1/messages", count: 1},
		{path: "/v1", folded: "/v1", count: 1},
		{path: "/v1/", folded: "/v1/", count: 1},
		{path: "/v1/v1/messages", folded: "/v1/messages", count: 2},
		{path: "/v1/v1/v1/messages", folded: "/v1/messages", count: 3},
		{path: "/v1/v1/v1/v1/v1/v1/messages", folded: "/v1/messages", count: 6},
		{path: "/v1/v1/messages/count_tokens", folded: "/v1/messages/count_tokens", count: 2},
		{path: "/v1/v1/models/claude-opus-5", folded: "/v1/models/claude-opus-5", count: 2},
		{path: "/v1/v1", folded: "/v1", count: 2},
		{path: "/v1/v1/", folded: "/v1/", count: 2},
		// 以下路径都不应被折叠。
		{path: "/messages", folded: "/messages", count: 0},
		{path: "/api/v1/admin/users", folded: "/api/v1/admin/users", count: 0},
		{path: "/v1beta/models", folded: "/v1beta/models", count: 0},
		{path: "/v1/v1beta/models", folded: "/v1/v1beta/models", count: 1},
		{path: "/v1/v12/models", folded: "/v1/v12/models", count: 1},
		{path: "/v2/messages", folded: "/v2/messages", count: 0},
		{path: "/", folded: "/", count: 0},
		{path: "", folded: "", count: 0},
	}
	for _, tc := range cases {
		folded, count := FoldDuplicateVersionPrefix(tc.path)
		require.Equal(t, tc.folded, folded, "path=%q", tc.path)
		require.Equal(t, tc.count, count, "path=%q", tc.path)
	}
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
}

func TestGatewayPathAliasAddsMissingVersionPrefix(t *testing.T) {
	router, seen := newGatewayPathAliasTestRouter()
	handler := GatewayPathAlias(router)

	req := httptest.NewRequest(http.MethodPost, "/messages?x=1", strings.NewReader(`{"model":"claude-opus-5"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, *seen, 1)
	got := (*seen)[0]
	require.Equal(t, "/v1/messages", got.Path)
	require.Equal(t, "x=1", got.RawQuery)
	require.Equal(t, PathAliasRuleMissingVersionPrefix, got.AliasRule)
	require.Equal(t, "/messages", got.AliasOrig)
	require.Equal(t, 0, got.AliasDupes)
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
	// 这些路径要么本来就带单 /v1（规范），要么不属于网关协议根路径；
	// 规范化必须原样透传，尤其不能把 /api/v1、/admin、/assets、/health、/usage 改掉。
	cases := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/v1/admin/users", http.StatusTeapot},
		{http.MethodGet, "/assets/index-abc.js", http.StatusTeapot},
		{http.MethodGet, "/health", http.StatusTeapot},
		{http.MethodGet, "/usage", http.StatusTeapot},
		{http.MethodGet, "/v1beta/models", http.StatusTeapot},
		{http.MethodGet, "/admin/dashboard", http.StatusTeapot},
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
