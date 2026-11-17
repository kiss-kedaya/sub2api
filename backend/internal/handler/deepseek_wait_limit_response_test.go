package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The service ends a silent DeepSeek request with this failover error (see
// service/openai_upstream_wait_limit.go). These are the bytes the client gets.
func TestUpstreamWaitLimitClientResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	failover := &service.UpstreamFailoverError{
		StatusCode:        http.StatusGatewayTimeout,
		ResponseBody:      []byte(`{"error":{"type":"upstream_error","message":"Upstream did not respond in time"}}`),
		Scope:             service.GatewayFailureScopeRequest,
		NextAccountAction: service.NextAccountStop,
	}
	require.False(t, failover.ShouldRetryNextAccount())

	t.Run("before anything was written", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failover, false)
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
		require.JSONEq(t, `{"error":{"type":"upstream_error","message":"Upstream did not respond in time"}}`, rec.Body.String())
	})

	t.Run("after the heartbeat committed the stream", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte(": keepalive\n\n"))
		(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, failover, true)
		require.Equal(t, http.StatusOK, rec.Code)
		t.Logf("stream body: %q", rec.Body.String())
		require.Contains(t, rec.Body.String(), "Upstream did not respond in time")
		require.Contains(t, rec.Body.String(), "response.failed")
	})
}
