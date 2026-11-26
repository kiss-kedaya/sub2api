package handler

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
)

// ValidateStrictOpusMessagesRequest 校验 claude-opus-5-5 的 /v1/messages 请求体。
//
// 口径（2026-10-10）：该模型对非法参数直接 400，不得静默修正后再转发。
//
//	max_tokens 缺失或 < 1        -> max_tokens: Field required
//	temperature 非数字 / 越界     -> temperature: range: 0..1
//	type=text 缺 text 或全空白    -> messages.<i>.content.<j>.text: text content blocks must contain non-whitespace text
//	messages 缺失或空数组         -> field messages is required
//	开 thinking 又传非默认采样    -> temperature/top_p/top_k may only be set to <默认值> when thinking is enabled
//
// 返回 ("", true) 放行；否则返回文案，调用方以 400 + invalid_request_error 回复。
//
// 必须在拿到客户端原始请求体后、任何默认值注入之前调用。OAuth 归一化会在
// service 层补 max_tokens=128000 / temperature=1，那属于本函数之后的行为，
// 不能当作放行理由。
//
// 实现分两层，对任何输入的返回值完全一致：
//   - validateStrictOpusMessagesScan：对整段请求体只扫一遍、零分配。它只在能证明
//     与 gjson 语义一致时才给结论（见该函数注释），其余写法一律交回第二层。
//   - validateStrictOpusMessagesGJSON：原 gjson 实现，逐字保留，作为兜底。
//
// 这个函数跑在限流/调度之前，长上下文请求（几 MB、数百条消息）被 429/503 拒绝前
// 也要付这笔钱，所以第一层不能走 gjson：gjson 每个顶层字段各扫一遍整段请求体、
// messages 数组被物化两次、每个 text 块再反转义一份副本。
func ValidateStrictOpusMessagesRequest(model string, body []byte) (string, bool) {
	if !claude.IsOpus55(model) {
		return "", true
	}
	if len(body) == 0 {
		return "field messages is required", false
	}
	if message, ok, done := validateStrictOpusMessagesScan(body); done {
		return message, ok
	}
	return validateStrictOpusMessagesGJSON(body)
}

// validateStrictOpusMessagesGJSON 是单遍扫描出现之前的原实现（空 body 与模型判断已在
// 调用方完成），逐字保留。不要在这里“顺手优化”：它是扫描器无法确定时的语义基准。
func validateStrictOpusMessagesGJSON(body []byte) (string, bool) {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return "field messages is required", false
	}

	maxTokens := gjson.GetBytes(body, "max_tokens")
	if !maxTokens.Exists() || maxTokens.Type != gjson.Number || maxTokens.Float() < 1 {
		return "max_tokens: Field required", false
	}

	if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() {
		if temperature.Type != gjson.Number || temperature.Float() < 0 || temperature.Float() > 1 {
			return "temperature: range: 0..1", false
		}
	}

	for i, message := range messages.Array() {
		content := message.Get("content")
		if !content.IsArray() {
			continue
		}
		for j, block := range content.Array() {
			if block.Get("type").String() != "text" {
				continue
			}
			// text 缺失、非字符串、或只有空白都属于同一类：Anthropic 拒收空文本块。
			if strings.TrimSpace(block.Get("text").String()) == "" {
				return fmt.Sprintf(
					"messages.%d.content.%d.text: text content blocks must contain non-whitespace text", i, j), false
			}
		}
	}

	// 开启 thinking 时不允许同时指定非默认采样参数。这里只做拒绝，不改写请求。
	if thinkingType := strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()); thinkingType == "enabled" || thinkingType == "adaptive" {
		if temperature := gjson.GetBytes(body, "temperature"); temperature.Exists() && temperature.Float() != 1 {
			return "temperature may only be set to 1 when thinking is enabled", false
		}
		if topP := gjson.GetBytes(body, "top_p"); topP.Exists() && topP.Float() != 1 {
			return "top_p may only be set to 1 when thinking is enabled", false
		}
		if topK := gjson.GetBytes(body, "top_k"); topK.Exists() && topK.Float() != 0 {
			return "top_k may only be set to 0 when thinking is enabled", false
		}
	}

	return "", true
}

