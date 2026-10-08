package apicompat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
)

// DeepSeek V4 writes thinking tags and DSML tool calls into ordinary text when
// the upstream Responses adapter does not parse them. Bars are U+FF5C.
// ponytail: one 64KiB DSML buffer, flush as text if a call never closes.

const (
	dsmlBar     = "\uFF5C"
	dsmlHead2   = dsmlBar + dsmlBar + "DSML" + dsmlBar + dsmlBar
	dsmlHead1   = dsmlBar + "DSML" + dsmlBar
	dsmlMaxHold = 64 * 1024
)

type DeepSeekCall struct {
	CallID    string
	Name      string
	Arguments string
}

type DeepSeekPiece struct {
	Text      string
	Reasoning string
	Calls     []DeepSeekCall
}

type deepSeekMode int

const (
	deepSeekText deepSeekMode = iota
	deepSeekThink
	deepSeekDSML
)

type DeepSeekInlineFilter struct {
	mode     deepSeekMode
	buf      string
	closeTag string
}

func (f *DeepSeekInlineFilter) Push(delta string) DeepSeekPiece {
	if f == nil {
		return DeepSeekPiece{Text: delta}
	}
	f.buf += delta
	return f.drain(false)
}

func (f *DeepSeekInlineFilter) Flush() DeepSeekPiece {
	if f == nil {
		return DeepSeekPiece{}
	}
	piece := f.drain(true)
	if f.buf == "" {
		return piece
	}
	rest := f.buf
	f.buf = ""
	switch f.mode {
	case deepSeekThink:
		f.mode = deepSeekText
		f.closeTag = ""
		piece.Reasoning += rest
	case deepSeekDSML:
		f.mode = deepSeekText
		calls := parseDSMLBlock(rest)
		if len(calls) == 0 {
			piece.Text += rest
		} else {
			piece.Calls = append(piece.Calls, calls...)
		}
	default:
		piece.Text += rest
	}
	return piece
}

func (f *DeepSeekInlineFilter) drain(flush bool) DeepSeekPiece {
	var piece DeepSeekPiece
	for {
		switch f.mode {
		case deepSeekThink:
			if f.closeTag != "" {
				if idx := strings.Index(f.buf, f.closeTag); idx >= 0 {
					piece.Reasoning += strings.TrimRight(f.buf[:idx], inlineThinkSeparators)
					f.buf = f.buf[idx+len(f.closeTag):]
					f.closeTag = ""
					f.mode = deepSeekText
					continue
				}
			}
			if flush {
				return piece
			}
			hold := reasoningHoldbackLen(f.buf)
			if hold < len(f.buf) {
				piece.Reasoning += f.buf[:len(f.buf)-hold]
				f.buf = f.buf[len(f.buf)-hold:]
			}
			return piece
		case deepSeekDSML:
			if end, ok := dsmlBlockEnd(f.buf); ok {
				calls := parseDSMLBlock(f.buf[:end])
				piece.Calls = append(piece.Calls, calls...)
				f.buf = f.buf[end:]
				f.mode = deepSeekText
				continue
			}
			if len(f.buf) > dsmlMaxHold {
				piece.Text += f.buf
				f.buf = ""
				f.mode = deepSeekText
				continue
			}
			return piece
		default:
			next, kind, end := classifyDeepSeekBracket(f.buf)
			if next < 0 {
				if flush {
					piece.Text += f.buf
					f.buf = ""
					return piece
				}
				hold := deepSeekHoldback(f.buf)
				piece.Text += f.buf[:len(f.buf)-hold]
				f.buf = f.buf[len(f.buf)-hold:]
				return piece
			}
			piece.Text += f.buf[:next]
			f.buf = f.buf[next:]
			switch kind {
			case bracketPartial:
				return piece
			case bracketThinkOpen:
				f.closeTag = thinkCloseFor(f.buf)
				f.buf = f.buf[end:]
				f.mode = deepSeekThink
			case bracketDSMLOpen:
				piece.Text = strings.TrimRight(piece.Text, inlineThinkSeparators)
				f.mode = deepSeekDSML
			case bracketDSMLClose:
				piece.Text = strings.TrimRight(piece.Text, inlineThinkSeparators)
				f.buf = f.buf[end:]
			default:
				piece.Text += "<"
				f.buf = f.buf[1:]
			}
		}
	}
}

func ExtractDeepSeekInline(text string) (visible, reasoning string, calls []DeepSeekCall) {
	var filter DeepSeekInlineFilter
	piece := filter.Push(text)
	tail := filter.Flush()
	return piece.Text + tail.Text, piece.Reasoning + tail.Reasoning, append(piece.Calls, tail.Calls...)
}

type bracketKind int

const (
	bracketLiteral bracketKind = iota
	bracketPartial
	bracketThinkOpen
	bracketDSMLOpen
	bracketDSMLClose
)

