//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 国产供应商原生 Anthropic 直通路径（api_protocol=anthropic）的 reasoning_effort 记录。
// 回归背景：该路径此前从不提取 Claude 协议的 output_config.effort，也不做
// thinking-enabled 兜底，导致 kimi/zhipu/deepseek 平台分组的 /v1/messages 请求
// usage_log.reasoning_effort 恒为 NULL。

func nativeAnthropicTestAccount() *Account {
	return &Account{
		ID:          702,
		Name:        "kimi-native",
		Platform:    PlatformKimi,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"api_protocol": APIProtocolAnthropic,
			"api_base_urls": map[string]any{
				APIProtocolAnthropic: "http://anthropic.example",
			},
		},
	}
}

func nativeAnthropicGLMTestAccount() *Account {
	account := nativeAnthropicTestAccount()
	account.Name = "zhipu-native"
	account.Platform = PlatformZhipu
	return account
}

func nativeAnthropicBufferedResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"msg_1","type":"message","role":"assistant","model":"k3",` +
				`"content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn",` +
				`"usage":{"input_tokens":93,"output_tokens":16}}`,
		)),
	}
}

func nativeAnthropicStreamResponse() *http.Response {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"k3","content":[],"stop_reason":null,"usage":{"input_tokens":93,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":16}}

event: message_stop
data: {"type":"message_stop"}

`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
}

func TestNativeAnthropicPassthroughRecordsOutputConfigEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"k3","max_tokens":32,"stream":false,` +
		`"output_config":{"effort":"low"},` +
		`"messages":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{resp: nativeAnthropicBufferedResponse()}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(),
		adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "low", *result.ReasoningEffort)
}

func TestNativeAnthropicPassthroughThinkingEnabledFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 未显式传 effort，但 thinking 已启用：k3 属于 passback-required 白名单，应兜底记为 high。
	body := []byte(`{"model":"k3","max_tokens":32,"stream":false,` +
		`"thinking":{"type":"enabled","budget_tokens":1024},` +
		`"messages":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{resp: nativeAnthropicBufferedResponse()}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(),
		adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "high", *result.ReasoningEffort)
}

func TestNativeAnthropicPassthroughStreamRecordsEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"k3","max_tokens":32,"stream":true,` +
		`"output_config":{"effort":"max"},` +
		`"messages":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{resp: nativeAnthropicStreamResponse()}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(),
		adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

func TestNativeAnthropicPassthroughNoEffortStaysNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 既无 output_config.effort 也未启用 thinking：保持 nil，不做语义注入。
	body := []byte(`{"model":"k3","max_tokens":32,"stream":false,` +
		`"messages":[{"role":"user","content":"hi"}]}`)
	upstream := &httpUpstreamRecorder{resp: nativeAnthropicBufferedResponse()}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsAnthropic(context.Background(),
		adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.ReasoningEffort)
}

func TestNativeAnthropicPassthroughNormalizesGLM53Thinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		stream     bool
		preference string
		wantEffort string
	}{
		{name: "disabled buffered", preference: `"thinking":{"type":"disabled"},`, wantEffort: "low"},
		{name: "off buffered", preference: `"thinking":{"type":"off"},`, wantEffort: "low"},
		{name: "none buffered", preference: `"thinking":{"type":"none"},`, wantEffort: "low"},
		{name: "enabled buffered", preference: `"thinking":{"type":"enabled"},`, wantEffort: "high"},
		{name: "enabled streaming", stream: true, preference: `"thinking":{"type":"enabled"},`, wantEffort: "high"},
		{name: "adaptive buffered", preference: `"thinking":{"type":"adaptive"},`, wantEffort: "high"},
		{name: "adaptive streaming", stream: true, preference: `"thinking":{"type":"adaptive"},`, wantEffort: "high"},
		{name: "minimal buffered", preference: `"output_config":{"effort":"minimal"},`, wantEffort: "low"},
		{name: "low buffered", preference: `"output_config":{"effort":"low"},`, wantEffort: "low"},
		{name: "medium streaming", stream: true, preference: `"output_config":{"effort":"medium"},`, wantEffort: "high"},
		{name: "high buffered", preference: `"output_config":{"effort":"high"},`, wantEffort: "high"},
		{name: "xhigh buffered", preference: `"output_config":{"effort":"xhigh"},`, wantEffort: "max"},
		{name: "max buffered", preference: `"output_config":{"effort":"max"},`, wantEffort: "max"},
		{name: "ultra buffered", preference: `"output_config":{"effort":"ultra"},`, wantEffort: "max"},
		{name: "output effort wins over thinking", preference: `"thinking":{"type":"adaptive"},"output_config":{"effort":"low"},`, wantEffort: "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"glm-5.3","max_tokens":32,"stream":%t,%s"messages":[{"role":"user","content":"hi"}]}`, tt.stream, tt.preference))
			response := nativeAnthropicBufferedResponse()
			if tt.stream {
				response = nativeAnthropicStreamResponse()
			}
			upstream := &httpUpstreamRecorder{resp: response}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			_, err := svc.ForwardAsAnthropic(context.Background(),
				adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicGLMTestAccount(), body, "", "")
			require.NoError(t, err)
			require.Equal(t, "enabled", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
			require.Equal(t, tt.wantEffort, gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
		})
	}
}

