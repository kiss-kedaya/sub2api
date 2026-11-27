package service

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/tidwall/gjson"
)

// dropOrphanDeepSeekToolMessages removes role=tool messages whose
// tool_call_id was never announced by a preceding assistant tool_calls
// message. DeepSeek rejects the whole request with
// "Messages with role 'tool' must be a response to a preceding message with
// 'tool_calls'" and an agent history rewritten by a client can contain such
// orphans (e.g. after a reconnect or a dropped assistant turn).
//
// The scan reads the body in place (no copy of the messages array); the
// rebuilt array is written once, and only when something is dropped.
func dropOrphanDeepSeekToolMessages(body []byte) []byte {
	messages := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !messages.IsArray() {
		return body
	}
	var announced map[string]struct{}
	var dropped []int
	index := 0
	messages.ForEach(func(_, m gjson.Result) bool {
		switch m.Get("role").String() {
		case "assistant":
			for _, tc := range m.Get("tool_calls").Array() {
				if id := tc.Get("id").String(); id != "" {
					if announced == nil {
						announced = make(map[string]struct{})
					}
					announced[id] = struct{}{}
				}
			}
		case "tool":
			id := m.Get("tool_call_id").String()
			if _, ok := announced[id]; id == "" || !ok {
				dropped = append(dropped, index)
			}
		}
		index++
		return true
	})
	if len(dropped) == 0 {
		return body
	}
	arrayStart, arrayEnd, ok := jsonResultSpan(body, messages)
	if !ok || !gjson.Valid(messages.Raw) {
		return body
	}
	out := make([]byte, 0, len(body))
	out = append(out, body[:arrayStart]...)
	out = append(out, '[')
	index = 0
	next := 0
	intact := true
	messages.ForEach(func(_, m gjson.Result) bool {
		i := index
		index++
		if next < len(dropped) && dropped[next] == i {
			next++
			return true
		}
		start, end, ok := jsonResultSpan(body, m)
		if !ok {
			intact = false
			return false
		}
		if len(out) > arrayStart+1 {
			out = append(out, ',')
		}
		out = append(out, body[start:end]...)
		return true
	})
	if !intact {
		return body
	}
	out = append(out, ']')
	return append(out, body[arrayEnd:]...)
}

// normalizeDeepSeekChatMessages rebuilds tool pairs into the exact order
// DeepSeek requires (assistant tool_calls immediately followed by its tool
// replies). The orphan-only filter is not enough when another message sits
// between the assistant and its replies.
func normalizeDeepSeekChatMessages(body []byte) []byte {
	return normalizeDeepSeekChatMessagesCore(body, false)
}

// normalizeAndDropOrphanDeepSeekChatMessages fuses orphan tool removal and
// tool-pair normalization into a single zero-copy scan over messages.
func normalizeAndDropOrphanDeepSeekChatMessages(body []byte) []byte {
	return normalizeDeepSeekChatMessagesCore(body, true)
}

type chatMsgSpanMeta struct {
	absStart     int
	absEnd       int
	role         string
	toolCallID   string
	toolIDStart  int
	toolIDCount  int
	canonical    bool
	hasToolCalls bool
}

func addDSStringSet(list []string, bufCap int, m map[string]struct{}, val string) ([]string, map[string]struct{}, bool) {
	if m != nil {
		if _, exists := m[val]; exists {
			return list, m, false
		}
		m[val] = struct{}{}
		return list, m, true
	}
	if slices.Contains(list, val) {
		return list, nil, false
	}
	if len(list) < bufCap {
		return append(list, val), nil, true
	}
	nm := make(map[string]struct{}, len(list)+8)
	for _, k := range list {
		nm[k] = struct{}{}
	}
	nm[val] = struct{}{}
	return list, nm, true
}

func inDSStringSet(list []string, m map[string]struct{}, val string) bool {
	if m != nil {
		_, ok := m[val]
		return ok
	}
	return slices.Contains(list, val)
}

