package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapUpstreamErrorForClient_KeepsUpstreamStatusAndMessage(t *testing.T) {
	status, errType, _, message := WrapUpstreamErrorForClient(http.StatusServiceUnavailable, []byte(`{"error":{"type":"overloaded_error","message":"Our servers are currently overloaded. Please try again later."}}`))
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "overloaded_error", errType)
	require.Equal(t, "Our servers are currently overloaded. Please try again later.", message)
}

func TestWrapUpstreamErrorForClient_KeepsRateLimit(t *testing.T) {
	status, errType, _, message := WrapUpstreamErrorForClient(http.StatusTooManyRequests, []byte(`{"error":{"type":"rate_limit_error","message":"Rate limit reached"}}`))
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Rate limit reached", message)
}

func TestWrapUpstreamErrorForClient_DoesNotMap5xxTo502(t *testing.T) {
	status, _, _, message := WrapUpstreamErrorForClient(http.StatusBadGateway, []byte(`{"error":{"message":"Bad Gateway"}}`))
	require.Equal(t, http.StatusBadGateway, status)
	require.Equal(t, "Bad Gateway", message)

	status, errType, _, message := WrapUpstreamErrorForClient(http.StatusInternalServerError, []byte(`{"error":{"message":"Internal error during token generation"}}`))
	require.Equal(t, http.StatusInternalServerError, status)
	require.Equal(t, "api_error", errType)
	require.Equal(t, "Internal error during token generation", message)
}

func TestWrapUpstreamErrorForClient_RemapsWrappedClientJSONObjectError(t *testing.T) {
	body := []byte(`{"error":{"message":"Response input messages must contain the word 'json' in some form to use 'text.format' of type 'json_object'."}}`)
	status, errType, _, message := WrapUpstreamErrorForClient(http.StatusBadGateway, body)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request_error", errType)
	require.Contains(t, message, "json_object")
}

func TestWrapUpstreamErrorForClient_RemapsWebsocketPolicyViolation(t *testing.T) {
	status, errType, _, message := WrapUpstreamErrorForClient(0, []byte(`websocket: close 1008 (policy violation)`))
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "permission_error", errType)
	require.Contains(t, message, "1008")
}

func TestWrapUpstreamErrorForClient_ClaudeAndGeminiBodies(t *testing.T) {
	status, errType, _, message := WrapUpstreamErrorForClient(529, []byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	require.Equal(t, 529, status)
	require.Equal(t, "overloaded_error", errType)
	require.Equal(t, "Overloaded", message)

	status, _, _, message = WrapUpstreamErrorForClient(http.StatusServiceUnavailable, []byte(`{"error":{"code":503,"message":"The model is overloaded. Please try again later.","status":"UNAVAILABLE"}}`))
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "The model is overloaded. Please try again later.", message)
}
