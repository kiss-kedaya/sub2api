package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestChatAnthropicCacheControlPreservesBlocksAndToolResults(t *testing.T) {
	t.Parallel()
	var req ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"claude-opus-4-8","cache_control":{"type":"ephemeral","ttl":"1h"},
		"tools":[
			{"type":"function","cache_control":{"type":"ephemeral","ttl":"5m"},"function":{"name":"read","parameters":{"type":"object","properties":{"cache_control":{"type":"string"}}}}},
			{"type":"function","function":{"name":"list","parameters":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}}
		],
		"messages":[
			{"role":"system","content":[{"type":"text","text":"  stable system\n","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"<total_tokens>10</total_tokens>"}]},
			{"role":"developer","content":" developer instruction ","cache_control":{"type":"ephemeral","ttl":"5m"}},
			{"role":"user","content":[{"type":"text","text":"inspect","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="},"cache_control":{"type":"ephemeral","ttl":"5m"}}]},
			{"role":"assistant","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"second"}],"cache_control":{"type":"ephemeral","ttl":"5m"},"tool_calls":[{"id":"call_read","type":"function","function":{"name":"read","arguments":"{}"}},{"id":"call_list","type":"function","function":{"name":"list","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_list","content":[{"type":"text","text":"result list"}],"cache_control":{"type":"ephemeral","ttl":"5m"}},
			{"role":"tool","tool_call_id":"call_read","content":[{"type":"text","text":"result read"}],"cache_control":{"type":"ephemeral","ttl":"1h"}}
		]
	}`), &req))
	out, err := ChatCompletionsToAnthropicRequest(&req)
	require.NoError(t, err)
	body, err := json.Marshal(out)
	require.NoError(t, err)

	require.Equal(t, "1h", gjson.GetBytes(body, "cache_control.ttl").String())
	require.Len(t, gjson.GetBytes(body, "system").Array(), 3)
	require.Equal(t, "  stable system\n", gjson.GetBytes(body, "system.0.text").String())
	require.Equal(t, "1h", gjson.GetBytes(body, "system.0.cache_control.ttl").String())
	require.Equal(t, "<total_tokens>10</total_tokens>", gjson.GetBytes(body, "system.1.text").String())
	require.False(t, gjson.GetBytes(body, "system.1.cache_control").Exists())
	require.Equal(t, " developer instruction ", gjson.GetBytes(body, "system.2.text").String())
	require.Equal(t, "5m", gjson.GetBytes(body, "system.2.cache_control.ttl").String())
	require.Equal(t, "read", out.Tools[0].Name)
	require.Equal(t, "list", out.Tools[1].Name)
	require.Equal(t, "5m", out.Tools[0].CacheControl.TTL)
	require.Equal(t, "1h", out.Tools[1].CacheControl.TTL)
	require.Equal(t, "string", gjson.GetBytes(body, "tools.0.input_schema.properties.cache_control.type").String())

	require.Len(t, out.Messages, 3)
	user := parseContentBlocks(out.Messages[0].Content)
	require.Len(t, user, 2)
	require.Equal(t, "image", user[1].Type)
	require.Equal(t, "5m", user[1].CacheControl.TTL)
	assistant := parseContentBlocks(out.Messages[1].Content)
	require.Len(t, assistant, 4)
	require.Equal(t, "first", assistant[0].Text)
	require.Equal(t, "1h", assistant[0].CacheControl.TTL)
	require.Equal(t, "second", assistant[1].Text)
	require.Nil(t, assistant[1].CacheControl)
	require.Equal(t, "call_read", assistant[2].ID)
	require.Nil(t, assistant[2].CacheControl)
	require.Equal(t, "call_list", assistant[3].ID)
	require.Equal(t, "5m", assistant[3].CacheControl.TTL)
	results := parseContentBlocks(out.Messages[2].Content)
	require.Len(t, results, 2)
	require.Equal(t, "call_read", results[0].ToolUseID)
	require.Equal(t, "1h", results[0].CacheControl.TTL)
	require.Equal(t, "result read", gjson.GetBytes(results[0].Content, "0.text").String())
	require.Equal(t, "call_list", results[1].ToolUseID)
	require.Equal(t, "5m", results[1].CacheControl.TTL)
}

func TestChatAnthropicCacheControlDoesNotLeakToOpenAI(t *testing.T) {
	t.Parallel()
	var req ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"gpt-5","cache_control":{"type":"ephemeral","ttl":"1h"},
		"tools":[{"type":"function","cache_control":{"type":"ephemeral"},"function":{"name":"read","cache_control":{"type":"ephemeral"},"parameters":{"type":"object","properties":{"cache_control":{"type":"string"}}}}}],
		"messages":[
			{"role":"system","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"checking","cache_control":{"type":"ephemeral"}}],"tool_calls":[{"id":"call_1","cache_control":{"type":"ephemeral"},"function":{"name":"read","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","cache_control":{"type":"ephemeral"},"content":"{\"cache_control\":\"business value\"}"}
		]
	}`), &req))
	out, err := ChatCompletionsToResponses(&req)
	require.NoError(t, err)
	body, err := json.Marshal(out)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(body, "cache_control").Exists())
	require.False(t, gjson.GetBytes(body, "tools.0.cache_control").Exists())
	require.Equal(t, "string", gjson.GetBytes(body, "tools.0.parameters.properties.cache_control.type").String())
	for _, item := range gjson.GetBytes(body, "input").Array() {
		require.False(t, item.Get("cache_control").Exists())
		for _, part := range item.Get("content").Array() {
			require.False(t, part.Get("cache_control").Exists())
		}
		if item.Get("type").String() == "function_call_output" {
			require.Equal(t, `{"cache_control":"business value"}`, item.Get("output").String())
		}
	}
}

