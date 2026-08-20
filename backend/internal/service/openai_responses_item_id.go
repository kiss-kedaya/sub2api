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
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	type inputItem struct {
		body        []byte
		itemType    string
		id          string
		callID      string
		stripID     bool
		stripCallID bool
		drop        bool
		isObject    bool
	}

	items := make([]inputItem, 0)
	strippedIDs := make(map[string]struct{})
	validCallIDs := make(map[string]struct{})
	input.ForEach(func(_, item gjson.Result) bool {
		parsed := inputItem{body: []byte(item.Raw), isObject: item.IsObject()}
		if item.IsObject() {
			itemType := item.Get("type")
			id := item.Get("id")
			parsed.itemType = strings.TrimSpace(itemType.String())
			parsed.callID = strings.TrimSpace(item.Get("call_id").String())
			parsed.stripCallID = item.Get("call_id").Exists() && shouldStripOpenAIResponsesNonPairCallID(parsed.itemType)
			if id.Type == gjson.String {
				parsed.id = id.String()
				parsed.stripID = shouldStripOpenAIResponsesInputItemID(parsed.itemType, parsed.id)
				if parsed.stripID && parsed.id != "" {
					strippedIDs[parsed.id] = struct{}{}
				}
			}
			if isCodexToolCallContextItemType(parsed.itemType) && parsed.callID != "" {
				validCallIDs[parsed.callID] = struct{}{}
			}
		}
		items = append(items, parsed)
		return true
	})
	hasSanitization := false
	for _, item := range items {
		if item.stripID || item.stripCallID {
			hasSanitization = true
			break
		}
	}
	if !hasSanitization {
		return body, false, nil
	}

	// First decide which outputs become dangling because their call_id points at
	// an item ID that is being removed and no call item owns that call_id. This
	// must happen before computing retained IDs: a dropped output cannot keep an
	// item_reference alive merely because it used to have the same id.
	for index := range items {
		item := &items[index]
		if !item.isObject || !isCodexToolCallOutputItemType(item.itemType) {
			continue
		}
		if _, pointsAtStrippedID := strippedIDs[item.callID]; !pointsAtStrippedID {
			continue
		}
		if _, hasMatchingCallID := validCallIDs[item.callID]; !hasMatchingCallID {
			item.drop = true
		}
	}

	removedItemIDs := make(map[string]struct{}, len(strippedIDs))
	retainedItemIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.id == "" || item.itemType == "item_reference" {
			continue
		}
		if item.stripID || item.drop {
			removedItemIDs[item.id] = struct{}{}
			continue
		}
		retainedItemIDs[item.id] = struct{}{}
	}
	for id := range retainedItemIDs {
		delete(removedItemIDs, id)
	}

	rebuiltItems := make([][]byte, 0, len(items))
	for index, item := range items {
		if item.isObject {
			if item.itemType == "item_reference" {
				if _, dangling := removedItemIDs[item.id]; dangling {
					continue
				}
			}
			if item.drop {
				continue
			}
		}
		itemBody := item.body
		if item.stripID {
			var err error
			itemBody, err = sjson.DeleteBytes(itemBody, "id")
			if err != nil {
				return nil, false, fmt.Errorf("delete input.%d.id: %w", index, err)
			}
		}
		if item.stripCallID {
			var err error
			itemBody, err = sjson.DeleteBytes(itemBody, "call_id")
			if err != nil {
				return nil, false, fmt.Errorf("delete input.%d.call_id: %w", index, err)
			}
		}
		rebuiltItems = append(rebuiltItems, itemBody)
	}

	rebuiltInput := make([]byte, 0, len(input.Raw))
	rebuiltInput = append(rebuiltInput, '[')
	for i, item := range rebuiltItems {
		if i > 0 {
			rebuiltInput = append(rebuiltInput, ',')
		}
		rebuiltInput = append(rebuiltInput, item...)
	}
	rebuiltInput = append(rebuiltInput, ']')

	sanitized, err := sjson.SetRawBytes(body, "input", rebuiltInput)
	if err != nil {
		return nil, false, fmt.Errorf("replace sanitized input: %w", err)
	}
	return sanitized, true, nil
}
