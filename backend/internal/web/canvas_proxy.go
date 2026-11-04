package web

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

const (
	infiniteCanvasPathPrefix = "/canvas"
	defaultCanvasStaticDir   = "/opt/infinite-canvas/html/canvas"
)

// canvasBootstrapScriptHashes are CSP source expressions for the two verified
// canvas bootstrap scripts. A hash only matches a byte-identical script body,
// so no other inline script gains execution.
var canvasBootstrapScriptHashes = []string{
	"'sha256-m0aYpSd9MsHzKnbbFe87yH92kbeFQYFIknWKtzL4O/g='",
	"'sha256-I6LPtG0ZaWWZjaqo01/h/CYOOBc9+Ljxd5XeZLu6aEI='",
}

// InfiniteCanvasHandler serves Infinite Canvas on the same origin as the
// Sub2API UI so the sidebar page does not jump hosts and browser calls to
// /v1 stay same-origin (no CORS). Prefer a local static dir; otherwise reverse
// proxy INFINITE_CANVAS_UPSTREAM.
func InfiniteCanvasHandler() gin.HandlerFunc {
	staticDir := strings.TrimSpace(os.Getenv("INFINITE_CANVAS_STATIC_DIR"))
	if staticDir == "" {
		staticDir = defaultCanvasStaticDir
	}
	upstream := strings.TrimSpace(os.Getenv("INFINITE_CANVAS_UPSTREAM"))
	var proxy *httputil.ReverseProxy
	if parsed, err := parseCanvasUpstream(upstream); err != nil {
		fmt.Fprintf(os.Stderr, "infinite canvas upstream disabled: %v\n", err)
	} else {
		// Operator-configured INFINITE_CANVAS_UPSTREAM; scheme/host already allowlisted.
		proxy = &httputil.ReverseProxy{
			Rewrite: func(req *httputil.ProxyRequest) {
				req.SetURL(parsed)
				req.Out.Host = parsed.Host
			},
			ModifyResponse: func(res *http.Response) error {
				if res != nil {
					middleware.AllowSameOriginEmbedHeaders(res.Header)
					// Every CSP header is a separate policy, so extend each one
					// instead of removing the upstream policy.
					addCanvasScriptHashes(res.Header)
				}
				return nil
			},
		}
	}

	fileServer := http.StripPrefix(infiniteCanvasPathPrefix, http.FileServer(http.Dir(staticDir)))

	return func(c *gin.Context) {
		if !isInfiniteCanvasPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		middleware.AllowSameOriginEmbedHeaders(c.Writer.Header())
		addCanvasScriptHashes(c.Writer.Header())
		if c.Request.URL.Path == infiniteCanvasPathPrefix {
			c.Redirect(http.StatusTemporaryRedirect, infiniteCanvasPathPrefix+"/")
			c.Abort()
			return
		}
		if serveInfiniteCanvasFile(c, staticDir, fileServer) {
			return
		}
		if proxy != nil {
			proxy.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}
		c.String(http.StatusNotFound, "Infinite Canvas is not installed on this host")
		c.Abort()
	}
}

func isInfiniteCanvasPath(pathName string) bool {
	return pathName == infiniteCanvasPathPrefix || strings.HasPrefix(pathName, infiniteCanvasPathPrefix+"/")
}

func parseCanvasUpstream(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty upstream")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("invalid upstream host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("upstream must not contain query or fragment")
	}
	// Rebuild from allowlisted fields only so gosec does not treat env input as a live SSRF URL.
	return &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}, nil
}

func serveInfiniteCanvasFile(c *gin.Context, staticDir string, fileServer http.Handler) bool {
	if staticDir == "" {
		return false
	}
	staticRoot, err := filepath.Abs(staticDir)
	if err != nil {
		return false
	}
	info, err := os.Stat(staticRoot)
	if err != nil || !info.IsDir() {
		return false
	}
	rel := strings.TrimPrefix(c.Request.URL.Path, infiniteCanvasPathPrefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	clean := path.Clean("/" + rel)
	full := filepath.Join(staticRoot, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	relative, err := filepath.Rel(staticRoot, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		fileServer.ServeHTTP(c.Writer, c.Request)
		c.Abort()
		return true
	}
	index := filepath.Join(staticRoot, "index.html")
	if _, err := os.Stat(index); err != nil {
		return false
	}
	c.File(index)
	c.Abort()
	return true
}

// addCanvasScriptHashes extends every Content-Security-Policy value with the
// verified canvas bootstrap hashes. Multiple CSP headers intersect, so each
// policy is enhanced independently and none is removed. Both script-src and
// script-src-elem are covered: when script-src-elem is present it governs
// element scripts and needs its own hash sources.
func addCanvasScriptHashes(header http.Header) {
	if header == nil {
		return
	}
	values := header.Values("Content-Security-Policy")
	if len(values) == 0 {
		return
	}
	header.Del("Content-Security-Policy")
	for _, value := range values {
		header.Add("Content-Security-Policy", addCanvasScriptHashesToPolicy(value))
	}
}

func addCanvasScriptHashesToPolicy(policy string) string {
	segments := strings.Split(policy, ";")
	changed := false
	for i, segment := range segments {
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "script-src", "script-src-elem":
		default:
			continue
		}
		missing := make([]string, 0, len(canvasBootstrapScriptHashes))
		for _, hash := range canvasBootstrapScriptHashes {
			if !cspSourcesContain(fields[1:], hash) {
				missing = append(missing, hash)
			}
		}
		if len(missing) == 0 {
			continue
		}
		trimmed := strings.TrimRight(segment, " \t")
		segments[i] = trimmed + " " + strings.Join(missing, " ") + segment[len(trimmed):]
		changed = true
	}
	if !changed {
		return policy
	}
	return strings.Join(segments, ";")
}

func cspSourcesContain(sources []string, want string) bool {
	for _, source := range sources {
		if source == want {
			return true
		}
	}
	return false
}
