package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

// Claude 违规拒答：上游 HTTP 200，content 为空、output_tokens=0，只有 stop_reason="refusal"。
// 这里只验证「识别」：只认结构化 stop_reason，上游给的 token 数照常保留。
// 「拒答不计费」见 claude_refusal_billing_test.go。

const (
	claudeRefusalStartEvent = `{"type":"message_start","message":{"model":"claude-fable-5-2","id":"msg_test1","type":"message","role":"assistant","content":[],"container":null,"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":44,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"output_tokens":0,"service_tier":"standard"}}}`
	// 注意：真实拒答的 message_delta 没有 usage 字段，也没有任何 content_block_* 事件。
	claudeRefusalDeltaEvent = `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"general_harms","explanation":"This request was blocked under the usage policy."}}}`
	claudeStopEvent         = `{"type":"message_stop"}`

	claudeRefusalStreamSSE = "event: message_start\ndata: " + claudeRefusalStartEvent + "\n\n" +
		"event: message_delta\ndata: " + claudeRefusalDeltaEvent + "\n\n" +
		"event: message_stop\ndata: " + claudeStopEvent + "\n\n"

	claudeRefusalJSONBody = `{"model":"claude-fable-5-2","id":"msg_test2","type":"message","role":"assistant","content":[],"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"general_harms","explanation":"This request was blocked under the usage policy."},"usage":{"input_tokens":44,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0,"service_tier":"standard"}}`
)

