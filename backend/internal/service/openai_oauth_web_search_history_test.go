package service

import "testing"

func TestEnsureOpenAIOAuthWebSearchToolForHistoryDeclaresCachedTool(t *testing.T) {
	req := map[string]any{
		"input": []any{
			map[string]any{"type": "web_search_call", "id": "ws_1"},
			map[string]any{"type": "message", "role": "user", "content": "next"},
		},
	}
	if !ensureOpenAIOAuthWebSearchToolForHistory(req, false) {
		t.Fatal("expected tool declaration")
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", tools)
	}
	if req["tool_choice"] != "none" {
		t.Fatalf("tool_choice=%v", req["tool_choice"])
	}
}

func TestEnsureOpenAIOAuthWebSearchToolForHistoryLiteUsesAdditionalTools(t *testing.T) {
	req := map[string]any{
		"input": []any{
			map[string]any{"type": "web_search_call", "id": "ws_1"},
			map[string]any{"type": "compaction_trigger"},
		},
	}
	if !ensureOpenAIOAuthWebSearchToolForHistory(req, true) {
		t.Fatal("expected lite declaration")
	}
	if _, ok := req["tools"]; ok {
		t.Fatal("lite must not add top-level tools")
	}
	input := req["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input len=%d", len(input))
	}
	added := input[1].(map[string]any)
	if added["type"] != "additional_tools" {
		t.Fatalf("inserted=%v", added)
	}
	if input[2].(map[string]any)["type"] != "compaction_trigger" {
		t.Fatal("compaction trigger must stay last")
	}
}

func TestEnsureOpenAIOAuthWebSearchToolForHistorySkipsExistingDeclaration(t *testing.T) {
	req := map[string]any{
		"tools": []any{map[string]any{"type": "web_search"}},
		"input": []any{map[string]any{"type": "web_search_call"}},
	}
	if ensureOpenAIOAuthWebSearchToolForHistory(req, false) {
		t.Fatal("existing web_search must not be duplicated")
	}
}
