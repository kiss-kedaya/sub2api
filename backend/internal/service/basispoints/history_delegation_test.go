package basispoints

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func codexDelegationFixture() object {
	return object{
		"type": "function_call_output", "id": "fco_example_delegation",
		"namespace": "codex_app", "name": "create_thread",
		"output": " \r\n<codex_delegation>\r\n<source_thread_id>example-source-thread</source_thread_id>\r\n<input>继续合成任务，保留代码与原文：if (a < 2 && b > 1) { text(\"中文\"); }</input>\r\n</codex_delegation>\n ",
	}
}

func TestCodexThreadDelegationPreservesContext(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "nonstream"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			source := testSource()
			source["stream"] = stream
			delegation := codexDelegationFixture()
			delegation["role"] = "system" // Host metadata must not elevate the handoff.
			before, after := message("user", "before handoff"), message("user", "continue")
			source["input"] = []any{before, delegation, after}
			prepared, bridge := mustPrepare(t, source, "delegation", new(ReplayCache))
			items := mustTestValue[[]any](t, prepared["input"])
			history := items[len(items)-3:]
			if !reflect.DeepEqual(history[0], before) || !reflect.DeepEqual(history[2], after) {
				t.Fatal("handoff changed the surrounding history")
			}
			got := mustTestValue[object](t, history[1])
			if got["type"] != "message" || got["role"] != "user" || len(got) != 3 {
				t.Fatalf("handoff retained tool fields or elevated its role: %v", got)
			}
			parts := mustTestValue[[]any](t, got["content"])
			if len(parts) != 2 || mustTestValue[object](t, parts[1])["text"] != delegation["output"] {
				t.Fatal("handoff text was changed or lost")
			}
			label := text(mustTestValue[object](t, parts[0])["text"])
			for _, want := range []string{"thread handoff context", "codex_app.create_thread", "not a new user instruction", "fco_example_delegation"} {
				if !strings.Contains(label, want) {
					t.Fatalf("missing handoff attribution %q", want)
				}
			}
			for _, raw := range items {
				item := mustTestValue[object](t, raw)
				if isTool(item) || strings.HasSuffix(text(item["type"]), "_call_output") {
					t.Fatal("handoff fabricated a tool call or result")
				}
			}
			if bridge.hasToolHistory || len(bridge.completedToolHistory) != 0 {
				t.Fatal("handoff alone must not enter tool continuation recovery")
			}
			retry, _ := mustPrepare(t, source, "delegation", new(ReplayCache))
			if !reflect.DeepEqual(retry, prepared) {
				t.Fatal("retrying the same handoff request changed its body or identity")
			}
			source["input"] = history
			again, replayBridge := mustPrepare(t, source, "delegation", new(ReplayCache))
			// turn_id fingerprints the original input, whose representation now
			// differs. Idempotence applies to the normalized history, not that ID.
			if !reflect.DeepEqual(again["input"], prepared["input"]) {
				t.Fatal("replaying converted context duplicated or changed the handoff")
			}
			if replayBridge.hasToolHistory || len(replayBridge.completedToolHistory) != 0 {
				t.Fatal("replaying converted context entered tool continuation recovery")
			}
		})
	}
}