// validateStrictOpusMessagesScan 单遍扫描整段请求体，按与 gjson 实现完全相同的顺序给出结论。
// done=false 表示请求体里有无法保证与 gjson 逐字一致的写法，调用方必须改走 gjson 实现；
// done=true 时 (message, ok) 与 gjson 实现对同一输入的返回值相同。
//
// 等价性靠“只处理能证明一致的子集”保证，下列情形一律 done=false：
//   - 顶层不是以 { 开头的对象（gjson 会跳过任意前缀垃圾直到第一个 { 或 [，例如 BOM）；
//   - 被解释的对象（顶层、message、content 块、thinking）里出现非标准结构：缺逗号/冒号、
//     多余逗号、非字符串键、控制字符分隔、NaN/Infinity/前导零等 gjson 宽松接受的数字与字面量；
//   - 被解释对象的键名里有反斜杠（gjson 会先反转义再比较，type 也算 type）；
//   - 顶层 messages / max_tokens / temperature / thinking / top_p / top_k 重复出现
//     （thinking.type 在重复键上会继续往后找，语义不是“首个胜出”）；
//   - messages 或 content 数组里有非对象元素（gjson 的 Get 会跳过任意前缀字符去找第一个 { 或 [，
//     这类元素没有标准语义可复刻，客户端也不会发）；
//   - thinking.type / top_p / top_k 的字符串值含反斜杠（需要 gjson 的反转义规则）；
//   - content 块的 type 值含反斜杠（反转义后可能等于 text）：仅该块退回 gjson 逐字求值，其余照常。
//
// 不被解释的值（system、tools、metadata、image 的 source、tool_use 的 input ……）只用与
// gjson.parseSquash 完全相同的规则跳过：花括号/方括号/圆括号计深度、字符串按“引号前奇数个
// 连续反斜杠为转义”收尾，所以边界与 gjson 逐字节一致，内容不校验，gjson 也不校验。
//
// text 块是否为空白（opusTextBlank）：只看字符串开头，遇到第一个非空白字符即为非空，大段文本不再被
// 反转义、复制；转义序列按 gjson.unescape 的规则逐字复刻，不依赖 gjson。
//
// ponytail: 上面这些非标准写法一律整体退回 gjson，没有逐个加进扫描器；退回的请求成本与改前基本相同
// （最坏多一遍扫描，见基准 fallback_*）。线上若发现某种写法占比不低，再单独把它补进扫描器。
func validateStrictOpusMessagesScan(body []byte) (message string, ok bool, done bool) {
	s := opusScan{b: body, badMsg: -1}
	if _, scanned := scanMembers(body, skipJSONSpace(body, 0), s.visitTop); !scanned {
		return "", false, false
	}

	// 以下顺序与 gjson 实现逐条对应。
	if !s.hasMessages || !s.messagesArray || s.msgCount == 0 {
		return "field messages is required", false, true
	}
	if !s.maxTokens.set || s.maxTokens.kind != jsonNumber || s.maxTokens.mustFloat() < 1 {
		return "max_tokens: Field required", false, true
	}
	if s.temperature.set {
		if s.temperature.kind != jsonNumber {
			return "temperature: range: 0..1", false, true
		}
		if v := s.temperature.mustFloat(); v < 0 || v > 1 {
			return "temperature: range: 0..1", false, true
		}
	}
	if s.badMsg >= 0 {
		return fmt.Sprintf(
			"messages.%d.content.%d.text: text content blocks must contain non-whitespace text", s.badMsg, s.badBlk), false, true
	}

	thinkingOn, ok := s.thinkingEnabled()
	if !ok {
		return "", false, false
	}
	if thinkingOn {
		// 走到这里 temperature 要么不存在，要么是 [0,1] 内的数字。
		if s.temperature.set && s.temperature.mustFloat() != 1 {
			return "temperature may only be set to 1 when thinking is enabled", false, true
		}
		if s.topP.set {
			v, exact := s.topP.float()
			if !exact {
				return "", false, false
			}
			if v != 1 {
				return "top_p may only be set to 1 when thinking is enabled", false, true
			}
		}
		if s.topK.set {
			v, exact := s.topK.float()
			if !exact {
				return "", false, false
			}
			if v != 0 {
				return "top_k may only be set to 0 when thinking is enabled", false, true
			}
		}
	}
	return "", true, true
}

