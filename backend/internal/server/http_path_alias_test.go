//go:build unit

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestProvideHTTPServerAppliesGatewayPathAlias 确认生产 HTTP handler 真的套了
// 网关路径规范化层（0~N 个 /v1 前缀都能命中同一个普通路由）。
func TestProvideHTTPServerAppliesGatewayPathAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/messages", func(c *gin.Context) {
		c.String(http.StatusOK, "canonical")
	})

	srv := ProvideHTTPServer(&config.Config{}, router)
	require.NotNil(t, srv.Handler)

	for _, requestPath := range []string{
		"/v1/messages",
		"/v1/v1/messages",
		"/v1/v1/v1/v1/v1/v1/messages",
		"/messages",
	} {
		req := httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		srv.Handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "path=%q", requestPath)
		require.Equal(t, "canonical", w.Body.String(), "path=%q", requestPath)
	}

	// 折叠后仍然不存在的子路径要拿到 JSON 诊断，而不是空 404。
	req := httptest.NewRequest(http.MethodPost, "/v1/v1/definitely-not-a-route", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "application/json")
	require.Contains(t, w.Body.String(), "Base URL")
}
