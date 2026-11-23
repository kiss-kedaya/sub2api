package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func mustRaw(t *testing.T, value string) json.RawMessage {
	t.Helper()
	return json.RawMessage(value)
}

// Responses 里转换器没映射的顶层字段，只有 Anthropic Messages 自己定义的白名单键
// （top_k / stop_sequences）才原样带给上游；其余（尤其是 Codex 几乎每个请求都带的
// store / include / parallel_tool_calls / prompt_cache_key / reasoning / text …）
// 一律丢弃——Anthropic 对未知顶层键返回 400 "Extra inputs are not permitted"，且不换号不重试。
// 转换器已消费的字段同样不得重复透传。
func TestResponsesUnsupportedPassthroughFields_OnlyAnthropicAllowListedKeysPassThrough(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"input":"hi",
		"instructions":"be brief",
		"max_output_tokens":64,
		"temperature":0.2,
		"top_p":0.9,
		"stream":false,
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],
		"tool_choice":"auto",
		"reasoning":{"effort":"high"},
		"cache_control":{"type":"ephemeral"},
		"previous_response_id":"resp_prev_1",
		"store":true,
		"include":["reasoning.encrypted_content"],
		"parallel_tool_calls":false,
		"metadata":{"trace":"abc"},
		"prompt_cache_key":"cache-key-1",
		"service_tier":"priority",
		"speed":"fast",
		"text":{"verbosity":"low"},
		"truncation":"auto",
		"user":"u-1",
		"brand_new_field":{"nested":1},
		"top_k":40,
		"stop_sequences":["END"],
		"null_field":null,
		"bad.key":"dropped"
	}`)

	fields := ResponsesUnsupportedPassthroughFields(body)
	require.NotNil(t, fields)

	// 白名单里的 Anthropic 原生键原样保留。
	require.Len(t, fields, 2, "only the allow-listed Anthropic keys may be forwarded, got %v", fields)
	require.JSONEq(t, `40`, string(fields["top_k"]))
	require.JSONEq(t, `["END"]`, string(fields["stop_sequences"]))

	// OpenAI 专属键、会改变计费的键、未来新增的未知键都不能出现在发往 Anthropic 的请求体里。
	for _, key := range []string{
		"previous_response_id", "store", "include", "parallel_tool_calls", "metadata",
		"prompt_cache_key", "service_tier", "speed", "text", "truncation", "user", "brand_new_field",
	} {
		_, present := fields[key]
		require.False(t, present, "field %q is not an Anthropic Messages key and must be dropped", key)
	}

	// 转换器已消费的字段不能重复透传。
	for _, key := range []string{
		"model", "input", "instructions", "max_output_tokens", "temperature",
		"top_p", "stream", "tools", "tool_choice", "reasoning", "cache_control",
	} {
		_, present := fields[key]
		require.False(t, present, "converted field %q must not be passed through", key)
	}

	// 空值与无法安全表达为 JSON 顶层键的异常键名降级丢弃，但不能让请求失败。
	_, hasNull := fields["null_field"]
	require.False(t, hasNull)
	_, hasBadKey := fields["bad.key"]
	require.False(t, hasBadKey, "keys with dots would be addressed as nested paths")
}

func TestResponsesUnsupportedPassthroughFields_MalformedBodyIsNotFatal(t *testing.T) {
	require.Nil(t, ResponsesUnsupportedPassthroughFields(nil))
	require.Nil(t, ResponsesUnsupportedPassthroughFields([]byte("not-json")))
	require.Nil(t, ResponsesUnsupportedPassthroughFields([]byte(`["array"]`)))
	require.Nil(t, ResponsesUnsupportedPassthroughFields([]byte(`{}`)))
}

func TestMergeResponsesPassthroughFields_ConversionWins(t *testing.T) {
	converted := []byte(`{"model":"claude-opus-5-5","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":true}`)
	fields := map[string]json.RawMessage{
		"top_k":          mustRaw(t, `40`),
		"stop_sequences": mustRaw(t, `["END"]`),
		"model":          mustRaw(t, `"must-not-overwrite"`),
		"messages":       mustRaw(t, `[{"role":"user","content":"raw"}]`),
	}

	merged, err := MergeResponsesPassthroughFields(converted, fields)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5-5", gjson.GetBytes(merged, "model").String(), "converted fields win")
	require.Equal(t, "hi", gjson.GetBytes(merged, "messages.0.content").String(), "raw messages must never overwrite conversion")
	require.Equal(t, int64(40), gjson.GetBytes(merged, "top_k").Int())
	require.Equal(t, "END", gjson.GetBytes(merged, "stop_sequences.0").String())
	require.Equal(t, int64(64), gjson.GetBytes(merged, "max_tokens").Int())
}

// 转换+透传端到端：映射字段走转换，内置/未知工具都保留；只有 Anthropic 白名单键透传。
func TestResponsesToAnthropicRequestWithPassthrough_MapsKnownKeepsUnknown(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"instructions":"system prompt",
		"max_output_tokens":128,
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[
			{"type":"function","name":"read","parameters":{"type":"object"}},
			{"type":"file_search","name":"search_files","parameters":{"type":"object"}}
		],
		"tool_choice":{"type":"function","name":"read"},
		"stream":true,
		"previous_response_id":"resp_prev_2",
		"metadata":{"user_id":"u-1"},
		"top_k":5
	}`)
	var req ResponsesRequest
	require.NoError(t, json.Unmarshal(body, &req))

	converted, passthrough, err := ResponsesToAnthropicRequestWithPassthrough(&req, body, nil)
	require.NoError(t, err)
	require.Equal(t, 128, converted.MaxTokens)
	require.True(t, converted.Stream)
	require.Len(t, converted.Messages, 1)
	require.Contains(t, string(converted.System), "system prompt")
	require.Len(t, converted.Tools, 2)
	require.Equal(t, "read", converted.Tools[0].Name)
	require.Equal(t, "file_search", converted.Tools[1].Type, "unknown/builtin tool types are preserved verbatim")
	require.Len(t, passthrough, 1)
	require.JSONEq(t, `5`, string(passthrough["top_k"]))
}