// JSON 值的种类（opusScalar.kind / scanValue 的返回值）。
const (
	jsonString = '"'
	jsonObject = '{'
	jsonArray  = '['
	jsonNumber = '0'
	jsonTrue   = 't'
	jsonFalse  = 'f'
	jsonNull   = 'n'
)

// opusScalar 记录顶层字段首次出现的值（种类 + 原始字节，字符串含引号）。
type opusScalar struct {
	set  bool
	kind byte
	tok  []byte
}

// scan 登记 b[i:] 开头的值；字段重复出现一律放弃（done=false）。
func (f *opusScalar) scan(b []byte, i int) (int, bool) {
	if f.set {
		return 0, false
	}
	end, kind, ok := scanValue(b, i)
	if !ok {
		return 0, false
	}
	f.set, f.kind, f.tok = true, kind, b[i:end]
	return end, true
}

// mustFloat 只用于 kind == jsonNumber：与 gjson 一致，数值就是 ParseFloat(原始 token)，
// 越界得到 ±Inf，错误被忽略。
func (f *opusScalar) mustFloat() float64 {
	v, _ := strconv.ParseFloat(string(f.tok), 64)
	return v
}

// float 复刻 gjson.Result.Float()：数字取其值，true=1，字符串按 ParseFloat（取反转义后的值），
// 其余（false/null/对象/数组）为 0。字符串含反斜杠时需要 gjson 的反转义规则，返回 exact=false。
func (f *opusScalar) float() (v float64, exact bool) {
	switch f.kind {
	case jsonNumber:
		return f.mustFloat(), true
	case jsonTrue:
		return 1, true
	case jsonString:
		inner := f.tok[1 : len(f.tok)-1]
		if bytes.IndexByte(inner, '\\') >= 0 {
			return 0, false
		}
		v, _ = strconv.ParseFloat(string(inner), 64)
		return v, true
	}
	return 0, true
}

// opusScan 保存单遍扫描收集到的字段。
type opusScan struct {
	b []byte

	hasMessages, messagesArray bool
	msgCount                   int
	badMsg, badBlk             int // 首个空白 text 块的下标；badMsg < 0 表示没有

	maxTokens, temperature, thinking, topP, topK opusScalar
}

// visitTop 处理顶层成员。顶层字段只取首次出现的值，与 gjson.Get 一致。
func (s *opusScan) visitTop(key []byte, v int) (int, bool) {
	b := s.b
	switch string(key) {
	case "messages":
		if s.hasMessages {
			return 0, false
		}
		s.hasMessages = true
		if b[v] != jsonArray {
			end, _, ok := scanValue(b, v)
			return end, ok
		}
		s.messagesArray = true
		return scanItems(b, v, s.visitMessage)
	case "max_tokens":
		return s.maxTokens.scan(b, v)
	case "temperature":
		return s.temperature.scan(b, v)
	case "thinking":
		return s.thinking.scan(b, v)
	case "top_p":
		return s.topP.scan(b, v)
	case "top_k":
		return s.topK.scan(b, v)
	}
	end, _, ok := scanValue(b, v)
	return end, ok
}

// visitMessage 处理 messages 的第 idx 个元素：只取第一个 content 键（与 message.Get 一致），
// 它不是数组就整条跳过。
func (s *opusScan) visitMessage(idx, v int) (int, bool) {
	b := s.b
	if b[v] != '{' {
		return 0, false
	}
	seenContent := false
	end, ok := scanMembers(b, v, func(key []byte, mv int) (int, bool) {
		if !seenContent && string(key) == "content" {
			seenContent = true
			if b[mv] == '[' {
				return scanItems(b, mv, func(j, bv int) (int, bool) { return s.visitBlock(idx, j, bv) })
			}
		}
		end, _, ok := scanValue(b, mv)
		return end, ok
	})
	if ok {
		s.msgCount++
	}
	return end, ok
}

