package service

import (
	"strings"
	"unsafe"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type codexBootstrapItemMeta struct {
	namespace string
	name      string
}

type responsesInputItemMeta struct {
	start, end      int32
	isObject, tight bool
	itemType        string
	id              string
	callID          string
	idIsString      bool
	hasCallID       bool

	nsMemberIdx  int16
	nsDropStart  int32
	nsDropEnd    int32
	isNSToolCall bool

	stripID         bool
	stripCallID     bool
	idDropEditCount uint8
	idDropEdits     [2]jsonEdit
}

type responsesInputIndex struct {
	isArray, usable bool
	inputStart      int32
	inputEnd        int32

	hasCompactionTrigger               bool
	hasExactFunctionCallOutput         bool
	hasToolCallOutput                  bool
	hasToolCallContext                 bool
	hasFunctionCallOutputMissingCallID bool
	hasAdditionalNamespaceTool         bool
	hasNamespaceField                  bool
	hasDeepSeekToolCall                bool
	hasReasoningNonEmptyContent        bool
	hasIDSanitization                  bool

	codexBootstrapCandidates []codexBootstrapItemMeta
	items                    []responsesInputItemMeta
}

func (x *bodyTopIndex) responsesInput() *responsesInputIndex {
	x.inputOnce.Do(func() {
		x.inputIdx = buildResponsesInputIndex(x)
	})
	return x.inputIdx
}

func internInputItemType(s string) string {
	switch s {
	case "":
		return ""
	case "message":
		return "message"
	case "reasoning":
		return "reasoning"
	case "function_call":
		return "function_call"
	case "function_call_output":
		return "function_call_output"
	case "custom_tool_call":
		return "custom_tool_call"
	case "custom_tool_call_output":
		return "custom_tool_call_output"
	case "local_shell_call":
		return "local_shell_call"
	case "local_shell_call_output":
		return "local_shell_call_output"
	case "tool_search_call":
		return "tool_search_call"
	case "tool_search_output":
		return "tool_search_output"
	case "mcp_tool_call":
		return "mcp_tool_call"
	case "mcp_tool_call_output":
		return "mcp_tool_call_output"
	case "tool_call":
		return "tool_call"
	case "web_search_call":
		return "web_search_call"
	case "image_generation_call":
		return "image_generation_call"
	case "item_reference":
		return "item_reference"
	case "additional_tools":
		return "additional_tools"
	case "compaction_trigger":
		return "compaction_trigger"
	default:
		return strings.Clone(s)
	}
}

func internBootstrapNamespace(s string) string {
	switch s {
	case "codex_app":
		return "codex_app"
	case "codex_tui":
		return "codex_tui"
	case "agent":
		return "agent"
	default:
		return ""
	}
}

func jsonSpanHasNamespaceTool(b []byte, arrStart, arrEnd int) bool {
	if arrStart < 0 || arrEnd > len(b) || arrEnd-arrStart < 2 || b[arrStart] != '[' {
		return false
	}
	found := false
	ok := jsonForEachElementSpan(b, arrStart, arrEnd, func(elemStart, elemEnd int) bool {
		if b[elemStart] != '{' {
			return true
		}
		var typeRaw string
		var hasType bool
		memberOK := jsonForEachMemberSpan(b, elemStart, elemEnd, func(key string, valStart, valEnd, _, _ int) bool {
			if key == "type" {
				typeRaw = jsonView(b[valStart:valEnd])
				hasType = true
				return false
			}
			return true
		})
		if !memberOK {
			if strings.EqualFold(strings.TrimSpace(gjson.Parse(jsonView(b[elemStart:elemEnd])).Get("type").String()), "namespace") {
				found = true
				return false
			}
			return true
		}
		if hasType && strings.EqualFold(strings.TrimSpace(extractJSONMemberString(typeRaw, false)), "namespace") {
			found = true
			return false
		}
		return true
	})
	if ok {
		return found
	}
	tools := gjson.Parse(jsonView(b[arrStart:arrEnd]))
	if !tools.IsArray() {
		return false
	}
	tools.ForEach(func(_, tool gjson.Result) bool {
		if strings.EqualFold(strings.TrimSpace(tool.Get("type").String()), "namespace") {
			found = true
			return false
		}
		return true
	})
	return found
}

func jsonSpanArrayNonEmpty(b []byte, arrStart, arrEnd int) bool {
	if arrStart < 0 || arrEnd > len(b) || arrEnd-arrStart < 2 || b[arrStart] != '[' {
		return false
	}
	nonEmpty := false
	ok := jsonForEachElementSpan(b, arrStart, arrEnd, func(_, _ int) bool {
		nonEmpty = true
		return false
	})
	if ok {
		return nonEmpty
	}
	res := gjson.Parse(jsonView(b[arrStart:arrEnd]))
	return res.IsArray() && len(res.Array()) > 0
}

func buildResponsesInputIndex(x *bodyTopIndex) *responsesInputIndex {
	if x == nil || len(x.body) > 1<<31-2 {
		return &responsesInputIndex{usable: false}
	}
	res, handled := x.get("input")
	if !handled {
		return &responsesInputIndex{usable: false}
	}
	if !res.Exists() || res.Type != gjson.JSON || len(res.Raw) == 0 || res.Raw[0] != '[' {
		return &responsesInputIndex{isArray: false, usable: true}
	}
	inputStart := res.Index
	inputEnd := inputStart + len(res.Raw)
	if inputStart < 0 || inputEnd > len(x.body) ||
		unsafe.StringData(res.Raw) != unsafe.SliceData(x.body[inputStart:]) ||
		x.body[inputEnd-1] != ']' {
		return &responsesInputIndex{usable: false}
	}

	idx := &responsesInputIndex{
		isArray:    true,
		usable:     true,
		inputStart: int32(inputStart),
		inputEnd:   int32(inputEnd),
		items:      make([]responsesInputItemMeta, 0, 16),
	}

	b := x.body
	var membersBuf [16]jsonObjMember
	var dropBuf [2]jsonEdit

	elemOK := jsonForEachElementSpan(b, inputStart, inputEnd, func(elemStart, elemEnd int) bool {
		if b[elemStart] != '{' {
			idx.items = append(idx.items, responsesInputItemMeta{
				start:       int32(elemStart),
				end:         int32(elemEnd),
				isObject:    false,
				nsMemberIdx: -1,
			})
			return true
		}

		members := membersBuf[:0]
		var (
			hasType, hasID, hasCallID, hasNamespace, hasName, hasTools, hasContent bool
			typeValStart, typeValEnd                                               int
			idValStart, idValEnd                                                   int
			callIDValStart, callIDValEnd                                           int
			nsValStart, nsValEnd                                                   int
			nameValStart, nameValEnd                                               int
			toolsValStart, toolsValEnd                                             int
			contentValStart, contentValEnd                                         int
			idMember, callIDMember, nsMemberIdx                                    = -1, -1, -1
		)

		memberOK := jsonForEachMemberSpan(b, elemStart, elemEnd, func(key string, valStart, valEnd, memberStart, memberEnd int) bool {
			switch key {
			case "type":
				if !hasType {
					hasType = true
					typeValStart, typeValEnd = valStart, valEnd
				}
			case "id":
				if !hasID {
					hasID = true
					idValStart, idValEnd = valStart, valEnd
					idMember = len(members)
				}
			case "call_id":
				if !hasCallID {
					hasCallID = true
					callIDValStart, callIDValEnd = valStart, valEnd
					callIDMember = len(members)
				}
			case "namespace":
				if !hasNamespace {
					hasNamespace = true
					nsValStart, nsValEnd = valStart, valEnd
					nsMemberIdx = len(members)
				}
			case "name":
				if !hasName {
					hasName = true
					nameValStart, nameValEnd = valStart, valEnd
				}
			case "tools":
				if !hasTools {
					hasTools = true
					toolsValStart, toolsValEnd = valStart, valEnd
				}
			case "content":
				if !hasContent {
					hasContent = true
					contentValStart, contentValEnd = valStart, valEnd
				}
			}
			members = append(members, jsonObjMember{start: memberStart, end: memberEnd})
			return true
		})
		if !memberOK || len(members) > 32767 {
			idx.usable = false
			return false
		}

		var itemType string
		if hasType {
			itemType = internInputItemType(extractJSONMemberString(jsonView(b[typeValStart:typeValEnd]), false))
		}
		trimmedType := strings.TrimSpace(itemType)

		var idStr string
		idIsString := hasID && b[idValStart] == '"'
		if idIsString || (hasID && itemType == "item_reference") {
			idStr = extractJSONMemberString(jsonView(b[idValStart:idValEnd]), true)
		}

		var callIDStr string
		if hasCallID {
			callIDStr = extractJSONMemberString(jsonView(b[callIDValStart:callIDValEnd]), true)
		}

		tight := jsonObjMembersTight(b, elemStart, members)
		it := responsesInputItemMeta{
			start:        int32(elemStart),
			end:          int32(elemEnd),
			isObject:     true,
			tight:        tight,
			itemType:     itemType,
			id:           idStr,
			callID:       callIDStr,
			idIsString:   idIsString,
			hasCallID:    hasCallID,
			nsMemberIdx:  -1,
			isNSToolCall: isOpenAIResponsesToolCallItemType(itemType),
		}

		if itemType == "compaction_trigger" {
			idx.hasCompactionTrigger = true
		}
		if itemType == "function_call_output" {
			idx.hasExactFunctionCallOutput = true
			if hasNamespace {
				if nsInterned := internBootstrapNamespace(extractJSONMemberString(jsonView(b[nsValStart:nsValEnd]), false)); nsInterned != "" {
					var nameStr string
					if hasName {
						nameStr = extractJSONMemberString(jsonView(b[nameValStart:nameValEnd]), true)
					}
					idx.codexBootstrapCandidates = append(idx.codexBootstrapCandidates, codexBootstrapItemMeta{
						namespace: nsInterned,
						name:      nameStr,
					})
				}
			}
		}
		if strings.EqualFold(trimmedType, "additional_tools") && hasTools && b[toolsValStart] == '[' && !idx.hasAdditionalNamespaceTool {
			if jsonSpanHasNamespaceTool(b, toolsValStart, toolsValEnd) {
				idx.hasAdditionalNamespaceTool = true
			}
		}
		if isCodexToolCallOutputItemType(itemType) {
			idx.hasToolCallOutput = true
			if strings.TrimSpace(callIDStr) == "" {
				idx.hasFunctionCallOutputMissingCallID = true
			}
		} else if isCodexToolCallContextItemType(itemType) {
			if strings.TrimSpace(callIDStr) != "" {
				idx.hasToolCallContext = true
			}
		}
		if isDeepSeekToolCallType(itemType) {
			idx.hasDeepSeekToolCall = true
		}
		if nsMemberIdx >= 0 {
			idx.hasNamespaceField = true
			it.nsMemberIdx = int16(nsMemberIdx)
			if tight {
				drops := appendJSONMemberDrops(dropBuf[:0], members, nsMemberIdx)
				if len(drops) == 1 {
					it.nsDropStart = int32(drops[0].start)
					it.nsDropEnd = int32(drops[0].end)
				}
			}
		}
		if trimmedType == "reasoning" && hasContent && b[contentValStart] == '[' && !idx.hasReasoningNonEmptyContent {
			if jsonSpanArrayNonEmpty(b, contentValStart, contentValEnd) {
				idx.hasReasoningNonEmptyContent = true
			}
		}

		stripID := idIsString && shouldStripOpenAIResponsesInputItemID(trimmedType, idStr)
		stripCallID := callIDMember >= 0 && shouldStripOpenAIResponsesNonPairCallID(trimmedType)
		if stripID || stripCallID {
			idx.hasIDSanitization = true
			it.stripID = stripID
			it.stripCallID = stripCallID
			if tight {
				var drops []jsonEdit
				switch {
				case stripID && stripCallID && idMember < callIDMember:
					drops = appendJSONMemberDrops(dropBuf[:0], members, idMember, callIDMember)
				case stripID && stripCallID:
					drops = appendJSONMemberDrops(dropBuf[:0], members, callIDMember, idMember)
				case stripID:
					drops = appendJSONMemberDrops(dropBuf[:0], members, idMember)
				default:
					drops = appendJSONMemberDrops(dropBuf[:0], members, callIDMember)
				}
				it.idDropEditCount = uint8(len(drops))
				copy(it.idDropEdits[:], drops)
			}
		}

		idx.items = append(idx.items, it)
		return true
	})

	if !elemOK || !idx.usable {
		idx.usable = false
		idx.items = nil
		idx.codexBootstrapCandidates = nil
	}
	return idx
}

// WalkCodexBootstrapInputCandidates visits input items with type == "function_call_output",
// yielding their (namespace, name) pairs until fn returns false.
func WalkCodexBootstrapInputCandidates(c *gin.Context, body []byte, fn func(namespace, name string) bool) {
	if len(body) == 0 || fn == nil {
		return
	}
	if idx := RequestBodyTop(c, body).responsesInput(); idx != nil && idx.usable {
		if !idx.isArray || !idx.hasExactFunctionCallOutput {
			return
		}
		for _, cand := range idx.codexBootstrapCandidates {
			if !fn(cand.namespace, cand.name) {
				return
			}
		}
		return
	}
	inputStart, inputEnd, isArray, ok := locateJSONInputArraySpan(c, body)
	if !ok {
		input := parseRawJSONView(body).Get("input")
		if !input.IsArray() {
			return
		}
		input.ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() != "function_call_output" {
				return true
			}
			return fn(item.Get("namespace").String(), item.Get("name").String())
		})
		return
	}
	if !isArray {
		return
	}
	stopped := false
	elemOK := jsonForEachElementSpan(body, inputStart, inputEnd, func(elemStart, elemEnd int) bool {
		if body[elemStart] != '{' {
			return true
		}
		elem := jsonView(body[elemStart:elemEnd])
		if !strings.Contains(elem, "function_call_output") && !strings.Contains(elem, `\u`) {
			return true
		}
		var typeRaw, nsRaw, nameRaw string
		var hasType, hasNS, hasName bool
		memberOK := jsonForEachMemberSpan(body, elemStart, elemEnd, func(key string, valStart, valEnd, _, _ int) bool {
			switch key {
			case "type":
				if !hasType {
					typeRaw = jsonView(body[valStart:valEnd])
					hasType = true
				}
			case "namespace":
				if !hasNS {
					nsRaw = jsonView(body[valStart:valEnd])
					hasNS = true
				}
			case "name":
				if !hasName {
					nameRaw = jsonView(body[valStart:valEnd])
					hasName = true
				}
			}
			return !hasType || !hasNS || !hasName
		})
		if !memberOK {
			item := gjson.Parse(elem)
			if item.Get("type").String() != "function_call_output" {
				return true
			}
			if !fn(item.Get("namespace").String(), item.Get("name").String()) {
				stopped = true
				return false
			}
			return true
		}
		if !hasType || extractJSONMemberString(typeRaw, false) != "function_call_output" {
			return true
		}
		var ns, name string
		if hasNS {
			ns = extractJSONMemberString(nsRaw, false)
		}
		if hasName {
			name = extractJSONMemberString(nameRaw, false)
		}
		if !fn(ns, name) {
			stopped = true
			return false
		}
		return true
	})
	if !elemOK && !stopped {
		gjson.Parse(jsonView(body[inputStart:inputEnd])).ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() != "function_call_output" {
				return true
			}
			return fn(item.Get("namespace").String(), item.Get("name").String())
		})
	}
}
