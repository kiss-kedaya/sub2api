package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 非透明账号（没开 accounts.extra.anthropic_passthrough：OAuth / Vertex / 官方或未知上游的
// API Key）走 2.3.153 的转换口径：Codex 几乎每个请求都带的 OpenAI 专属顶层字段不能出现在
// 发往 Anthropic 的请求体里——Anthropic 对未知顶层键返回 400，且这类 400 不换号、不重试。
// reasoning.effort 照旧映射成 thinking / output_config，temperature 等已映射字段保持原值。
func TestResponsesIngressNonTransparentAccountKeepsLegacyConversion(t *testing.T) {
	account := newAnthropicAPIKeyAccountForTest()
	delete(account.Extra, "anthropic_passthrough")
	require.False(t, account.IsAnthropicAPIKeyPassthroughEnabled())

	upstream := responsesBridgeUpstreamRecorder()
	svc := newResponsesBridgeTestService(upstream)
	groupID := int64(118)
	apiKey := &APIKey{
		ID: 1, UserID: 2, GroupID: &groupID, Status: StatusActive,
		Group: &Group{ID: groupID, Name: "Claude 嘿嘿嘿", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true,
			CrossProtocolConversionEnabled: true},
	}
	c, rec := responsesBridgeTestContext(t, "/v1/responses", apiKey)

	body := []byte(`{
		"model":"claude-opus-5-5",
		"instructions":"be brief",
		"max_output_tokens":128,
		"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"stream":false,
		"previous_response_id":"resp_prev_9",
		"store":true,
		"include":["reasoning.encrypted_content"],
		"parallel_tool_calls":true,
		"prompt_cache_key":"cache-key-1",
		"text":{"verbosity":"low"},
		"metadata":{"trace":"t-1"},
		"service_tier":"priority",
		"user":"codex-user",
		"reasoning":{"effort":"high","summary":"auto"},
		"temperature":0.3,
		"top_k":5,
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]
	}`)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-opus-5-5"}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
	require.Equal(t, http.StatusOK, rec.Code)

	// 发往 Anthropic 的最终请求体里不能有任何 Anthropic Messages 不认识的顶层键。
	known := map[string]struct{}{
		"model": {}, "messages": {}, "system": {}, "max_tokens": {}, "stream": {},
		"temperature": {}, "top_p": {}, "top_k": {}, "stop_sequences": {},
		"tools": {}, "tool_choice": {}, "thinking": {}, "output_config": {}, "metadata": {},
	}
	gjson.ParseBytes(upstream.lastBody).ForEach(func(key, _ gjson.Result) bool {
		_, ok := known[key.String()]
		require.True(t, ok, "%q is not an Anthropic Messages top-level key; strict upstreams answer 400 and the gateway does not retry", key.String())
		return true
	})
	for _, key := range []string{"previous_response_id", "store", "include", "parallel_tool_calls", "prompt_cache_key", "text", "service_tier", "user", "reasoning"} {
		require.False(t, gjson.GetBytes(upstream.lastBody, key).Exists(), "%q must not reach an Anthropic upstream", key)
	}
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata.trace").Exists(), "free-form metadata keys are rejected by Anthropic")

	// 已映射字段与白名单键保持原值；reasoning.effort 仍映射成 output_config（2.3.153 口径）。
	require.InDelta(t, 0.3, gjson.GetBytes(upstream.lastBody, "temperature").Float(), 1e-9)
	require.Equal(t, int64(5), gjson.GetBytes(upstream.lastBody, "top_k").Int())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "output_config.effort").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "thinking").Exists())
}