// visitBlock 处理第 msgIdx 条消息的第 j 个 content 块。type / text 都只取第一次出现的键。
func (s *opusScan) visitBlock(msgIdx, j, v int) (int, bool) {
	b := s.b
	if b[v] != '{' {
		return 0, false
	}
	var typeSeen, textSeen, isText, unsureType bool
	textBlank := true // text 缺失与 null 同属“空”
	end, ok := scanMembers(b, v, func(key []byte, mv int) (int, bool) {
		switch string(key) {
		case "type":
			if !typeSeen {
				typeSeen = true
				end, kind, ok := scanValue(b, mv)
				if ok && kind == jsonString {
					inner := b[mv+1 : end-1]
					if string(inner) == "text" {
						isText = true
					} else if bytes.IndexByte(inner, '\\') >= 0 {
						unsureType = true // 反转义后可能等于 text
					}
				}
				return end, ok
			}
		case "text":
			if !textSeen {
				textSeen = true
				end, kind, ok := scanValue(b, mv)
				if ok {
					switch kind {
					case jsonString:
						textBlank = opusTextBlank(b[mv+1 : end-1])
					case jsonNull:
						textBlank = true
					default:
						// 数字/布尔/对象/数组的字符串形式都非空
						textBlank = false
					}
				}
				return end, ok
			}
		}
		end, _, ok := scanValue(b, mv)
		return end, ok
	})
	if !ok {
		return 0, false
	}
	if s.badMsg < 0 {
		var blank bool
		if unsureType {
			blank = opusTextBlockBlank(b[v:end])
		} else {
			blank = isText && textBlank
		}
		if blank {
			s.badMsg, s.badBlk = msgIdx, j
		}
	}
	return end, true
}

// thinkingEnabled 复刻 strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()) 是否为
// enabled 或 adaptive。thinking 不是对象时 gjson 找不到 type（数组按下标匹配、其余标量没有子键）；
// 非字符串的 type 值其字符串形式（数字/true/false/null/原始对象）不可能是 enabled/adaptive。
// exact=false 表示 thinking 写法不规范或 type 值含反斜杠，需要退回 gjson。
func (s *opusScan) thinkingEnabled() (enabled, exact bool) {
	if !s.thinking.set || s.thinking.kind != jsonObject {
		return false, true
	}
	tk := s.thinking.tok
	found := false
	_, ok := scanMembers(tk, 0, func(key []byte, v int) (int, bool) {
		end, kind, ok := scanValue(tk, v)
		if ok && !found && string(key) == "type" {
			found = true
			if kind == jsonString {
				inner := tk[v+1 : end-1]
				if bytes.IndexByte(inner, '\\') >= 0 {
					return 0, false
				}
				// bytes.TrimSpace 与 strings.TrimSpace 同为 unicode.IsSpace 口径，这里避免把值拷成字符串。
				t := bytes.TrimSpace(inner)
				enabled = string(t) == "enabled" || string(t) == "adaptive"
			}
		}
		return end, ok
	})
	return enabled, ok
}

// opusTextBlank 判断 JSON 字符串内容（引号之间的原始字节）经 gjson 取值 + strings.TrimSpace 后是否为空，
// 即 strings.TrimSpace(gjson.Result{字符串}.String()) == ""。gjson 取字符串值的规则：
//   - 不含反斜杠：值就是原文；
//   - 含反斜杠（哪怕在很后面）：整段交给 unescape，它会在原始控制字符（< 0x20）处截断。
func opusTextBlank(s []byte) bool {
	// 绝大多数 text 以普通字符开头：不论后面有没有反斜杠，值的第一个字节都与原文相同，且不是空白。
	if len(s) > 0 {
		if c := s[0]; c > ' ' && c < utf8.RuneSelf && c != '\\' {
			return false
		}
	}
	if bytes.IndexByte(s, '\\') < 0 {
		return runesAllSpace(s)
	}
	return unescapedAllSpace(s)
}

// runesAllSpace：s 里每个字符都满足 unicode.IsSpace（strings.TrimSpace 的口径；非法 UTF-8 字节按 U+FFFD 处理，不是空白）。
func runesAllSpace(s []byte) bool {
	for p := 0; p < len(s); {
		r, n := utf8.DecodeRune(s[p:])
		if !unicode.IsSpace(r) {
			return false
		}
		p += n
	}
	return true
}

