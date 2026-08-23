package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// committedGatewayKeepaliveContext forces the pre-header heartbeat through the
// service manager instead of waiting on a timer, keeping these protocol tests
// deterministic while still exercising the same writer/lifecycle path.
func committedGatewayKeepaliveContext(t *testing.T, path string) (*gin.Context, *httptest.ResponseRecorder, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	stop := service.StartOpenAIStreamSSEKeepalive(c, time.Hour)
	written, err, handled := service.WriteOpenAIStreamSSEHeartbeat(c, []byte(": keepalive\n\n"))
	require.True(t, handled)
	require.NoError(t, err)
	require.Positive(t, written)
	// The handler's accounting marker is normally updated by the slot-wait
	// helper immediately after this service call.
	recordGatewayStreamHeartbeat(c, written)
	return c, recorder, stop
}

func TestGatewayStreamingAwareErrorAfterPreHeaderKeepalive(t *testing.T) {
	c, recorder, stop := committedGatewayKeepaliveContext(t, EndpointMessages)
	defer stop()

	(&GatewayHandler{}).handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "provider stalled", false)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, ": keepalive\n\n")
	require.Contains(t, body, `data: {"type":"error"`)
	require.Contains(t, body, "provider stalled")
	require.NotContains(t, body, `{"type":"error","error":{"code"`)
}

func TestGatewayResponsesErrorAfterPreHeaderKeepaliveUsesResponseFailed(t *testing.T) {
	c, recorder, stop := committedGatewayKeepaliveContext(t, EndpointResponses)
	defer stop()

	(&GatewayHandler{}).responsesErrorResponse(c, http.StatusBadGateway, "upstream_error", "provider stalled")

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Contains(t, body, "event: response.failed\n")
	require.Contains(t, body, `"type":"response.failed"`)
	require.NotContains(t, body, `{"error":{"code"`)
}

func TestGatewayChatCompletionsFailoverAfterPreHeaderKeepaliveEmitsOneError(t *testing.T) {
	c, recorder, stop := committedGatewayKeepaliveContext(t, EndpointChatCompletions)
	defer stop()

	(&GatewayHandler{}).chatCompletionsErrorResponse(c, http.StatusBadGateway, "upstream_error", "provider stalled")

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	require.Equal(t, 1, strings.Count(body, `data: {"type":"error"`))
	require.NotContains(t, body, `{"error":{"type"`)
}

func TestGatewayStreamingErrorsBeforeFirstKeepaliveRemainJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointResponses, nil)
	stop := service.StartOpenAIStreamSSEKeepalive(c, time.Hour)
	defer stop()

	(&GatewayHandler{}).responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "bad request")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), `{"error":{"code":"invalid_request_error"`)
	require.NotContains(t, recorder.Body.String(), "event: response.failed")
}

func TestGatewayStreamingAwareErrorAppendsAfterSemanticBytes(t *testing.T) {
	c, recorder, stop := committedGatewayKeepaliveContext(t, EndpointMessages)
	defer stop()

	_, err := c.Writer.Write([]byte("event: message_start\ndata: {}\n\n"))
	require.NoError(t, err)
	(&GatewayHandler{}).handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "late failure", false)

	require.Contains(t, recorder.Body.String(), "late failure")
}
