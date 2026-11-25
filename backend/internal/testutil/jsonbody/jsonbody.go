// Package jsonbody builds random gateway-request-shaped JSON bodies for
// differential tests: duplicate and escaped keys, nested same-name keys, mixed
// value types, pretty printing, and bodies damaged in the ways gjson tolerates
// (BOM, trailing garbage, stray parens, bad numbers) or that are not objects at all.
//
// It has no dependency on the packages under test, so both service and handler
// tests can use it.
package jsonbody

import (
	"fmt"
	"math/rand"
	"strings"
)

// Gen is a seeded generator; the same seed yields the same bodies.
type Gen struct{ r *rand.Rand }

// New returns a generator for seed.
func New(seed int64) *Gen { return &Gen{r: rand.New(rand.NewSource(seed))} }

// Rand exposes the underlying source for callers that need their own choices.
func (g *Gen) Rand() *rand.Rand { return g.r }

func (g *Gen) chance(p int) bool { return g.r.Intn(100) < p }

func (g *Gen) pick(opts ...string) string { return opts[g.r.Intn(len(opts))] }

// TopKeys are the top-level keys bodies are built from: the ones the gateway
// reads, and decoys (case variants, near misses, keys path syntax treats specially).
var TopKeys = []string{
	"model", "stream", "service_tier", "max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig",
	"generation_config", "reasoning", "reasoning_effort", "output_config", "thinking", "tools", "input", "messages",
	"tool_choice", "metadata", "prompt_cache_key", "previous_response_id", "client_metadata", "store", "instructions",
	"include", "parallel_tool_calls", "temperature", "type", "response", "speed", "user", "text", "system", "contents",
	"Model", "MODEL", "stream_", "_stream", "max", "tokens", "a", "b", "", "é", "key with space", "a.b", "a|b", "*", "?", "#", "@this", "0",
	"model ", " model", "Reasoning", "reasoning.effort", "functions",
}

var nestedKeys = []string{
	"effort", "summary", "maxOutputTokens", "max_output_tokens", "type", "ws_request_header_x_openai_internal_codex_responses_lite",
	"user_id", "content", "role", "name", "model", "tools", "input", "id", "stream", "Effort", "effort ", "0", "a.b",
	"call_id", "output", "text", "image_url", "function", "arguments", "namespace",
}

var strs = []string{
	`""`, `"x"`, `"high"`, `"none"`, `"priority"`, `"fast"`, `" padded "`, `"true"`, `"123"`, `"a\"b"`, `"back\\slash"`,
	`"line1\nline2"`, `"é世界😀"`, `"{\"max_tokens\":5}"`, `"[1,2,3]"`, `"}"`, `"{"`, `"]"`, `"["`, `"("`, `")"`,
	`"\\"`, `"\\\\"`, `"ends with backslash \\"`, `"quote at end \""`, `"😀"`, `"\ud800"`, `"\/"`, `"\u0000"`,
	`"input_image"`, `"namespace"`, `"anthropic-metadata-x"`, `"tab\there"`, "\"raw\ttab\"", "\"raw\nnewline\"", "\"\xff\xfe\"",
	`"image_generation"`, `"function_call_output"`, `"function"`, `"web_search_preview"`, `"data:image/png;base64,"`,
	`"reasoning"`, `"additional_tools"`, `"gpt-5.5"`, `"deepseek-chat"`,
}

var nums = []string{
	`0`, `-0`, `1`, `-1`, `5`, `128000`, `4096`, `1.5`, `1e3`, `1E+2`, `-1.5e-3`, `12345678901234567890`, `1e400`, `-1e400`,
	`0.1`, `2147483648`, `9007199254740993`, `0e0`, `1.0`,
}

func (g *Gen) str() string { return strs[g.r.Intn(len(strs))] }
func (g *Gen) num() string { return nums[g.r.Intn(len(nums))] }
func (g *Gen) lit() string { return g.pick("true", "false", "null") }

func (g *Gen) scalar() string {
	switch g.r.Intn(8) {
	case 0, 1, 2, 3:
		return g.str()
	case 4, 5:
		return g.num()
	default:
		return g.lit()
	}
}