func classifyDeepSeekBracket(s string) (idx int, kind bracketKind, end int) {
	at := strings.IndexByte(s, '<')
	if at < 0 {
		return -1, bracketLiteral, 0
	}
	rest := s[at:]
	if k, n, ok := matchThinkOpen(rest); ok {
		return at, k, n
	}
	closing, _, _, n, partial, ok := scanDSMLTag(rest)
	if partial {
		return at, bracketPartial, 0
	}
	if ok {
		if closing {
			return at, bracketDSMLClose, n
		}
		return at, bracketDSMLOpen, n
	}
	if deepSeekPartialBracket(rest) {
		return at, bracketPartial, 0
	}
	return at, bracketLiteral, 1
}

func thinkCloseFor(open string) string {
	for _, pair := range inlineThinkPairs {
		if strings.HasPrefix(open, pair[0]) {
			return pair[1]
		}
	}
	return ""
}

func matchThinkOpen(s string) (bracketKind, int, bool) {
	for _, pair := range inlineThinkPairs {
		if strings.HasPrefix(s, pair[0]) {
			return bracketThinkOpen, len(pair[0]), true
		}
	}
	return bracketLiteral, 0, false
}

func deepSeekPartialBracket(s string) bool {
	if s == "<" || s == "</" {
		return true
	}
	for _, pair := range inlineThinkPairs {
		if strings.HasPrefix(pair[0], s) || strings.HasPrefix(pair[1], s) {
			return true
		}
	}
	return strings.HasPrefix("<"+dsmlHead2, s) || strings.HasPrefix("<"+dsmlHead1, s) ||
		strings.HasPrefix("</"+dsmlHead2, s) || strings.HasPrefix("</"+dsmlHead1, s)
}

func deepSeekHoldback(s string) int {
	max := 0
	limit := len(s)
	if limit > len(dsmlHead2)+4 {
		limit = len(dsmlHead2) + 4
	}
	for n := 1; n <= limit; n++ {
		suf := s[len(s)-n:]
		if deepSeekPartialBracket(suf) && n > max {
			max = n
		}
	}
	return max
}

func matchDSMLHead(s string) (n int, partial, ok bool) {
	for _, head := range []string{dsmlHead2, dsmlHead1} {
		if strings.HasPrefix(s, head) {
			return len(head), false, true
		}
		if s != "" && strings.HasPrefix(head, s) {
			return 0, true, false
		}
	}
	return 0, false, false
}

func scanDSMLTag(s string) (closing bool, name string, attrs map[string]string, end int, partial, ok bool) {
	if !strings.HasPrefix(s, "<") {
		return false, "", nil, 0, false, false
	}
	i := 1
	if strings.HasPrefix(s[i:], "/") {
		closing = true
		i++
	}
	if i >= len(s) {
		return false, "", nil, 0, true, false
	}
	n, headPartial, headOK := matchDSMLHead(s[i:])
	if headPartial {
		return false, "", nil, 0, true, false
	}
	if !headOK {
		return false, "", nil, 0, false, false
	}
	i += n
	if i >= len(s) {
		return false, "", nil, 0, true, false
	}
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	nameAt := i
	for i < len(s) {
		r := rune(s[i])
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			i++
			continue
		}
		break
	}
	if i == nameAt {
		if i >= len(s) {
			return false, "", nil, 0, true, false
		}
		return false, "", nil, 0, false, false
	}
	name = s[nameAt:i]
	attrs = map[string]string{}
	for i < len(s) {
		for i < len(s) && unicode.IsSpace(rune(s[i])) {
			i++
		}
		if i < len(s) && s[i] == '>' {
			return closing, name, attrs, i + 1, false, true
		}
		if i >= len(s) {
			return false, "", nil, 0, true, false
		}
		keyAt := i
		for i < len(s) {
			r := rune(s[i])
			if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
				i++
				continue
			}
			break
		}
		if i == keyAt || i >= len(s) || s[i] != '=' {
			return false, "", nil, 0, false, false
		}
		key := s[keyAt:i]
		i++
		if i >= len(s) || s[i] != '"' {
			return false, "", nil, 0, i >= len(s), false
		}
		i++
		valAt := i
		for i < len(s) && s[i] != '"' {
			i++
		}
		if i >= len(s) {
			return false, "", nil, 0, true, false
		}
		attrs[key] = s[valAt:i]
		i++
	}
	return false, "", nil, 0, true, false
}

func dsmlBlockEnd(s string) (int, bool) {
	rest := s
	off := 0
	seenOpen := false
	for len(rest) > 0 {
		at := strings.IndexByte(rest, '<')
		if at < 0 {
			return 0, false
		}
		rest = rest[at:]
		off += at
		closing, name, _, end, partial, ok := scanDSMLTag(rest)
		if partial {
			return 0, false
		}
		if !ok {
			rest = rest[1:]
			off++
			continue
		}
		if !closing && (name == "calls" || name == "tool_calls" || name == "invoke") {
			seenOpen = true
		}
		if closing && seenOpen && (name == "calls" || name == "tool_calls") {
			return off + end, true
		}
		rest = rest[end:]
		off += end
	}
	return 0, false
}

