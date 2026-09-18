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
	pending := make([]responsesToolOutputMedia, 0)
	changed := false

	flushMedia := func() {
		if len(pending) == 0 {
			return
		}

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

		rewritten = append(rewritten, map[string]any{
			"type":    "message",
			"role":    "user",
			"content": content,
		})
		pending = pending[:0]
	}

	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || !isResponsesToolOutputItem(item) {
			flushMedia()
			rewritten = append(rewritten, rawItem)
			continue
		}

		output, exists := item["output"]
		if !exists {
			rewritten = append(rewritten, item)
			continue
		}
		outputRaw, err := json.Marshal(output)
		if err != nil {
			rewritten = append(rewritten, item)
			continue
		}

		outputText, media, didRewrite := extractToolOutputMedia(outputRaw)
		if !didRewrite {
			rewritten = append(rewritten, item)
			continue
		}

		item["output"] = outputText
		callID := strings.TrimSpace(stringValue(item["call_id"]))
		for _, part := range media {
			if part.ImageURL == nil {
				continue
			}
			imageURL := strings.TrimSpace(part.ImageURL.URL)
			if imageURL == "" {
				continue
			}
			pending = append(pending, responsesToolOutputMedia{
				callID:   callID,
				imageURL: imageURL,
			})
		}
		changed = true
		rewritten = append(rewritten, item)
	}

	flushMedia()
	if !changed {
		return input, false
	}
	return rewritten, true
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
