package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestInjectResponsesReasoningPlaceholdersAddsOnePerCall(t *testing.T) {
	input := []any{
		map[string]any{"type": "message", "role": "user"},
		map[string]any{"type": "function_call", "call_id": "call_a"},
		map[string]any{"type": "function_call_output", "call_id": "call_a"},
		map[string]any{"type": "function_call", "call_id": "call_b"},
		map[string]any{"type": "function_call_output", "call_id": "call_b"},
	}
	out, changed := InjectResponsesReasoningPlaceholders(input)
	require.True(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, items, 7)

	// Each call is preceded by exactly one placeholder, right before the call.
	require.Equal(t, "reasoning", itemType(t, items[1]))
	require.Equal(t, "function_call", itemType(t, items[2]))
	require.Equal(t, "reasoning", itemType(t, items[4]))
	require.Equal(t, "function_call", itemType(t, items[5]))
}

func TestInjectResponsesReasoningPlaceholdersLeavesRealReasoningAlone(t *testing.T) {
	input := []any{
		map[string]any{"type": "reasoning", "id": "rs_real"},
		map[string]any{"type": "function_call", "call_id": "call_a"},
	}
	out, changed := InjectResponsesReasoningPlaceholders(input)
	require.False(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)
	require.Len(t, items, 2)
}

func TestInjectResponsesReasoningPlaceholdersSkipsPlainConversations(t *testing.T) {
	input := []any{map[string]any{"type": "message", "role": "user"}}
	_, changed := InjectResponsesReasoningPlaceholders(input)
	require.False(t, changed)
}

func TestInjectResponsesReasoningPlaceholdersIDsAreUnique(t *testing.T) {
	input := []any{
		map[string]any{"type": "function_call", "call_id": "a"},
		map[string]any{"type": "function_call", "call_id": "b"},
	}
	out, _ := InjectResponsesReasoningPlaceholders(input)
	raw, ok := out.([]any)
	require.True(t, ok)
	seen := map[string]bool{}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok || m["type"] != "reasoning" {
			continue
		}
		id, _ := m["id"].(string)
		require.False(t, seen[id], "placeholder ids must be unique")
		seen[id] = true
	}
	require.Len(t, seen, 2)
}

var _ = gjson.Get

func itemType(t *testing.T, raw any) string {
	t.Helper()
	m, ok := raw.(map[string]any)
	require.True(t, ok, "item must be an object")
	typ, _ := m["type"].(string)
	return typ
}

func TestNormalizeResponsesReasoningForDeepSeekFillsIDsAndDropsEncryptedContent(t *testing.T) {
	input := []any{
		map[string]any{"type": "reasoning", "summary": []any{}, "content": []any{}, "encrypted_content": "gAAAA"},
		map[string]any{"type": "reasoning", "id": "rs_real", "content": []any{}},
	}
	out, changed := NormalizeResponsesReasoningForDeepSeek(input)
	require.True(t, changed)
	items, ok := out.([]any)
	require.True(t, ok)

	first, ok := items[0].(map[string]any)
	require.True(t, ok)
	require.NotEmpty(t, first["id"], "a reasoning item without an id breaks the upstream match")
	_, kept := first["encrypted_content"]
	require.False(t, kept, "encrypted_content is an OpenAI-only field")

	second, ok := items[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "rs_real", second["id"], "an upstream-issued id must be preserved")
}
