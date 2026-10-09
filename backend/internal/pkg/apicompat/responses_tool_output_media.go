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

// DedupeResponsesCallIDs drops a repeated call_id. The first tool call and
// its first output stay. Later copies are the same call, not a second one.
func DedupeResponsesCallIDs(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) < 2 {
		return input, false
	}
	seenCall := make(map[string]struct{}, len(items))
	seenOut := make(map[string]struct{}, len(items))
	out := make([]any, 0, len(items))
	changed := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		callID, slot, tracked := responsesCallIDSlot(item)
		if !tracked {
			out = append(out, raw)
			continue
		}
		seen := seenCall
		if slot == "output" {
			seen = seenOut
		}
		if _, dup := seen[callID]; dup {
			changed = true
			continue
		}
		seen[callID] = struct{}{}
		out = append(out, raw)
	}
	if !changed {
		return input, false
	}
	return out, true
}

// DedupeChatToolCallIDs drops a repeated chat tool id. The first assistant
// tool_calls entry and the first tool message keep it.
func DedupeChatToolCallIDs(messages any) (any, bool) {
	items, ok := messages.([]any)
	if !ok || len(items) < 2 {
		return messages, false
	}
	seenCall := make(map[string]struct{}, len(items))
	seenOut := make(map[string]struct{}, len(items))
	out := make([]any, 0, len(items))
	changed := false
	for _, raw := range items {
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if calls, ok := msg["tool_calls"].([]any); ok {
			kept := make([]any, 0, len(calls))
			for _, rawCall := range calls {
				call, ok := rawCall.(map[string]any)
				if !ok {
					kept = append(kept, rawCall)
					continue
				}
				callID := strings.TrimSpace(stringValue(call["id"]))
				if callID == "" {
					kept = append(kept, rawCall)
					continue
				}
				if _, dup := seenCall[callID]; dup {
					changed = true
					continue
				}
				seenCall[callID] = struct{}{}
				kept = append(kept, rawCall)
			}
			if len(kept) != len(calls) {
				if len(kept) == 0 {
					delete(msg, "tool_calls")
				} else {
					msg["tool_calls"] = kept
				}
			}
		}
		callID := strings.TrimSpace(stringValue(msg["tool_call_id"]))
		if callID != "" {
			if _, dup := seenOut[callID]; dup {
				changed = true
				continue
			}
			seenOut[callID] = struct{}{}
		}
		out = append(out, msg)
	}
	if !changed {
		return messages, false
	}
	return out, true
}

func responsesCallIDSlot(item map[string]any) (callID, slot string, ok bool) {
	callID = strings.TrimSpace(stringValue(item["call_id"]))
	if callID == "" {
		return "", "", false
	}
	itemType := strings.TrimSpace(stringValue(item["type"]))
	if itemType == "" || itemType == "message" {
		return "", "", false
	}
	if strings.Contains(itemType, "output") {
		return callID, "output", true
	}
	return callID, "call", true
}