func locateDeepSeekMessagesArray(body []byte) (int, int, bool) {
	if len(body) == 0 {
		return 0, 0, false
	}
	s := jsonView(body)
	var messagesVal string
	var hasMessages bool
	ok := jsonForEachMember(s, func(key, val string) bool {
		if key == "messages" {
			messagesVal = val
			hasMessages = true
			return false
		}
		return true
	})
	if !ok {
		raw := gjson.Get(s, "messages")
		if !raw.IsArray() {
			return 0, 0, false
		}
		return jsonResultSpan(body, raw)
	}
	if !hasMessages || len(messagesVal) < 2 || messagesVal[0] != '[' {
		return 0, 0, false
	}
	start := int(uintptr(unsafe.Pointer(unsafe.StringData(messagesVal))) - uintptr(unsafe.Pointer(unsafe.SliceData(body))))
	end := start + len(messagesVal)
	if start < 0 || end > len(body) || body[start] != '[' || body[end-1] != ']' {
		return 0, 0, false
	}
	return start, end, true
}

func normalizeDeepSeekChatMessagesLegacy(body []byte, dropOrphans bool) []byte {
	if dropOrphans {
		body = dropOrphanDeepSeekToolMessages(body)
	}
	raw := gjson.Get(openAIWSPayloadStringView(body), "messages")
	if !raw.IsArray() {
		return body
	}
	start, end, ok := jsonResultSpan(body, raw)
	if !ok {
		return body
	}
	var messages []apicompat.ChatMessage
	if err := json.Unmarshal(body[start:end], &messages); err != nil {
		return body
	}
	normalized := apicompat.NormalizeChatMessages(messages)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return body
	}
	if bytes.Equal(encoded, body[start:end]) {
		return body
	}
	out := make([]byte, 0, len(body)-(end-start)+len(encoded))
	out = append(out, body[:start]...)
	out = append(out, encoded...)
	return append(out, body[end:]...)
}