func TestChatAnthropicCacheControlPrefixStaysStableAcrossTurns(t *testing.T) {
	t.Parallel()
	var req ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"claude-opus-4-8",
		"tools":[{"type":"function","function":{"name":"read","parameters":{"properties":{"z":{"type":"string"},"a":{"type":"integer"}},"type":"object"}},"cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"messages":[{"role":"system","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"<total_tokens>10</total_tokens>"}]},{"role":"user","content":"first"}]
	}`), &req))
	first, err := ChatCompletionsToAnthropicRequest(&req)
	require.NoError(t, err)
	req.Messages[0].Content = json.RawMessage(`[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"<total_tokens>20</total_tokens>"}]`)
	req.Messages = append(req.Messages, ChatMessage{Role: "assistant", Content: json.RawMessage(`"answer"`)}, ChatMessage{Role: "user", Content: json.RawMessage(`"next"`)})
	second, err := ChatCompletionsToAnthropicRequest(&req)
	require.NoError(t, err)
	firstTools, err := json.Marshal(first.Tools)
	require.NoError(t, err)
	secondTools, err := json.Marshal(second.Tools)
	require.NoError(t, err)
	require.Equal(t, firstTools, secondTools)
	require.Equal(t, gjson.GetBytes(first.System, "0").Raw, gjson.GetBytes(second.System, "0").Raw)
	require.Equal(t, "<total_tokens>20</total_tokens>", gjson.GetBytes(second.System, "1.text").String())
	require.Len(t, second.Messages, 3)
}

func TestResponsesAnthropicCacheControlPreservesToolOutputWire(t *testing.T) {
	t.Parallel()
	var req ResponsesRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"model":"claude-opus-4-8","cache_control":{"type":"ephemeral"},
		"tools":[{"type":"function","name":"read","parameters":{"type":"object"},"cache_control":{"type":"ephemeral","ttl":"1h"}}],
		"input":[
			{"role":"system","content":[{"type":"input_text","text":"prefix","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"input_text","text":"suffix"}]},
			{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}","cache_control":{"type":"ephemeral"}},
			{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"result"}],"cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"type":"function_call_output","call_id":"orphan","output":"ignored","cache_control":{"type":"ephemeral"}}
		]
	}`), &req))
	out, err := ResponsesToAnthropicRequest(&req)
	require.NoError(t, err)
	require.NotNil(t, out.CacheControl)
	require.Equal(t, "1h", out.Tools[0].CacheControl.TTL)
	require.Equal(t, "1h", gjson.GetBytes(out.System, "0.cache_control.ttl").String())
	require.Len(t, out.Messages, 2)
	results := parseContentBlocks(out.Messages[1].Content)
	require.Len(t, results, 1)
	require.Equal(t, "call_1", results[0].ToolUseID)
	require.Equal(t, "1h", results[0].CacheControl.TTL)
	require.Equal(t, "result", gjson.GetBytes(results[0].Content, "0.text").String())

	var item ResponsesInputItem
	require.NoError(t, json.Unmarshal([]byte(`{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"result"}]}`), &item))
	wire, err := json.Marshal(item)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(wire, "output").IsArray())
}

func TestChatAnthropicCacheControlAbsentDoesNotCreateMarkers(t *testing.T) {
	t.Parallel()
	var req ChatCompletionsRequest
	require.NoError(t, json.Unmarshal([]byte(`{"model":"claude-opus-4-8","messages":[{"role":"system","content":"system"},{"role":"user","content":"hi"}],"functions":[{"name":"read","parameters":{"type":"object"}}]}`), &req))
	out, err := ChatCompletionsToAnthropicRequest(&req)
	require.NoError(t, err)
	body, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), `"cache_control"`)
}
