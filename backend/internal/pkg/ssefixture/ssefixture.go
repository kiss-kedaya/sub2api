// Package ssefixture builds deterministic SSE streams for tests and
// benchmarks of the response-side observers. Everything is synthetic: names,
// ids and text are generated, and the package is never imported by production
// code.
package ssefixture

import (
	"fmt"
	"math/rand"
	"strings"
)

var words = strings.Fields("the quick brown fox jumps over a lazy dog while streaming tokens through the gateway and counting usage for every request that arrives func main return value error handling retry window cache hit miss 你好 世界 数据 流式 输出")

// Text returns n pseudo-random words; some contain characters JSON escapes.
func Text(r *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[r.Intn(len(words))])
		switch r.Intn(40) {
		case 0:
			b.WriteString(`\n`)
		case 1:
			b.WriteString(`\"`)
		}
	}
	return b.String()
}

func frame(event, data string) []byte {
	if event == "" {
		return []byte("data: " + data + "\n\n")
	}
	return []byte("event: " + event + "\ndata: " + data + "\n\n")
}

// ResponsesStream returns an OpenAI Responses stream of n events, one frame
// each: created, deltas, then the done events and response.completed.
func ResponsesStream(n int) [][]byte { return ResponsesStreamWithEcho(n, 4, 300) }

// ResponsesStreamWithEcho is ResponsesStream where the response object carried
// by created/in_progress/completed echoes the request: tools function tools
// and instructionWords words of instructions, as coding agents send.
func ResponsesStreamWithEcho(n, tools, instructionWords int) [][]byte {
	r := rand.New(rand.NewSource(1))
	var frames [][]byte
	seq := 0
	add := func(event, data string) {
		frames = append(frames, frame(event, strings.Replace(data, "SEQ", fmt.Sprint(seq), 1)))
		seq++
	}
	var toolList []string
	for i := 0; i < tools; i++ {
		toolList = append(toolList, fmt.Sprintf(`{"type":"function","name":"tool_%d","description":"%s","strict":false,"parameters":{"type":"object","properties":{"path":{"type":"string","description":"%s"},"content":{"type":"string","description":"%s"},"recursive":{"type":"boolean"},"mode":{"type":"string","enum":["read","write","append"]},"limit":{"type":"integer","minimum":1,"maximum":1000}},"required":["path"],"additionalProperties":false}}`, i, Text(r, 12), Text(r, 8), Text(r, 8)))
	}
	instructions := Text(r, instructionWords)
	response := func(status, output, usage string) string {
		return `{"id":"resp_000001","object":"response","created_at":1700000000,"status":"` + status + `","background":false,"error":null,"incomplete_details":null,"instructions":"` + instructions + `","max_output_tokens":null,"model":"model-a","output":` + output + `,"parallel_tool_calls":true,"previous_response_id":null,"reasoning":{"effort":null,"summary":null},"service_tier":"default","store":true,"temperature":1.0,"text":{"format":{"type":"text"}},"tool_choice":"auto","tools":[` + strings.Join(toolList, ",") + `],"top_p":1.0,"truncation":"disabled","usage":` + usage + `,"user":null,"metadata":{}}`
	}
	add("response.created", `{"type":"response.created","sequence_number":SEQ,"response":`+response("in_progress", "[]", "null")+`}`)
	add("response.in_progress", `{"type":"response.in_progress","sequence_number":SEQ,"response":`+response("in_progress", "[]", "null")+`}`)
	add("response.output_item.added", `{"type":"response.output_item.added","sequence_number":SEQ,"output_index":0,"item":{"id":"msg_000001","type":"message","status":"in_progress","content":[],"role":"assistant"}}`)
	add("response.content_part.added", `{"type":"response.content_part.added","sequence_number":SEQ,"item_id":"msg_000001","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":""}}`)
	var text strings.Builder
	for len(frames) < n-4 {
		delta := Text(r, 1) + " "
		text.WriteString(delta)
		add("response.output_text.delta", `{"type":"response.output_text.delta","sequence_number":SEQ,"item_id":"msg_000001","output_index":0,"content_index":0,"delta":"`+delta+`","logprobs":[],"obfuscation":"abcdefghij"}`)
	}
	full := text.String()
	add("response.output_text.done", `{"type":"response.output_text.done","sequence_number":SEQ,"item_id":"msg_000001","output_index":0,"content_index":0,"text":"`+full+`","logprobs":[]}`)
	add("response.content_part.done", `{"type":"response.content_part.done","sequence_number":SEQ,"item_id":"msg_000001","output_index":0,"content_index":0,"part":{"type":"output_text","annotations":[],"logprobs":[],"text":"`+full+`"}}`)
	item := `{"id":"msg_000001","type":"message","status":"completed","content":[{"type":"output_text","annotations":[],"logprobs":[],"text":"` + full + `"}],"role":"assistant"}`
	add("response.output_item.done", `{"type":"response.output_item.done","sequence_number":SEQ,"output_index":0,"item":`+item+`}`)
	add("response.completed", `{"type":"response.completed","sequence_number":SEQ,"response":`+response("completed", "["+item+"]", `{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":300,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":420}`)+`}`)
	return frames
}