func normalizeDeepSeekChatMessagesCore(body []byte, dropOrphans bool) []byte {
	start, end, ok := locateDeepSeekMessagesArray(body)
	if !ok {
		return body
	}
	if end-start == 2 {
		return body
	}

	messagesStr := jsonView(body[start:end])
	var metasBuf [16]chatMsgSpanMeta
	metas := metasBuf[:0]
	var toolIDsBuf [16]string
	toolIDs := toolIDsBuf[:0]
	var parsedMsgs map[int]apicompat.ChatMessage

	var announcedBuf [16]string
	announcedList := announcedBuf[:0]
	var announcedMap map[string]struct{}

	var seenToolBuf [16]string
	seenToolList := seenToolBuf[:0]
	var seenToolMap map[string]struct{}

	isArrayTight := true
	allCanonical := true
	hasDropped := false
	inOrder := true
	pendingStart := 0
	pendingCount := 0
	pendingIdx := 0
	nonCanonicalCount := 0

	n := len(messagesStr)
	p := 1
	elemIndex := 0
	for {
		sp := jsonSkipSpace(messagesStr, p)
		if sp != p {
			isArrayTight = false
			allCanonical = false
		}
		if sp >= n {
			return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
		}
		if messagesStr[sp] == ']' {
			if sp+1 != n {
				return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
			}
			break
		}
		if elemIndex > 0 {
			if messagesStr[sp] != ',' {
				return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
			}
			sp2 := jsonSkipSpace(messagesStr, sp+1)
			if sp2 != sp+1 {
				isArrayTight = false
				allCanonical = false
			}
			sp = sp2
			if sp >= n || messagesStr[sp] == ']' || messagesStr[sp] == ',' {
				return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
			}
		}
		elemEnd, elemOK := jsonSkipValue(messagesStr, sp)
		if !elemOK || elemEnd <= sp {
			return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
		}
		elem := messagesStr[sp:elemEnd]
		if elem[0] != '{' {
			return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
		}

		var meta chatMsgSpanMeta
		meta.absStart = start + sp
		meta.absEnd = start + elemEnd

		toolIDStart := len(toolIDs)
		var nextToolIDs []string
		meta.role, meta.toolCallID, meta.hasToolCalls, meta.canonical, nextToolIDs = inspectCanonicalChatMessage(elem, toolIDs)
		if meta.canonical {
			toolIDs = nextToolIDs
			meta.toolIDStart = toolIDStart
			meta.toolIDCount = len(toolIDs) - toolIDStart
		} else {
			allCanonical = false
			nonCanonicalCount++
			if !isArrayTight && nonCanonicalCount >= 4 {
				return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
			}
			var parsed apicompat.ChatMessage
			if err := json.Unmarshal(body[meta.absStart:meta.absEnd], &parsed); err != nil {
				return normalizeDeepSeekChatMessagesLegacy(body, dropOrphans)
			}
			if parsedMsgs == nil {
				parsedMsgs = make(map[int]apicompat.ChatMessage)
			}
			parsedMsgs[len(metas)] = parsed
			meta.role = parsed.Role
			meta.toolCallID = parsed.ToolCallID
			meta.hasToolCalls = len(parsed.ToolCalls) > 0
			if meta.hasToolCalls {
				meta.toolIDStart = len(toolIDs)
				meta.toolIDCount = len(parsed.ToolCalls)
				for _, tc := range parsed.ToolCalls {
					toolIDs = append(toolIDs, tc.ID)
				}
			}
		}

		if dropOrphans {
			switch meta.role {
			case "assistant":
				if meta.hasToolCalls {
					for k := 0; k < meta.toolIDCount; k++ {
						id := toolIDs[meta.toolIDStart+k]
						if id != "" {
							announcedList, announcedMap, _ = addDSStringSet(announcedList, len(announcedBuf), announcedMap, id)
						}
					}
				}
			case "tool":
				if meta.toolCallID == "" || !inDSStringSet(announcedList, announcedMap, meta.toolCallID) {
					hasDropped = true
					allCanonical = false
					p = elemEnd
					elemIndex++
					continue
				}
			}
		}

		if allCanonical {
			if pendingIdx < pendingCount {
				if meta.role == "tool" && meta.toolCallID == toolIDs[pendingStart+pendingIdx] {
					var added bool
					seenToolList, seenToolMap, added = addDSStringSet(seenToolList, len(seenToolBuf), seenToolMap, meta.toolCallID)
					if !added {
						inOrder = false
					}
					pendingIdx++
				} else {
					inOrder = false
				}
			} else {
				if meta.role == "tool" && meta.toolCallID != "" {
					inOrder = false
				} else if meta.hasToolCalls {
					pendingStart = meta.toolIDStart
					pendingCount = meta.toolIDCount
					pendingIdx = 0
					for k := 0; k < pendingCount; k++ {
						id := toolIDs[pendingStart+k]
						if id == "" {
							inOrder = false
							break
						}
						for prev := 0; prev < k; prev++ {
							if toolIDs[pendingStart+prev] == id {
								inOrder = false
								break
							}
						}
						if !inOrder {
							break
						}
					}
				}
			}
		}

		metas = append(metas, meta)
		p = elemEnd
		elemIndex++
	}

	if isArrayTight && allCanonical && !hasDropped && inOrder && pendingIdx == pendingCount {
		return body
	}

	// Partial / span-preserving rebuild path: preserve raw bytes for canonical
	// untouched messages instead of unmarshaling and re-marshaling large content.
	var replies map[string]int
	for i := range metas {
		if metas[i].role == "tool" && metas[i].toolCallID != "" {
			if replies == nil {
				replies = make(map[string]int)
			}
			replies[metas[i].toolCallID] = i
		}
	}

	encoded := make([]byte, 0, end-start)
	encoded = append(encoded, '[')
	getParsed := func(idx int) (apicompat.ChatMessage, bool) {
		if m, ok := parsedMsgs[idx]; ok {
			return m, true
		}
		var m apicompat.ChatMessage
		if err := json.Unmarshal(body[metas[idx].absStart:metas[idx].absEnd], &m); err != nil {
			return apicompat.ChatMessage{}, false
		}
		if parsedMsgs == nil {
			parsedMsgs = make(map[int]apicompat.ChatMessage)
		}
		parsedMsgs[idx] = m
		return m, true
	}
	emitRawOrMarshaled := func(idx int) ([]byte, bool) {
		if metas[idx].canonical {
			return body[metas[idx].absStart:metas[idx].absEnd], true
		}
		m, ok := getParsed(idx)
		if !ok {
			return nil, false
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, false
		}
		return b, true
	}
	appendElem := func(b []byte) {
		if len(encoded) > 1 {
			encoded = append(encoded, ',')
		}
		encoded = append(encoded, b...)
	}

	for i := range metas {
		m := &metas[i]
		switch {
		case m.role == "tool":
			if m.toolCallID == "" {
				b, ok := emitRawOrMarshaled(i)
				if !ok {
					return body
				}
				appendElem(b)
			}
			continue
		case m.hasToolCalls:
			allCallsKept := true
			var seenCallBuf [8]string
			seenCallList := seenCallBuf[:0]
			var seenCallMap map[string]struct{}
			keptCount := 0
			for k := 0; k < m.toolIDCount; k++ {
				id := toolIDs[m.toolIDStart+k]
				if id == "" {
					allCallsKept = false
					continue
				}
				if _, ok := replies[id]; !ok {
					allCallsKept = false
					continue
				}
				var added bool
				seenCallList, seenCallMap, added = addDSStringSet(seenCallList, len(seenCallBuf), seenCallMap, id)
				if !added {
					allCallsKept = false
					continue
				}
				keptCount++
			}
			if keptCount == 0 {
				pm, ok := getParsed(i)
				if !ok {
					return body
				}
				norm := apicompat.NormalizeChatMessages([]apicompat.ChatMessage{pm})
				for _, nm := range norm {
					b, err := json.Marshal(nm)
					if err != nil {
						return body
					}
					appendElem(b)
				}
				continue
			}
			if allCallsKept && m.canonical {
				appendElem(body[m.absStart:m.absEnd])
				for k := 0; k < m.toolIDCount; k++ {
					id := toolIDs[m.toolIDStart+k]
					rb, ok := emitRawOrMarshaled(replies[id])
					if !ok {
						return body
					}
					appendElem(rb)
				}
				continue
			}
			pm, ok := getParsed(i)
			if !ok {
				return body
			}
			var keptCalls []apicompat.ChatToolCall
			if allCallsKept {
				keptCalls = pm.ToolCalls
			} else {
				keptCalls = make([]apicompat.ChatToolCall, 0, keptCount)
				seenCallList = seenCallBuf[:0]
				seenCallMap = nil
				for _, tc := range pm.ToolCalls {
					if tc.ID == "" {
						continue
					}
					if _, ok := replies[tc.ID]; !ok {
						continue
					}
					var added bool
					seenCallList, seenCallMap, added = addDSStringSet(seenCallList, len(seenCallBuf), seenCallMap, tc.ID)
					if !added {
						continue
					}
					keptCalls = append(keptCalls, tc)
				}
			}
			copied := pm
			copied.ToolCalls = keptCalls
			mb, err := json.Marshal(copied)
			if err != nil {
				return body
			}
			appendElem(mb)
			for _, tc := range keptCalls {
				rb, ok := emitRawOrMarshaled(replies[tc.ID])
				if !ok {
					return body
				}
				appendElem(rb)
			}
		default:
			b, ok := emitRawOrMarshaled(i)
			if !ok {
				return body
			}
			appendElem(b)
		}
	}
	encoded = append(encoded, ']')
	if bytes.Equal(encoded, body[start:end]) {
		return body
	}
	out := make([]byte, 0, len(body)-(end-start)+len(encoded))
	out = append(out, body[:start]...)
	out = append(out, encoded...)
	return append(out, body[end:]...)
}