func parseDSMLBlock(block string) []DeepSeekCall {
	var calls []DeepSeekCall
	var cur *DeepSeekCall
	var params []dsmlParam
	rest := block
	for len(rest) > 0 {
		at := strings.IndexByte(rest, '<')
		if at < 0 {
			break
		}
		rest = rest[at:]
		closing, name, attrs, end, partial, ok := scanDSMLTag(rest)
		if partial || !ok {
			if len(rest) == 0 {
				break
			}
			rest = rest[1:]
			continue
		}
		switch {
		case !closing && name == "invoke":
			if cur != nil {
				cur.Arguments = dsmlArgs(params)
				calls = append(calls, *cur)
			}
			cur = &DeepSeekCall{CallID: newDeepSeekCallID(), Name: attrs["name"]}
			params = nil
		case !closing && name == "parameter" && cur != nil:
			body, consumed := dsmlParameterBody(rest[end:])
			params = append(params, dsmlParam{name: attrs["name"], asString: attrs["string"] != "false", raw: body})
			rest = rest[end+consumed:]
			continue
		case closing && name == "invoke" && cur != nil:
			cur.Arguments = dsmlArgs(params)
			calls = append(calls, *cur)
			cur = nil
			params = nil
		}
		rest = rest[end:]
	}
	if cur != nil {
		cur.Arguments = dsmlArgs(params)
		calls = append(calls, *cur)
	}
	return calls
}

type dsmlParam struct {
	name     string
	asString bool
	raw      string
}

func dsmlParameterBody(s string) (string, int) {
	rest := s
	off := 0
	for len(rest) > 0 {
		at := strings.IndexByte(rest, '<')
		if at < 0 {
			return s, len(s)
		}
		closing, name, _, end, partial, ok := scanDSMLTag(rest[at:])
		if partial {
			return s, len(s)
		}
		if ok && closing && name == "parameter" {
			return s[:off+at], off + at + end
		}
		if !ok {
			rest = rest[at+1:]
			off += at + 1
			continue
		}
		rest = rest[at+end:]
		off += at + end
	}
	return s, len(s)
}

func dsmlArgs(params []dsmlParam) string {
	if len(params) == 0 {
		return "{}"
	}
	obj := make(map[string]any, len(params))
	for _, param := range params {
		if param.name == "" {
			continue
		}
		if param.asString {
			obj[param.name] = param.raw
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(strings.TrimSpace(param.raw)), &value); err != nil {
			obj[param.name] = param.raw
			continue
		}
		obj[param.name] = value
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func newDeepSeekCallID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "call_" + hex.EncodeToString(buf)
}

func (state *ChatCompletionsToResponsesStreamState) absorbDeepSeek(text string, flush bool) (string, string, []ResponsesStreamEvent) {
	if state == nil {
		return text, "", nil
	}
	piece := state.deepseekInline.Push(text)
	if flush {
		tail := state.deepseekInline.Flush()
		piece.Text += tail.Text
		piece.Reasoning += tail.Reasoning
		piece.Calls = append(piece.Calls, tail.Calls...)
	}
	return piece.Text, piece.Reasoning, state.deepSeekCallEvents(piece.Calls)
}

func (state *ChatCompletionsToResponsesStreamState) deepSeekCallEvents(calls []DeepSeekCall) []ResponsesStreamEvent {
	if len(calls) == 0 {
		return nil
	}
	if state.ToolCalls == nil {
		state.ToolCalls = map[int]*ChatToolCall{}
	}
	if state.ToolItemIDs == nil {
		state.ToolItemIDs = map[int]string{}
	}
	if state.ToolOutputIndex == nil {
		state.ToolOutputIndex = map[int]int{}
	}
	if state.toolAnnounced == nil {
		state.toolAnnounced = map[int]bool{}
	}
	if state.toolIsCustom == nil {
		state.toolIsCustom = map[int]bool{}
	}
	if state.toolIsToolSearch == nil {
		state.toolIsToolSearch = map[int]bool{}
	}
	if state.toolIsLocalShell == nil {
		state.toolIsLocalShell = map[int]bool{}
	}
	if state.toolNamespace == nil {
		state.toolNamespace = map[int]NamespacedToolName{}
	}
	var events []ResponsesStreamEvent
	events = append(events, closeChatReasoningItem(state)...)
	for _, call := range calls {
		idx := 0
		for {
			if _, exists := state.ToolCalls[idx]; !exists {
				break
			}
			idx++
		}
		arguments := call.Arguments
		if strings.TrimSpace(arguments) == "" {
			arguments = "{}"
		}
		stored := &ChatToolCall{ID: call.CallID, Type: "function"}
		if stored.ID == "" {
			stored.ID = generateItemID()
		}
		stored.Function.Name = call.Name
		stored.Function.Arguments = arguments
		state.ToolCalls[idx] = stored
		state.ToolItemIDs[idx] = generateItemID()
		state.ToolOutputIndex[idx] = state.allocOutputIndex()
		events = append(events, announceChatToolItem(state, idx, stored, true)...)
	}
	return events
}
