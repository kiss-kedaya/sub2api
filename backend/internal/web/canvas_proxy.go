package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	infiniteCanvasPathPrefix = "/canvas"
	defaultCanvasStaticDir   = "/opt/infinite-canvas/html/canvas"
)

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
	if parsed, err := url.Parse(upstream); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		proxy = httputil.NewSingleHostReverseProxy(parsed)
		original := proxy.Director
		proxy.Director = func(req *http.Request) {
			original(req)
			req.Host = parsed.Host
		}
	}

	fileServer := http.StripPrefix(infiniteCanvasPathPrefix, http.FileServer(http.Dir(staticDir)))

	return func(c *gin.Context) {
		if !isInfiniteCanvasPath(c.Request.URL.Path) {
			c.Next()
			return
		}
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

func serveInfiniteCanvasFile(c *gin.Context, staticDir string, fileServer http.Handler) bool {
	if staticDir == "" {
		return false
	}
	info, err := os.Stat(staticDir)
	if err != nil || !info.IsDir() {
		return false
	}
	rel := strings.TrimPrefix(c.Request.URL.Path, infiniteCanvasPathPrefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	clean := path.Clean("/" + rel)
	full := filepath.Join(staticDir, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if st, err := os.Stat(full); err == nil && !st.IsDir() {
		fileServer.ServeHTTP(c.Writer, c.Request)
		c.Abort()
		return true
	}
	index := filepath.Join(staticDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		return false
	}
	c.File(index)
	c.Abort()
	return true
}
