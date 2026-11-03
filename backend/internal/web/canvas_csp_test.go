package web

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	canvasBootstrapHashOne = "'sha256-m0aYpSd9MsHzKnbbFe87yH92kbeFQYFIknWKtzL4O/g='"
	canvasBootstrapHashTwo = "'sha256-I6LPtG0ZaWWZjaqo01/h/CYOOBc9+Ljxd5XeZLu6aEI='"
)

// canvasThemeBootstrapBody is the real inline theme bootstrap shipped by the
// canvas index.html. TestInfiniteCanvasCSPHashAuthorizesKnownBootstrapOnly hashes it to
// prove canvasBootstrapHashTwo maps to real script content, not a placeholder.
const canvasThemeBootstrapBody = `
            try {
                var s = JSON.parse(localStorage.getItem("infinite-canvas:theme_store") || "{}");
                var t = s.state && s.state.theme === "light" ? "light" : "dark";
                document.documentElement.classList.toggle("dark", t === "dark");
                document.documentElement.style.colorScheme = t;
            } catch (e) {}
        `

func newCanvasTestRouter(withSecurityHeaders bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if withSecurityHeaders {
		r.Use(middleware.SecurityHeaders(config.CSPConfig{Enabled: true, Policy: ""}, nil))
	}
	r.Use(InfiniteCanvasHandler())
	r.GET("/other", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return r
}

func serveCanvasTestRequest(t *testing.T, router *gin.Engine, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(method, target, nil))
	return res
}

func serveCanvasProxyRequest(t *testing.T, router *gin.Engine, method, target string) (*http.Response, []byte) {
	t.Helper()
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	req, err := http.NewRequest(method, gateway.URL+target, nil)
	require.NoError(t, err)
	client := gateway.Client()
	client.Timeout = 5 * time.Second
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

func cspDirective(policy, name string) string {
	for _, segment := range strings.Split(policy, ";") {
		fields := strings.Fields(segment)
		if len(fields) > 0 && strings.EqualFold(fields[0], name) {
			return strings.Join(fields[1:], " ")
		}
	}
	return ""
}

func cspHashSources(policy string) map[string]struct{} {
	hashes := map[string]struct{}{}
	for _, name := range []string{"script-src", "script-src-elem"} {
		for _, source := range strings.Fields(cspDirective(policy, name)) {
			if strings.HasPrefix(source, "'sha256-") && strings.HasSuffix(source, "'") {
				hashes[source] = struct{}{}
			}
		}
	}
	return hashes
}

func requireCanvasHashes(t *testing.T, policy string) {
	t.Helper()
	scriptSrc := cspDirective(policy, "script-src")
	scriptSrcElem := cspDirective(policy, "script-src-elem")
	require.True(t, strings.Contains(scriptSrc, canvasBootstrapHashOne) || strings.Contains(scriptSrcElem, canvasBootstrapHashOne), "policy misses first canvas hash: %q", policy)
	require.True(t, strings.Contains(scriptSrc, canvasBootstrapHashTwo) || strings.Contains(scriptSrcElem, canvasBootstrapHashTwo), "policy misses second canvas hash: %q", policy)
}

func TestAddCanvasScriptHashesToPolicy(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		check  func(t *testing.T, policy string)
	}{
		{
			name:   "adds hashes to script-src and keeps everything else",
			policy: "default-src 'self'; script-src 'self' 'nonce-abc'; frame-ancestors 'none'",
			check: func(t *testing.T, policy string) {
				scriptSrc := cspDirective(policy, "script-src")
				require.Contains(t, scriptSrc, canvasBootstrapHashOne)
				require.Contains(t, scriptSrc, canvasBootstrapHashTwo)
				require.Contains(t, scriptSrc, "'nonce-abc'")
				require.Contains(t, scriptSrc, "'self'")
				require.Equal(t, "'self'", cspDirective(policy, "default-src"))
				require.Equal(t, "'none'", cspDirective(policy, "frame-ancestors"))
			},
		},
		{
			name:   "script-src-elem is extended when present",
			policy: "default-src 'self'; script-src-elem 'self'",
			check: func(t *testing.T, policy string) {
				require.Equal(t, "", cspDirective(policy, "script-src"))
				scriptSrcElem := cspDirective(policy, "script-src-elem")
				require.Contains(t, scriptSrcElem, canvasBootstrapHashOne)
				require.Contains(t, scriptSrcElem, canvasBootstrapHashTwo)
				require.Contains(t, scriptSrcElem, "'self'")
			},
		},
		{
			name:   "both directives are extended independently",
			policy: "script-src 'self'; script-src-elem 'self'",
			check: func(t *testing.T, policy string) {
				for _, name := range []string{"script-src", "script-src-elem"} {
					directive := cspDirective(policy, name)
					require.Contains(t, directive, canvasBootstrapHashOne)
					require.Contains(t, directive, canvasBootstrapHashTwo)
				}
			},
		},
		{
			name:   "policies without script directives are untouched",
			policy: "default-src 'none'; img-src 'self'",
			check: func(t *testing.T, policy string) {
				require.Equal(t, "default-src 'none'; img-src 'self'", policy)
			},
		},
		{
			name:   "directive matching is case insensitive",
			policy: "SCRIPT-SRC 'self'",
			check: func(t *testing.T, policy string) {
				require.Contains(t, policy, canvasBootstrapHashOne)
				require.Contains(t, policy, canvasBootstrapHashTwo)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addCanvasScriptHashesToPolicy(tt.policy)
			tt.check(t, got)
			require.Equal(t, got, addCanvasScriptHashesToPolicy(got), "enhancement must be idempotent")
			expectedHashes := 0
			if cspDirective(got, "script-src") != "" {
				expectedHashes += 2
			}
			if cspDirective(got, "script-src-elem") != "" {
				expectedHashes += 2
			}
			require.Equal(t, expectedHashes, strings.Count(got, "'sha256-"), "only the two verified hashes per directive may be added")
		})
	}
}

