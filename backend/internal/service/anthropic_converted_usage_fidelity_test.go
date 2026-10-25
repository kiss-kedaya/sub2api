//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertedAnthropicUsageFidelity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, start, delta string
		want               ClaudeUsage
	}{
		{"upstream one hour cache write", `{"input_tokens":11,"output_tokens":4,"cache_creation_input_tokens":600,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":600}}`, `{"output_tokens":8}`, ClaudeUsage{InputTokens: 11, OutputTokens: 8, CacheCreationInputTokens: 600, CacheCreation1hTokens: 600}},
		{"final read clears provisional write", `{"input_tokens":11,"output_tokens":4,"cache_creation_input_tokens":600,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":600}}`, `{"input_tokens":0,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":600}`, ClaudeUsage{OutputTokens: 8, CacheReadInputTokens: 600}},
		{"mixed cache durations", `{"input_tokens":2,"cache_creation_input_tokens":600,"cache_read_input_tokens":900,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":500}}`, `{"output_tokens":8}`, ClaudeUsage{InputTokens: 2, OutputTokens: 8, CacheCreationInputTokens: 600, CacheReadInputTokens: 900, CacheCreation5mTokens: 100, CacheCreation1hTokens: 500}},
		{"partial cache duration update", `{"input_tokens":2,"cache_creation_input_tokens":600,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":500}}`, `{"output_tokens":8,"cache_creation_input_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":0}}`, ClaudeUsage{InputTokens: 2, OutputTokens: 8, CacheCreationInputTokens: 500, CacheCreation1hTokens: 500}},
	} {
		for _, protocol := range []string{"chat", "responses"} {
			for _, stream := range []bool{false, true} {
				name := tc.name + "/" + protocol
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					start := `{"type":"message_start","message":{"id":"msg_cache","type":"message","role":"assistant","content":[],"model":"claude-test","usage":` + tc.start + `}}`
					delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + tc.delta + `}`
					sse := "event: message_start\ndata: " + start + "\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"OK\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: " + delta + "\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					var nativeUsage ClaudeUsage
					parseSSEUsagePassthrough(start, &nativeUsage)
					parseSSEUsagePassthrough(delta, &nativeUsage)
					require.Equal(t, tc.want, nativeUsage, "native stream billing is the comparison baseline")
					var generalUsage ClaudeUsage
					newMinimalGatewayService().parseSSEUsage(start, &generalUsage)
					newMinimalGatewayService().parseSSEUsage(delta, &generalUsage)
					require.Equal(t, tc.want, generalUsage, "the general Messages path must use the same final usage")
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse))}}
					cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
					svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg)}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					path := "/v1/chat/completions"
					payload := map[string]any{"model": "claude-test", "messages": []map[string]string{{"role": "user", "content": "hello"}}, "stream": stream, "stream_options": map[string]bool{"include_usage": true}}
					if protocol == "responses" {
						path = "/v1/responses"
						payload = map[string]any{"model": "claude-test", "input": "hello", "stream": stream}
					}
					c.Request = httptest.NewRequest(http.MethodPost, path, nil)
					c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
					body, err := json.Marshal(payload)
					require.NoError(t, err)
					parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-test"}
					var result *ForwardResult
					if protocol == "chat" {
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, parsed)
					} else {
						result, err = svc.ForwardAsResponses(context.Background(), c, newAnthropicAPIKeyAccountForTest(), body, parsed)
					}
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
					require.Equal(t, tc.want, result.Usage)
					wireUsage := convertedAnthropicResponseUsage(rec.Body.String(), stream, protocol)
					require.True(t, wireUsage.Exists(), "converted response must contain final usage")
					creationPath, readPath := "cache_creation_input_tokens", "input_tokens_details.cached_tokens"
					if protocol == "chat" {
						creationPath, readPath = "prompt_tokens_details.cache_creation_tokens", "prompt_tokens_details.cached_tokens"
					}
					require.Equal(t, tc.want.CacheCreationInputTokens, int(wireUsage.Get(creationPath).Int()))
					require.Equal(t, tc.want.CacheReadInputTokens, int(wireUsage.Get(readPath).Int()))
					require.Equal(t, tc.want.CacheCreation1hTokens, int(wireUsage.Get("cache_creation.ephemeral_1h_input_tokens").Int()))
				})
			}
		}
	}
}

func convertedAnthropicResponseUsage(body string, stream bool, protocol string) gjson.Result {
	if !stream {
		return gjson.Get(body, "usage")
	}
	var usage gjson.Result
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		event := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		path := "usage"
		if protocol == "responses" {
			path = "response.usage"
		}
		if current := event.Get(path); current.Exists() {
			usage = current
		}
	}
	return usage
}

func TestConvertedAnthropicUsageAfterDisconnect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
			c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			svc := newMinimalGatewayService()
			sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_disconnect\",\"model\":\"claude-test\",\"usage\":{\"input_tokens\":2,\"output_tokens\":8,\"cache_creation_input_tokens\":600}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":0,\"output_tokens\":19,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":600}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(sse))}
			var result *ForwardResult
			var err error
			if protocol == "chat" {
				result, err = svc.handleCCStreamingFromAnthropic(response, c, "claude-test", "claude-test", nil, time.Now(), true)
			} else {
				result, err = svc.handleResponsesStreamingResponse(response, c, "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			}
			require.NoError(t, err)
			require.True(t, result.ClientDisconnect)
			require.Equal(t, ClaudeUsage{OutputTokens: 19, CacheReadInputTokens: 600}, result.Usage, "client write failure must not settle provisional message_start usage")
		})
	}
}

func TestConvertedAnthropicUsageDrainHasIdleLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = writer.Close() })
			writeDone := make(chan struct{})
			go func() {
				defer close(writeDone)
				_, _ = io.WriteString(writer, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_idle\",\"usage\":{\"input_tokens\":2,\"cache_creation_input_tokens\":600}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
			}()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
			c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			svc := newMinimalGatewayService()
			svc.cfg.Gateway.StreamDataIntervalTimeout = 1
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
			var result *ForwardResult
			var err error
			if protocol == "chat" {
				result, err = svc.handleCCStreamingFromAnthropic(response, c, "claude-test", "claude-test", nil, time.Now(), true)
			} else {
				result, err = svc.handleResponsesStreamingResponse(response, c, "claude-test", "claude-test", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			}
			require.ErrorIs(t, err, errAnthropicNativeStreamIdle)
			require.True(t, result.ClientDisconnect)
			_, err = writer.Write([]byte("late"))
			require.ErrorIs(t, err, io.ErrClosedPipe, "idle drain must release the original upstream body")
			<-writeDone
		})
	}
}
