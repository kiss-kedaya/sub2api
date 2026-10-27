package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A transmitted terminal error must remain a failed Forward result while
// retaining observed usage and the original upstream event.
func TestGatewayService_AnthropicAPIKeyPassthrough_StreamErrorEventIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	svc := &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
		},
		rateLimitService: &RateLimitService{},
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":11}}}`, "",
			`event: error`,
			`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, "",
		}, "\n"))),
	}

	result, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(
		context.Background(), resp, c, &Account{ID: 1}, time.Now(), "claude-3-7-sonnet-20250219",
	)
	var streamFailure *AnthropicPassthroughStreamError
	require.ErrorAs(t, err, &streamFailure)
	require.Equal(t, 529, streamFailure.StatusCode)
	require.True(t, streamFailure.ResponseWritten)
	require.NotNil(t, result)
	require.Equal(t, 11, result.usage.InputTokens)
	require.Contains(t, rec.Body.String(), "overloaded_error")
	require.NotContains(t, rec.Body.String(), "Upstream request failed")

	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, 529, streamErr.IntendedStatus)
	require.Equal(t, "overloaded_error", streamErr.ErrType)
	require.True(t, streamErr.CountTowardsSLA)
}

func TestAnthropicPassthroughForwardStreamErrorsPreserveFailureAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const relayPayload = `{"error":{"message":"stream relay failure: scanner_error: unexpected EOF","type":"relay_stream_error"},"type":"error"}`
	const explicitPayload = `{"type":"error","error":{"type":"relay_stream_error","message":"upstream interrupted","status_code":500}}`
	const noTypePayload = `{"error":{"type":"relay_stream_error","message":"upstream interrupted"},"status_code":500}`
	const multilinePayload = "{\n\"error\":{\"type\":\"relay_stream_error\",\"message\":\"upstream interrupted\",\"status_code\":500},\n\"type\":\"error\"}"
	const usagePrefix = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\n"

	for _, tc := range []struct {
		name       string
		payload    string
		frame      string
		status     int
		withUsage  bool
		trailing   string
		readErr    error
		failClient bool
	}{
		{name: "explicit 500", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload + "\n\n", status: 500, withUsage: true},
		{name: "relay without status remains 502", payload: relayPayload, frame: "event: error\ndata: " + relayPayload + "\n\n", status: 502, withUsage: true},
		{name: "later message stop cannot erase failure", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload + "\n\n", status: 500, withUsage: true,
			trailing: "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":999}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{name: "error followed by unexpected EOF", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload + "\n\n", status: 500, withUsage: true, readErr: io.ErrUnexpectedEOF},
		{name: "no trailing blank line", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload, status: 500, withUsage: true},
		{name: "no trailing blank line and unexpected EOF", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload, status: 500, withUsage: true, readErr: io.ErrUnexpectedEOF},
		{name: "named error without root type", payload: noTypePayload, frame: "event:error\ndata: " + noTypePayload + "\n\n", status: 500, withUsage: true},
		{name: "multiline named error", payload: multilinePayload, frame: "event: error\ndata: {\ndata: \"error\":{\"type\":\"relay_stream_error\",\"message\":\"upstream interrupted\",\"status_code\":500},\ndata: \"type\":\"error\"}\n\n", status: 500, withUsage: true},
		{name: "data only error", payload: relayPayload, frame: "data: " + relayPayload + "\n\n", status: 502, withUsage: true},
		{name: "bare JSON error", payload: relayPayload, frame: relayPayload + "\n\n", status: 502, withUsage: true},
		{name: "no usage is not fabricated", payload: relayPayload, frame: "event: error\ndata: " + relayPayload + "\n\n", status: 502},
		{name: "client write failure retains observed usage", payload: explicitPayload, frame: "event: error\ndata: " + explicitPayload + "\n\n", status: 500, withUsage: true, failClient: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			if tc.failClient {
				c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			}
			prefix := ""
			if tc.withUsage {
				prefix = usagePrefix
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"req_passthrough_error"}},
				Body:       &streamReadCloser{payload: []byte(prefix + tc.frame + tc.trailing), err: tc.readErr},
			}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			body := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), &ParsedRequest{
				Body: NewRequestBodyRef(body), Model: "claude-3-7-sonnet-20250219", Stream: true,
			})

			var streamFailure *AnthropicPassthroughStreamError
			require.ErrorAs(t, err, &streamFailure)
			require.Equal(t, tc.status, streamFailure.StatusCode)
			require.Equal(t, tc.payload, streamFailure.RawData)
			require.Equal(t, !tc.failClient, streamFailure.ResponseWritten)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "an already accepted failed stream must not replay")
			if tc.withUsage {
				require.NotNil(t, result)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 7, result.Usage.CacheReadInputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens, "usage after the first terminal error must be ignored")
				require.Equal(t, tc.failClient, result.ClientDisconnect)
			} else {
				require.Nil(t, result, "a stream with no observed usage cannot invent billable tokens")
			}
			marker, ok := GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, tc.status, marker.IntendedStatus)
			require.True(t, marker.CountTowardsSLA)
			if !tc.failClient {
				expectedFrame := strings.TrimRight(tc.frame, "\n") + "\n\n"
				require.Equal(t, prefix+expectedFrame, rec.Body.String(), "preserve the original error and finish only that event")
				require.NotContains(t, rec.Body.String(), "Upstream request failed")
				require.NotContains(t, rec.Body.String(), "message_stop")
			}
		})
	}
}

func TestAnthropicStreamErrorPayload_DetectsBareJSONAndSSE(t *testing.T) {
	payload, ok := anthropicStreamErrorPayload(`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	require.True(t, ok)
	require.Contains(t, payload, "overloaded_error")

	_, ok = anthropicStreamErrorPayload(`{"error":{"type":"overloaded_error","message":"Overloaded"},"type":"error"}`)
	require.True(t, ok)

	_, ok = anthropicStreamErrorPayload(`data: {"type":"content_block_delta","delta":{"text":"hi"}}`)
	require.False(t, ok)
	_, ok = anthropicStreamErrorPayload(`event: message_stop`)
	require.False(t, ok)
}
