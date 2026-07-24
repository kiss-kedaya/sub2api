package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Invalid replayed IDs are removed rather than rewritten because a fabricated
// msg/fc ID may point at a different upstream object.
func shouldStripOpenAIResponsesInputItemID(itemType, id string) bool {
	if id == "" {
		return false
	}
	if itemType == "message" {
		return !strings.HasPrefix(id, "msg")
	}
	if isCodexToolCallInputType(itemType) {
		return !strings.HasPrefix(id, "fc")
	}
	return false
}

func sanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}

	paths := make([]string, 0)
	index := 0
	input.ForEach(func(_, item gjson.Result) bool {
		currentIndex := index
		index++
		if !item.IsObject() {
			return true
		}
		itemType := item.Get("type")
		id := item.Get("id")
		if itemType.Type == gjson.String && id.Type == gjson.String &&
			shouldStripOpenAIResponsesInputItemID(itemType.String(), id.String()) {
			paths = append(paths, fmt.Sprintf("input.%d.id", currentIndex))
		}
		return true
	})
	if len(paths) == 0 {
		return body, false, nil
	}

	sanitized := body
	for _, path := range paths {
		var err error
		sanitized, err = sjson.DeleteBytes(sanitized, path)
		if err != nil {
			return nil, false, fmt.Errorf("delete %s: %w", path, err)
		}
	}
	return sanitized, true, nil
}