// unescapedAllSpace 逐字复刻 gjson.unescape 的输出，判断它是不是全是空白：
// 原始控制字符、未知转义、被截断的 \u 都会让 gjson 在该处停止并返回此前的输出（此前都是空白）；
// \u 后四位按 strconv.ParseUint(..., 16, 64) 取值（非法得 0）。代理项（\ud800–\udfff）无论是否与
// 后面的 \uXXXX 配对，输出（配对后的字符或 U+FFFD）都不是空白，所以不需要复刻配对逻辑。
func unescapedAllSpace(s []byte) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c < ' ':
			return true
		case c == '\\':
			i++
			if i >= len(s) {
				return true
			}
			switch s[i] {
			case 'f', 'n', 'r', 't':
				// 换页、换行、回车、制表：空白，继续
			case '\\', '/', '"', 'b':
				return false // 输出 \ / " 或退格符（U+0008 不是空白）
			case 'u':
				if i+5 > len(s) {
					return true
				}
				if !unicode.IsSpace(hexRune4(s[i+1:])) {
					return false
				}
				i += 4 // 跳过四位十六进制（循环的 i++ 再越过最后一位）
			default:
				return true
			}
		default:
			r, n := utf8.DecodeRune(s[i:])
			if !unicode.IsSpace(r) {
				return false
			}
			i += n - 1
		}
	}
	return true
}

// hexRune4 复刻 gjson.runeit：把 s[:4] 当十六进制解析，非法得 0。
func hexRune4(s []byte) rune {
	var n rune
	for _, c := range s[:4] {
		switch {
		case c >= '0' && c <= '9':
			n = n<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			n = n<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			n = n<<4 | rune(c-'A'+10)
		default:
			return 0
		}
	}
	return n
}

// opusTextBlockBlank 对单个 content 块逐字复刻 gjson 实现里的判断：
// type 是 text 且 text 缺失/非字符串/全空白。
func opusTextBlockBlank(block []byte) bool {
	if gjson.GetBytes(block, "type").String() != "text" {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(block, "text").String()) == ""
}

// ---- 最小 JSON 扫描原语 ----

// opusSquashClass 与 gjson.parseSquash 的 vchars 表一致：引号 2、开括号 3、闭括号 1，
// 圆括号也计深度——合法 JSON 里它只会出现在字符串中，写成一样是为了畸形输入下边界仍一致。
var opusSquashClass = [256]uint8{'"': 2, '{': 3, '(': 3, '[': 3, '}': 1, ')': 1, ']': 1}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' }

func skipJSONSpace(b []byte, i int) int {
	for i < len(b) && isJSONSpace(b[i]) {
		i++
	}
	return i
}

func skipDigits(b []byte, i int) int {
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	return i
}

// scanString：b[i] 是开引号，返回收尾引号的下标。收尾规则与 gjson 相同：
// 引号前有奇数个连续反斜杠才算转义，不校验字符串内容。大段文本靠 IndexByte 跳过。
func scanString(b []byte, i int) (int, bool) {
	p := i + 1
	for p < len(b) {
		j := bytes.IndexByte(b[p:], '"')
		if j < 0 {
			return 0, false
		}
		q := p + j
		n := 0
		for k := q - 1; k > i && b[k] == '\\'; k-- {
			n++
		}
		if n%2 == 0 {
			return q, true
		}
		p = q + 1
	}
	return 0, false
}