// inspectCanonicalChatMessage checks whether elem is already byte-for-byte
// identical to json.Marshal(unmarshaledChatMessage) and extracts role,
// tool_call_id, and tool_calls IDs without allocating on normal messages.
func inspectCanonicalChatMessage(elem string, toolIDsDst []string) (role, toolCallID string, hasToolCalls, canonical bool, nextToolIDs []string) {
	n := len(elem)
	if n < 2 || elem[0] != '{' || elem[n-1] != '}' {
		return "", "", false, false, toolIDsDst
	}
	if n == 2 {
		return "", "", false, false, toolIDsDst // role is required (no omitempty)
	}
	origLen := len(toolIDsDst)
	i := 1
	lastField := -1
	hasRole := false
	for {
		if i >= n || elem[i] != '"' {
			return "", "", false, false, toolIDsDst[:origLen]
		}
		kEnd, kEsc, kOK := jsonSkipString(elem, i)
		if !kOK || kEsc || kEnd >= n || elem[kEnd] != ':' {
			return "", "", false, false, toolIDsDst[:origLen]
		}
		key := elem[i+1 : kEnd-1]
		valStart := kEnd + 1
		valEnd, valOK := jsonSkipValue(elem, valStart)
		if !valOK || valEnd > n-1 {
			return "", "", false, false, toolIDsDst[:origLen]
		}
		val := elem[valStart:valEnd]

		var fieldOrder int
		switch key {
		case "role":
			fieldOrder = 1
			hasRole = true
			if !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
			if strings.IndexByte(val, '\\') < 0 {
				role = val[1 : len(val)-1]
			} else {
				role = jsonRawStringValue(val)
			}
		case "content":
			fieldOrder = 2
			if len(val) == 0 || !isCanonicalRawJSON(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
		case "refusal":
			fieldOrder = 3
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
		case "reasoning_content":
			fieldOrder = 4
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
		case "reasoning":
			fieldOrder = 5
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
		case "name":
			fieldOrder = 6
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
		case "tool_calls":
			fieldOrder = 7
			var tcOK bool
			toolIDsDst, tcOK = inspectCanonicalChatToolCalls(val, toolIDsDst)
			if !tcOK || len(toolIDsDst) == origLen {
				return "", "", false, false, toolIDsDst[:origLen]
			}
			hasToolCalls = true
		case "tool_call_id":
			fieldOrder = 8
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", "", false, false, toolIDsDst[:origLen]
			}
			if strings.IndexByte(val, '\\') < 0 {
				toolCallID = val[1 : len(val)-1]
			} else {
				toolCallID = jsonRawStringValue(val)
			}
		default:
			// cache_control, function_call, or any unknown key forces unmarshal/marshal.
			return "", "", false, false, toolIDsDst[:origLen]
		}
		if fieldOrder <= lastField {
			return "", "", false, false, toolIDsDst[:origLen]
		}
		lastField = fieldOrder

		if valEnd == n-1 {
			break
		}
		if elem[valEnd] != ',' {
			return "", "", false, false, toolIDsDst[:origLen]
		}
		i = valEnd + 1
	}
	if !hasRole {
		return "", "", false, false, toolIDsDst[:origLen]
	}
	return role, toolCallID, hasToolCalls, true, toolIDsDst
}

