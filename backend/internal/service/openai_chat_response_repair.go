package service

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// repairChatCompletionMessageRole fills in an empty assistant role on a
// non-streaming chat.completion response.
//
// Several upstream relays (including sub2api-based ones) emit
// choices[].message.role as "". Strict OpenAI clients reject that, and the
// field carries no information beyond "assistant", so restoring it is lossless.
// Responses that already carry a role are returned untouched.
func repairChatCompletionMessageRole(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	if !bytes.Contains(body, []byte(`"chat.completion"`)) {
		return body
	}
	choices := gjson.GetBytes(body, "choices")
	if !choices.IsArray() {
		return body
	}

	out := body
	changed := false
	for i, choice := range choices.Array() {
		msg := choice.Get("message")
		if !msg.Exists() || !msg.IsObject() {
			continue
		}
		role := msg.Get("role")
		if role.Exists() && role.String() != "" {
			continue
		}
		path := "choices." + strconv.Itoa(i) + ".message.role"
		next, err := sjson.SetBytes(out, path, "assistant")
		if err != nil {
			continue
		}
		out = next
		changed = true
	}
	if !changed {
		return body
	}
	return out
}

// repairChatCompletionChunkRole is the streaming counterpart: the opening delta
// of a chat.completion.chunk carries the role, and some relays send it empty.
// Only a delta with no other payload is treated as the role announcement, so
// content and tool-call deltas are never rewritten.
func repairChatCompletionChunkRole(payload []byte) []byte {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload
	}
	if !bytes.Contains(payload, []byte(`"chat.completion.chunk"`)) {
		return payload
	}
	choices := gjson.GetBytes(payload, "choices")
	if !choices.IsArray() {
		return payload
	}

	out := payload
	changed := false
	for i, choice := range choices.Array() {
		delta := choice.Get("delta")
		if !delta.Exists() || !delta.IsObject() {
			continue
		}
		if role := delta.Get("role"); role.Exists() && role.String() != "" {
			continue
		}
		if delta.Get("content").Exists() ||
			delta.Get("tool_calls").Exists() ||
			delta.Get("refusal").Exists() {
			continue
		}
		path := "choices." + strconv.Itoa(i) + ".delta.role"
		next, err := sjson.SetBytes(out, path, "assistant")
		if err != nil {
			continue
		}
		out = next
		changed = true
	}
	if !changed {
		return payload
	}
	return out
}

// repairChatCompletionChunkSSELine applies repairChatCompletionChunkRole to a
// single SSE line, preserving the "data: " prefix and any trailing framing.
// It reports whether the line changed.
func repairChatCompletionChunkSSELine(line string) (string, bool) {
	const prefix = "data: "
	if !strings.HasPrefix(line, prefix) {
		return line, false
	}
	payload := line[len(prefix):]
	trimmed := strings.TrimSpace(payload)
	if trimmed == "[DONE]" || trimmed == "" {
		return line, false
	}
	fixed := repairChatCompletionChunkRole([]byte(payload))
	if bytes.Equal(fixed, []byte(payload)) {
		return line, false
	}
	return prefix + string(fixed), true
}
