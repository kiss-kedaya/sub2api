package apicompat

import (
	"encoding/json"
	"testing"
)

func TestDedupeResponsesCallIDs_ArtexShape(t *testing.T) {
	callID := "call_00_NGDdtohYPFUNxkT8wU2X4174"
	input := []any{
		map[string]any{"type": "function_call", "id": callID, "call_id": callID, "name": "shell", "arguments": "{\"cmd\":\"pwd\"}"},
		map[string]any{"type": "function_call", "id": callID, "call_id": callID, "name": "shell", "arguments": ""},
		map[string]any{"type": "function_call_output", "call_id": callID, "output": "/tmp"},
	}
	out, changed := DedupeResponsesCallIDs(input)
	if !changed {
		b, _ := json.Marshal(out)
		t.Fatalf("expected change, got none: %s", b)
	}
	items, _ := out.([]any)
	if len(items) != 2 {
		b, _ := json.Marshal(items)
		t.Fatalf("want 2 items, got %d: %s", len(items), b)
	}
	calls := 0
	for _, raw := range items {
		m := raw.(map[string]any)
		if m["type"] == "function_call" {
			calls++
			if m["arguments"] != "{\"cmd\":\"pwd\"}" {
				t.Fatalf("surviving call lost arguments: %v", m["arguments"])
			}
			if m["id"] == callID {
				t.Fatalf("id still equals call_id: %v", m["id"])
			}
		}
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 function_call, got %d", calls)
	}
}
