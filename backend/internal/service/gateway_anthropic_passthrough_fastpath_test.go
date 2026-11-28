package service

import (
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

type chunkRecordingResponseWriter struct {
	gin.ResponseWriter
	chunks []string
}

func (w *chunkRecordingResponseWriter) Write(p []byte) (int, error) {
	w.chunks = append(w.chunks, string(p))
	return w.ResponseWriter.Write(p)
}

func (w *chunkRecordingResponseWriter) WriteString(s string) (int, error) {
	w.chunks = append(w.chunks, s)
	return w.ResponseWriter.WriteString(s)
}

func TestAnthropicPassthrough_StreamingWritesCompleteLinesWithNewline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	recorder := &chunkRecordingResponseWriter{ResponseWriter: c.Writer}
	c.Writer = recorder

	sseBody := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":15,"output_tokens":1}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sseBody)),
	}
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}

	res, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(
		c.Request.Context(),
		resp,
		c,
		&Account{ID: 42},
		time.Now(),
		"claude-sonnet-4-5",
	)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, 15, res.usage.InputTokens)
	require.Equal(t, 8, res.usage.OutputTokens)
	require.Equal(t, sseBody, rec.Body.String())

	// Every written chunk must end with '\n' so downstream opsCaptureWriter hits
	// its #725 complete-SSE-line fast path without buffering byte-by-byte.
	require.NotEmpty(t, recorder.chunks)
	for i, chunk := range recorder.chunks {
		require.True(t, strings.HasSuffix(chunk, "\n"), "chunk %d missing trailing newline: %q", i, chunk)
		if chunk != "\n" {
			require.Greater(t, len(chunk), 1, "non-empty line should not be split from its newline")
		}
	}
}

func TestParseSSEUsagePassthrough_ContentBlockDeltaFastPathAndEdgeCases(t *testing.T) {
	t.Run("plain content_block_delta leaves usage untouched", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 10, OutputTokens: 2}
		parseSSEUsagePassthrough(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello world"}}`, usage)
		require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, *usage)
	})

	t.Run("content_block_delta mentioning usage in text leaves usage untouched", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 10, OutputTokens: 2}
		parseSSEUsagePassthrough(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"check \"usage\" and \"message\" fields"}}`, usage)
		require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, *usage)
	})

	t.Run("content_block_delta with unicode escape leaves usage untouched", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 10, OutputTokens: 2}
		parseSSEUsagePassthrough(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a \u003c b"}}`, usage)
		require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, *usage)
	})

	t.Run("content_block_delta carrying actual usage node still parses", func(t *testing.T) {
		usage := &ClaudeUsage{}
		parseSSEUsagePassthrough(`{"type":"content_block_delta","usage":{"cached_tokens":7,"server_tool_use":{"web_search_requests":2}}}`, usage)
		require.Equal(t, 7, usage.CacheReadInputTokens)
		require.Equal(t, 2, usage.WebSearchRequests)
	})

	t.Run("message_delta with stop_reason only records refusal", func(t *testing.T) {
		usage := &ClaudeUsage{InputTokens: 10}
		parseSSEUsagePassthrough(`{"type":"message_delta","delta":{"stop_reason":"refusal"}}`, usage)
		require.True(t, usage.Refusal)
		require.Equal(t, 10, usage.InputTokens)
	})

	t.Run("unicode-escaped usage key still parses", func(t *testing.T) {
		usage := &ClaudeUsage{}
		parseSSEUsagePassthrough(`{"type":"message_delta","\u0075sage":{"output_tokens":19}}`, usage)
		require.Equal(t, 19, usage.OutputTokens)
	})
}

func BenchmarkParseSSEUsagePassthrough_ContentBlockDelta(b *testing.B) {
	data := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The quick brown fox jumps over the lazy dog."}}`
	usage := &ClaudeUsage{InputTokens: 100}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseSSEUsagePassthrough(data, usage)
	}
}
