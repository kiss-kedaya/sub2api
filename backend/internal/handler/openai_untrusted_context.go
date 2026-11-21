package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var codexSelectedPageName = regexp.MustCompile(`^codex_apps_selected_page_part_([1-9][0-9]*)_of_([1-9][0-9]*)$`)

// Codex 桌面端/IDE 会把外部上下文作为“没有对应模型工具调用”的 function_call_output
// 直接塞进首轮 input（namespace=agent）。这些条目不是工具结果，HTTP Responses 的
// 工具续链校验会把它们当成缺 call_id 的工具输出而整单 400（#8609）。
// 这里把已识别的上下文还原成历史版本使用的 user 文本形式：保留内容与顺序，
// 也不静默丢弃；真正的工具输出与未知条目仍走原有校验。
func normalizeCodexUntrustedContext(body []byte) ([]byte, bool) {
	if !mayContainCodexUntrustedContext(body) || !hasUniqueJSONMembers(body) {
		return body, false
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		return body, false
	}
	var input []json.RawMessage
	if json.Unmarshal(request["input"], &input) != nil {
		return body, false
	}
	changed := false
	for i, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		readString := func(key string) string {
			var value string
			_ = json.Unmarshal(item[key], &value)
			return value
		}
		if readString("type") != "function_call_output" || readString("namespace") != "agent" {
			continue
		}
		// 只有完全不带 call_id（或空串）的条目才是上下文注入；已配对的真实
		// 工具输出一律不动，交给工具续链校验。
		if callID, exists := item["call_id"]; exists {
			var value string
			if bytes.Equal(bytes.TrimSpace(callID), []byte("null")) || json.Unmarshal(callID, &value) != nil || strings.TrimSpace(value) != "" {
				continue
			}
		}
		name := readString("name")
		var output *string
		if json.Unmarshal(item["output"], &output) != nil || output == nil || !validCodexUntrustedContext(name, *output) {
			continue
		}
		text := "<external_" + name + ">" + *output + "</external_" + name + ">"
		if validCodexPageContext(name, *output) {
			text = "<external_" + name + ">\n" + *output + "\n</external_" + name + ">"
		}
		message := map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]string{{
				"type": "input_text",
				"text": text,
			}},
		}
		encoded, err := json.Marshal(message)
		if err != nil {
			return body, false
		}
		input[i] = encoded
		changed = true
	}
	if !changed {
		return body, false
	}
	encodedInput, err := json.Marshal(input)
	if err != nil {
		return body, false
	}
	request["input"] = encodedInput
	encoded, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return encoded, true
}

// codexUntrustedContextNames 是 Codex 客户端注入外部上下文时使用的工具名。
// 页面快照另带 part_N_of_M 后缀，用前缀匹配。
var codexUntrustedContextNames = []string{
	"codex_apps_open_page",
	"codex_apps_selected_page_part_",
	"browser_info",
	"goal_objective",
	"untrusted_context",
	"untrusted_content",
	"ide_context",
	"proposed_plan",
}

func isCodexUntrustedContextCandidate(namespace, name string) bool {
	if namespace != "agent" {
		return false
	}
	if name == "codex_apps_open_page" || strings.HasPrefix(name, "codex_apps_selected_page_part_") {
		return true
	}
	switch name {
	case "browser_info", "goal_objective", "untrusted_context", "untrusted_content", "ide_context", "proposed_plan":
		return true
	default:
		return false
	}
}

// mayContainCodexUntrustedContext 是归一化的必要前置条件，先做零解码扫描：
// 类型与工具名都是 JSON 字符串，除非被 \u 转义，否则在正文里原样出现。
func mayContainCodexUntrustedContext(body []byte) bool {
	if !bytes.Contains(body, []byte("function_call_output")) {
		return false
	}
	if bytes.Contains(body, []byte(`\u`)) {
		return true
	}
	for _, name := range codexUntrustedContextNames {
		if bytes.Contains(body, []byte(name)) {
			return true
		}
	}
	return false
}

func validCodexUntrustedContext(name, output string) bool {
	switch name {
	case "browser_info", "goal_objective", "untrusted_context", "ide_context":
		return strings.TrimSpace(output) != ""
	case "untrusted_content":
		// Codex 对未命名上下文源的兜底允许空文本。
		return true
	case "proposed_plan":
		// Codex 按字节长度切分已批准的计划，某一段可能全是空白。
		return output != ""
	default:
		return validCodexPageContext(name, output)
	}
}

func validCodexPageContext(name, output string) bool {
	if name == "codex_apps_open_page" {
		return validCodexPageSnapshot(output)
	}
	parts := codexSelectedPageName.FindStringSubmatch(name)
	if len(parts) != 3 {
		return false
	}
	part, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	total, err := strconv.Atoi(parts[2])
	if err != nil || part > total {
		return false
	}
	header, snapshot, ok := strings.Cut(output, "\n")
	if !ok || strings.TrimSuffix(header, "\r") != fmt.Sprintf("Companion Page snapshot part %d of %d:", part, total) || strings.TrimSpace(snapshot) == "" {
		return false
	}
	// 多段快照可能从 JSON 字符串中间切开，各段原样保留。
	return total > 1 || validCodexPageSnapshot(snapshot)
}

func validCodexPageSnapshot(text string) bool {
	if !hasUniqueJSONMembers([]byte(text)) {
		return false
	}
	var snapshot map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &snapshot) != nil {
		return false
	}
	id, exists := snapshot["page_id"]
	if !exists {
		return false
	}
	if bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return true
	}
	var value string
	return json.Unmarshal(id, &value) == nil && strings.TrimSpace(value) != ""
}
