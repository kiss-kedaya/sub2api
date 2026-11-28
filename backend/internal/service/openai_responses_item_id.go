package service

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/gin-gonic/gin"
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
	return sanitizeOpenAIResponsesInputItemIDsCtx(nil, body)
}

func sanitizeOpenAIResponsesInputItemIDsCtx(c *gin.Context, body []byte) ([]byte, bool, error) {
	if len(body) == 0 {
		return body, false, nil
	}
	if idx := RequestBodyTop(c, body).responsesInput(); idx != nil && idx.usable {
		if !idx.isArray || !idx.hasIDSanitization {
			return body, false, nil
		}
		var edits []jsonEdit
		for itemIndex, it := range idx.items {
			if !it.stripID && !it.stripCallID {
				continue
			}
			if it.tight && it.idDropEditCount > 0 {
				edits = append(edits, it.idDropEdits[:it.idDropEditCount]...)
				continue
			}
			replacement, err := deleteInputItemKeysWithSJSON(jsonView(body[it.start:it.end]), it.stripID, it.stripCallID, itemIndex)
			if err != nil {
				return nil, false, err
			}
			edits = append(edits, jsonEdit{start: int(it.start), end: int(it.end), replacement: replacement})
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

	inputStart, inputEnd, isArray, ok := locateJSONInputArraySpan(c, body)
	if !ok {
		return body, false, fmt.Errorf("locate top-level OpenAI Responses input array")
	}
	if !isArray {
		return body, false, nil
	}

	var (
		edits      []jsonEdit
		membersBuf [16]jsonObjMember
		index      int
		walkErr    error
	)
	elemOK := jsonForEachElementSpan(body, inputStart, inputEnd, func(elemStart, elemEnd int) bool {
		itemIndex := index
		index++
		if body[elemStart] != '{' {
			return true
		}
		members := membersBuf[:0]
		var typeRaw string
		var hasType, hasID bool
		var idValStart, idValEnd int
		idMember, callIDMember := -1, -1
		memberOK := jsonForEachMemberSpan(body, elemStart, elemEnd, func(key string, valStart, valEnd, memberStart, memberEnd int) bool {
			switch key {
			case "type":
				if !hasType {
					typeRaw = jsonView(body[valStart:valEnd])
					hasType = true
				}
			case "id":
				if idMember < 0 {
					hasID = true
					idValStart, idValEnd = valStart, valEnd
					idMember = len(members)
				}
			case "call_id":
				if callIDMember < 0 {
					callIDMember = len(members)
				}
			}
			members = append(members, jsonObjMember{start: memberStart, end: memberEnd})
			return true
		})
		if !memberOK {
			elem := jsonView(body[elemStart:elemEnd])
			item := gjson.Parse(elem)
			if !item.IsObject() {
				return true
			}
			trimmedItemType := strings.TrimSpace(item.Get("type").String())
			id := item.Get("id")
			stripID := id.Type == gjson.String && shouldStripOpenAIResponsesInputItemID(trimmedItemType, id.String())
			stripCallID := item.Get("call_id").Exists() && shouldStripOpenAIResponsesNonPairCallID(trimmedItemType)
			if !stripID && !stripCallID {
				return true
			}
			replacement, err := deleteInputItemKeysWithSJSON(elem, stripID, stripCallID, itemIndex)
			if err != nil {
				walkErr = err
				return false
			}
			edits = append(edits, jsonEdit{start: elemStart, end: elemEnd, replacement: replacement})
			return true
		}
		var trimmedItemType string
		if hasType {
			trimmedItemType = strings.TrimSpace(extractJSONMemberString(typeRaw, false))
		}
		stripID := hasID && body[idValStart] == '"' &&
			shouldStripOpenAIResponsesInputItemID(trimmedItemType, extractJSONMemberString(jsonView(body[idValStart:idValEnd]), false))
		stripCallID := callIDMember >= 0 && shouldStripOpenAIResponsesNonPairCallID(trimmedItemType)
		if !stripID && !stripCallID {
			return true
		}
		if jsonObjMembersTight(body, elemStart, members) {
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
		replacement, err := deleteInputItemKeysWithSJSON(jsonView(body[elemStart:elemEnd]), stripID, stripCallID, itemIndex)
		if err != nil {
			walkErr = err
			return false
		}
		edits = append(edits, jsonEdit{start: elemStart, end: elemEnd, replacement: replacement})
		return true
	})
	if walkErr != nil {
		return nil, false, walkErr
	}
	if !elemOK {
		return sanitizeOpenAIResponsesInputItemIDsLegacyGJSON(body)
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

func sanitizeOpenAIResponsesInputItemIDsLegacyGJSON(body []byte) ([]byte, bool, error) {
	input := parseRawJSONView(body).Get("input")
	if !input.IsArray() {
		return body, false, nil
	}
	if input.Index < 0 || input.Index+len(input.Raw) > len(body) ||
		unsafe.StringData(input.Raw) != unsafe.SliceData(body[input.Index:]) {
		return body, false, fmt.Errorf("locate top-level OpenAI Responses input array")
	}

	var (
		edits   []jsonEdit
		members []jsonObjMember
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
