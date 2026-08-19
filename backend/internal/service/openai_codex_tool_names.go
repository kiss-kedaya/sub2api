package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	codexReservedPythonToolName = "python"
	codexPythonToolAlias        = "python__sub2api"
	codexToolNameReverseKey     = "openai_codex_tool_name_reverse"
)

type codexToolNameField struct {
	object map[string]any
	key    string
	name   string
}

// aliasOpenAIOAuthReservedToolNames avoids names reserved by the ChatGPT
// Codex backend. It validates every declaration/reference before mutating so
// collisions cannot leave a partially rewritten request.
func aliasOpenAIOAuthReservedToolNames(reqBody map[string]any) (map[string]string, bool, error) {
	if reqBody == nil {
		return nil, false, nil
	}

	fields := collectOpenAIResponsesToolNameFields(reqBody)
	owners := make(map[string]string)
	reverse := make(map[string]string)
	for _, field := range fields {
		normalized := aliasOpenAIOAuthReservedToolName(field.name)
		original := field.name
		if normalized != field.name {
			original = strings.TrimSpace(field.name)
		}
		if previous, exists := owners[normalized]; exists && previous != original {
			return nil, false, fmt.Errorf("tool names %q and %q both normalize to %q", previous, original, normalized)
		}
		owners[normalized] = original
		if normalized != field.name {
			reverse[normalized] = original
		}
	}
	if len(reverse) == 0 {
		return nil, false, nil
	}
	for _, field := range fields {
		if aliased := aliasOpenAIOAuthReservedToolName(field.name); aliased != field.name {
			field.object[field.key] = aliased
		}
	}
	return reverse, true, nil
}

func aliasOpenAIOAuthReservedToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.EqualFold(trimmed, codexReservedPythonToolName) {
		return codexPythonToolAlias
	}
	return name
}

func collectOpenAIResponsesToolNameFields(reqBody map[string]any) []codexToolNameField {
	fields := make([]codexToolNameField, 0, 8)
	appendName := func(object map[string]any, key string) {
		if object == nil {
			return
		}
		name, ok := object[key].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return
		}
		fields = append(fields, codexToolNameField{object: object, key: key, name: name})
	}
	var collectTools func(any)
	collectTools = func(rawTools any) {
		tools, ok := rawTools.([]any)
		if !ok {
			return
		}
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			toolType := strings.ToLower(strings.TrimSpace(firstNonEmptyString(tool["type"])))
			if toolType != "namespace" {
				appendName(tool, "name")
			}
			if function, ok := tool["function"].(map[string]any); ok {
				appendName(function, "name")
			}
			collectTools(tool["tools"])
		}
	}
	collectTools(reqBody["tools"])
	collectTools(reqBody["functions"])
	if choice, ok := reqBody["tool_choice"].(map[string]any); ok {
		if !strings.EqualFold(strings.TrimSpace(firstNonEmptyString(choice["type"])), "namespace") {
			appendName(choice, "name")
		}
		if function, ok := choice["function"].(map[string]any); ok {
			appendName(function, "name")
		}
	}
	if input, ok := reqBody["input"].([]any); ok {
		for _, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(firstNonEmptyString(item["type"])))
			if typ == "additional_tools" {
				collectTools(item["tools"])
			}
			if strings.HasSuffix(typ, "_call") || typ == "tool_call" {
				appendName(item, "name")
				if function, ok := item["function"].(map[string]any); ok {
					appendName(function, "name")
				}
			}
		}
	}
	return fields
}

func aliasOpenAIOAuthReservedToolNamesBody(body []byte) ([]byte, map[string]string, bool, error) {
	if len(body) == 0 || !containsASCIIFold(body, []byte(codexReservedPythonToolName)) {
		return body, nil, false, nil
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return body, nil, false, fmt.Errorf("decode OAuth reserved tool names: %w", err)
	}
	reverse, changed, err := aliasOpenAIOAuthReservedToolNames(reqBody)
	if err != nil || !changed {
		return body, reverse, false, err
	}
	normalized, err := json.Marshal(reqBody)
	if err != nil {
		return body, nil, false, fmt.Errorf("encode OAuth reserved tool names: %w", err)
	}
	return normalized, reverse, true, nil
}

func containsASCIIFold(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		matched := true
		for j := range needle {
			a, b := haystack[i+j], needle[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func setCodexToolNameReverse(c *gin.Context, reverse map[string]string) {
	if c == nil {
		return
	}
	copyMap := make(map[string]string, len(reverse))
	for aliased, original := range reverse {
		copyMap[aliased] = original
	}
	c.Set(codexToolNameReverseKey, copyMap)
}

func codexToolNameReverseFromContext(c *gin.Context) map[string]string {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(codexToolNameReverseKey)
	if !ok {
		return nil
	}
	reverse, _ := raw.(map[string]string)
	return reverse
}

func restoreCodexToolNamesInJSON(data []byte, reverse map[string]string) []byte {
	if len(data) == 0 || len(reverse) == 0 || !json.Valid(data) {
		return data
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return data
	}
	if !restoreCodexToolNameFields(decoded, reverse) {
		return data
	}
	restored, err := json.Marshal(decoded)
	if err != nil {
		return data
	}
	return restored
}

func restoreCodexToolNamesFromContext(c *gin.Context, data []byte) []byte {
	return restoreCodexToolNamesInJSON(data, codexToolNameReverseFromContext(c))
}

func restoreCodexToolNameFields(value any, reverse map[string]string) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		if name, ok := typed["name"].(string); ok {
			if original, exists := reverse[name]; exists {
				typed["name"] = original
				changed = true
			}
		}
		for _, child := range typed {
			if restoreCodexToolNameFields(child, reverse) {
				changed = true
			}
		}
	case []any:
		for _, child := range typed {
			if restoreCodexToolNameFields(child, reverse) {
				changed = true
			}
		}
	}
	return changed
}