// Codex 形态的请求体（每个请求都带 store/include/parallel_tool_calls/prompt_cache_key/
// reasoning/text）经 anthropic 桥转换并合并透传字段后，发往 Anthropic 的最终请求体里
// 不能出现任何 Anthropic Messages 不认识的顶层键——否则严格上游整体 400，且不换号不重试。
func TestResponsesToAnthropicBridge_CodexShapedBodyHasOnlyAnthropicTopLevelKeys(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5-5",
		"instructions":"be brief",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],
		"tool_choice":"auto",
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"store":false,
		"stream":true,
		"include":["reasoning.encrypted_content"],
		"prompt_cache_key":"019a-cache-key",
		"text":{"verbosity":"medium"},
		"metadata":{"trace":"abc"},
		"service_tier":"priority",
		"previous_response_id":"resp_prev_9",
		"user":"codex-user"
	}`)
	var req ResponsesRequest
	require.NoError(t, json.Unmarshal(body, &req))

	for _, passthroughReasoning := range []bool{true, false} {
		converted, passthrough, err := ResponsesToAnthropicRequestWithPassthrough(&req, body,
			&ResponsesToAnthropicOptions{PassthroughReasoning: passthroughReasoning})
		require.NoError(t, err)
		raw, err := json.Marshal(converted)
		require.NoError(t, err)
		merged, err := MergeResponsesPassthroughFields(raw, passthrough)
		require.NoError(t, err)

		// Anthropic Messages 顶层键（含转换器自己合成的 thinking / output_config）。
		known := map[string]struct{}{
			"model": {}, "messages": {}, "system": {}, "max_tokens": {}, "stream": {},
			"temperature": {}, "top_p": {}, "tools": {}, "tool_choice": {},
			"thinking": {}, "output_config": {}, "top_k": {}, "stop_sequences": {},
		}
		gjson.ParseBytes(merged).ForEach(func(key, _ gjson.Result) bool {
			_, ok := known[key.String()]
			require.True(t, ok, "passthroughReasoning=%v: %q is not an Anthropic Messages key; strict upstreams answer 400 and the gateway does not retry", passthroughReasoning, key.String())
			return true
		})
		require.False(t, gjson.GetBytes(merged, "reasoning").Exists(), "raw reasoning object must never be forwarded to Anthropic")
	}
}
