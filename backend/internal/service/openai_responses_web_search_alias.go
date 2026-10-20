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
// 43 组实测：直连上游 routex.best 时 web_search=200、web_search_preview=400，
// 说明网关此前是原样透传。这里在出站前把旧名折算成新名，语义不变。
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
