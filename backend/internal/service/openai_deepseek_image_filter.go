package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// StripDeepSeekUnsupportedImageInputs removes image content parts from the
// request shapes accepted by the OpenAI-compatible HTTP handlers. DeepSeek
// text accounts do not accept image input, while the gateway must keep the
// surrounding text, tool calls, and unknown fields intact for the retry.
//
// Only known protocol content arrays are touched. Arbitrary fields named
// "image" inside tool schemas, function arguments, or other user data are
// deliberately left unchanged.
func StripDeepSeekUnsupportedImageInputs(body []byte, endpoint string) ([]byte, int, bool, error) {
	var document any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return nil, 0, false, fmt.Errorf("decode request body: %w", err)
	}
	root, ok := document.(map[string]any)
	if !ok {
		return body, 0, false, nil
	}

	removed := 0
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "chat_completions", "messages":
		if messages, ok := root["messages"].([]any); ok {
			for _, raw := range messages {
				message, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				count, _ := stripContentField(message, "content")
				removed += count
			}
		}
	case "responses":
		if input, ok := root["input"].([]any); ok {
			filtered := make([]any, 0, len(input))
			for _, raw := range input {
				item, ok := raw.(map[string]any)
				if !ok {
					filtered = append(filtered, raw)
					continue
				}
				if isImagePart(item) {
					removed++
					continue
				}
				typeName, _ := item["type"].(string)
				switch strings.ToLower(strings.TrimSpace(typeName)) {
				case "message":
					count, _ := stripContentField(item, "content")
					removed += count
				case "function_call_output":
					// Only this protocol item has a structured media output
					// array. Unknown item types may use the same field name for
					// unrelated data and must pass through unchanged.
					count, _ := stripContentField(item, "output")
					removed += count
				}
				filtered = append(filtered, item)
			}
			if removed > 0 {
				root["input"] = filtered
			}
		}
	}

	if removed == 0 {
		return body, 0, false, nil
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return nil, removed, false, fmt.Errorf("encode request body: %w", err)
	}
	return encoded, removed, true, nil
}

func stripContentField(parent map[string]any, field string) (int, bool) {
	content, exists := parent[field]
	if !exists {
		return 0, false
	}
	parts, ok := content.([]any)
	if !ok {
		if part, ok := content.(map[string]any); ok && isImagePart(part) {
			parent[field] = ""
			return 1, true
		}
		return 0, false
	}
	filtered := make([]any, 0, len(parts))
	removed := 0
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if ok && isImagePart(part) {
			removed++
			continue
		}
		filtered = append(filtered, raw)
	}
	if removed == 0 {
		return 0, false
	}
	if len(filtered) == 0 {
		// Keep the message structurally valid when it contained only an
		// image. The model is not told that it saw an image.
		parent[field] = ""
	} else {
		parent[field] = filtered
	}
	return removed, true
}

func isImagePart(part map[string]any) bool {
	typeName, _ := part["type"].(string)
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "image", "image_url", "input_image":
		return true
	default:
		return false
	}
}