// squashEnd：b[i] 是 { 或 [，返回匹配收尾之后的下标，规则同 gjson.parseSquash。
func squashEnd(b []byte, i int) (int, bool) {
	depth := 1
	i++
	for i < len(b) {
		switch opusSquashClass[b[i]] {
		case 0:
			i++
		case 2:
			q, ok := scanString(b, i)
			if !ok {
				return 0, false
			}
			i = q + 1
		case 3:
			depth++
			i++
		default:
			depth--
			i++
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// scanNumber 只接受标准 JSON 数字：-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func scanNumber(b []byte, i int) (int, bool) {
	if b[i] == '-' {
		i++
	}
	if i >= len(b) {
		return 0, false
	}
	switch {
	case b[i] == '0':
		i++
	case b[i] >= '1' && b[i] <= '9':
		i = skipDigits(b, i)
	default:
		return 0, false
	}
	if i < len(b) && b[i] == '.' {
		j := skipDigits(b, i+1)
		if j == i+1 {
			return 0, false
		}
		i = j
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		j := i + 1
		if j < len(b) && (b[j] == '+' || b[j] == '-') {
			j++
		}
		k := skipDigits(b, j)
		if k == j {
			return 0, false
		}
		i = k
	}
	return i, true
}

func hasLiteralAt(b []byte, i int, lit string) bool {
	return len(b)-i >= len(lit) && string(b[i:i+len(lit)]) == lit
}

// scanValue 跳过 b[i] 开头的一个值，返回结束下标与种类。对象/数组只做 squash 跳过，
// 数字与 true/false/null 只接受标准写法；调用方负责检查值后面紧跟的是分隔符。
func scanValue(b []byte, i int) (end int, kind byte, ok bool) {
	switch c := b[i]; {
	case c == '"':
		q, ok := scanString(b, i)
		return q + 1, jsonString, ok
	case c == '{' || c == '[':
		end, ok := squashEnd(b, i)
		return end, c, ok
	case c == '-' || (c >= '0' && c <= '9'):
		end, ok := scanNumber(b, i)
		return end, jsonNumber, ok
	case c == 't' && hasLiteralAt(b, i, "true"):
		return i + 4, jsonTrue, true
	case c == 'f' && hasLiteralAt(b, i, "false"):
		return i + 5, jsonFalse, true
	case c == 'n' && hasLiteralAt(b, i, "null"):
		return i + 4, jsonNull, true
	}
	return 0, 0, false
}

// scanKeyColon 读 "key" ws ':' ws，返回键（不含引号）与值的起点。键里有反斜杠一律放弃。
func scanKeyColon(b []byte, i int) (key []byte, v int, ok bool) {
	if i >= len(b) || b[i] != '"' {
		return nil, 0, false
	}
	end := i + 1
	for end < len(b) && b[end] != '"' {
		if b[end] == '\\' {
			return nil, 0, false
		}
		end++
	}
	if end >= len(b) {
		return nil, 0, false
	}
	v = skipJSONSpace(b, end+1)
	if v >= len(b) || b[v] != ':' {
		return nil, 0, false
	}
	v = skipJSONSpace(b, v+1)
	if v >= len(b) {
		return nil, 0, false
	}
	return b[i+1 : end], v, true
}

// scanMembers 严格遍历 b[i] 开头的对象，对每个成员调用 visit（返回值的结束下标）。
// 结构不是标准 JSON（缺/多逗号、缺冒号、非字符串键、未闭合）一律失败。
func scanMembers(b []byte, i int, visit func(key []byte, v int) (int, bool)) (int, bool) {
	if i >= len(b) || b[i] != '{' {
		return 0, false
	}
	i = skipJSONSpace(b, i+1)
	if i < len(b) && b[i] == '}' {
		return i + 1, true
	}
	for {
		key, v, ok := scanKeyColon(b, i)
		if !ok {
			return 0, false
		}
		end, ok := visit(key, v)
		if !ok {
			return 0, false
		}
		i = skipJSONSpace(b, end)
		if i >= len(b) {
			return 0, false
		}
		switch b[i] {
		case '}':
			return i + 1, true
		case ',':
			i = skipJSONSpace(b, i+1)
		default:
			return 0, false
		}
	}
}

// scanItems 严格遍历 b[i] 开头的数组，对每个元素调用 visit（下标、起点，返回结束下标）。
func scanItems(b []byte, i int, visit func(idx, v int) (int, bool)) (int, bool) {
	if i >= len(b) || b[i] != '[' {
		return 0, false
	}
	i = skipJSONSpace(b, i+1)
	if i < len(b) && b[i] == ']' {
		return i + 1, true
	}
	for idx := 0; ; idx++ {
		if i >= len(b) {
			return 0, false
		}
		end, ok := visit(idx, i)
		if !ok {
			return 0, false
		}
		i = skipJSONSpace(b, end)
		if i >= len(b) {
			return 0, false
		}
		switch b[i] {
		case ']':
			return i + 1, true
		case ',':
			i = skipJSONSpace(b, i+1)
		default:
			return 0, false
		}
	}
}