func (g *Gen) key(pool []string) string {
	k := pool[g.r.Intn(len(pool))]
	if g.chance(2) && k != "" { // spell it with an escape: gjson matches after unescaping
		switch g.r.Intn(3) {
		case 0:
			return `"` + `\u00` + fmt.Sprintf("%02x", k[0]) + k[1:] + `"`
		case 1:
			return `"` + strings.ReplaceAll(k, "e", `\u0065`) + `"`
		default:
			return `"` + k[:len(k)/2] + `\/` + k[len(k)/2:] + `"`
		}
	}
	return `"` + k + `"`
}

func (g *Gen) value(depth int) string {
	switch k := g.r.Intn(12); {
	case k < 6 || depth <= 0:
		return g.scalar()
	case k < 9:
		return g.object(depth - 1)
	default:
		return g.array(depth - 1)
	}
}

func (g *Gen) object(depth int) string {
	n := g.r.Intn(4)
	keys := make([]string, 0, n+1)
	parts := make([]string, 0, n+1)
	for i := 0; i < n; i++ {
		k := g.key(nestedKeys)
		keys = append(keys, k)
		parts = append(parts, k+g.colon()+g.value(depth))
	}
	if g.chance(20) && len(keys) > 0 { // same key again, different value
		parts = append(parts, keys[g.r.Intn(len(keys))]+g.colon()+g.value(depth))
	}
	return "{" + strings.Join(parts, g.sep()) + "}"
}

func (g *Gen) array(depth int) string {
	n := g.r.Intn(5)
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if g.chance(50) {
			parts = append(parts, g.object(depth))
		} else {
			parts = append(parts, g.value(depth))
		}
	}
	return "[" + strings.Join(parts, g.sep()) + "]"
}

func (g *Gen) sep() string   { return g.pick(",", ",", ", ", " ,", ",\n  ", "\t,\t") }
func (g *Gen) colon() string { return g.pick(":", ":", ": ", " : ", ":\n") }

// valueFor biases the shape of well-known keys so the fields the gateway reads
// are usually present and usually plausibly typed, sometimes not.
func (g *Gen) valueFor(key string) string {
	if g.chance(30) {
		return g.value(3)
	}
	switch strings.Trim(key, `"`) {
	case "reasoning":
		return `{"effort":` + g.pick(`"high"`, `"none"`, ` "low" `, `null`, `5`, `true`, `{"x":1}`, `""`) + g.pick("", `,"summary":"auto"`, `,"effort":"medium"`, `,"generate_summary":"x"`) + `}`
	case "output_config":
		return `{"effort":` + g.pick(`"high"`, `"max"`, `null`, `1`) + `}`
	case "generationConfig":
		return `{"maxOutputTokens":` + g.num() + g.pick("", `,"temperature":0.5`) + `}`
	case "generation_config":
		return `{"max_output_tokens":` + g.num() + `}`
	case "client_metadata":
		return `{"ws_request_header_x_openai_internal_codex_responses_lite":` + g.pick(`"true"`, `"false"`, `true`, `null`, `"1"`) + `}`
	case "thinking":
		return `{"type":` + g.pick(`"enabled"`, `"adaptive"`, `"disabled"`) + `}`
	case "metadata":
		return `{"user_id":` + g.str() + `}`
	case "stream", "store", "parallel_tool_calls":
		return g.pick("true", "false", "null", `"true"`, `1`, `0`)
	case "max_tokens", "max_completion_tokens", "max_output_tokens":
		return g.pick(g.num(), g.num(), `"128"`, `null`, `true`)
	case "service_tier":
		return g.pick(`"priority"`, `"fast"`, `"flex"`, `"auto"`, `"bogus"`, `""`, `null`, `5`, `" Priority "`)
	case "prompt_cache_key":
		return g.pick(`"anthropic-metadata-abc"`, `"anthropic-cache-1"`, `"anthropic-digest-z"`, `"plain-key"`, `""`, `5`, `null`)
	case "tools":
		return "[" + g.pick(`{"type":"namespace","name":"n","tools":[]}`, `{"type":"image_generation","format":"png"}`, `{"type":"function","name":"f"}`, `{"type":"web_search_preview"}`, `5`, `{}`) + g.pick("", `,{"type":"namespace"}`) + "]"
	case "input":
		return "[" + g.pick(
			`{"type":"additional_tools","tools":[{"type":"namespace","name":"x"}]}`,
			`{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,"}]}`,
			`{"type":"reasoning","id":"rs_1","summary":null,"encrypted_content":""}`,
			`{"type":"function_call_output","call_id":"c1","output":"x"}`,
			`{"type":"additional_tools","tools":[]}`, `5`, `"s"`) + g.pick("", `,{"type":"message","role":"user","content":"hi"}`, `,{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"}`) + "]"
	case "messages", "contents", "include", "functions":
		return g.array(3)
	default:
		return g.value(3)
	}
}

