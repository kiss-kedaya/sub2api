package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestInfiniteCanvasHandler_ServesLocalIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>canvas</html>"), 0o644))
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", dir)
	t.Setenv("INFINITE_CANVAS_UPSTREAM", "")

	r := gin.New()
	r.Use(InfiniteCanvasHandler())
	r.GET("/other", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/canvas/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "canvas")

	other := httptest.NewRecorder()
	r.ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/other", nil))
	require.Equal(t, http.StatusOK, other.Code)
	require.Equal(t, "ok", other.Body.String())
}

func TestInfiniteCanvasHandler_ProxiesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const query = "v=123&tag=one&tag=two&q=hello+world&next=%2Fcanvas%2F&empty="
	tests := []struct {
		name         string
		upstreamPath string
		requestPath  string
		wantPath     string
		wantRawPath  string
	}{
		{name: "index", requestPath: "/canvas/", wantPath: "/canvas/"},
		{name: "asset", requestPath: "/canvas/assets/app.js", wantPath: "/canvas/assets/app.js"},
		{name: "upstream_root", upstreamPath: "/", requestPath: "/canvas/assets/app.js", wantPath: "/canvas/assets/app.js"},
		{name: "upstream_prefix", upstreamPath: "/base", requestPath: "/canvas/assets/app.js", wantPath: "/base/canvas/assets/app.js"},
		{name: "encoded_path_with_prefix", upstreamPath: "/base/", requestPath: "/canvas/boards/board%2Fone", wantPath: "/base/canvas/boards/board/one", wantRawPath: "/base/canvas/boards/board%2Fone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan *http.Request, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r
				w.Header().Set("Content-Type", "text/plain")
				_, _ = io.WriteString(w, "upstream canvas")
			}))
			t.Cleanup(upstream.Close)
			upstreamURL, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
			t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL+tt.upstreamPath)

			r := gin.New()
			r.Use(InfiniteCanvasHandler())
			gateway := httptest.NewServer(r)
			t.Cleanup(gateway.Close)
			client := gateway.Client()
			client.Timeout = 5 * time.Second
			req, err := http.NewRequest(http.MethodGet, gateway.URL+tt.requestPath+"?"+query, nil)
			require.NoError(t, err)
			req.Host = "canvas.example.test"
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "upstream canvas", string(body))
			require.Equal(t, "text/plain", resp.Header.Get("Content-Type"))

			select {
			case got := <-received:
				require.Equal(t, tt.wantPath, got.URL.Path)
				require.Equal(t, tt.wantRawPath, got.URL.RawPath)
				require.Equal(t, query, got.URL.RawQuery)
				require.Equal(t, upstreamURL.Host, got.Host)
			default:
				t.Fatal("upstream did not receive the request")
			}
		})
	}
}

func TestIsInfiniteCanvasPath(t *testing.T) {
	require.True(t, isInfiniteCanvasPath("/canvas"))
	require.True(t, isInfiniteCanvasPath("/canvas/"))
	require.True(t, isInfiniteCanvasPath("/canvas/assets/app.js"))
	require.False(t, isInfiniteCanvasPath("/infinite-canvas"))
	require.False(t, isInfiniteCanvasPath("/v1/models"))
}

func TestInfiniteCanvasHandler_AllowsSameOriginEmbed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>canvas</html>"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644))
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", dir)
	t.Setenv("INFINITE_CANVAS_UPSTREAM", "")

	r := gin.New()
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: ""}, nil))
	r.Use(InfiniteCanvasHandler())
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "home") })
	r.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	home := httptest.NewRecorder()
	r.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, "DENY", home.Header().Get("X-Frame-Options"))
	require.Contains(t, home.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
	require.NotContains(t, home.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'")

	for _, path := range []string{"/canvas/", "/canvas/app.js"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code, path)
		require.Equal(t, []string{"SAMEORIGIN"}, w.Header().Values("X-Frame-Options"), path)
		csp := w.Header().Get("Content-Security-Policy")
		require.Contains(t, csp, "frame-ancestors 'self'", path)
		require.NotContains(t, csp, "frame-ancestors 'none'", path)
		require.Contains(t, csp, "'nonce-", path)
	}

	api := httptest.NewRecorder()
	r.ServeHTTP(api, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	require.Equal(t, "DENY", api.Header().Get("X-Frame-Options"))
	require.Empty(t, api.Header().Get("Content-Security-Policy"))
}

func TestInfiniteCanvasHandler_OverridesUpstreamFrameDeny(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "upstream canvas")
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
	t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL)

	r := gin.New()
	r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: ""}, nil))
	r.Use(InfiniteCanvasHandler())
	gateway := httptest.NewServer(r)
	t.Cleanup(gateway.Close)

	resp, err := gateway.Client().Get(gateway.URL + "/canvas/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "SAMEORIGIN", resp.Header.Get("X-Frame-Options"))
	for _, csp := range resp.Header.Values("Content-Security-Policy") {
		require.NotContains(t, csp, "frame-ancestors 'none'")
		require.Contains(t, csp, "frame-ancestors 'self'")
	}
}