// ChatStream returns a Chat Completions stream of n frames ending with a
// finish chunk, a usage-only chunk and [DONE].
func ChatStream(n int) [][]byte {
	r := rand.New(rand.NewSource(2))
	var frames [][]byte
	chunk := func(delta, finish, usage string) string {
		choices := `[{"index":0,"delta":` + delta + `,"logprobs":null,"finish_reason":` + finish + `}]`
		if usage != "" {
			choices = `[]`
		}
		s := `{"id":"chatcmpl-000001","object":"chat.completion.chunk","created":1700000000,"model":"model-a","system_fingerprint":"fp_000001","choices":` + choices
		if usage != "" {
			s += `,"usage":` + usage
		}
		return s + `}`
	}
	frames = append(frames, frame("", chunk(`{"role":"assistant","content":""}`, "null", "")))
	for len(frames) < n-3 {
		frames = append(frames, frame("", chunk(`{"content":"`+Text(r, 1)+` "}`, "null", "")))
	}
	frames = append(frames, frame("", chunk(`{}`, `"stop"`, "")))
	frames = append(frames, frame("", chunk("", "", `{"prompt_tokens":120,"completion_tokens":300,"total_tokens":420,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}`)))
	frames = append(frames, frame("", "[DONE]"))
	return frames
}