func inspectCanonicalChatToolCalls(val string, dst []string) ([]string, bool) {
	n := len(val)
	if n < 4 || val[0] != '[' || val[n-1] != ']' {
		return dst, false
	}
	origLen := len(dst)
	i := 1
	for {
		if i >= n || val[i] != '{' {
			return dst[:origLen], false
		}
		end, ok := jsonSkipValue(val, i)
		if !ok || end > n-1 {
			return dst[:origLen], false
		}
		tc := val[i:end]
		id, tcOK := inspectCanonicalChatToolCall(tc)
		if !tcOK {
			return dst[:origLen], false
		}
		dst = append(dst, id)
		if end == n-1 {
			break
		}
		if val[end] != ',' {
			return dst[:origLen], false
		}
		i = end + 1
	}
	return dst, true
}

func inspectCanonicalChatToolCall(tc string) (string, bool) {
	n := len(tc)
	if n < 2 || tc[0] != '{' || tc[n-1] != '}' {
		return "", false
	}
	i := 1
	lastField := -1
	hasFunction := false
	id := ""
	for {
		if i >= n || tc[i] != '"' {
			return "", false
		}
		kEnd, kEsc, kOK := jsonSkipString(tc, i)
		if !kOK || kEsc || kEnd >= n || tc[kEnd] != ':' {
			return "", false
		}
		key := tc[i+1 : kEnd-1]
		valStart := kEnd + 1
		valEnd, valOK := jsonSkipValue(tc, valStart)
		if !valOK || valEnd > n-1 {
			return "", false
		}
		val := tc[valStart:valEnd]

		var fieldOrder int
		switch key {
		case "id":
			fieldOrder = 2
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", false
			}
			if strings.IndexByte(val, '\\') < 0 {
				id = val[1 : len(val)-1]
			} else {
				id = jsonRawStringValue(val)
			}
		case "type":
			fieldOrder = 3
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return "", false
			}
		case "function":
			fieldOrder = 4
			if !isCanonicalChatFunctionCall(val) {
				return "", false
			}
			hasFunction = true
		default:
			return "", false
		}
		if fieldOrder <= lastField {
			return "", false
		}
		lastField = fieldOrder

		if valEnd == n-1 {
			break
		}
		if tc[valEnd] != ',' {
			return "", false
		}
		i = valEnd + 1
	}
	if !hasFunction {
		return "", false
	}
	return id, true
}