func TestAddCanvasScriptHashesHandlesEveryHeaderValue(t *testing.T) {
	header := http.Header{}
	header.Add("Content-Security-Policy", "script-src 'self'")
	header.Add("Content-Security-Policy", "script-src-elem 'self'; frame-ancestors 'none'")
	addCanvasScriptHashes(header)

	values := header.Values("Content-Security-Policy")
	require.Len(t, values, 2)
	for _, value := range values {
		requireCanvasHashes(t, value)
	}
	require.Contains(t, values[0], "script-src 'self'")
	require.Contains(t, values[1], "script-src-elem 'self'")
	require.Equal(t, "'none'", cspDirective(values[1], "frame-ancestors"))

	empty := http.Header{}
	addCanvasScriptHashes(empty)
	require.Empty(t, empty.Values("Content-Security-Policy"))
	addCanvasScriptHashes(nil)
}

// TestInfiniteCanvasCSPHashAuthorizesKnownBootstrapOnly proves policy-level
// authorisation without a browser: the real theme bootstrap content hashes to
// the allowlisted source, while an unknown inline script hashes to a source
// that is absent. CSP only executes inline scripts whose content matches a
// listed hash, so the unknown script is not authorised.
func TestInfiniteCanvasCSPHashAuthorizesKnownBootstrapOnly(t *testing.T) {
	themeDigest := sha256.Sum256([]byte(canvasThemeBootstrapBody))
	themeSource := "'sha256-" + base64.StdEncoding.EncodeToString(themeDigest[:]) + "'"
	require.Equal(t, canvasBootstrapHashTwo, themeSource, "allowlist entry must map to the real theme bootstrap body")

	unknownBody := "\nconsole.log(\"unknown inline\");\n"
	unknownDigest := sha256.Sum256([]byte(unknownBody))
	unknownSource := "'sha256-" + base64.StdEncoding.EncodeToString(unknownDigest[:]) + "'"
	require.NotEqual(t, canvasBootstrapHashOne, unknownSource)
	require.NotEqual(t, canvasBootstrapHashTwo, unknownSource)

	dir := t.TempDir()
	page := "<html><head><script>" + canvasThemeBootstrapBody + "</script></head>" +
		"<body><script>" + unknownBody + "</script></body></html>"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644))
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", dir)
	t.Setenv("INFINITE_CANVAS_UPSTREAM", "")

	res := serveCanvasTestRequest(t, newCanvasTestRouter(true), http.MethodGet, "/canvas/")
	require.Equal(t, http.StatusOK, res.Code)
	policy := res.Header().Get("Content-Security-Policy")
	allowed := cspHashSources(policy)

	require.Equal(t, map[string]struct{}{
		canvasBootstrapHashOne: {},
		canvasBootstrapHashTwo: {},
	}, allowed, "policy must authorise exactly the two verified bootstrap hashes")
	require.Contains(t, allowed, themeSource)
	require.NotContains(t, allowed, unknownSource)
	require.NotContains(t, cspDirective(policy, "script-src"), "'unsafe-inline'")
	for _, source := range strings.Fields(cspDirective(policy, "script-src")) {
		require.NotEqual(t, "*", source, "wildcard script source would authorise unknown inline scripts")
	}
}