// claudeControlStreamSSE 是对照组：正常有内容的流。stopReason 是 JSON 字面量（"end_turn" / null ...）。
func claudeControlStreamSSE(stopReasonLiteral, text string, withMessageStop bool) string {
	textJSON, _ := json.Marshal(text)
	var b strings.Builder
	_, _ = b.WriteString("event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_ctl","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"stop_reason":null,"usage":{"input_tokens":44,"output_tokens":1}}}` + "\n\n")
	_, _ = b.WriteString("event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n")
	_, _ = b.WriteString("event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + string(textJSON) + `}}` + "\n\n")
	_, _ = b.WriteString("event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n")
	_, _ = b.WriteString("event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":` + stopReasonLiteral + `,"stop_sequence":null},"usage":{"output_tokens":12}}` + "\n\n")
	if withMessageStop {
		_, _ = b.WriteString("event: message_stop\ndata: " + claudeStopEvent + "\n\n")
	}
	return b.String()
}

func claudeControlJSONBody(stopReasonLiteral, text string) string {
	textJSON, _ := json.Marshal(text)
	return `{"id":"msg_ctl","type":"message","role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":` + string(textJSON) + `}],"stop_reason":` + stopReasonLiteral + `,"stop_sequence":null,"usage":{"input_tokens":44,"output_tokens":12}}`
}

// 对照组：这些都必须照常计费，Refusal 一律为 false。
var claudeNonRefusalCases = []struct {
	name       string
	stopReason string
	text       string
}{
	{"end_turn", `"end_turn"`, "hello"},
	{"tool_use", `"tool_use"`, "calling a tool"},
	{"max_tokens", `"max_tokens"`, "cut off"},
	{"null_stop_reason", `null`, "partial"},
	{"text_mentions_refusal_word", `"end_turn"`, "I will not do that, this is a refusal"},
	{"text_contains_stop_reason_json", `"end_turn"`, `the field is {"stop_reason":"refusal"} in the docs`},
}

func claudeRefusalTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c, rec
}

func claudeRefusalSSEResponse(wire string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req_refusal_test"}},
		Body:       io.NopCloser(strings.NewReader(wire)),
	}
}

func claudeRefusalJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

func claudeRefusalGatewayService() *GatewayService {
	return &GatewayService{
		cfg:              &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		rateLimitService: &RateLimitService{},
	}
}

// 流式识别路径：通用解析、透传解析逐事件喂入。
func TestClaudeRefusalSSEUsageParsers(t *testing.T) {
	parsers := map[string]func(events ...string) *ClaudeUsage{
		"general": func(events ...string) *ClaudeUsage {
			svc := claudeRefusalGatewayService()
			usage := &ClaudeUsage{}
			for _, e := range events {
				svc.parseSSEUsage(e, usage)
			}
			return usage
		},
		"passthrough": func(events ...string) *ClaudeUsage {
			usage := &ClaudeUsage{}
			for _, e := range events {
				parseSSEUsagePassthrough(e, usage)
			}
			return usage
		},
	}
	for name, parse := range parsers {
		t.Run(name+"/refusal_without_usage_keeps_start_input", func(t *testing.T) {
			usage := parse(claudeRefusalStartEvent, claudeRefusalDeltaEvent, claudeStopEvent)
			require.True(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens, "拒答事件没有 usage，不能把 message_start 的 input 覆盖丢")
			require.Zero(t, usage.OutputTokens)
		})
		t.Run(name+"/usage_delta_then_refusal_delta", func(t *testing.T) {
			usage := parse(
				claudeRefusalStartEvent,
				`{"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":7}}`,
				claudeRefusalDeltaEvent,
			)
			require.True(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens)
			require.Equal(t, 7, usage.OutputTokens, "先到的 usage 不能被后到的无 usage 拒答事件清掉")
		})
		t.Run(name+"/refusal_delta_then_usage_delta", func(t *testing.T) {
			usage := parse(
				claudeRefusalStartEvent,
				claudeRefusalDeltaEvent,
				`{"type":"message_delta","delta":{},"usage":{"input_tokens":0,"output_tokens":9}}`,
			)
			require.True(t, usage.Refusal, "后到的 usage 事件不能把拒答标志改回去")
			require.Equal(t, 44, usage.InputTokens, "占位 input_tokens=0 不覆盖 message_start 的值")
			require.Equal(t, 9, usage.OutputTokens)
		})
		t.Run(name+"/refusal_is_sticky_against_later_stop_reason", func(t *testing.T) {
			usage := parse(
				claudeRefusalStartEvent,
				claudeRefusalDeltaEvent,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`,
			)
			require.True(t, usage.Refusal)
		})
		t.Run(name+"/refusal_and_usage_in_one_delta", func(t *testing.T) {
			usage := parse(
				claudeRefusalStartEvent,
				`{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":2}}`,
			)
			require.True(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens)
			require.Equal(t, 2, usage.OutputTokens)
		})
		t.Run(name+"/stop_reason_only_counts_on_message_delta", func(t *testing.T) {
			// 同名字段出现在别的事件里不算：只认 message_delta.delta.stop_reason。
			usage := parse(
				claudeRefusalStartEvent,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","stop_reason":"refusal","text":"x"}}`,
				`{"type":"message_stop","stop_reason":"refusal"}`,
			)
			require.False(t, usage.Refusal)
		})
		for _, tc := range claudeNonRefusalCases {
			tc := tc
			t.Run(name+"/control_"+tc.name, func(t *testing.T) {
				textJSON, _ := json.Marshal(tc.text)
				usage := parse(
					`{"type":"message_start","message":{"usage":{"input_tokens":44,"output_tokens":1}}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(textJSON)+`}}`,
					`{"type":"message_delta","delta":{"stop_reason":`+tc.stopReason+`},"usage":{"output_tokens":12}}`,
					claudeStopEvent,
				)
				require.False(t, usage.Refusal)
				require.Equal(t, 44, usage.InputTokens)
				require.Equal(t, 12, usage.OutputTokens)
			})
		}
	}
}

// 非流式识别：透传解析。
func TestClaudeRefusalNonStreamBodyParser(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(claudeRefusalJSONBody))
	require.True(t, usage.Refusal)
	require.Equal(t, 44, usage.InputTokens)
	require.Zero(t, usage.OutputTokens)

	// 缺 usage 的拒答也要识别（token 数自然为 0）。
	usage = parseClaudeUsageFromResponseBody([]byte(`{"type":"message","content":[],"stop_reason":"refusal"}`))
	require.True(t, usage.Refusal)
	require.Zero(t, usage.InputTokens)

	// 上游不能通过 usage 里塞字段来伪造拒答。
	usage = parseClaudeUsageFromResponseBody([]byte(`{"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":6,"Refusal":true,"refusal":true}}`))
	require.False(t, usage.Refusal)

	for _, tc := range claudeNonRefusalCases {
		tc := tc
		t.Run("control_"+tc.name, func(t *testing.T) {
			usage := parseClaudeUsageFromResponseBody([]byte(claudeControlJSONBody(tc.stopReason, tc.text)))
			require.False(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens)
			require.Equal(t, 12, usage.OutputTokens)
		})
	}
}

// 每条会产出 ClaudeUsage 的真实处理链：拒答必须置位，且客户端看到的响应保持原样。
func TestClaudeRefusalDetectedByEveryHandler(t *testing.T) {
	type handler struct {
		name string
		run  func(t *testing.T, c *gin.Context, rec *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error)
	}
	streamHandlers := []handler{
		{"general_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleStreamingResponse(context.Background(), claudeRefusalSSEResponse(wire), c, &Account{ID: 1}, time.Now(), "claude-fable-5-2", "claude-fable-5-2", false)
			if res == nil {
				return nil, err
			}
			return res.usage, err
		}},
		{"passthrough_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), claudeRefusalSSEResponse(wire), c, &Account{ID: 1}, time.Now(), "claude-fable-5-2")
			if res == nil {
				return nil, err
			}
			return res.usage, err
		}},
		{"chat_completions_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleCCStreamingFromAnthropic(claudeRefusalSSEResponse(wire), c, "claude-fable-5-2", "claude-fable-5-2", nil, time.Now(), true, anthropicEstimateSource{})
			if res == nil {
				return nil, err
			}
			return &res.Usage, err
		}},
		{"chat_completions_buffered", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleCCBufferedFromAnthropic(claudeRefusalSSEResponse(wire), c, "claude-fable-5-2", "claude-fable-5-2", nil, time.Now(), anthropicEstimateSource{})
			if res == nil {
				return nil, err
			}
			return &res.Usage, err
		}},
		{"responses_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleResponsesStreamingResponse(claudeRefusalSSEResponse(wire), c, "claude-fable-5-2", "claude-fable-5-2", nil, time.Now(), apicompat.ResponsesClientToolMapping{}, anthropicEstimateSource{})
			if res == nil {
				return nil, err
			}
			return &res.Usage, err
		}},
		{"responses_buffered", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, wire string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleResponsesBufferedStreamingResponse(claudeRefusalSSEResponse(wire), c, "claude-fable-5-2", "claude-fable-5-2", nil, time.Now(), apicompat.ResponsesClientToolMapping{}, anthropicEstimateSource{})
			if res == nil {
				return nil, err
			}
			return &res.Usage, err
		}},
	}
	jsonHandlers := []handler{
		{"general_non_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, body string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleNonStreamingResponse(context.Background(), claudeRefusalJSONResponse(body), c, &Account{ID: 1}, "claude-fable-5-2", "claude-fable-5-2")
			if res == nil {
				return nil, err
			}
			return res.ClaudeUsage, err
		}},
		{"passthrough_non_stream", func(t *testing.T, c *gin.Context, _ *httptest.ResponseRecorder, body string) (*ClaudeUsage, error) {
			res, err := claudeRefusalGatewayService().handleNonStreamingResponseAnthropicAPIKeyPassthrough(context.Background(), claudeRefusalJSONResponse(body), c, &Account{ID: 1})
			if res == nil {
				return nil, err
			}
			return res.ClaudeUsage, err
		}},
	}

	for _, h := range streamHandlers {
		h := h
		t.Run(h.name+"/refusal", func(t *testing.T) {
			c, _ := claudeRefusalTestContext()
			usage, err := h.run(t, c, nil, claudeRefusalStreamSSE)
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.True(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens, "token 数保留用于排查")
			require.Zero(t, usage.OutputTokens)
		})
		for _, tc := range claudeNonRefusalCases {
			tc := tc
			t.Run(fmt.Sprintf("%s/control_%s", h.name, tc.name), func(t *testing.T) {
				c, _ := claudeRefusalTestContext()
				usage, err := h.run(t, c, nil, claudeControlStreamSSE(tc.stopReason, tc.text, true))
				require.NoError(t, err)
				require.NotNil(t, usage)
				require.False(t, usage.Refusal)
				require.Equal(t, 44, usage.InputTokens)
				require.Equal(t, 12, usage.OutputTokens)
			})
		}
		// 半截流：stop_reason 为 null 且没有 message_stop，照旧按已观测 usage 处理，不能被当成拒答。
		t.Run(h.name+"/control_truncated_stream", func(t *testing.T) {
			c, _ := claudeRefusalTestContext()
			usage, _ := h.run(t, c, nil, claudeControlStreamSSE(`null`, "partial", false))
			if usage != nil {
				require.False(t, usage.Refusal)
				require.Equal(t, 44, usage.InputTokens)
			}
		})
	}
	for _, h := range jsonHandlers {
		h := h
		t.Run(h.name+"/refusal", func(t *testing.T) {
			c, rec := claudeRefusalTestContext()
			usage, err := h.run(t, c, rec, claudeRefusalJSONBody)
			require.NoError(t, err)
			require.NotNil(t, usage)
			require.True(t, usage.Refusal)
			require.Equal(t, 44, usage.InputTokens)
			require.Zero(t, usage.OutputTokens)
			require.JSONEq(t, claudeRefusalJSONBody, rec.Body.String(), "客户端响应保持上游原样")
		})
		for _, tc := range claudeNonRefusalCases {
			tc := tc
			t.Run(fmt.Sprintf("%s/control_%s", h.name, tc.name), func(t *testing.T) {
				c, _ := claudeRefusalTestContext()
				usage, err := h.run(t, c, nil, claudeControlJSONBody(tc.stopReason, tc.text))
				require.NoError(t, err)
				require.NotNil(t, usage)
				require.False(t, usage.Refusal)
				require.Equal(t, 44, usage.InputTokens)
				require.Equal(t, 12, usage.OutputTokens)
			})
		}
	}
}

// 拒答流式响应原样转给客户端，不被网关改写。
func TestClaudeRefusalStreamIsForwardedUnchanged(t *testing.T) {
	c, rec := claudeRefusalTestContext()
	res, err := claudeRefusalGatewayService().handleStreamingResponse(context.Background(), claudeRefusalSSEResponse(claudeRefusalStreamSSE), c, &Account{ID: 1}, time.Now(), "claude-fable-5-2", "claude-fable-5-2", false)
	require.NoError(t, err)
	require.True(t, res.usage.Refusal)
	require.Contains(t, rec.Body.String(), `"stop_reason":"refusal"`)
	require.Contains(t, rec.Body.String(), "event: message_stop")
}
