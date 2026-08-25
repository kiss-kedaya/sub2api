package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func openAIResponsesInputItemIDPrefix(itemType string) (string, bool) {
	switch strings.TrimSpace(itemType) {
	case "message":
		return "msg", true
	case "reasoning":
		return "rs", true
	case "web_search_call":
		return "ws", true
	case "custom_tool_call":
		return openAIResponsesToolCallIDPrefix(itemType), true
	case "tool_search_call":
		return openAIResponsesToolCallIDPrefix(itemType), true
	case "custom_tool_call_output":
		// Although custom calls use ctc IDs, OpenAI validates replayed custom
		// call output item IDs against the generic fc namespace.
		return "fc", true
	default:
		if isCodexToolCallInputType(itemType) {
			return openAIResponsesToolCallIDPrefix(itemType), true
		}
		return "", false
	}
}

func openAIResponsesToolCallIDPrefix(itemType string) string {
	switch strings.TrimSpace(itemType) {
	case "custom_tool_call", "custom_tool_call_output":
		return "ctc"
	case "tool_search_call", "tool_search_output":
		return "tsc"
	default:
		return "fc"
	}
}

// Invalid replayed IDs are removed rather than rewritten because a fabricated
// ID may point at a different upstream object.
func shouldStripOpenAIResponsesInputItemID(itemType, id string) bool {
	prefix, constrained := openAIResponsesInputItemIDPrefix(itemType)
	if !constrained {
		return false
	}
	return id == "" || !strings.HasPrefix(id, prefix)
}

func shouldStripOpenAIResponsesNonPairCallID(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "message", "reasoning", "image_generation_call":
		return true
	default:
		return false
	}
}

func sanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	return rewriteOpenAIResponsesInput(body, func(index int, item gjson.Result) (string, bool, bool, error) {
		if !item.IsObject() {
			return item.Raw, true, false, nil
		}
		itemType := strings.TrimSpace(item.Get("type").String())
		id := item.Get("id")
		stripID := id.Type == gjson.String && shouldStripOpenAIResponsesInputItemID(itemType, id.String())
		stripCallID := item.Get("call_id").Exists() && shouldStripOpenAIResponsesNonPairCallID(itemType)
		if !stripID && !stripCallID {
			return item.Raw, true, false, nil
		}
		itemBody := item.Raw
		var err error
		if stripID {
			itemBody, err = sjson.Delete(itemBody, "id")
			if err != nil {
				return "", false, false, fmt.Errorf("delete input.%d.id: %w", index, err)
			}
		}
		if stripCallID {
			itemBody, err = sjson.Delete(itemBody, "call_id")
			if err != nil {
				return "", false, false, fmt.Errorf("delete input.%d.call_id: %w", index, err)
			}
		}
		return itemBody, true, true, nil
	})
}
