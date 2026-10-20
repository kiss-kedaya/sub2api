package service

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// openAIResponsesWebSearchPreviewType 是 OpenAI 已退役的工具类型名。
// 官方仍接受它（内部折算成 web_search），但多数第三方 Responses 兼容上游
// 只认新名，收到 preview 直接 400：
//
//	Unsupported tool type: web_search_preview
//
// 第三方上游可接受 web_search 而拒绝 preview。出站前将旧名规整为新名。
const openAIResponsesWebSearchPreviewType = "web_search_preview"

// normalizeOpenAIResponsesLegacyWebSearchToolType 把 tools[].type 与
// tool_choice.type 上的 web_search_preview（含带日期后缀的变体）改写成
// web_search，其余字段原样保留。
//
// 无命中时返回原切片（fast path 只做一次子串搜索，不解析 JSON）。
func normalizeOpenAIResponsesLegacyWebSearchToolType(body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !bytes.Contains(body, []byte(openAIResponsesWebSearchPreviewType)) {
		return body, false, nil
	}

	out := body
	changed := false

	if tools := gjson.GetBytes(out, "tools"); tools.IsArray() {
		for i, tool := range tools.Array() {
			if !isLegacyWebSearchPreviewType(tool.Get("type").String()) {
				continue
			}
			next, err := sjson.SetBytes(out, "tools."+strconv.Itoa(i)+".type", "web_search")
			if err != nil {
				return body, false, err
			}
			out = next
			changed = true
		}
	}

	if choice := gjson.GetBytes(out, "tool_choice"); choice.IsObject() &&
		isLegacyWebSearchPreviewType(choice.Get("type").String()) {
		next, err := sjson.SetBytes(out, "tool_choice.type", "web_search")
		if err != nil {
			return body, false, err
		}
		out = next
		changed = true
	}

	if !changed {
		return body, false, nil
	}
	return out, true, nil
}

func isLegacyWebSearchPreviewType(toolType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(toolType)), openAIResponsesWebSearchPreviewType)
}