func TestNativeAnthropicPassthroughLeavesOtherThinkingUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
	}{
		{name: "glm 5.3 unspecified", body: `{"model":"glm-5.3","max_tokens":32,"stream":false,"messages":[]}`},
		{name: "glm 5.2 disabled", body: `{"model":"glm-5.2","max_tokens":32,"stream":false,"thinking":{"type":"disabled"},"messages":[]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.body)
			upstream := &httpUpstreamRecorder{resp: nativeAnthropicBufferedResponse()}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			_, err := svc.ForwardAsAnthropic(context.Background(),
				adaptiveProtocolTestContext("/v1/messages", body), nativeAnthropicGLMTestAccount(), body, "", "")
			require.NoError(t, err)
			require.JSONEq(t, tt.body, string(upstream.lastBody))
		})
	}
}

func TestNativeAnthropicPassthroughNormalizesNonMsgIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		upstreamID string
		wantID     string
	}{
		{"chatcmpl dash prefix", "chatcmpl-01a220e7deadbeef", "msg_01a220e7deadbeef"},
		{"chatcmpl underscore prefix", "chatcmpl_01a220e7deadbeef", "msg_01a220e7deadbeef"},
		{"opaque non-msg id", "kimi-req-99", "msg_kimi-req-99"},
		{"already msg_ prefixed", "msg_already_valid", "msg_already_valid"},
	}

	for _, tc := range cases {
		t.Run("buffered/"+tc.name, func(t *testing.T) {
			reqBody := []byte(`{"model":"kimi-k3.1-pro","max_tokens":32,"stream":false,"messages":[{"role":"user","content":"hi"}]}`)
			respJSON := fmt.Sprintf(
				`{"id":%q,"type":"message","role":"assistant","model":"kimi-k3.1-pro","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":4}}`,
				tc.upstreamID,
			)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(respJSON)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
			c.Request.Header.Set("Content-Type", "application/json")

			result, err := svc.ForwardAsAnthropic(context.Background(), c, nativeAnthropicTestAccount(), reqBody, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, tc.wantID, gjson.GetBytes(rec.Body.Bytes(), "id").String())
			require.Equal(t, tc.wantID, result.ResponseMsgID)
		})

		t.Run("streaming/"+tc.name, func(t *testing.T) {
			reqBody := []byte(`{"model":"kimi-k3.1-pro","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			sse := fmt.Sprintf(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"model\":\"kimi-k3.1-pro\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":12,\"output_tokens\":1}}}\n\n"+
					"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"pong\"}}\n\n"+
					"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
					"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\n"+
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
				tc.upstreamID,
			)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(sse)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(reqBody))
			c.Request.Header.Set("Content-Type", "application/json")

			result, err := svc.ForwardAsAnthropic(context.Background(), c, nativeAnthropicTestAccount(), reqBody, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, http.StatusOK, rec.Code)

			var gotStartID string
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if data, ok := extractAnthropicSSEDataLine(line); ok {
					root := gjson.Parse(strings.TrimSpace(data))
					if root.Get("type").String() == "message_start" {
						gotStartID = root.Get("message.id").String()
					}
				}
			}
			require.Equal(t, tc.wantID, gotStartID)
			require.Equal(t, tc.wantID, result.ResponseMsgID)
		})
	}

	t.Run("error envelope id is untouched", func(t *testing.T) {
		raw := []byte(`{"id":"err_1","type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)
		require.Equal(t, string(raw), string(normalizeNativeAnthropicBufferedMessageID(raw)))
	})
}
