package service

import (
	"fmt"
	"strings"
	"unsafe"

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

// sanitizeOpenAIResponsesInputItemIDs 删除 input 里上游不认的回放 ID / call_id。
//
// 一遍扫描：逐项只读取 type / id / call_id 并记下成员区间；没有要删的就原样返回 body
// （不分配、不复制）；有要删的，把“删哪些字节”收成 edit，最后一次精确大小输出。
// 旧实现对每个被改的 item 还要 []byte(item) → sjson.DeleteBytes → string(...)
// 复制 3~4 遍，大 item（带 base64 图片的 message）一个就是几 MB 的垃圾。
func sanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return body, false, nil
	}
	if input.Index < 0 || input.Index+len(input.Raw) > len(body) ||
		unsafe.StringData(input.Raw) != unsafe.SliceData(body[input.Index:]) {
		// parseRawJSONView 直接解析 body，子结果必须保留精确的源偏移；对不上就不动手。
		return body, false, fmt.Errorf("locate top-level OpenAI Responses input array")
	}

	var (
		edits   []jsonEdit
		members []jsonObjMember // 逐 item 复用
		index   int
		walkErr error
	)
	input.ForEach(func(_, item gjson.Result) bool {
		itemIndex := index
		index++
		if !item.IsObject() {
			return true
		}
		members = members[:0]
		var itemType, id gjson.Result
		typeSeen := false
		idMember, callIDMember := -1, -1
		item.ForEach(func(key, value gjson.Result) bool {
			// 重复键只认第一个，与 gjson.Get 一致。
			switch key.Str {
			case "type":
				if !typeSeen {
					itemType, typeSeen = value, true
				}
			case "id":
				if idMember < 0 {
					id, idMember = value, len(members)
				}
			case "call_id":
				if callIDMember < 0 {
					callIDMember = len(members)
				}
			}
			members = append(members, jsonObjMember{start: key.Index, end: value.Index + len(value.Raw)})
			return true
		})
		trimmedItemType := strings.TrimSpace(itemType.String())
		stripID := id.Type == gjson.String && shouldStripOpenAIResponsesInputItemID(trimmedItemType, id.String())
		stripCallID := callIDMember >= 0 && shouldStripOpenAIResponsesNonPairCallID(trimmedItemType)
		if !stripID && !stripCallID {
			return true
		}

		if jsonObjMembersTight(body, item.Index, members) {
			switch {
			case stripID && stripCallID && idMember < callIDMember:
				edits = appendJSONMemberDrops(edits, members, idMember, callIDMember)
			case stripID && stripCallID:
				edits = appendJSONMemberDrops(edits, members, callIDMember, idMember)
			case stripID:
				edits = appendJSONMemberDrops(edits, members, idMember)
			default:
				edits = appendJSONMemberDrops(edits, members, callIDMember)
			}
			return true
		}
		replacement, err := deleteInputItemKeysWithSJSON(item.Raw, stripID, stripCallID, itemIndex)
		if err != nil {
			walkErr = err
			return false
		}
		edits = append(edits, jsonEdit{start: item.Index, end: item.Index + len(item.Raw), replacement: replacement})
		return true
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	if len(edits) == 0 {
		return body, false, nil
	}
	sanitized, err := applyJSONEdits(body, edits)
	if err != nil {
		return nil, false, err
	}
	return sanitized, true, nil
}

// deleteInputItemKeysWithSJSON 处理排版宽松（成员之间有空白）的 item：逗号/空白的取舍
// 以 sjson 为准，先删 id 再删 call_id，与历史实现逐字节一致。
// sjson.DeleteBytes 不会写入入参，所以直接以只读视图喂给它，省掉 []byte(raw) 的复制。
func deleteInputItemKeysWithSJSON(raw string, stripID, stripCallID bool, index int) (string, error) {
	item := unsafe.Slice(unsafe.StringData(raw), len(raw))
	var err error
	if stripID {
		if item, err = sjson.DeleteBytes(item, "id"); err != nil {
			return "", fmt.Errorf("delete input.%d.id: %w", index, err)
		}
	}
	if stripCallID {
		if item, err = sjson.DeleteBytes(item, "call_id"); err != nil {
			return "", fmt.Errorf("delete input.%d.call_id: %w", index, err)
		}
	}
	return unsafe.String(unsafe.SliceData(item), len(item)), nil
}
