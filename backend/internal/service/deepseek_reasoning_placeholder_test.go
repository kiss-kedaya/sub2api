//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEnsureDeepSeekChatReasoningPlaceholders(t *testing.T) {
	deepseek := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	other := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "http://upstream.example"}}

	body := []byte(`{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"ok"}]}`)
	out := ensureDeepSeekChatReasoningPlaceholders(deepseek, body)
	require.Equal(t, deepSeekReasoningPlaceholderText, gjson.GetBytes(out, "messages.1.reasoning_content").String())
	require.False(t, gjson.GetBytes(out, "messages.0.reasoning_content").Exists())
	require.False(t, gjson.GetBytes(out, "messages.2.reasoning_content").Exists())

	plain := []byte(`{"messages":[{"role":"assistant","reasoning_content":"real","content":"ok"}]}`)
	require.Equal(t, string(plain), string(ensureDeepSeekChatReasoningPlaceholders(deepseek, plain)),
		"real plaintext must win over the placeholder")

	require.Equal(t, string(body), string(ensureDeepSeekChatReasoningPlaceholders(other, body)),
		"a non-DeepSeek upstream must stay byte-for-byte unchanged")
}

func TestEnsureDeepSeekNativeReasoningPlaceholders(t *testing.T) {
	deepseek := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"q"}]},{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque","content":[]}]}`)
	out := ensureDeepSeekNativeReasoningPlaceholders(deepseek, body)
	reasoning := gjson.GetBytes(out, `input.#(type=="reasoning")`)
	require.Equal(t, "reasoning_text", reasoning.Get("content.0.type").String())
	require.Equal(t, deepSeekReasoningPlaceholderText, reasoning.Get("content.0.text").String())
	require.Equal(t, "opaque", reasoning.Get("encrypted_content").String())

	explicit := []byte(`{"input":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"real"}]}]}`)
	require.Equal(t, string(explicit), string(ensureDeepSeekNativeReasoningPlaceholders(deepseek, explicit)))

	other := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.Equal(t, string(body), string(ensureDeepSeekNativeReasoningPlaceholders(other, body)))
}
