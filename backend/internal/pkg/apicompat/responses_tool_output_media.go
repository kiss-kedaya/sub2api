package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
)

type responsesToolOutputMedia struct {
	callID   string
	imageURL string
}

// LiftResponsesToolOutputMedia moves image parts out of Responses tool outputs
// and into a following user message. Native Responses endpoints such as
// DeepSeek accept function_call_output.output as a string, but Codex view_image
// returns an array containing input_image. Keeping the image inside the tool
// output makes the upstream report "No tool output found for tool call ...".
func LiftResponsesToolOutputMedia(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok {
		return input, false
	}

	rewritten := make([]any, 0, len(items)+1)
	changed := false

	// DeepSeek validates parallel tool outputs as one contiguous run. Codex can
	// inject developer notices between image outputs, so keep those notices
	// pending until the full batch and its lifted media have been emitted.
	for index := 0; index < len(items); {
		item, ok := items[index].(map[string]any)
		if !ok || !isResponsesToolOutputItem(item) {
			rewritten = append(rewritten, items[index])
			index++
			continue
		}

		batchStart := index
		outputs := make([]any, 0)
		trailing := make([]any, 0)
		pending := make([]responsesToolOutputMedia, 0)
		batchChanged := false

		for index < len(items) {
			rawItem := items[index]
			item, ok := rawItem.(map[string]any)
			if !ok {
				break
			}
			if isResponsesToolOutputItem(item) {
				rewrittenItem, media, didRewrite := liftResponsesToolOutputMediaItem(item)
				if didRewrite {
					batchChanged = true
					pending = append(pending, media...)
				}
				outputs = append(outputs, rewrittenItem)
				index++
				continue
			}
			if isResponsesToolBatchInstruction(item) {
				trailing = append(trailing, rawItem)
				index++
				continue
			}
			break
		}

		if !batchChanged {
			rewritten = append(rewritten, items[batchStart:index]...)
			continue
		}

		rewritten = append(rewritten, outputs...)
		if len(pending) > 0 {
			rewritten = append(rewritten, buildResponsesToolOutputMediaMessage(pending))
		}
		rewritten = append(rewritten, trailing...)
		changed = true
	}

	if !changed {
		return input, false
	}
	return rewritten, true
}

func liftResponsesToolOutputMediaItem(item map[string]any) (any, []responsesToolOutputMedia, bool) {
	output, exists := item["output"]
	if !exists {
		return item, nil, false
	}
	outputRaw, err := json.Marshal(output)
	if err != nil {
		return item, nil, false
	}

	outputText, media, didRewrite := extractToolOutputMedia(outputRaw)
	if !didRewrite {
		return item, nil, false
	}

	item["output"] = outputText
	callID := strings.TrimSpace(stringValue(item["call_id"]))
	lifted := make([]responsesToolOutputMedia, 0, len(media))
	for _, part := range media {
		if part.ImageURL == nil {
			continue
		}
		imageURL := strings.TrimSpace(part.ImageURL.URL)
		if imageURL == "" {
			continue
		}
		lifted = append(lifted, responsesToolOutputMedia{
			callID:   callID,
			imageURL: imageURL,
		})
	}
	return item, lifted, true
}

func buildResponsesToolOutputMediaMessage(pending []responsesToolOutputMedia) map[string]any {
	content := make([]map[string]any, 0, len(pending)*2)
	lastCallID := ""
	for _, media := range pending {
		if media.callID != lastCallID {
			text := "Tool output media"
			if media.callID != "" {
				text = fmt.Sprintf(toolOutputMediaAttribution, media.callID)
			}
			content = append(content, map[string]any{
				"type": "input_text",
				"text": text,
			})
			lastCallID = media.callID
		}
		content = append(content, map[string]any{
			"type":      "input_image",
			"image_url": media.imageURL,
		})
	}

	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
}

func isResponsesToolBatchInstruction(item map[string]any) bool {
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemType != "" && itemType != "message" {
		return false
	}
	role := strings.TrimSpace(stringValue(item["role"]))
	return role == "developer" || role == "system"
}

func isResponsesToolOutputItem(item map[string]any) bool {
	switch strings.TrimSpace(stringValue(item["type"])) {
	case "function_call_output", "custom_tool_call_output",
		"tool_search_output", "tool_search_call_output", "mcp_tool_call_output":
		return true
	default:
		return false
	}
}

// DedupeResponsesCallIDs drops repeated tool-call ids as complete pairs.
//
// When one call_id shows up more than once, only its first call and first
// output are kept; every later call AND every later output of that id are
// dropped together. Pairing is the whole point: dropping the call side and the
// output side with independent counters can leave a call whose output is gone
// (or an output whose call is gone). Upstreams that reject a lone slot
// (DeepSeek) then ask the client to resend the turn, which is what made a
// single tool invocation run twice.
//
// The walk is the whole input tree, not only the top-level array. Codex stores
// a second copy inside message content, and it also sets item id equal to
// call_id. DeepSeek counts both, so a single item whose id and call_id are the
// same string is still "Duplicate call_id". Entries whose id is not duplicated
// are returned untouched, so a genuinely missing output is not rewritten here.
func DedupeResponsesCallIDs(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return input, false
	}
	return dedupeResponseItemSlice(items, &callIDSeen{
		calls:   map[string]int{},
		outputs: map[string]int{},
	})
}

type callIDSeen struct {
	calls   map[string]int
	outputs map[string]int
}

func (s *callIDSeen) keep(id, slot string) bool {
	if slot == "output" {
		s.outputs[id]++
		return s.outputs[id] == 1
	}
	s.calls[id]++
	return s.calls[id] == 1
}

