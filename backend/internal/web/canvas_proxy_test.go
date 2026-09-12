package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func TestIsInfiniteCanvasPath(t *testing.T) {
	require.True(t, isInfiniteCanvasPath("/canvas"))
	require.True(t, isInfiniteCanvasPath("/canvas/"))
	require.True(t, isInfiniteCanvasPath("/canvas/assets/app.js"))
	require.False(t, isInfiniteCanvasPath("/infinite-canvas"))
	require.False(t, isInfiniteCanvasPath("/v1/models"))
}
