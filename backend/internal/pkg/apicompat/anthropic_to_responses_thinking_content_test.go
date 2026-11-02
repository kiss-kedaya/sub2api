package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicToResponsesPreservesThinkingOnlyWithProviderOption(t *testing.T) {
	req := &AnthropicRequest{Model: "deepseek-flash", MaxTokens: 2048, Messages: []AnthropicMessage{
		{Role: "assistant", Content: json.RawMessage(`[{"type":"thinking","thinking":"  genuine provider thought  ","signature":"opaque-source"},{"type":"tool_use","id":"call_one","name":"lookup","input":{"x":7}}]`)},
	}}
	for _, preserve := range []bool{false, true} {
		out, err := AnthropicToResponsesWithOptions(req, &AnthropicToResponsesOptions{PreserveThinkingContent: preserve})
		require.NoError(t, err)
		var items []ResponsesInputItem
		require.NoError(t, json.Unmarshal(out.Input, &items))
		require.Len(t, items, 2)
		require.Equal(t, "reasoning", items[0].Type)
		if preserve {
			var parts []ResponsesContentPart
			require.NoError(t, json.Unmarshal(items[0].Content, &parts))
			require.Equal(t, "reasoning_text", parts[0].Type)
			require.Equal(t, "  genuine provider thought  ", parts[0].Text)
			require.Empty(t, items[0].EncryptedContent, "do not transplant a foreign Anthropic signature")
		} else {
			require.Empty(t, items[0].Content, "default providers retain opaque-only behavior")
			require.Equal(t, "opaque-source", items[0].EncryptedContent)
		}
		require.Equal(t, "function_call", items[1].Type)
		require.Equal(t, "call_one", items[1].CallID)
	}
}
