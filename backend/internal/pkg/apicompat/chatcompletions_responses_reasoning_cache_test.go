package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Encrypted-only reasoning items (empty summary + opaque encrypted_content,
// e.g. after codex remote compaction) carry no plaintext the bridge can map to
// reasoning_content. The gateway-side cache keyed by reasoning item id restores
// it; without the restore, DeepSeek thinking mode rejects the history with 400
// "The `reasoning_content` in the thinking mode must be passed back to the API".
func TestResponsesToChat_ReasoningCacheLookup_RestoresEncryptedOnlyItem(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_enc1","summary":[],"encrypted_content":"opaque"},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequestWithOptions(req, &ResponsesToChatOptions{
		ReasoningContentByID: func(itemID string) string {
			if itemID == "item_enc1" {
				return "cached thinking"
			}
			return ""
		},
	})
	require.NoError(t, err)
	require.Len(t, out.Messages, 3)
	require.Equal(t, "assistant", out.Messages[0].Role)
	require.Equal(t, "cached thinking", out.Messages[0].ReasoningContent)
	require.Len(t, out.Messages[0].ToolCalls, 1)
	require.Equal(t, "call_1", out.Messages[0].ToolCalls[0].ID)
	require.Equal(t, "tool", out.Messages[1].Role)
	require.Equal(t, "user", out.Messages[2].Role)
}

// A cache miss keeps the original behavior: no reasoning_content, no error.
func TestResponsesToChat_ReasoningCacheLookup_MissKeepsOriginalBehavior(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_unknown","summary":[],"encrypted_content":"opaque"},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequestWithOptions(req, &ResponsesToChatOptions{
		ReasoningContentByID: func(string) string { return "" },
	})
	require.NoError(t, err)
	require.Len(t, out.Messages, 3)
	require.Empty(t, out.Messages[0].ReasoningContent)

	// Nil options (legacy path) behaves identically.
	legacy, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Equal(t, out.Messages, legacy.Messages)
}

// Plaintext summary wins and the cache lookup is not consulted.
func TestResponsesToChat_ReasoningCacheLookup_PlaintextPreferred(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-reasoner",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_plain","summary":[{"type":"summary_text","text":"plain thinking"}]},
			{"type":"function_call","call_id":"call_1","name":"get_value","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"go on"}]}
		]`),
	}

	lookupCalled := false
	out, err := ResponsesToChatCompletionsRequestWithOptions(req, &ResponsesToChatOptions{
		ReasoningContentByID: func(string) string {
			lookupCalled = true
			return "cached thinking"
		},
	})
	require.NoError(t, err)
	require.Len(t, out.Messages, 3)
	require.Equal(t, "plain thinking", out.Messages[0].ReasoningContent)
	require.False(t, lookupCalled, "plaintext summary present → cache lookup must not run")
}

func TestExtractResponsesReasoningItem(t *testing.T) {
	id, text, ok := ExtractResponsesReasoningItem(json.RawMessage(
		`{"type":"reasoning","id":"item_a","summary":[{"type":"summary_text","text":"think"}]}`))
	require.True(t, ok)
	require.Equal(t, "item_a", id)
	require.Equal(t, "think", text)

	// Encrypted-only item: ok with id but empty text.
	id, text, ok = ExtractResponsesReasoningItem(json.RawMessage(
		`{"type":"reasoning","id":"item_b","summary":[],"encrypted_content":"opaque"}`))
	require.True(t, ok)
	require.Equal(t, "item_b", id)
	require.Empty(t, text)

	// Non-reasoning items are skipped.
	_, _, ok = ExtractResponsesReasoningItem(json.RawMessage(
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}`))
	require.False(t, ok)

	_, _, ok = ExtractResponsesReasoningItem(json.RawMessage(`"bare string"`))
	require.False(t, ok)
}
