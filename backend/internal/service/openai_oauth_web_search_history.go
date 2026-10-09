package service

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Codex 压缩后的历史会带 web_search_call，但 tools 被清空。ChatGPT OAuth
// 端点要求同时声明 web_search，否则返回 response protection is unavailable。
// Responses Lite 不接受顶层 hosted tool，改放进 input additional_tools。
// 调用方本来没有工具时，把 tool_choice 钉成 none，避免注入的工具被真正调用。

const (
	openAIWebSearchCallItemType      = "web_search_call"
	openAIAdditionalToolsItemType    = "additional_tools"
	openAICompactionTriggerItemType  = "compaction_trigger"
	openAIAdditionalToolsDefaultRole = "developer"
)

var openAIWebSearchHistoryTool = map[string]any{
	"type":                "web_search",
	"external_web_access": false,
}

func isOpenAIWebSearchToolType(toolType string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolType), "web_search")
}

func openAIToolsContainWebSearch(rawTools any) bool {
	tools, ok := rawTools.([]any)
	if !ok {
		return false
	}
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && isOpenAIWebSearchToolType(firstNonEmptyString(tool["type"])) {
			return true
		}
	}
	return false
}

func shouldPinOpenAIWebSearchHistoryToolChoice(choice any) bool {
	switch typed := choice.(type) {
	case nil:
		return true
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "" || normalized == "auto" || normalized == "none"
	default:
		return false
	}
}

func openAIAdditionalToolsInsertIndex(itemTypes []string) int {
	if n := len(itemTypes); n > 0 && itemTypes[n-1] == openAICompactionTriggerItemType {
		return n - 1
	}
	return len(itemTypes)
}

func ensureOpenAIOAuthWebSearchToolForHistory(reqBody map[string]any, responsesLite bool) bool {
	if reqBody == nil {
		return false
	}
	input, ok := reqBody["input"].([]any)
	if !ok {
		return false
	}
	hasWebSearchCall := false
	callerDeclaredTools := false
	additionalToolsIndex := -1
	itemTypes := make([]string, len(input))
	for i, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		itemTypes[i] = strings.TrimSpace(firstNonEmptyString(item["type"]))
		switch itemTypes[i] {
		case openAIWebSearchCallItemType:
			hasWebSearchCall = true
		case openAIAdditionalToolsItemType:
			if openAIToolsContainWebSearch(item["tools"]) {
				return false
			}
			if tools, _ := item["tools"].([]any); len(tools) > 0 {
				callerDeclaredTools = true
			}
			if additionalToolsIndex < 0 {
				additionalToolsIndex = i
			}
		}
	}
	if !hasWebSearchCall || openAIToolsContainWebSearch(reqBody["tools"]) {
		return false
	}
	tools, _ := reqBody["tools"].([]any)
	callerDeclaredTools = callerDeclaredTools || len(tools) > 0

	switch {
	case !responsesLite:
		reqBody["tools"] = append(tools, cloneOpenAIWebSearchHistoryTool())
	case additionalToolsIndex >= 0:
		item, _ := input[additionalToolsIndex].(map[string]any)
		existing, _ := item["tools"].([]any)
		item["tools"] = append(existing, cloneOpenAIWebSearchHistoryTool())
	default:
		at := openAIAdditionalToolsInsertIndex(itemTypes)
		additional := map[string]any{
			"type":  openAIAdditionalToolsItemType,
			"role":  openAIAdditionalToolsDefaultRole,
			"tools": []any{cloneOpenAIWebSearchHistoryTool()},
		}
		next := make([]any, 0, len(input)+1)
		next = append(next, input[:at]...)
		next = append(next, additional)
		next = append(next, input[at:]...)
		reqBody["input"] = next
	}
	if !callerDeclaredTools {
		if choice, exists := reqBody["tool_choice"]; !exists || shouldPinOpenAIWebSearchHistoryToolChoice(choice) {
			reqBody["tool_choice"] = "none"
		}
	}
	return true
}