func TestInfiniteCanvasStaticHTMLCSPHashes(t *testing.T) {
	trustedOne := "<script>\nconst one = true;\n</script>"
	trustedTwo := "<script type=\"module\">\nconsole.log(\"two\");\n</script>"
	untrusted := "<script>alert(\"untrusted\");</script>"
	page := "<!doctype html><html><head>" + trustedOne + "</head><body>" + trustedTwo + untrusted + "</body></html>"

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(page), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('asset');"), 0o644))
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", dir)
	t.Setenv("INFINITE_CANVAS_UPSTREAM", "")

	router := newCanvasTestRouter(true)

	get := serveCanvasTestRequest(t, router, http.MethodGet, "/canvas/")
	require.Equal(t, http.StatusOK, get.Code)
	require.Equal(t, page, get.Body.String(), "HTML body must not be rewritten")
	cspValues := get.Header().Values("Content-Security-Policy")
	require.Len(t, cspValues, 1)
	policy := cspValues[0]
	require.Contains(t, policy, "'nonce-")
	requireCanvasHashes(t, policy)
	require.NotContains(t, cspDirective(policy, "script-src"), "'unsafe-inline'")
	require.Equal(t, 2, strings.Count(policy, "'sha256-"))
	require.Equal(t, "SAMEORIGIN", get.Header().Get("X-Frame-Options"))
	require.NotContains(t, get.Body.String(), `nonce="`)

	head := serveCanvasTestRequest(t, router, http.MethodHead, "/canvas/")
	require.Equal(t, http.StatusOK, head.Code)
	require.Empty(t, head.Body.String())
	requireCanvasHashes(t, head.Header().Get("Content-Security-Policy"))

	asset := serveCanvasTestRequest(t, router, http.MethodGet, "/canvas/app.js")
	require.Equal(t, http.StatusOK, asset.Code)
	require.Equal(t, "console.log('asset');", asset.Body.String())
	require.NotEqual(t, "no-store", asset.Header().Get("Cache-Control"))

	spa := serveCanvasTestRequest(t, router, http.MethodGet, "/canvas/boards/board-1")
	require.Equal(t, http.StatusOK, spa.Code)
	require.Equal(t, page, spa.Body.String())
	requireCanvasHashes(t, spa.Header().Get("Content-Security-Policy"))
}

func TestInfiniteCanvasProxyHTMLKeepsCSPAndAddsHashes(t *testing.T) {
	upstreamPage := "<html><body><script>alert(\"upstream inline\");</script></body></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; frame-ancestors 'none'")
		w.Header().Set("ETag", `"upstream"`)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = io.WriteString(w, upstreamPage)
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
	t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL)

	resp, body := serveCanvasProxyRequest(t, newCanvasTestRouter(true), http.MethodGet, "/canvas/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, upstreamPage, string(body), "proxy body must not be rewritten")

	values := resp.Header.Values("Content-Security-Policy")
	require.Len(t, values, 2, "gateway and upstream policies must both stay")
	for _, value := range values {
		requireCanvasHashes(t, value)
		require.NotContains(t, value, "frame-ancestors 'none'")
	}
	require.Contains(t, values[0], "'nonce-")
	require.Contains(t, values[1], "default-src 'self'")
	require.Contains(t, values[1], "script-src 'self'")
	require.Equal(t, "public, max-age=3600", resp.Header.Get("Cache-Control"))
	require.Equal(t, `"upstream"`, resp.Header.Get("ETag"))
	require.Equal(t, "SAMEORIGIN", resp.Header.Get("X-Frame-Options"))
}

func TestInfiniteCanvasProxyAddsHashesToEveryUpstreamCSPHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Add("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		w.Header().Add("Content-Security-Policy", "default-src 'self'; script-src-elem 'self'")
		_, _ = io.WriteString(w, "<html><body>canvas</body></html>")
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
	t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL)

	resp, body := serveCanvasProxyRequest(t, newCanvasTestRouter(true), http.MethodGet, "/canvas/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "<html><body>canvas</body></html>", string(body))

	values := resp.Header.Values("Content-Security-Policy")
	require.Len(t, values, 3, "one gateway policy plus two upstream policies")
	scriptSrcElemPolicies := 0
	for _, value := range values {
		requireCanvasHashes(t, value)
		if cspDirective(value, "script-src-elem") != "" {
			scriptSrcElemPolicies++
			require.Contains(t, cspDirective(value, "script-src-elem"), "'self'")
		}
	}
	require.Equal(t, 1, scriptSrcElemPolicies)
}

func TestInfiniteCanvasProxyHEADKeepsCSPHashes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, "<html><body>canvas</body></html>")
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
	t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL)

	resp, body := serveCanvasProxyRequest(t, newCanvasTestRouter(true), http.MethodHead, "/canvas/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, body)
	values := resp.Header.Values("Content-Security-Policy")
	require.Len(t, values, 2)
	for _, value := range values {
		requireCanvasHashes(t, value)
	}
	require.Equal(t, "SAMEORIGIN", resp.Header.Get("X-Frame-Options"))
}

func TestInfiniteCanvasProxyJSAssetBodyUntouched(t *testing.T) {
	const assetBody = `console.log("canvas asset");`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = io.WriteString(w, assetBody)
	}))
	t.Cleanup(upstream.Close)
	t.Setenv("INFINITE_CANVAS_STATIC_DIR", t.TempDir())
	t.Setenv("INFINITE_CANVAS_UPSTREAM", upstream.URL)

	resp, body := serveCanvasProxyRequest(t, newCanvasTestRouter(true), http.MethodGet, "/canvas/assets/app.js")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, assetBody, string(body))
	require.Equal(t, "public, max-age=31536000, immutable", resp.Header.Get("Cache-Control"))
}
