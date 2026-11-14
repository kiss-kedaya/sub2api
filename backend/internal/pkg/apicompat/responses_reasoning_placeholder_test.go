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
	items := out.([]any)
	require.Len(t, items, 7)

	// Each call is preceded by exactly one placeholder, right before the call.
	require.Equal(t, "reasoning", items[1].(map[string]any)["type"])
	require.Equal(t, "function_call", items[2].(map[string]any)["type"])
	require.Equal(t, "reasoning", items[4].(map[string]any)["type"])
	require.Equal(t, "function_call", items[5].(map[string]any)["type"])
}

func TestInjectResponsesReasoningPlaceholdersLeavesRealReasoningAlone(t *testing.T) {
	input := []any{
		map[string]any{"type": "reasoning", "id": "rs_real"},
		map[string]any{"type": "function_call", "call_id": "call_a"},
	}
	out, changed := InjectResponsesReasoningPlaceholders(input)
	require.False(t, changed)
	require.Len(t, out.([]any), 2)
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
	raw := out.([]any)
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
