package securityaudit

import (
	"encoding/json"
	"strings"
)

// marshalPromptToolPayload turns a client-controlled tool argument/result into
// bounded, JSON-safe audit text. json.Marshal is intentional: it escapes angle
// brackets, so a tool result cannot close the scanner's surrounding wrapper.
// Empty tool fields stay empty, preserving the existing no-prompt behavior for
// genuinely empty requests.
func marshalPromptToolPayload(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return ""
	}
	cleaned := normalizeEmbeddedToolJSON(value, 0)
	raw, err := json.Marshal(stripPromptToolBinary(cleaned, 0))
	if err != nil || len(raw) == 0 {
		return ""
	}
	switch string(raw) {
	case "null", "{}", "[]", `""`:
		return ""
	default:
		return string(raw)
	}
}

// marshalPromptToolInvocation normalizes the common name + arguments shape
// without exposing a second protocol-specific representation to the scanner.
func marshalPromptToolInvocation(name string, arguments any) string {
	payload := make(map[string]any, 2)
	if strings.TrimSpace(name) != "" {
		payload["name"] = name
	}
	if arguments != nil {
		if text, ok := arguments.(string); ok && strings.TrimSpace(text) == "" {
			arguments = nil
		}
	}
	if arguments != nil {
		payload["arguments"] = arguments
	}
	return marshalPromptToolPayload(payload)
}

func extractChatToolCalls(value any) []promptSegment {
	calls, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]promptSegment, 0, len(calls))
	for _, item := range calls {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		function, _ := call["function"].(map[string]any)
		name := stringValue(function["name"])
		arguments := function["arguments"]
		if arguments == nil {
			arguments = call["arguments"]
		}
		text := marshalPromptToolInvocation(name, arguments)
		if text == "" {
			continue
		}
		if id := stringValue(call["id"]); id != "" {
			text = marshalPromptToolPayload(map[string]any{"id": id, "call": json.RawMessage(text)})
		}
		if text != "" {
			result = append(result, promptSegment{text: text, role: "assistant", tool: true})
		}
	}
	return result
}

func extractInlineToolSegments(value any) []promptSegment {
	blocks, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]promptSegment, 0, len(blocks))
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		typeName := strings.ToLower(stringValue(block["type"]))
		switch typeName {
		case "tool_use", "tool_call", "function_call":
			text := marshalPromptToolInvocation(stringValue(block["name"]), firstNonNil(block["input"], block["arguments"]))
			if text != "" {
				result = append(result, promptSegment{text: text, role: "assistant", tool: true})
			}
		case "tool_result", "function_call_output":
			payload := map[string]any{}
			if id := stringValue(firstNonNil(block["tool_use_id"], block["call_id"])); id != "" {
				payload["id"] = id
			}
			if content := firstNonNil(block["content"], block["output"]); content != nil {
				payload["content"] = content
			}
			if text := marshalPromptToolPayload(payload); text != "" {
				result = append(result, promptSegment{text: text, role: "tool", tool: true})
			}
		}
	}
	return result
}

func marshalPromptResponseToolItem(entry map[string]any) (string, bool) {
	switch strings.ToLower(stringValue(entry["type"])) {
	case "function_call", "tool_call":
		text := marshalPromptToolInvocation(stringValue(entry["name"]), entry["arguments"])
		if text == "" {
			return "", true
		}
		if id := stringValue(firstNonNil(entry["call_id"], entry["id"])); id != "" {
			text = marshalPromptToolPayload(map[string]any{"id": id, "call": json.RawMessage(text)})
		}
		return text, true
	case "function_call_output", "tool_result":
		payload := map[string]any{}
		if id := stringValue(firstNonNil(entry["call_id"], entry["tool_call_id"], entry["id"])); id != "" {
			payload["id"] = id
		}
		if output := firstNonNil(entry["output"], entry["content"]); output != nil {
			payload["output"] = output
		}
		return marshalPromptToolPayload(payload), true
	default:
		return "", false
	}
}

func marshalPromptGeminiToolPart(part map[string]any) (string, bool) {
	if call := firstNonNil(part["functionCall"], part["function_call"]); call != nil {
		if callMap, ok := call.(map[string]any); ok {
			return marshalPromptToolInvocation(stringValue(callMap["name"]), firstNonNil(callMap["args"], callMap["arguments"])), true
		}
		return marshalPromptToolPayload(call), true
	}
	if response := firstNonNil(part["functionResponse"], part["function_response"]); response != nil {
		if responseMap, ok := response.(map[string]any); ok {
			payload := map[string]any{}
			if name := stringValue(responseMap["name"]); name != "" {
				payload["name"] = name
			}
			if responseValue := responseMap["response"]; responseValue != nil {
				payload["response"] = responseValue
			}
			return marshalPromptToolPayload(payload), true
		}
		return marshalPromptToolPayload(response), true
	}
	return "", false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// normalizeEmbeddedToolJSON decodes JSON strings in the fields where APIs
// commonly embed arguments/results. This lets binary stripping inspect nested
// values while leaving ordinary prose strings untouched.
func normalizeEmbeddedToolJSON(value any, depth int) any {
	if depth > 64 {
		return "[nested payload omitted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if isEmbeddedToolField(key) {
				if text, ok := child.(string); ok {
					var decoded any
					if json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded) == nil {
						child = decoded
					}
				}
			}
			result[key] = normalizeEmbeddedToolJSON(child, depth+1)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = normalizeEmbeddedToolJSON(child, depth+1)
		}
		return result
	default:
		return value
	}
}

func isEmbeddedToolField(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", "")) {
	case "arguments", "input", "output", "content", "response", "args":
		return true
	default:
		return false
	}
}

var promptToolBinaryKeys = map[string]struct{}{
	"data": {}, "inlinedata": {}, "filedata": {}, "b64json": {}, "image": {},
	"imageurl": {}, "audio": {}, "bytes": {}, "bytesbase64encoded": {},
}

func stripPromptToolBinary(value any, depth int) any {
	if depth > 64 {
		return "[nested payload omitted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			normalizedKey := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), "-", ""))
			if _, binary := promptToolBinaryKeys[normalizedKey]; binary && looksLikePromptToolBinary(child) {
				result[key] = "[binary omitted]"
				continue
			}
			result[key] = stripPromptToolBinary(child, depth+1)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = stripPromptToolBinary(child, depth+1)
		}
		return result
	case string:
		if looksLikePromptToolBinary(typed) {
			return "[binary omitted]"
		}
		return typed
	default:
		return value
	}
}

func looksLikePromptToolBinary(value any) bool {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "data:image/") || strings.HasPrefix(lower, "data:video/") || strings.HasPrefix(lower, "data:audio/") {
			return true
		}
		if len(trimmed) >= 256 {
			for _, r := range trimmed {
				if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '/' && r != '=' {
					return false
				}
			}
			return true
		}
	case map[string]any, []any:
		return true
	}
	return false
}