func dedupeResponseItemSlice(items []any, seen *callIDSeen) ([]any, bool) {
	changed := false
	out := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			if child, ok := raw.([]any); ok {
				next, did := dedupeResponseItemSlice(child, seen)
				if did {
					changed = true
					raw = next
				}
			}
			out = append(out, raw)
			continue
		}
		callID, slot, tracked := responsesCallIDSlot(item)
		if tracked {
			if !seen.keep(callID, slot) {
				changed = true
				continue
			}
			if separateCollidingCallItemID(item) {
				changed = true
			}
		}
		if dedupeNestedCallContent(item, seen) {
			changed = true
		}
		out = append(out, item)
	}
	if !changed {
		return items, false
	}
	return out, true
}

func dedupeNestedCallContent(item map[string]any, seen *callIDSeen) bool {
	content, ok := item["content"].([]any)
	if !ok || len(content) == 0 {
		return false
	}
	next, changed := dedupeResponseItemSlice(content, seen)
	if !changed {
		return false
	}
	item["content"] = next
	return true
}

// separateCollidingCallItemID makes id and call_id different strings.
// DeepSeek rejects one item when both fields carry the same call_ id.
func separateCollidingCallItemID(item map[string]any) bool {
	callID := strings.TrimSpace(stringValue(item["call_id"]))
	itemID := strings.TrimSpace(stringValue(item["id"]))
	if callID == "" && strings.HasPrefix(itemID, "call_") {
		item["call_id"] = itemID
		item["id"] = "fc_" + strings.TrimPrefix(itemID, "call_")
		return true
	}
	if callID != "" && itemID == callID {
		item["id"] = "fc_" + strings.TrimPrefix(callID, "call_")
		return true
	}
	return false
}

// DedupeChatToolCallIDs drops repeated chat tool ids as complete pairs.
//
// The first assistant tool_calls entry of an id and the first tool message of
// that id are kept; later copies on either side are dropped together. Chat has
// the same lone-slot hazard as Responses: dropping a tool message while its
// assistant call survives (or the reverse) makes an upstream that requires a
// complete pair re-ask the client, which shows up as the tool running twice.
func DedupeChatToolCallIDs(messages any) (any, bool) {
	items, ok := messages.([]any)
	if !ok || len(items) < 2 {
		return messages, false
	}

	type callSlot struct {
		msgIndex  int
		callIndex int
	}
	calls := make(map[string][]callSlot, 4)
	outputs := make(map[string][]int, 4)
	for mi, raw := range items {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if callList, ok := msg["tool_calls"].([]any); ok {
			for ci, rawCall := range callList {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				id := strings.TrimSpace(stringValue(call["id"]))
				if id == "" {
					continue
				}
				calls[id] = append(calls[id], callSlot{msgIndex: mi, callIndex: ci})
			}
		}
		if id := strings.TrimSpace(stringValue(msg["tool_call_id"])); id != "" {
			outputs[id] = append(outputs[id], mi)
		}
	}

	dropCalls := make(map[callSlot]struct{})
	dropMsgs := make(map[int]struct{})
	for id, list := range calls {
		outList := outputs[id]
		if len(list) <= 1 && len(outList) <= 1 {
			continue
		}
		for _, slot := range list[1:] {
			dropCalls[slot] = struct{}{}
		}
		for _, mi := range outList[1:] {
			dropMsgs[mi] = struct{}{}
		}
	}
	for id, list := range outputs {
		if _, hasCall := calls[id]; hasCall {
			continue
		}
		for _, mi := range list[1:] {
			dropMsgs[mi] = struct{}{}
		}
	}
	if len(dropCalls) == 0 && len(dropMsgs) == 0 {
		return messages, false
	}

	out := make([]any, 0, len(items)-len(dropMsgs))
	for mi, raw := range items {
		if _, drop := dropMsgs[mi]; drop {
			continue
		}
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if callList, ok := msg["tool_calls"].([]any); ok {
			kept := make([]any, 0, len(callList))
			for ci, rawCall := range callList {
				if _, drop := dropCalls[callSlot{msgIndex: mi, callIndex: ci}]; drop {
					continue
				}
				kept = append(kept, rawCall)
			}
			if len(kept) != len(callList) {
				if len(kept) == 0 {
					// The whole message existed only to carry the dropped call.
					// Leaving an empty assistant shell behind is malformed, so
					// drop the message; a visible content/text field (rare for a
					// tool-call-only assistant turn) keeps it alive.
					delete(msg, "tool_calls")
					if !chatMessageHasVisibleContent(msg) {
						continue
					}
				} else {
					msg["tool_calls"] = kept
				}
			}
		}
		out = append(out, msg)
	}
	return out, true
}

// chatMessageHasVisibleContent reports whether an assistant message still
// carries something a client would render once its tool_calls are gone.
func chatMessageHasVisibleContent(msg map[string]any) bool {
	for _, key := range []string{"content", "text"} {
		if value, ok := msg[key]; ok && value != nil {
			if text, ok := value.(string); !ok || strings.TrimSpace(text) != "" {
				return true
			}
		}
	}
	return false
}

func responsesCallIDSlot(item map[string]any) (callID, slot string, ok bool) {
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemType == "" || itemType == "message" || itemType == "reasoning" {
		return "", "", false
	}
	callID = strings.TrimSpace(stringValue(item["call_id"]))
	if callID == "" {
		// Codex sometimes stores the call id in id and omits call_id.
		itemID := strings.TrimSpace(stringValue(item["id"]))
		if strings.HasPrefix(itemID, "call_") {
			callID = itemID
		}
	}
	if callID == "" {
		return "", "", false
	}
	if strings.Contains(itemType, "output") {
		return callID, "output", true
	}
	return callID, "call", true
}
