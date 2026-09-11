package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiNativeRequestToChatCompletions(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"temperature":0.2,"maxOutputTokens":16}}`)
	got, err := geminiNativeRequestToChatCompletions("gemini-2.5-flash", body, false)
	require.NoError(t, err)
	require.Equal(t, "gemini-2.5-flash", gjson.GetBytes(got, "model").String())
	require.Equal(t, "user", gjson.GetBytes(got, "messages.0.role").String())
	require.Equal(t, "hi", gjson.GetBytes(got, "messages.0.content").String())
	require.Equal(t, 0.2, gjson.GetBytes(got, "temperature").Float())
	require.Equal(t, int64(16), gjson.GetBytes(got, "max_tokens").Int())
}

func TestChatCompletionsToGeminiNative(t *testing.T) {
	raw := []byte(`{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	body, usage := chatCompletionsToGeminiNative(raw, "gemini-2.5-flash")
	require.Equal(t, "hello", gjson.GetBytes(body, "candidates.0.content.parts.0.text").String())
	require.Equal(t, "STOP", gjson.GetBytes(body, "candidates.0.finishReason").String())
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 2, usage.OutputTokens)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
}