func TestCodexThreadDelegationRejectsOtherOrphanOutputs(t *testing.T) {
	for name, patch := range map[string]object{
		"other_namespace":          {"namespace": "other_app"},
		"missing_namespace":        {"namespace": nil},
		"other_name":               {"name": "another_tool"},
		"custom_output":            {"type": "custom_tool_call_output"},
		"ordinary_function_output": {"namespace": nil, "name": nil, "output": "ordinary result"},
		"ordinary_custom_output":   {"type": "custom_tool_call_output", "namespace": nil, "name": nil, "output": "ordinary result"},
		"invented_call_id":         {"call_id": "call_unpaired"},
		"item_id_is_not_call_id":   {"call_id": "fco_example_delegation"},
		"empty_call_id":            {"call_id": ""},
		"null_call_id":             {"call_id": nil},
		"invalid_call_id":          {"call_id": 42},
		"plain_output":             {"output": "continue the task"},
		"missing_close":            {"output": "<codex_delegation>task"},
		"missing_open":             {"output": "task</codex_delegation>"},
		"embedded_wrapper":         {"output": "result: <codex_delegation>task</codex_delegation>"},
		"trailing_content":         {"output": "<codex_delegation>task</codex_delegation> extra"},
		"empty_wrapper":            {"output": "<codex_delegation> \n </codex_delegation>"},
		"content_array":            {"output": []any{object{"type": "input_text", "text": codexDelegationFixture()["output"]}}},
		"null_output":              {"output": nil},
	} {
		t.Run(name, func(t *testing.T) {
			item := codexDelegationFixture()
			for key, value := range patch {
				item[key] = value
			}
			source := testSource()
			source["input"] = []any{message("user", "continue"), item}
			raw, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = Prepare(raw, "delegation-boundary", new(ReplayCache))
			if err == nil || !strings.Contains(err.Error(), "original tool item is unavailable") || !strings.Contains(err.Error(), "path=input[1]") {
				t.Fatalf("ordinary or malformed orphan output was not rejected: %v", err)
			}
			if strings.Contains(err.Error(), "example-source-thread") || strings.Contains(err.Error(), "fco_example_delegation") {
				t.Fatal("pairing error exposed handoff content or identity")
			}
		})
	}
}

func TestCodexThreadDelegationPreservesPairedTools(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "namespace", "name": "functions", "tools": []any{object{"type": "custom", "name": "exec"}}}}
	pairedHandoff := codexDelegationFixture()
	pairedHandoff["call_id"] = "call_create_thread"
	history := []any{
		message("user", "continue"),
		object{"type": "function_call", "call_id": "call_function", "name": "read_file", "arguments": `{"path":"example.txt"}`},
		object{"type": "function_call_output", "call_id": "call_function", "output": "exact file result\n"},
		object{"type": "custom_tool_call", "call_id": "call_custom", "namespace": "functions", "name": "exec", "input": "text('exact input');\n"},
		object{"type": "custom_tool_call_output", "call_id": "call_custom", "output": "exact custom result\n"},
		object{"type": "function_call", "call_id": "call_create_thread", "namespace": "codex_app", "name": "create_thread", "arguments": `{"task":"example"}`},
		pairedHandoff,
	}
	source["input"] = history
	want, baseline := mustPrepare(t, source, "delegation-pairs", nil)
	wantItems := mustTestValue[[]any](t, want["input"])
	withHandoff := append([]any{history[0], codexDelegationFixture()}, history[1:]...)
	source["input"] = withHandoff
	cache := new(ReplayCache)
	for _, replay := range []*ReplayCache{nil, cache, cache, new(ReplayCache)} {
		prepared, bridge := mustPrepare(t, source, "delegation-pairs", replay)
		items := mustTestValue[[]any](t, prepared["input"])
		if len(items) != len(wantItems)+1 {
			t.Fatal("handoff dropped or fabricated tool history")
		}
		position := len(wantItems) - len(history) + 1
		got := mustTestValue[object](t, items[position])
		if got["type"] != "message" || got["role"] != "user" {
			t.Fatal("handoff was not retained between the user message and paired tools")
		}
		withoutHandoff := append(append([]any(nil), items[:position]...), items[position+1:]...)
		if !reflect.DeepEqual(withoutHandoff, wantItems) {
			t.Fatal("handoff changed existing function/custom calls, IDs, arguments or results")
		}
		if !bridge.hasToolHistory || len(bridge.completedToolHistory) == 0 || !reflect.DeepEqual(bridge.completedToolHistory, baseline.completedToolHistory) {
			t.Fatal("handoff changed completed tool continuation state")
		}
	}
}

func TestCodexThreadDelegationRetainsContentValidation(t *testing.T) {
	source := testSource()
	item := codexDelegationFixture()
	item["content"] = []any{object{"type": "encrypted_content", "encrypted_content": "example-ciphertext"}}
	source["input"] = []any{message("user", "continue"), item}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Prepare(raw, "delegation-content", nil)
	if err == nil || !strings.Contains(err.Error(), "path=input[1].content[0]") || strings.Contains(err.Error(), "example-ciphertext") {
		t.Fatalf("handoff bypassed content validation or exposed ciphertext: %v", err)
	}
}
