package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRepairChatCompletionMessageRole_FillsEmptyRole(t *testing.T) {
	body := []byte(`{"id":"cmb-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"","content":"hi"},"finish_reason":"stop"}]}`)
	out := repairChatCompletionMessageRole(body)
	require.Equal(t, "assistant", gjson.GetBytes(out, "choices.0.message.role").String())
	require.Equal(t, "hi", gjson.GetBytes(out, "choices.0.message.content").String())
}

func TestRepairChatCompletionMessageRole_LeavesValidRole(t *testing.T) {
	body := []byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`)
	require.Equal(t, string(body), string(repairChatCompletionMessageRole(body)))
}

func TestRepairChatCompletionMessageRole_IgnoresOtherObjects(t *testing.T) {
	body := []byte(`{"object":"response","output":[]}`)
	require.Equal(t, string(body), string(repairChatCompletionMessageRole(body)))
}

func TestRepairChatCompletionMessageRole_MultiChoice(t *testing.T) {
	body := []byte(`{"object":"chat.completion","choices":[{"index":0,"message":{"role":""}},{"index":1,"message":{"role":"assistant"}}]}`)
	out := repairChatCompletionMessageRole(body)
	require.Equal(t, "assistant", gjson.GetBytes(out, "choices.0.message.role").String())
	require.Equal(t, "assistant", gjson.GetBytes(out, "choices.1.message.role").String())
}

func TestRepairChatCompletionChunkRole_OpeningDelta(t *testing.T) {
	line := `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":""},"finish_reason":null}]}`
	out, changed := repairChatCompletionChunkSSELine(line)
	require.True(t, changed)
	require.Contains(t, out, `"role":"assistant"`)
}

func TestRepairChatCompletionChunkRole_ContentDeltaUntouched(t *testing.T) {
	line := `data: {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}`
	out, changed := repairChatCompletionChunkSSELine(line)
	require.False(t, changed)
	require.Equal(t, line, out)
}

func TestRepairChatCompletionChunkRole_DoneUntouched(t *testing.T) {
	out, changed := repairChatCompletionChunkSSELine("data: [DONE]")
	require.False(t, changed)
	require.Equal(t, "data: [DONE]", out)
}

func TestRepairChatCompletionChunkRole_NonDataLineUntouched(t *testing.T) {
	out, changed := repairChatCompletionChunkSSELine("event: message")
	require.False(t, changed)
	require.Equal(t, "event: message", out)
}
