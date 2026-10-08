package apicompat

import (
	"encoding/json"
	"fmt"
	"strconv"
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

// DedupeResponsesCallIDs renames a repeated call_id so DeepSeek does not
// reject the request. The first tool call and its first output keep the
// original id. Later calls and outputs of that id are paired onto id~2, id~3.
func DedupeResponsesCallIDs(input any) (any, bool) {
	items, ok := input.([]any)
	if !ok || len(items) < 2 {
		return input, false
	}
	callOrd := make(map[string]int, len(items))
	outputOrd := make(map[string]int, len(items))
	assigned := make(map[string][]string, len(items))
	changed := false
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		callID, slot, tracked := responsesCallIDSlot(item)
		if !tracked {
			continue
		}
		if slot == "call" {
			n := callOrd[callID]
			callOrd[callID] = n + 1
			newID := callID
			if n > 0 {
				newID = duplicateCallID(callID, n+1)
				item["call_id"] = newID
				changed = true
			}
			assigned[callID] = append(assigned[callID], newID)
			continue
		}
		n := outputOrd[callID]
		outputOrd[callID] = n + 1
		if n == 0 {
			continue
		}
		newID := duplicateCallID(callID, n+1)
		if ids := assigned[callID]; n < len(ids) {
			newID = ids[n]
		}
		item["call_id"] = newID
		changed = true
	}
	if !changed {
		return input, false
	}
	return items, true
}

// DedupeChatToolCallIDs is the chat-completions form of DedupeResponsesCallIDs.
// Assistant tool_calls[].id and the following tool messages share one sequence.
func DedupeChatToolCallIDs(messages any) (any, bool) {
	items, ok := messages.([]any)
	if !ok || len(items) < 2 {
		return messages, false
	}
	callOrd := make(map[string]int, len(items))
	assigned := make(map[string][]string, len(items))
	changed := false
	for _, raw := range items {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		calls, ok := msg["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, rawCall := range calls {
			call, ok := rawCall.(map[string]any)
			if !ok {
				continue
			}
			callID := strings.TrimSpace(stringValue(call["id"]))
			if callID == "" {
				continue
			}
			n := callOrd[callID]
			callOrd[callID] = n + 1
			newID := callID
			if n > 0 {
				newID = duplicateCallID(callID, n+1)
				call["id"] = newID
				changed = true
			}
			assigned[callID] = append(assigned[callID], newID)
		}
	}
	outputOrd := make(map[string]int, len(items))
	for _, raw := range items {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		callID := strings.TrimSpace(stringValue(msg["tool_call_id"]))
		if callID == "" {
			continue
		}
		n := outputOrd[callID]
		outputOrd[callID] = n + 1
		if n == 0 {
			continue
		}
		newID := duplicateCallID(callID, n+1)
		if ids := assigned[callID]; n < len(ids) {
			newID = ids[n]
		}
		msg["tool_call_id"] = newID
		changed = true
	}
	if !changed {
		return messages, false
	}
	return items, true
}

func duplicateCallID(id string, n int) string {
	return id + "~" + strconv.Itoa(n)
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
