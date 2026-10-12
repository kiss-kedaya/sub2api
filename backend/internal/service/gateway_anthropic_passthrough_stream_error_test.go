package service

import (
	"context"
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

// 上游把错误作为流内事件下发时，它就是语义终态：透传后不再报
// "missing terminal event"，ops 按官方语义记录（overloaded → 529）。
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
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), "overloaded_error")
	require.NotContains(t, rec.Body.String(), "Upstream request failed")

	streamErr, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, 529, streamErr.IntendedStatus)
	require.Equal(t, "overloaded_error", streamErr.ErrType)
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
