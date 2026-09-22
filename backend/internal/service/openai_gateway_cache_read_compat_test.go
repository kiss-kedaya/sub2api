//go:build unit

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
	"github.com/tidwall/gjson"
)

func TestGrokCacheReadAliasesReachDownstreamAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/buffered"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				payload := `{"type":"response.completed","response":{"id":"resp_cache","model":"grok-4.6","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":200,"output_tokens":5,"cache_read_input_tokens":128}}}`
				resp := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: " + payload + "\n\ndata: [DONE]\n\n")),
				}
				defer func() { require.NoError(t, resp.Body.Close()) }()
				svc := &OpenAIGatewayService{}
				account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey}
				var result *OpenAIForwardResult
				var err error
				switch {
				case protocol == "chat" && stream:
					result, err = svc.handleChatStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now(), 0)
				case protocol == "chat":
					result, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				case stream:
					result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				default:
					result, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, 128, result.Usage.CacheReadInputTokens)
				var wireUsage gjson.Result
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if strings.HasPrefix(line, "data: ") {
							usage := gjson.Get(strings.TrimPrefix(line, "data: "), "usage")
							if usage.Exists() {
								wireUsage = usage
							}
						}
					}
				} else {
					wireUsage = gjson.Get(rec.Body.String(), "usage")
				}
				field := "prompt_tokens_details.cached_tokens"
				if protocol == "messages" {
					field = "cache_read_input_tokens"
					require.Equal(t, int64(72), wireUsage.Get("input_tokens").Int())
				}
				require.Equal(t, int64(128), wireUsage.Get(field).Int(), "actual serialized downstream cache count")
			})
		}
	}
}

func TestCacheUsageChatFallbackMatchesRawBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, fields := range []string{
		`"cache_read_input_tokens":128,"cache_write_tokens":32`,
		`"input_tokens_details":{"cached_tokens":128},"prompt_tokens_details":{"cache_write_tokens":0,"cache_creation_tokens":64}`,
	} {
		for _, protocol := range []string{"responses", "messages"} {
			for _, stream := range []bool{false, true} {
				name := protocol + "/buffered/" + fields
				if stream {
					name = protocol + "/stream/" + fields
				}
				t.Run(name, func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
					usage := `{"prompt_tokens":200,"completion_tokens":5,"total_tokens":205,` + fields + `}`
					payload := `{"id":"chatcmpl-cache","model":"grok-4.6","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + usage + `}`
					if stream {
						payload = "data: " + `{"id":"chatcmpl-cache","model":"grok-4.6","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}` + "\n\ndata: " + `{"choices":[],"usage":` + usage + `}` + "\n\ndata: [DONE]\n\n"
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
					defer func() { require.NoError(t, resp.Body.Close()) }()
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
					var result *OpenAIForwardResult
					var err error
					switch {
					case protocol == "responses" && stream:
						result, err = svc.streamChatCompletionsAsResponses(c, resp, "grok-4.6", nil, nil, false, nil, nil, "grok-4.6", "grok-4.6", nil, nil, time.Now())
					case protocol == "responses":
						result, err = svc.bufferChatCompletionsAsResponses(c, resp, "grok-4.6", nil, nil, false, nil, nil, "grok-4.6", "grok-4.6", nil, nil, time.Now())
					case stream:
						result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "grok-4.6", "grok-4.6", "grok-4.6", nil, nil, time.Now())
					default:
						result, err = svc.bufferChatCompletionsAsAnthropic(c, resp, "grok-4.6", "grok-4.6", "grok-4.6", nil, nil, time.Now())
					}
					require.NoError(t, err)
					expected, ok := openAIUsageFromGJSON(gjson.Parse(usage))
					require.True(t, ok)
					require.Equal(t, expected, result.Usage, "billing must retain raw upstream usage")
					wire := cacheReviewWireUsage(rec.Body.String(), stream)
					readPath := "input_tokens_details.cached_tokens"
					if protocol == "messages" {
						readPath = "cache_read_input_tokens"
						require.Equal(t, int64(200-expected.CacheReadInputTokens-expected.CacheCreationInputTokens), wire.Get("input_tokens").Int())
					}
					require.Equal(t, int64(expected.CacheReadInputTokens), wire.Get(readPath).Int(), "wire cache read must match billing")
					require.Equal(t, int64(expected.CacheCreationInputTokens), wire.Get("cache_creation_input_tokens").Int(), "wire cache write must match billing")
				})
			}
		}
	}
}

func TestGrokCanceledCacheUsageReachesDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "messages"} {
		for _, terminal := range []string{"response.cancelled", "response.canceled"} {
			t.Run(protocol+"/"+terminal, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				payload := "data: " + `{"type":"response.output_text.delta","delta":"ok"}` + "\n\ndata: " + `{"type":"` + terminal + `","response":{"id":"resp_cache","status":"cancelled","usage":{"input_tokens":200,"output_tokens":5,"cache_read_input_tokens":128,"cache_write_tokens":32}}}` + "\n\n"
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}
				defer func() { require.NoError(t, resp.Body.Close()) }()
				svc := &OpenAIGatewayService{}
				account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey}
				var result *OpenAIForwardResult
				var err error
				if protocol == "chat" {
					result, err = svc.handleChatStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now(), 0)
				} else {
					result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, 128, result.Usage.CacheReadInputTokens)
				wire := cacheReviewWireUsage(rec.Body.String(), true)
				field := "prompt_tokens_details.cached_tokens"
				if protocol == "messages" {
					field = "cache_read_input_tokens"
				}
				require.Equal(t, int64(128), wire.Get(field).Int(), "canceled terminal must retain downstream cache usage")
			})
		}
	}
}

func cacheReviewWireUsage(body string, stream bool) gjson.Result {
	if !stream {
		return gjson.Get(body, "usage")
	}
	var usage gjson.Result
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		for _, path := range []string{"usage", "response.usage"} {
			if candidate := gjson.Get(payload, path); candidate.Exists() {
				usage = candidate
			}
		}
	}
	return usage
}

func TestCacheLabelsSurviveGrokToolSchemaPass(t *testing.T) {
	body := []byte(`{"prompt_cache_key":"session-\u003ckey\u003e","input":[{"type":"reasoning","encrypted_content":"cipher\u002btext\/=="},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi","cache_control": { "type":"ephemeral", "ttl":"1h" }}]}],"tools":[{"type":"function","name":"tool","parameters":{"type":null,"pattern":"(?=keep)","properties":{}}}]}`)
	out, changed, err := sanitizeOpenAIResponsesToolSchemasForPlatform(body, PlatformGrok)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "object", gjson.GetBytes(out, "tools.0.parameters.type").String())
	require.Equal(t, "(?=keep)", gjson.GetBytes(out, "tools.0.parameters.pattern").String())
	for _, path := range []string{"prompt_cache_key", "input.0.encrypted_content", "input.1.content.0.cache_control"} {
		require.Equal(t, gjson.GetBytes(body, path).Raw, gjson.GetBytes(out, path).Raw, "raw cache label bytes: %s", path)
	}
}
