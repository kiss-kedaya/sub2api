package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
)

// 随机 JSON 生成器：给 json_span_patch 的 oracle 对照测试造输入。
// 生成的文本默认是合法 JSON（corruptJSON 才会弄坏它），键序、空白、转义、重名键、
// 非对象元素都刻意打散，用来逼出新旧实现在定位/追加/替换上的任何分歧。

type spanGen struct{ r *rand.Rand }

func newSpanGen(seed int64) *spanGen { return &spanGen{r: rand.New(rand.NewSource(seed))} }

func (g *spanGen) pick(opts ...string) string { return opts[g.r.Intn(len(opts))] }
func (g *spanGen) chance(percent int) bool    { return g.r.Intn(100) < percent }

var spanGenStrings = []string{
	`""`, `" "`, `"  "`, `"\t"`, `"real"`, `"x"`, `"a\"b"`, `"back\\slash"`, `"line1\nline2"`,
	`"é世界😀"`, `"é世界"`, `"😀"`, `"{\"role\":\"assistant\"}"`, `"[1,2,3]"`,
	`"}"`, `"{"`, `","`, `"a\/b"`, `"\u0000"`, `"</script>&"`, `" "`,
	`"reasoning_content"`, `"\"reasoning_content\":\"\""`, `"messages"`,
}

func (g *spanGen) scalar() string {
	switch g.r.Intn(8) {
	case 0:
		return `null`
	case 1:
		return `true`
	case 2:
		return `false`
	case 3:
		return g.pick(`0`, `-1`, `1.5e3`, `12345678901234567890`, `0.0`, `-0`)
	default:
		return spanGenStrings[g.r.Intn(len(spanGenStrings))]
	}
}

// spanGenKeys 里有被各函数读写的键名：噪声里再出现一遍，专门考验「只认当前层」。
var spanGenKeys = []string{
	"id", "name", "meta", "x", "messages", "input", "content", "type", "role", "thinking", "text",
	"reasoning_content", "tool_calls", "tool_call_id", "call_id", "stream_options", "include_usage",
	"reasoning_content2", "xreasoning_content", `a\"b`, `role`,
}

