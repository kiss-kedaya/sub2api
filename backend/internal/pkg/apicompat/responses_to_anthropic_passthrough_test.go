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

// Responses 里 Anthropic 不认识的顶层字段必须原样收集，交给上游处理（上游忽略
// 即忽略），而不是在反序列化阶段静默丢弃；转换器已消费的字段不得重复透传。
func TestResponsesUnsupportedPassthroughFields_CollectsUnmappedTopLevelFields(t *testing.T) {
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
		"brand_new_field":{"nested":1},
		"null_field":null,
		"bad.key":"dropped"
	}`)

	fields := ResponsesUnsupportedPassthroughFields(body)
	require.NotNil(t, fields)

	// 未映射字段（含未来新增的未知字段）原样保留。
	for key, want := range map[string]string{
		"previous_response_id": `"resp_prev_1"`,
		"store":                "true",
		"include":              `["reasoning.encrypted_content"]`,
		"parallel_tool_calls":  "false",
		"metadata":             `{"trace":"abc"}`,
		"prompt_cache_key":     `"cache-key-1"`,
		"service_tier":         `"priority"`,
		"brand_new_field":      `{"nested":1}`,
	} {
		require.Contains(t, fields, key, "unmapped field %q must be passed through", key)
		require.JSONEq(t, want, string(fields[key]))
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
		"previous_response_id": mustRaw(t, `"resp_prev_1"`),
		"store":                mustRaw(t, `true`),
		"model":                mustRaw(t, `"must-not-overwrite"`),
		"messages":             mustRaw(t, `[{"role":"user","content":"raw"}]`),
	}

	merged, err := MergeResponsesPassthroughFields(converted, fields)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5-5", gjson.GetBytes(merged, "model").String(), "converted fields win")
	require.Equal(t, "hi", gjson.GetBytes(merged, "messages.0.content").String(), "raw messages must never overwrite conversion")
	require.Equal(t, "resp_prev_1", gjson.GetBytes(merged, "previous_response_id").String())
	require.True(t, gjson.GetBytes(merged, "store").Bool())
	require.Equal(t, int64(64), gjson.GetBytes(merged, "max_tokens").Int())
}

// 转换+透传端到端：映射字段走转换，未映射字段与内置/未知工具都保留。
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
		"metadata":{"user_id":"u-1"}
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
	require.NotNil(t, passthrough)
	require.JSONEq(t, `"resp_prev_2"`, string(passthrough["previous_response_id"]))
	require.JSONEq(t, `{"user_id":"u-1"}`, string(passthrough["metadata"]))
}