func ensureOpenAIOAuthWebSearchToolForHistoryBody(body []byte, responsesLite bool) ([]byte, bool, error) {
	if len(body) == 0 || !bytes.Contains(body, []byte(openAIWebSearchCallItemType)) {
		return body, false, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, false, nil
	}
	items := input.Array()
	hasWebSearchCall := false
	callerDeclaredTools := false
	additionalToolsIndex := -1
	itemTypes := make([]string, len(items))
	for i, item := range items {
		itemTypes[i] = strings.TrimSpace(item.Get("type").String())
		switch itemTypes[i] {
		case openAIWebSearchCallItemType:
			hasWebSearchCall = true
		case openAIAdditionalToolsItemType:
			itemTools := item.Get("tools")
			if gjsonToolsContainWebSearch(itemTools) {
				return body, false, nil
			}
			if itemTools.IsArray() && len(itemTools.Array()) > 0 {
				callerDeclaredTools = true
			}
			if additionalToolsIndex < 0 {
				additionalToolsIndex = i
			}
		}
	}
	if !hasWebSearchCall {
		return body, false, nil
	}
	tools := gjson.GetBytes(body, "tools")
	if gjsonToolsContainWebSearch(tools) {
		return body, false, nil
	}
	topLevelTools := tools.IsArray() && len(tools.Array()) > 0
	callerDeclaredTools = callerDeclaredTools || topLevelTools

	var (
		next []byte
		err  error
	)
	switch {
	case !responsesLite && topLevelTools:
		next, err = sjson.SetBytes(body, "tools.-1", cloneOpenAIWebSearchHistoryTool())
	case !responsesLite:
		next, err = sjson.SetBytes(body, "tools", []any{cloneOpenAIWebSearchHistoryTool()})
	case additionalToolsIndex >= 0 && items[additionalToolsIndex].Get("tools").IsArray():
		next, err = sjson.SetBytes(body, fmt.Sprintf("input.%d.tools.-1", additionalToolsIndex), cloneOpenAIWebSearchHistoryTool())
	case additionalToolsIndex >= 0:
		next, err = sjson.SetBytes(body, fmt.Sprintf("input.%d.tools", additionalToolsIndex), []any{cloneOpenAIWebSearchHistoryTool()})
	default:
		next, err = insertOpenAIAdditionalToolsItemRaw(body, items, openAIAdditionalToolsInsertIndex(itemTypes))
	}
	if err != nil {
		return body, false, fmt.Errorf("declare web_search tool for web_search_call history: %w", err)
	}
	if !callerDeclaredTools {
		choice := gjson.GetBytes(next, "tool_choice")
		if !choice.Exists() || choice.Type == gjson.Null || (choice.Type == gjson.String && shouldPinOpenAIWebSearchHistoryToolChoice(choice.String())) {
			next, err = sjson.SetBytes(next, "tool_choice", "none")
			if err != nil {
				return body, false, fmt.Errorf("pin tool_choice for web_search_call history: %w", err)
			}
		}
	}
	return next, true, nil
}

func insertOpenAIAdditionalToolsItemRaw(body []byte, items []gjson.Result, at int) ([]byte, error) {
	additional, err := marshalOpenAIUpstreamJSON(map[string]any{
		"type":  openAIAdditionalToolsItemType,
		"role":  openAIAdditionalToolsDefaultRole,
		"tools": []any{cloneOpenAIWebSearchHistoryTool()},
	})
	if err != nil {
		return nil, err
	}
	rawItems := make([][]byte, 0, len(items)+1)
	for i, item := range items {
		if i == at {
			rawItems = append(rawItems, additional)
		}
		rawItems = append(rawItems, []byte(item.Raw))
	}
	if at >= len(items) {
		rawItems = append(rawItems, additional)
	}
	raw := append([]byte{'['}, bytes.Join(rawItems, []byte{','})...)
	raw = append(raw, ']')
	return sjson.SetRawBytes(body, "input", raw)
}

func gjsonToolsContainWebSearch(tools gjson.Result) bool {
	if !tools.IsArray() {
		return false
	}
	for _, tool := range tools.Array() {
		if isOpenAIWebSearchToolType(tool.Get("type").String()) {
			return true
		}
	}
	return false
}

func cloneOpenAIWebSearchHistoryTool() map[string]any {
	tool := make(map[string]any, len(openAIWebSearchHistoryTool))
	for key, value := range openAIWebSearchHistoryTool {
		tool[key] = value
	}
	return tool
}