func (g *spanGen) value(depth int) string {
	if depth <= 0 || g.chance(55) {
		return g.scalar()
	}
	n := g.r.Intn(4)
	parts := make([]string, 0, n)
	if g.chance(50) {
		for i := 0; i < n; i++ {
			parts = append(parts, g.value(depth-1))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	for i := 0; i < n; i++ {
		parts = append(parts, `"`+spanGenKeys[g.r.Intn(len(spanGenKeys))]+`":`+g.value(depth-1))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (g *spanGen) noise(max int) []string {
	n := g.r.Intn(max + 1)
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, `"`+spanGenKeys[g.r.Intn(len(spanGenKeys))]+`":`+g.value(2))
	}
	return out
}

func (g *spanGen) shuffle(parts []string) {
	g.r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
}

func spanObject(members []string) string { return "{" + strings.Join(members, ",") + "}" }
func spanArray(items []string) string    { return "[" + strings.Join(items, ",") + "]" }

// nonObjectElement 是数组里不该出现、但客户端可能塞进来的非对象元素。
func (g *spanGen) nonObjectElement() string {
	return g.pick(`"assistant"`, `5`, `null`, `[]`, `[{"role":"assistant"}]`, `"{\"role\":\"assistant\"}"`, `true`, `{}`, `[1,[2]]`)
}

// size 给出消息条数：偶尔 0、多数很小、少数很大（把 sjson 的二次方放大出来）。
func (g *spanGen) size() int {
	switch v := g.r.Intn(100); {
	case v < 5:
		return 0
	case v < 55:
		return 1 + g.r.Intn(5)
	case v < 90:
		return 6 + g.r.Intn(35)
	default:
		return 41 + g.r.Intn(260)
	}
}

// sprinkleWhitespace 在合法 JSON 的结构符号两侧随机插入空白（含缩进、换行、制表、CRLF），字符串内部不动。
func (g *spanGen) sprinkleWhitespace(compact string) string {
	ws := []string{"", "", "", " ", "\n", "\t", "\r\n  ", "  ", "\n    "}
	var b strings.Builder
	inString := false
	for i := 0; i < len(compact); i++ {
		c := compact[i]
		if inString {
			_ = b.WriteByte(c)
			if c == '\\' && i+1 < len(compact) {
				i++
				_ = b.WriteByte(compact[i])
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			_, _ = b.WriteString(ws[g.r.Intn(len(ws))])
			_ = b.WriteByte(c)
			inString = true
		case '{', '[', ',', ':':
			_ = b.WriteByte(c)
			_, _ = b.WriteString(ws[g.r.Intn(len(ws))])
		case '}', ']':
			_, _ = b.WriteString(ws[g.r.Intn(len(ws))])
			_ = b.WriteByte(c)
		default:
			_ = b.WriteByte(c)
		}
	}
	return b.String()
}

// finish 把根对象的成员打散，并按概率套上空白；compact 表示没有额外空白。
func (g *spanGen) finish(members []string) (doc string, compact bool) {
	g.shuffle(members)
	doc = spanObject(members)
	if g.chance(40) {
		doc = g.sprinkleWhitespace(doc)
		if g.chance(30) {
			doc = "\n  " + doc + "\n"
		}
		return doc, false
	}
	return doc, true
}

// corruptJSON 把合法 JSON 弄坏：截断、删字节、替换字节、插入杂字符。
func (g *spanGen) corruptJSON(doc string) string {
	if doc == "" {
		return doc
	}
	switch g.r.Intn(5) {
	case 0:
		return doc[:g.r.Intn(len(doc))]
	case 1:
		i := g.r.Intn(len(doc))
		return doc[:i] + doc[i+1:]
	case 2:
		i := g.r.Intn(len(doc))
		return doc[:i] + string("{}[],:\"\\x"[g.r.Intn(9)]) + doc[i+1:]
	case 3:
		i := g.r.Intn(len(doc) + 1)
		return doc[:i] + g.pick(",", "}", "]", `"`, "{", "\x00", "  ") + doc[i:]
	default:
		return strings.TrimRight(doc, "}\n ")
	}
}

// ---- Chat Completions（reasoning_content 占位）----

func (g *spanGen) chatRole(assistantPct int) string {
	if g.chance(assistantPct) {
		return g.pick(`"assistant"`, `"assistant"`, `"assistant"`, `"assistant"`, `"assistant"`, `" assistant "`, `"assistant\n"`, `"assistant"`)
	}
	return g.pick(`"user"`, `"system"`, `"tool"`, `"Assistant"`, `"developer"`, `null`, `5`, `[]`, `{}`, `""`)
}

func (g *spanGen) chatMessage(assistantPct int) string {
	if g.chance(3) {
		return g.nonObjectElement()
	}
	var m []string
	if !g.chance(3) {
		m = append(m, `"role":`+g.chatRole(assistantPct))
	}
	switch g.r.Intn(6) {
	case 1:
		m = append(m, `"content":null`)
	case 2:
		m = append(m, `"content":`+g.pick(spanGenStrings...))
	case 3:
		m = append(m, `"content":[{"type":"text","text":`+g.pick(spanGenStrings...)+`}]`)
	case 4:
		m = append(m, `"content":`+g.value(2))
	}
	key := g.pick(`"reasoning_content"`, `"reasoning_content"`, `"reasoning_content"`, `"reasoning\u005fcontent"`)
	switch g.r.Intn(16) {
	case 0, 1, 2, 3, 4, 5:
	case 6, 7, 8:
		m = append(m, key+`:""`)
	case 9:
		m = append(m, key+`:" "`)
	case 10:
		m = append(m, key+`:"real thinking\nline2 é世界"`)
	case 11:
		m = append(m, key+`:null`)
	case 12:
		m = append(m, key+":"+g.pick(`0`, `false`, `true`, `{}`, `[]`, `"\u0000"`, `"   "`))
	default:
		m = append(m, key+":"+g.pick(spanGenStrings...))
	}
	if g.chance(8) {
		m = append(m, `"reasoning_content":`+g.pick(`""`, `"dup"`, `null`)) // 重名键：两边都只认第一个
	}
	if g.chance(15) {
		m = append(m, `"tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{}"}}]`)
	}
	m = append(m, g.noise(3)...)
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) chatDoc(assistantPct int) (doc string, compact bool) {
	n := g.size()
	msgs := make([]string, n)
	for i := range msgs {
		msgs[i] = g.chatMessage(assistantPct)
	}
	return g.messagesDoc(spanArray(msgs))
}

// messagesDoc 把 messages 值放进根对象，并按概率加入「错误形状 / 重名 / 嵌套诱饵」。
func (g *spanGen) messagesDoc(messages string) (doc string, compact bool) {
	if g.chance(6) {
		messages = g.pick(`"x"`, `{}`, `null`, `5`, `{"0":{"role":"assistant"}}`, `true`, `[]`)
	}
	members := []string{g.pick(`"messages"`, `"messages"`, `"messages"`, `"messag\u0065s"`) + ":" + messages, `"model":"deepseek-v4-pro"`}
	if g.chance(50) {
		members = append(members, `"stream":true`)
	}
	if g.chance(5) {
		members = append(members, `"messages":[{"role":"assistant"}]`) // 重名键，晚出现的不该被处理
	}
	if g.chance(8) {
		members = append(members, `"metadata":{"messages":[{"role":"assistant","content":"decoy"}]}`)
	}
	members = append(members, g.noise(3)...)
	return g.finish(members)
}

// ---- Anthropic messages（thinking 占位）----

func (g *spanGen) anthropicBlock() string {
	thinkingField := func() string {
		switch g.r.Intn(8) {
		case 0:
			return ""
		case 1, 2:
			return `"thinking":""`
		case 3:
			return `"thinking":" "`
		case 4:
			return `"thinking":"real"`
		case 5:
			return `"thinking":null`
		case 6:
			return `"thinking":5`
		default:
			return `"thinking":"line1\nline2"`
		}
	}
	var m []string
	switch g.r.Intn(12) {
	case 0, 1:
		m = []string{`"type":"text"`, `"text":` + g.pick(spanGenStrings...)}
	case 2, 3, 4:
		m = []string{`"type":"thinking"`}
	case 5:
		m = []string{`"type":" thinking "`}
	case 6:
		m = []string{`"type":"redacted_thinking"`, `"data":"opaque"`}
	case 7, 8:
		m = []string{`"type":"tool_use"`, `"id":"toolu_1"`, `"name":"f"`, `"input":{}`}
	case 9:
		m = []string{`"type":"tool_result"`, `"tool_use_id":"toolu_1"`, `"content":"ok"`}
	case 10:
		m = []string{`"type":"THINKING"`}
	default:
		return g.pick(`"str"`, `5`, `null`, `[]`, `{}`, `{"type":"tool_use"}`, `[{"type":"thinking","thinking":""}]`)
	}
	if m[0] == `"type":"thinking"` || m[0] == `"type":" thinking "` {
		if f := thinkingField(); f != "" {
			m = append(m, f)
		}
		if g.chance(6) {
			m = append(m, thinkingField())
		}
	}
	if g.chance(6) {
		m[0] = `"typ\u0065":` + m[0][len(`"type":`):] // 键用转义写法
	}
	m = append(m, g.noise(2)...)
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) anthropicMessage() string {
	if g.chance(3) {
		return g.nonObjectElement()
	}
	m := []string{`"role":` + g.chatRole(60)}
	switch g.r.Intn(10) {
	case 0:
	case 1:
		m = append(m, `"content":null`)
	case 2:
		m = append(m, `"content":"plain text"`)
	case 3:
		m = append(m, `"content":[]`)
	default:
		n := 1 + g.r.Intn(5)
		blocks := make([]string, n)
		for i := range blocks {
			blocks[i] = g.anthropicBlock()
		}
		m = append(m, `"content":`+spanArray(blocks))
	}
	if g.chance(5) {
		m = append(m, `"content":[{"type":"tool_use"}]`) // 重名键
	}
	m = append(m, g.noise(2)...)
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) anthropicDoc() (doc string, compact bool) {
	n := g.size()
	msgs := make([]string, n)
	for i := range msgs {
		msgs[i] = g.anthropicMessage()
	}
	return g.messagesDoc(spanArray(msgs))
}

// ---- Responses input（原生 reasoning 占位 / 缺失工具输出修复）----

func (g *spanGen) nativeReasoningContent() string {
	return g.pick(
		`[]`, `[]`, `null`,
		`[{"type":"reasoning_text","text":""}]`, `[{"type":"reasoning_text","text":" "}]`,
		`[{"type":"reasoning_text","text":"real"}]`, `[{"type":"summary_text","text":"s"}]`,
		`[{"text":"real"}]`, `[{"text":"  "}]`, `[{"type":"reasoning_text","text":null}]`, `[{"type":"reasoning_text"}]`,
		`"string"`, `{}`, `5`, `true`, `[1,2]`, `[null]`, `[[]]`, `[{"type":"reasoning_text","text":"a"},{"type":"reasoning_text","text":""}]`,
	)
}

func (g *spanGen) responsesItem() string {
	if g.chance(3) {
		return g.nonObjectElement()
	}
	switch g.r.Intn(8) {
	case 0:
		return `{"type":"message","role":"user","content":[{"type":"input_text","text":` + g.pick(spanGenStrings...) + `}]}`
	case 1:
		return `{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"}`
	case 2:
		return `{"role":"user","content":"no type key"}`
	}
	m := []string{g.pick(`"type":"reasoning"`, `"type":"reasoning"`, `"type":" reasoning "`, `"type":"reasoning"`), `"id":"rs_1"`}
	if g.chance(60) {
		m = append(m, `"summary":[]`)
	}
	if g.chance(40) {
		m = append(m, `"encrypted_content":"opaque"`)
	}
	if g.chance(85) {
		m = append(m, `"content":`+g.nativeReasoningContent())
	}
	if g.chance(6) {
		m = append(m, `"content":`+g.nativeReasoningContent()) // 重名键
	}
	m = append(m, g.noise(2)...)
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) nativeDoc() (doc string, compact bool) {
	n := g.size()
	items := make([]string, n)
	for i := range items {
		items[i] = g.responsesItem()
	}
	input := spanArray(items)
	if g.chance(6) {
		input = g.pick(`"x"`, `{}`, `null`, `5`, `[]`)
	}
	members := []string{g.pick(`"input"`, `"input"`, `"inp\u0075t"`) + ":" + input, `"model":"deepseek-v4-pro"`}
	if g.chance(5) {
		members = append(members, `"metadata":{"input":[{"type":"reasoning"}]}`)
	}
	members = append(members, g.noise(3)...)
	return g.finish(members)
}

func (g *spanGen) repairItem() string {
	if g.chance(3) {
		return g.nonObjectElement()
	}
	id := func() string {
		return g.pick(`"c1"`, `"c2"`, `"c3"`, `"c4"`, `"c5"`, `"c1"`, `"c2"`, `""`, `null`, `"c1"`, `"<a&b>"`, `5`, `"é\"q"`)
	}
	callTypes := []string{"function_call", "custom_tool_call", "local_shell_call", "tool_search_call"}
	outTypes := []string{"function_call_output", "custom_tool_call_output", "local_shell_call_output", "tool_search_output"}
	var m []string
	switch g.r.Intn(10) {
	case 0, 1, 2, 3:
		m = []string{`"type":"` + callTypes[g.r.Intn(4)] + `"`, `"name":"f"`}
		if g.chance(92) {
			m = append(m, `"call_id":`+id())
		}
	case 4, 5, 6:
		m = []string{`"type":"` + outTypes[g.r.Intn(4)] + `"`, `"output":"ok"`}
		if g.chance(92) {
			m = append(m, `"call_id":`+id())
		}
	case 7:
		m = []string{`"type":"message"`, `"role":"user"`, `"content":"hi"`}
	case 8:
		m = []string{`"type":"web_search_call"`, `"id":"ws_1"`}
	default:
		m = []string{`"role":"user"`, `"content":"no type"`}
	}
	m = append(m, g.noise(2)...)
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) repairDoc() (doc string, compact bool) {
	n := g.size()
	if n > 60 {
		n = 60 // 回放历史很长也只有一两处缺口，数量放大没有额外覆盖
	}
	items := make([]string, n)
	for i := range items {
		items[i] = g.repairItem()
	}
	input := spanArray(items)
	if g.chance(5) {
		input = g.pick(`"x"`, `{}`, `null`, `5`, `[]`)
	}
	members := []string{g.pick(`"input"`, `"input"`, `"inp\u0075t"`) + ":" + input}
	switch g.r.Intn(12) {
	case 0:
		members = append(members, `"previous_response_id":"resp_1"`)
	case 1:
		members = append(members, `"previous_response_id":""`)
	case 2:
		members = append(members, `"previous_response_id":null`)
	}
	if g.chance(5) {
		members = append(members, `"metadata":{"input":[{"type":"function_call","call_id":"zz"}]}`)
	}
	members = append(members, g.noise(3)...)
	return g.finish(members)
}

// ---- Chat 工具配对（dropOrphan / normalize）----

func (g *spanGen) pairingToolCalls() string {
	n := 1 + g.r.Intn(3)
	calls := make([]string, n)
	for i := range calls {
		id := g.pick(`"c1"`, `"c2"`, `"c3"`, `"c4"`, `"c5"`, `"c6"`, `""`, `"c1"`)
		call := []string{`"type":"function"`, `"function":{"name":"f","arguments":"{}"}`}
		if g.chance(95) {
			call = append(call, `"id":`+id)
		}
		g.shuffle(call)
		calls[i] = spanObject(call)
	}
	if g.chance(4) {
		return g.pick(`{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}`, `null`, `"x"`, `[]`)
	}
	return spanArray(calls)
}

func (g *spanGen) pairingMessage() string {
	if g.chance(2) {
		return g.nonObjectElement()
	}
	var m []string
	switch g.r.Intn(10) {
	case 0, 1:
		m = []string{`"role":"user"`, `"content":` + g.pick(`"hi"`, `[{"type":"text","text":"hi"}]`, `""`, `null`)}
	case 2:
		m = []string{`"role":"system"`, `"content":"sys"`}
	case 3, 4, 5:
		m = []string{`"role":"assistant"`, `"content":` + g.pick(`""`, `null`, `"calling"`, `[{"type":"text","text":"calling"}]`), `"tool_calls":` + g.pairingToolCalls()}
		if g.chance(30) {
			m = append(m, `"reasoning_content":"think"`)
		}
	case 6:
		m = []string{`"role":"assistant"`, `"content":"plain answer"`}
	default:
		m = []string{`"role":"tool"`, `"content":"result"`}
		if g.chance(88) {
			m = append(m, `"tool_call_id":`+g.pick(`"c1"`, `"c2"`, `"c3"`, `"c4"`, `"c5"`, `"c6"`, `""`, `"c9"`))
		}
	}
	if g.chance(10) {
		m = append(m, `"name":"n"`, `"audio":{"id":"a"}`, `"refusal":null`)
	}
	g.shuffle(m)
	return spanObject(m)
}

func (g *spanGen) pairingDoc() (doc string, compact bool) {
	n := g.size()
	msgs := make([]string, n)
	for i := range msgs {
		msgs[i] = g.pairingMessage()
	}
	return g.messagesDoc(spanArray(msgs))
}

// ---- stream_options ----

func (g *spanGen) streamOptionsDoc() (doc string, compact bool) {
	members := []string{`"model":"gpt-5.4"`, `"messages":[{"role":"user","content":"hi"}]`}
	if g.chance(50) {
		members = append(members, `"stream":true`)
	}
	switch g.r.Intn(16) {
	case 0, 1, 2, 3:
	case 4:
		members = append(members, `"stream_options":{}`)
	case 5, 6:
		members = append(members, `"stream_options":{"include_usage":true}`)
	case 7:
		members = append(members, `"stream_options":{"include_usage":false}`)
	case 8:
		members = append(members, `"stream_options":{"include_usage":null}`)
	case 9:
		members = append(members, `"stream_options":{"include_usage":"yes"}`)
	case 10:
		members = append(members, `"stream_options":{"a":1}`)
	case 11:
		members = append(members, `"stream_options":`+g.pick(`null`, `5`, `"s"`, `true`))
	case 12:
		members = append(members, `"stream_options":`+g.pick(`[]`, `[1]`, `[{"include_usage":false}]`))
	case 13:
		members = append(members, `"stream_options":{"a":{"include_usage":false},"include_usage":0}`)
	case 14:
		members = append(members, `"stream_options":{"include_usage":false}`)
	default:
		members = append(members, `"metadata":{"stream_options":{"include_usage":false}}`)
	}
	members = append(members, g.noise(2)...)
	doc, compact = g.finish(members)
	switch g.r.Intn(20) {
	case 0:
		return "", false
	case 1:
		return "  \n", false
	case 2:
		return g.pick(`[]`, `[1,2]`, `5`, `"x"`, `null`, `true`), false
	case 3:
		return g.corruptJSON(doc), false
	case 4:
		return "\ufeff" + doc, false
	case 5:
		return doc + "\n", false
	}
	return doc, compact
}

// ---- 比较辅助 ----

// decodeSpanJSON 用 UseNumber 解码，数字按原文比较，不受 float64 精度影响。
func decodeSpanJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

func sameSpanSlice(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// stripSpanWhitespace 删掉所有 ASCII 空白（字符串内部的也删，两边一致即可），用来判断「只差空白」。
func stripSpanWhitespace(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			out = append(out, c)
		}
	}
	return out
}

// spanEquivalent 判断新实现的输出 got 相对 oracle 的输出 want 是否「语义一致」。
// 返回空串表示一致，否则是失败原因。strictBytes 为真时要求逐字节一致。
func spanEquivalent(input, want, got []byte, strictBytes bool) string {
	if sameSpanSlice(want, input) {
		if !sameSpanSlice(got, input) {
			return "oracle returned the body untouched, the new code must return the same slice"
		}
		return ""
	}
	if bytes.Equal(want, got) {
		return ""
	}
	if !json.Valid(input) {
		// 非法 JSON：旧实现尽力打补丁（结果多半也是坏的），新实现要么原样返回，要么与旧实现一致；
		// 允许语义比较的函数还允许只差空白（旧实现重排数组时会去掉元素间空白）。
		if bytes.Equal(got, input) || (!strictBytes && bytes.Equal(stripSpanWhitespace(got), stripSpanWhitespace(want))) {
			return ""
		}
		return "invalid input: new output is neither the original body nor the oracle's output"
	}
	if strictBytes {
		return "expected byte-identical output"
	}
	wantValue, err := decodeSpanJSON(want)
	if err != nil {
		return "oracle output is not valid JSON: " + err.Error()
	}
	gotValue, err := decodeSpanJSON(got)
	if err != nil {
		return "new output is not valid JSON: " + err.Error()
	}
	if !reflect.DeepEqual(wantValue, gotValue) {
		return "parsed values differ"
	}
	return ""
}

func spanDescribe(seed int64, input, want, got []byte) string {
	clip := func(b []byte) string {
		if len(b) > 1500 {
			return string(b[:1500]) + "...(" + strconv.Itoa(len(b)) + " bytes)"
		}
		return string(b)
	}
	return "seed=" + strconv.FormatInt(seed, 10) + "\ninput=" + clip(input) + "\nwant =" + clip(want) + "\ngot  =" + clip(got)
}