func isCanonicalChatFunctionCall(fn string) bool {
	n := len(fn)
	if n < 2 || fn[0] != '{' || fn[n-1] != '}' {
		return false
	}
	i := 1
	lastField := -1
	hasArgs := false
	for {
		if i >= n || fn[i] != '"' {
			return false
		}
		kEnd, kEsc, kOK := jsonSkipString(fn, i)
		if !kOK || kEsc || kEnd >= n || fn[kEnd] != ':' {
			return false
		}
		key := fn[i+1 : kEnd-1]
		valStart := kEnd + 1
		valEnd, valOK := jsonSkipValue(fn, valStart)
		if !valOK || valEnd > n-1 {
			return false
		}
		val := fn[valStart:valEnd]

		var fieldOrder int
		switch key {
		case "name":
			fieldOrder = 0
			if len(val) <= 2 || !isCanonicalJSONString(val) {
				return false
			}
		case "arguments":
			fieldOrder = 1
			hasArgs = true
			if !isCanonicalJSONString(val) {
				return false
			}
		default:
			return false
		}
		if fieldOrder <= lastField {
			return false
		}
		lastField = fieldOrder

		if valEnd == n-1 {
			break
		}
		if fn[valEnd] != ',' {
			return false
		}
		i = valEnd + 1
	}
	return hasArgs
}

func isCanonicalJSONString(raw string) bool {
	n := len(raw)
	if n < 2 || raw[0] != '"' || raw[n-1] != '"' {
		return false
	}
	s := raw[1 : n-1]
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c < 0x20 || c == '"' || c == '<' || c == '>' || c == '&' {
				return false
			}
			if c == '\\' {
				if i+1 >= len(s) {
					return false
				}
				switch s[i+1] {
				case '"', '\\', 'n', 'r', 't', 'b', 'f':
					i += 2
				default:
					return false
				}
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r == '\u2028' || r == '\u2029' {
			return false
		}
		i += size
	}
	return true
}

func isCanonicalRawJSON(raw string) bool {
	if !validJSON(unsafe.Slice(unsafe.StringData(raw), len(raw))) {
		return false
	}
	inStr := false
	for i := 0; i < len(raw); {
		c := raw[i]
		if !inStr {
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				return false
			}
			if c == '"' {
				inStr = true
			}
			i++
			continue
		}
		if c < utf8.RuneSelf {
			if c == '"' {
				inStr = false
				i++
				continue
			}
			if c == '<' || c == '>' || c == '&' {
				return false
			}
			if c == '\\' {
				i += 2
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if r == '\u2028' || r == '\u2029' {
			return false
		}
		i += size
	}
	return !inStr
}