func (g *Gen) doc() string {
	n := g.r.Intn(14)
	keys := make([]string, 0, n+2)
	parts := make([]string, 0, n+2)
	for i := 0; i < n; i++ {
		k := g.key(TopKeys)
		keys = append(keys, k)
		parts = append(parts, k+g.colon()+g.valueFor(k))
	}
	for g.chance(25) && len(keys) > 0 { // a top-level key again, new value
		k := keys[g.r.Intn(len(keys))]
		parts = append(parts, k+g.colon()+g.valueFor(k))
	}
	g.r.Shuffle(len(parts), func(i, j int) { parts[i], parts[j] = parts[j], parts[i] })
	doc := "{" + strings.Join(parts, g.sep()) + "}"
	if g.chance(35) {
		doc = g.sprinkleWhitespace(doc)
	}
	if g.chance(15) {
		doc = g.pick(" ", "\n", "\t", "\r\n") + doc + g.pick(" ", "\n", "\t", "\r\n")
	}
	return doc
}

// sprinkleWhitespace inserts whitespace around the structural characters of valid
// JSON (indentation, newlines, tabs, CRLF); string contents are left alone.
func (g *Gen) sprinkleWhitespace(compact string) string {
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

// mutate deletes, replaces, inserts a byte or truncates.
func (g *Gen) mutate(s string) string {
	b := []byte(s)
	for k := g.r.Intn(3); k >= 0 && len(b) > 0; k-- {
		i := g.r.Intn(len(b))
		switch g.r.Intn(4) {
		case 0:
			b = append(b[:i], b[i+1:]...)
		case 1:
			b[i] = "{}[]\",:\\ 0e-.tfn\x00\n"[g.r.Intn(18)]
		case 2:
			b = append(b[:i], append([]byte{"{}[]\",:\\ 0e-.tfn"[g.r.Intn(16)]}, b[i:]...)...)
		default:
			b = b[:i]
		}
	}
	return string(b)
}

// Body returns a document: mostly well-formed, sometimes damaged in a way gjson
// shrugs off, sometimes not an object at all.
func (g *Gen) Body() []byte {
	doc := g.doc()
	switch v := g.r.Intn(100); {
	case v < 70:
	case v < 74:
		doc = g.pick("\ufeff", "\xef\xbb\xbf", "garbage", " \x00", "\x0b", "\x0c") + doc
	case v < 78:
		doc += g.pick(" x", "{}", ",", "}", "]", "\x00", " {\"model\":\"later\"}", "\n\n")
	case v < 82:
		doc = g.pick("", " ", "null", "true", "1", `"str"`, "[]", `[{"model":"x"}]`, "[", "{", "}", `"model":"x"`, "{}", " { } ")
	case v < 86:
		doc = strings.Replace(doc, ",", ",,", 1)
	case v < 89:
		doc = strings.Replace(doc, `":`, `"`, 1) // missing colon
	case v < 92:
		doc = strings.Replace(doc, g.pick("true", "false", "null", "0", "1"), g.pick("tru", "nulll", "True", "01", "+1", "1.", ".5", "NaN", "Infinity", "-", "1e", "0x1", "nul"), 1)
	case v < 94:
		doc = strings.Replace(doc, "[", g.pick("(", "[(", "[)", "[}"), 1)
	case v < 96:
		doc = strings.Replace(doc, `"`, `\"`, 1)
	default:
		doc = g.mutate(doc)
	}
	return []byte(doc)
}
