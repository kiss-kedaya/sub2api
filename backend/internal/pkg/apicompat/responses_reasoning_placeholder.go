package apicompat

import "strings"

// InjectResponsesReasoningPlaceholders makes a replayed tool conversation
// acceptable to DeepSeek's thinking-mode validator.
//
// The contract: every assistant turn that carries a tool call must also carry
// the reasoning that produced it. Clients that keep tool history but drop
// reasoning get the whole request rejected with
//
//	The `reasoning_text` in the thinking mode must be passed back to the API.
//
// An empty reasoning item satisfies the validator (verified against the
// upstream), which is strictly better than failing the turn: the model loses
// nothing it was not already missing, and the conversation continues.
//
// The rewrite only applies when the input carries tool calls and no reasoning
// at all, so a caller that already replays reasoning keeps its own items
// untouched.
func InjectResponsesReasoningPlaceholders(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return input, false
	}

	hasCall := false
	hasReasoning := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "function_call", "custom_tool_call", "local_shell_call":
			hasCall = true
		case "reasoning":
			hasReasoning = true
		}
	}
	if !hasCall || hasReasoning {
		return input, false
	}

	out := make([]any, 0, len(items)+4)
	seq := 0
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		switch item["type"] {
		case "function_call", "custom_tool_call", "local_shell_call":
			seq++
			out = append(out, map[string]any{
				"type":    "reasoning",
				"id":      "rs_sub2api_placeholder_" + itoa(seq),
				"summary": []any{},
				"content": []any{},
			})
		}
		out = append(out, item)
	}
	return out, true
}

// itoa keeps the placeholder ids stable without pulling strconv into the hot
// path signature; the values only need to be unique inside one request.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// NormalizeResponsesReasoningForDeepSeek makes reasoning input items acceptable
// to DeepSeek's strict validator.
//
// Observed on a live relay: clients replay reasoning items that carry
// content[].reasoning_text and OpenAI's encrypted_content, but no id. DeepSeek
// then rejects the whole turn with
//
//	The `reasoning_text` in the thinking mode must be passed back to the API.
//
// even though the text is right there. Two things fix it: give every reasoning
// item a stable id (the upstream needs one to line the item up with the turn it
// produced) and drop encrypted_content, which is an OpenAI-only field the
// upstream never issued.
func NormalizeResponsesReasoningForDeepSeek(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return input, false
	}
	changed := false
	seq := 0
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] != "reasoning" {
			continue
		}
		if _, has := item["encrypted_content"]; has {
			delete(item, "encrypted_content")
			changed = true
		}
		if id, _ := item["id"].(string); strings.TrimSpace(id) == "" {
			seq++
			item["id"] = "rs_sub2api_" + itoa(seq)
			changed = true
		}
	}
	if !changed {
		return input, false
	}
	return items, true
}