// AnthropicStream returns an Anthropic Messages stream of n frames.
func AnthropicStream(n int) [][]byte {
	r := rand.New(rand.NewSource(3))
	var frames [][]byte
	frames = append(frames, frame("message_start", `{"type":"message_start","message":{"id":"msg_000001","type":"message","role":"assistant","model":"model-a","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":120,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}`))
	frames = append(frames, frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`))
	frames = append(frames, frame("ping", `{"type":"ping"}`))
	for len(frames) < n-3 {
		frames = append(frames, frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+Text(r, 1)+` "}}`))
	}
	frames = append(frames, frame("content_block_stop", `{"type":"content_block_stop","index":0}`))
	frames = append(frames, frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":300}}`))
	frames = append(frames, frame("message_stop", `{"type":"message_stop"}`))
	return frames
}

// Join concatenates frames into one byte stream.
func Join(frames [][]byte) []byte {
	var out []byte
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

// Lines splits frames into SSE lines without their trailing newline; blank
// separator lines are kept as empty strings.
func Lines(frames [][]byte) []string {
	var lines []string
	for _, f := range frames {
		lines = append(lines, strings.Split(strings.TrimSuffix(string(f), "\n"), "\n")...)
	}
	return lines
}

// ---------------------------------------------------------------------------
// Random documents for differential tests.

// Gen produces random protocol-shaped documents; the same seed always yields
// the same sequence.
type Gen struct{ r *rand.Rand }

func NewGen(seed int64) *Gen { return &Gen{rand.New(rand.NewSource(seed))} }

func (g *Gen) Intn(n int) int              { return g.r.Intn(n) }
func (g *Gen) Chance(p float64) bool       { return g.r.Float64() < p }
func (g *Gen) Pick(items ...string) string { return items[g.r.Intn(len(items))] }

var interestingKeys = []string{"type", "delta", "status", "error", "response", "output", "usage", "item", "message", "id", "model", "content", "text", "choices", "index", "sequence_number", "output_index", "Type", "DELTA", "code", "param", "candidates", "finishReason", "finish_reason", "Error", "RESPONSE", "service_tier", "modelVersion"}

// InterestingTypes are event names the observers react to plus near misses.
var InterestingTypes = []string{
	"response.created", "response.in_progress", "response.output_text.delta", "response.output_item.added", "response.output_item.done",
	"response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled",
	"error", "message_start", "message_delta", "message_stop", "content_block_delta", "ping", "response.", "response", "Response.completed",
	"response.completed ", "", "x", "message_stop2", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta",
}

func (g *Gen) ws() string {
	if !g.Chance(0.15) {
		return ""
	}
	return g.Pick(" ", "  ", "\t", " \t ", "\r")
}

// Escape sometimes spells a name with \u escapes.
func (g *Gen) Escape(s string) string {
	if !g.Chance(0.15) || s == "" {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if g.Chance(0.5) && s[i] < 0x80 {
			fmt.Fprintf(&b, `\u%04x`, s[i])
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// StringBody returns JSON string content (without quotes), occasionally long
// or carrying characters that are invalid inside a JSON string.
func (g *Gen) StringBody() string {
	var b strings.Builder
	n := g.r.Intn(12)
	if g.Chance(0.05) {
		n = 200 + g.r.Intn(400)
	}
	for i := 0; i < n; i++ {
		switch g.r.Intn(16) {
		case 0:
			b.WriteString(`\"`)
		case 1:
			b.WriteString(`\\`)
		case 2:
			b.WriteString(`\n`)
		case 3:
			b.WriteString(`é`)
		case 4:
			b.WriteString(`😀`)
		case 5:
			b.WriteString("你好")
		case 6:
			b.WriteString("\x5cu00e9")
		case 7:
			b.WriteString(`\/`)
		case 8:
			if g.Chance(0.05) {
				b.WriteString(g.Pick("\t", "\x01", "\x7f", `\x`, `\u12`, `\uzzzz`, "\xff", "\xc3"))
			}
		case 9:
			b.WriteString("data: ")
		case 10:
			b.WriteString(g.Pick("response.completed", `\"type\":\"error\"`, "error", "finish_reason", `\"stop\"`, "message_stop", "STOP", "model", "ſtatus", "K"))
		case 11:
			b.WriteString(`\u` + g.Pick("0065", "0072", "0074", "006d", "0073"))
		default:
			b.WriteByte(byte('a' + g.r.Intn(26)))
		}
	}
	return b.String()
}

func (g *Gen) number() string {
	return g.Pick("0", "1", "42", "-1", "-0", "1.5", "1e5", "1E+5", "-2.5e-3", "01", "1.", ".5", "-", "1e", "+1", "12345678901234567890", strings.Repeat("9", 130), "0.0", "10")
}

func (g *Gen) literal() string {
	return g.Pick("true", "false", "null", "tru", "nul", "True", "nulll", "truefalse")
}

func (g *Gen) value(depth int) string {
	switch k := g.r.Intn(10); {
	case k < 3:
		return `"` + g.StringBody() + `"`
	case k < 5:
		return g.number()
	case k < 6:
		return g.literal()
	case k < 8 && depth < 3:
		return g.Object(depth+1, "")
	case k < 9 && depth < 3:
		var parts []string
		for i := g.r.Intn(4); i > 0; i-- {
			parts = append(parts, g.ws()+g.value(depth+1)+g.ws())
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return `"` + g.StringBody() + `"`
	}
}

// Object returns a random JSON object, led by a type member when eventType is set.
func (g *Gen) Object(depth int, eventType string) string {
	var parts []string
	add := func(key, value string) {
		parts = append(parts, g.ws()+`"`+g.Escape(key)+`"`+g.ws()+":"+g.ws()+value+g.ws())
	}
	if eventType != "" {
		add("type", `"`+g.Escape(eventType)+`"`)
	}
	for i := g.r.Intn(7); i > 0; i-- {
		key := g.Pick(interestingKeys...)
		if g.Chance(0.1) {
			key = g.StringBody()
		}
		var value string
		switch key {
		case "type", "Type":
			value = `"` + g.Escape(g.Pick(InterestingTypes...)) + `"`
		case "status":
			value = g.Pick(`"completed"`, `"failed"`, `"incomplete"`, `"cancelled"`, `"canceled"`, `"in_progress"`, `"completed"`, `"completed "`, `5`, `null`)
		case "error", "Error":
			value = g.Pick(`null`, `true`, `false`, `"x"`, `""`, `{"message":"m","code":"c"}`, `{}`, `[]`, `1`, `"`+g.StringBody()+`"`, `{"error":{"a":1}}`, `"`+strings.Repeat("e", 300)+`"`)
		case "delta", "DELTA":
			value = g.Pick(`"hello"`, `""`, `" "`, `"\n"`, `"`+g.StringBody()+`"`, `null`, `5`, `{"a":"b"}`, `[]`)
		case "model", "modelVersion", "service_tier":
			value = g.Pick(`"model-a"`, `"model-b"`, `""`, `" model-a "`, `null`, `5`, `"priority"`, `"default"`, `"auto"`)
		case "finish_reason", "finishReason":
			value = g.Pick(`null`, `"stop"`, `"length"`, `"tool_calls"`, `"function_call"`, `"content_filter"`, `"error"`, `"STOP"`, `"MAX_TOKENS"`, `"SAFETY"`, `""`, `5`)
		case "response", "RESPONSE":
			if depth < 3 && g.Chance(0.8) {
				value = g.Object(depth+1, "")
			} else {
				value = g.value(depth + 1)
			}
		default:
			value = g.value(depth)
		}
		add(key, value)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// Event returns one JSON document: usually a realistic protocol shape, else a
// random object.
func (g *Gen) Event() string {
	switch g.r.Intn(20) {
	case 0:
		return `{"type":"response.output_text.delta","sequence_number":4,"item_id":"msg_1","output_index":0,"content_index":0,"delta":` + g.Pick(`"Hello"`, `""`, `"\n"`, `"你好"`) + `,"logprobs":[],"obfuscation":"abc"}`
	case 1:
		return `{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"model-a","output":[{"type":"message","content":[{"type":"output_text","text":"` + g.StringBody() + `"}]}],"usage":{"input_tokens":1,"output_tokens":2}},"sequence_number":9}`
	case 2:
		return `{"type":"response.failed","response":{"status":"failed","error":{"code":"x","message":"` + g.StringBody() + `"}}}`
	case 3:
		return `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"model-a","choices":[{"index":0,"delta":{"content":"` + g.StringBody() + `"},"finish_reason":null}]}`
	case 4:
		return `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"model-a","content":[],"usage":{"input_tokens":3}}}`
	case 5:
		return `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + g.StringBody() + `"}}`
	case 6:
		return `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`
	case 7:
		return `{"type":"message_stop"}`
	case 8:
		return `{"type":"error","error":{"type":"overloaded_error","message":"` + g.StringBody() + `"}}`
	case 9:
		return `{"candidates":[{"content":{"parts":[{"text":"` + g.StringBody() + `"}]},"finishReason":"` + g.Pick("STOP", "MAX_TOKENS", "SAFETY") + `"}],"modelVersion":"model-a"}`
	case 10:
		return `{"type":"response.output_item.added","item":{"type":"response.completed","status":"completed"}}`
	case 11:
		return `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"model-a","choices":[{"index":0,"delta":{},"finish_reason":"` + g.Pick("stop", "length", "tool_calls", "function_call", "content_filter") + `"}]}`
	case 12:
		return `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"model-a","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	case 13:
		return `{"error":` + g.Pick(`{"message":"boom"}`, `null`, `"x"`, `{}`) + `}`
	default:
		return g.Object(0, g.Pick(append([]string{""}, InterestingTypes...)...))
	}
}

// Corrupt applies a few random byte-level edits.
func (g *Gen) Corrupt(s string) string {
	b := []byte(s)
	for i := 1 + g.r.Intn(3); i > 0 && len(b) > 0; i-- {
		pos := g.r.Intn(len(b))
		bad := []byte{'"', '\\', '{', '}', '[', ']', ',', ':', '\n', '\r', ' ', 0, 1, 31, 0x80, 0xff, 'u', 't', 'e'}
		switch g.r.Intn(4) {
		case 0:
			b = append(b[:pos], b[pos+1:]...)
		case 1:
			b = append(b[:pos], append([]byte{bad[g.r.Intn(len(bad))]}, b[pos:]...)...)
		case 2:
			b[pos] = bad[g.r.Intn(len(bad))]
		default:
			b = b[:pos]
		}
	}
	return string(b)
}

// ChatChunk returns one Chat Completions stream chunk in a random shape: text,
// refusal, reasoning, tool and function call deltas (some with empty
// identity), finish reasons, usage-only chunks and provider errors.
func (g *Gen) ChatChunk() string {
	var members []string
	add := func(m string) { members = append(members, m) }
	if g.Chance(0.9) {
		add(`"id":"chatcmpl-` + g.Pick("1", "2", "") + `"`)
	}
	if g.Chance(0.8) {
		add(`"object":"chat.completion.chunk"`)
	}
	if g.Chance(0.85) {
		add(`"model":` + g.Pick(`"model-a"`, `"model-b"`, `""`, `" model-a "`, `null`))
	}
	if g.Chance(0.3) {
		add(`"service_tier":` + g.Pick(`"default"`, `"priority"`, `"auto"`, `"flex"`, `"x"`))
	}
	if g.Chance(0.2) {
		add(`"system_fingerprint":"fp_1"`)
	}
	if g.Chance(0.1) {
		add(`"response":{"model":"model-b","service_tier":"priority"}`)
	}
	finish := g.Pick(`null`, `null`, `null`, `"stop"`, `"length"`, `"tool_calls"`, `"function_call"`, `"content_filter"`, `"error"`, `""`, `" "`)
	delta := func() string {
		var d []string
		if g.Chance(0.2) {
			d = append(d, `"role":"assistant"`)
		}
		if g.Chance(0.6) {
			d = append(d, `"content":"`+g.StringBody()+`"`)
		}
		if g.Chance(0.1) {
			d = append(d, `"refusal":"`+g.StringBody()+`"`)
		}
		if g.Chance(0.1) {
			d = append(d, g.Pick(`"reasoning_content":"think"`, `"reasoning":"think"`, `"reasoning_summary":"s"`, `"reasoning_content":null`))
		}
		if g.Chance(0.15) {
			id := g.Pick(`"id":"call_1",`, `"id":"",`, ``)
			name := g.Pick(`"name":"fn"`, `"name":""`, `"name":null`)
			d = append(d, `"tool_calls":[{"index":0,`+id+`"type":"function","function":{`+name+`,"arguments":"`+g.Pick("", `{\"a\":1}`, "x")+`"}}]`)
		}
		if g.Chance(0.05) {
			d = append(d, `"function_call":{"name":"`+g.Pick("fn", "")+`","arguments":"{}"}`)
		}
		return "{" + strings.Join(d, ",") + "}"
	}
	if g.Chance(0.9) {
		var choices []string
		for i := g.Intn(3); i >= 0; i-- {
			choices = append(choices, `{"index":`+fmt.Sprint(i)+`,"delta":`+delta()+`,"logprobs":null,"finish_reason":`+finish+`}`)
		}
		if g.Chance(0.05) {
			choices = nil
		}
		add(`"choices":[` + strings.Join(choices, ",") + `]`)
	}
	if g.Chance(0.15) {
		add(`"usage":` + g.Pick(`{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}`, `{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":1}}`, `null`, `{}`, `5`, `"x"`))
	}
	if g.Chance(0.05) {
		add(`"error":` + g.Pick(`{"message":"boom","code":"x"}`, `null`, `"x"`, `""`, `{}`, `{"code":"cyber_policy"}`))
	}
	if g.Chance(0.03) {
		add(`"type":` + g.Pick(`"error"`, `"response.failed"`, `"response.completed"`, `"x"`))
	}
	if g.Chance(0.03) {
		add(`"status":` + g.Pick(`"failed"`, `"completed"`, `5`))
	}
	g.r.Shuffle(len(members), func(i, j int) { members[i], members[j] = members[j], members[i] })
	return "{" + strings.Join(members, ",") + "}"
}

// SSEStream returns a short stream of random events with comments, event and
// id lines, CRLF endings, [DONE] variants and multi-line data fields.
func (g *Gen) SSEStream() string {
	var out strings.Builder
	eol := "\n"
	if g.Chance(0.2) {
		eol = "\r\n"
	}
	if g.Chance(0.05) {
		out.WriteString("\xef\xbb\xbf")
	}
	for events := 1 + g.r.Intn(6); events > 0; events-- {
		if g.Chance(0.2) {
			out.WriteString(": heartbeat " + g.StringBody() + eol)
		}
		if g.Chance(0.4) {
			out.WriteString("event: " + g.Pick(InterestingTypes...) + eol)
		}
		if g.Chance(0.1) {
			out.WriteString(g.Pick("id: 7", "retry: 100", "DATA: {}", "data : {}", "  data: {}", "d", "da", "dat", "data") + eol)
		}
		var data string
		switch {
		case g.Chance(0.08):
			data = g.Pick("[DONE]", " [DONE] ", "[DONE]x", "[DON", "[ DONE]", "[DONE", "[done]", "[DONE]\t")
		default:
			data = g.Event()
			if g.Chance(0.15) {
				data = g.Corrupt(data)
			}
		}
		sep := g.Pick(": ", ":", ":  ", ":\t")
		if g.Chance(0.12) && strings.Contains(data, ",") {
			for _, p := range strings.SplitAfter(data, ",") {
				out.WriteString("data" + sep + p + eol)
			}
		} else {
			out.WriteString("data" + sep + data + eol)
			if g.Chance(0.05) {
				out.WriteString("data" + sep + g.Pick("{}", "1", " ", "x") + eol)
			}
		}
		if !g.Chance(0.05) {
			out.WriteString(eol)
		}
		if g.Chance(0.05) {
			out.WriteString(eol)
		}
	}
	return out.String()
}

// JSONBody returns a plain (non-SSE) response body, sometimes damaged.
func (g *Gen) JSONBody() string {
	s := g.Event()
	if g.Chance(0.2) {
		s = g.Corrupt(s)
	}
	if g.Chance(0.15) {
		s = " \n" + s + "\n "
	}
	if g.Chance(0.05) {
		s += " trailing"
	}
	return s
}

// Cuts returns increasing split points for feeding an n-byte input in pieces.
func (g *Gen) Cuts(n int) []int {
	switch g.r.Intn(5) {
	case 0:
		return nil
	case 1:
		var cuts []int
		for i := 1; i < n; i++ {
			cuts = append(cuts, i)
		}
		return cuts
	default:
		var cuts []int
		size := 1 + g.r.Intn(40)
		for at := size; at < n; at += 1 + g.r.Intn(size) {
			cuts = append(cuts, at)
		}
		return cuts
	}
}
