package apicompat

import (
	"encoding/json"
	"testing"
)

func TestThinkingBlockAlwaysEmitsSignature(t *testing.T) {
	raw, err := json.Marshal(AnthropicContentBlock{Type: "thinking", Thinking: "hmm"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["signature"]; !ok {
		t.Fatalf("missing signature: %s", raw)
	}
}

func TestLegacyFunctionCallPairsWithResult(t *testing.T) {
	items, err := convertChatMessagesToResponsesInput([]ChatMessage{
		{Role: "assistant", FunctionCall: &ChatFunctionCall{Name: "lookup", Arguments: "{}"}},
		{Role: "function", Name: "lookup", Content: json.RawMessage(`"ok"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].CallID == "" || items[0].CallID != items[1].CallID {
		t.Fatalf("call ids %q %q", items[0].CallID, items[1].CallID)
	}
}

func TestResponsesRefusalReachesChatMessage(t *testing.T) {
	out := ResponsesToChatCompletions(&ResponsesResponse{
		Output: []ResponsesOutput{{
			Type: "message",
			Content: []ResponsesContentPart{{
				Type:    "refusal",
				Refusal: "no",
			}},
		}},
	}, "m")
	if out.Choices[0].Message.Refusal != "no" {
		t.Fatalf("refusal=%q", out.Choices[0].Message.Refusal)
	}
}
