//go:build unit

package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekStreamSilenceWSTurnKeepsZeroSettlementWithoutHealthPenalty(t *testing.T) {
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusGatewayTimeout, Reason: service.GatewayFailureReason("upstream_wait_limit"), NextAccountAction: service.NextAccountStop}
	result := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{InputTokens: 1000}, NonBillableUpstreamError: true}
	require.True(t, openAIWSTurnSilenceFailure(result, errors.Join(errors.New("synthetic read failure"), failure)))
	require.False(t, openAIWSTurnSilenceFailure(result, errors.New("synthetic reset")))
	require.False(t, openAIWSTurnSilenceFailure(nil, failure))
	require.False(t, openAIWSTurnSilenceFailure(&service.OpenAIForwardResult{}, failure))
}

func TestDeepSeekStreamSilenceHandlerDoesNotDuplicateCommittedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		for _, stream := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/buffered", true: "/stream"}[stream], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				service.MarkResponseCommitted(c)
				if stream {
					c.Data(http.StatusOK, "text/event-stream", []byte("event: error\ndata: {\"error\":{\"message\":\"Upstream did not respond in time\"}}\n\n"))
				} else {
					c.Data(http.StatusGatewayTimeout, "application/json", []byte(`{"error":{"message":"Upstream did not respond in time"}}`))
				}
				before := rec.Body.String()
				failure := &service.UpstreamFailoverError{StatusCode: http.StatusGatewayTimeout, Reason: service.GatewayFailureReason("upstream_wait_limit"), NextAccountAction: service.NextAccountStop}
				h := &OpenAIGatewayHandler{}
				if path == "/v1/messages" {
					h.handleAnthropicFailoverExhausted(c, failure, stream)
				} else {
					h.handleFailoverExhausted(c, failure, stream)
				}
				require.Equal(t, before, rec.Body.String(), "the service already emitted the protocol-specific terminal")
			})
		}
	}
}
