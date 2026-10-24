package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestConvertedAnthropicCachePolicyAtUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, mode := range []string{AnthropicCacheTTLModeInherit, AnthropicCacheTTLModeOff, AnthropicCacheTTLMode5m, AnthropicCacheTTLMode1h} {
				name := protocol + "/" + mode
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set("api_key", &APIKey{AnthropicCacheTTLMode: mode})
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"cache-forward-test"}},
						Body: io.NopCloser(strings.NewReader(strings.Join([]string{
							`event: message_start`,
							`data: {"type":"message_start","message":{"id":"msg_cache","type":"message","role":"assistant","content":[],"model":"claude-opus-4-8","usage":{"input_tokens":12,"cache_read_input_tokens":9,"cache_creation_input_tokens":3}}}`,
							``,
							`event: content_block_start`,
							`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
							``,
							`event: content_block_delta`,
							`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
							``,
							`event: content_block_stop`,
							`data: {"type":"content_block_stop","index":0}`,
							``,
							`event: message_delta`,
							`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
							``,
							`event: message_stop`,
							`data: {"type":"message_stop"}`,
							``, ``,
						}, "\n"))),
					}}
					cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
					svc := &GatewayService{cfg: cfg, httpUpstream: upstream, responseHeaderFilter: compileResponseHeaderFilter(cfg)}
					account := newAnthropicAPIKeyAccountForTest()
					payload := `{"model":"claude-opus-4-8","cache_control":{"type":"ephemeral","ttl":"5m"},"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"<total_tokens>10</total_tokens>"}]},{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`
					if protocol == "responses" {
						c.Request.URL.Path = "/v1/responses"
						payload = `{"model":"claude-opus-4-8","cache_control":{"type":"ephemeral","ttl":"5m"},"tools":[{"type":"function","name":"read","parameters":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"input":[{"role":"system","content":[{"type":"input_text","text":"stable","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"input_text","text":"<total_tokens>10</total_tokens>"}]},{"role":"user","content":[{"type":"input_text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`
					}
					var wire map[string]any
					require.NoError(t, json.Unmarshal([]byte(payload), &wire))
					wire["stream"] = stream
					if protocol == "chat" {
						wire["stream_options"] = map[string]bool{"include_usage": true}
					}
					body, err := json.Marshal(wire)
					require.NoError(t, err)
					parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-4-8"}
					var result *ForwardResult
					if protocol == "chat" {
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)
					} else {
						result, err = svc.ForwardAsResponses(context.Background(), c, account, body, parsed)
					}
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), "ok")
					require.Equal(t, 9, result.Usage.CacheReadInputTokens)
					require.Equal(t, 3, result.Usage.CacheCreationInputTokens)
					require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
					ttl := "5m"
					if mode == AnthropicCacheTTLMode1h {
						ttl = "1h"
						require.Contains(t, strings.Join(anthropicBetaHeaderValues(upstream.lastReq.Header), ","), claude.BetaExtendedCacheTTL)
					}
					for _, path := range []string{"cache_control.ttl", "tools.0.cache_control.ttl", "system.0.cache_control.ttl", "messages.0.content.0.cache_control.ttl"} {
						require.Equal(t, ttl, gjson.GetBytes(upstream.lastBody, path).String(), path)
					}
					require.Equal(t, "<total_tokens>10</total_tokens>", gjson.GetBytes(upstream.lastBody, "system.1.text").String())
				})
			}
		}
	}
}

func TestConvertedAnthropicAutomaticCacheLimit(t *testing.T) {
	t.Parallel()
	body := []byte(`{"cache_control":{"type":"ephemeral"},"tools":[{"name":"read","cache_control":{"type":"ephemeral"}}],"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"one","cache_control":{"type":"ephemeral"}},{"type":"text","text":"two","cache_control":{"type":"ephemeral"}},{"type":"thinking","thinking":"hidden","cache_control":{"type":"ephemeral"}}]}]}`)
	out := enforceCacheControlLimit(body)
	count := 0
	forEachCacheControl(out, func(gjson.Result) { count++ })
	require.Equal(t, 4, count, "automatic caching must fit within the four-breakpoint limit")
	require.True(t, gjson.GetBytes(out, "cache_control").Exists())
	require.False(t, gjson.GetBytes(out, "messages.0.content.2.cache_control").Exists())
	require.Equal(t, string(out), string(enforceCacheControlLimit(out)))
}

func TestNativeAnthropicCachePolicyAtBuilder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := []byte(`{"cache_control":{"type":"ephemeral","ttl":"5m"},"tools":[{"name":"read","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	svc := &OpenAIGatewayService{}
	account := newAnthropicAPIKeyAccountForTest()
	req, out, err := svc.buildNativeAnthropicUpstreamRequest(context.Background(), c, account, body, "test-key", "https://api.anthropic.com/v1/messages")
	require.NoError(t, err)
	defer func() { require.NoError(t, req.Body.Close()) }()
	assertNoTTLOrderViolation(t, out)
	require.Equal(t, "1h", gjson.GetBytes(out, "tools.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "cache_control.ttl").String())
	require.Contains(t, strings.Join(anthropicBetaHeaderValues(req.Header), ","), claude.BetaExtendedCacheTTL)
}

func TestConvertedAnthropicAutomaticCacheTTLConflict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		ttl  string
	}{
		{"final explicit marker", `{"cache_control":{"type":"ephemeral","ttl":"5m"},"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`, "1h"},
		{"skip thinking", `{"cache_control":{"type":"ephemeral"},"messages":[{"role":"assistant","content":[{"type":"text","text":"answer","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"thinking","thinking":"internal"}]}]}`, "1h"},
		{"plain last message", `{"cache_control":{"type":"ephemeral","ttl":"5m"},"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":"hi"}]}`, "5m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := normalizeCacheControlTTLOrder([]byte(tc.body))
			require.Equal(t, tc.ttl, gjson.GetBytes(out, "cache_control.ttl").String())
			assertNoTTLOrderViolation(t, out)
			require.Equal(t, string(out), string(normalizeCacheControlTTLOrder(out)))
		})
	}
}
