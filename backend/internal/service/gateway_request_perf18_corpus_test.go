package service

import (
	"fmt"
	"math/rand"
	"strings"
)

// perf18Shape picks how escape-dense the generated conversation text is.
type perf18Shape int

const (
	// perf18Prose: long single-paragraph strings, almost no backslashes.
	perf18Prose perf18Shape = iota
	// perf18Code: file/tool-result text with a "\n" every few dozen bytes and
	// sprinkled \" \\ \t \u00e9 escapes, which is what Claude Code traffic looks like.
	perf18Code
)

// perf18AnthropicBody builds a deterministic, realistic /v1/messages request of
// roughly size bytes: top-level scalars, metadata/thinking/output_config,
// a cache-controlled system array, a tools array with JSON schemas, and
// alternating user/assistant messages with tool_use / tool_result blocks, plus one
// base64 image block once the body is big enough. The top-level key order is
// fixed so ranges and probes can be compared across runs.
func perf18AnthropicBody(size int, shape perf18Shape, extraTop string) []byte {
	rng := rand.New(rand.NewSource(int64(size)*7 + int64(shape)))
	var b strings.Builder
	b.Grow(size + 4096)

	b.WriteString(`{"model":"claude-sonnet-4-5","max_tokens":8192,"stream":true,`)
	b.WriteString(`"metadata":{"user_id":"user_0123456789abcdef_account__session_00000000-0000-4000-8000-000000000000"},`)
	b.WriteString(`"thinking":{"type":"enabled","budget_tokens":4096},`)
	b.WriteString(`"output_config":{"effort":"high"},`)
	if extraTop != "" {
		b.WriteString(extraTop)
		b.WriteByte(',')
	}
	b.WriteString(`"system":[{"type":"text","text":`)
	b.WriteString(perf18JSONString(perf18Text(rng, perf18Clamp(size/10, 200, 6000), perf18Code)))
	b.WriteString(`,"cache_control":{"type":"ephemeral"}}],`)

	b.WriteString(`"tools":[`)
	for i := 0; i < perf18Clamp(size/2500, 2, 24); i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"name":"tool_%d","description":%s,"input_schema":{"type":"object","properties":{"path":{"type":"string","description":"File path"},"limit":{"type":"integer"},"pattern":{"type":"string"}},"required":["path"]}}`,
			i, perf18JSONString(perf18Text(rng, perf18Clamp(size/80, 60, 300), perf18Prose)))
	}
	b.WriteString(`],`)

	b.WriteString(`"messages":[`)
	body := b.String()
	var m strings.Builder
	n := 0
	for len(body)+m.Len() < size-200 {
		if n > 0 {
			m.WriteByte(',')
		}
		remaining := size - 200 - len(body) - m.Len()
		chunk := 3000 + rng.Intn(5000)
		if chunk > remaining {
			chunk = remaining
		}
		if chunk < 64 {
			chunk = 64
		}
		switch n % 4 {
		case 0:
			fmt.Fprintf(&m, `{"role":"user","content":[{"type":"text","text":%s}]}`, perf18JSONString(perf18Text(rng, chunk, shape)))
		case 1:
			fmt.Fprintf(&m, `{"role":"assistant","content":[{"type":"text","text":%s},{"type":"tool_use","id":"toolu_%016d","name":"tool_%d","input":{"path":"/src/file_%d.go","limit":200}}]}`,
				perf18JSONString(perf18Text(rng, chunk/4+16, perf18Prose)), n, n%24, n)
		case 2:
			fmt.Fprintf(&m, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%016d","content":%s}]}`, n-1, perf18JSONString(perf18Text(rng, chunk, perf18Code)))
		default:
			if size >= 150_000 && n == 3 {
				// one pasted screenshot: a long string with no escapes at all
				fmt.Fprintf(&m, `{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}},{"type":"text","text":"what is this?"}]}`, perf18Base64(rng, 40_000))
			} else {
				fmt.Fprintf(&m, `{"role":"assistant","content":[{"type":"text","text":%s}]}`, perf18JSONString(perf18Text(rng, chunk/3+16, perf18Prose)))
			}
		}
		n++
	}
	return []byte(body + m.String() + `]}`)
}

func perf18Text(rng *rand.Rand, n int, shape perf18Shape) string {
	words := []string{"func", "return", "const", "the", "value", "error", "import", "context", "request", "package", "handler", "if", "nil", "string", "gateway", "store", "model", "stream"}
	var sb strings.Builder
	sb.Grow(n + 32)
	lineLen := 0
	for sb.Len() < n {
		w := words[rng.Intn(len(words))]
		sb.WriteString(w)
		lineLen += len(w) + 1
		switch {
		case shape == perf18Code && lineLen > 40+rng.Intn(40):
			sb.WriteByte('\n')
			if rng.Intn(3) == 0 {
				sb.WriteByte('\t')
			}
			lineLen = 0
		case shape == perf18Code && rng.Intn(23) == 0:
			sb.WriteString(` "` + w + `" `)
		case shape == perf18Code && rng.Intn(97) == 0:
			sb.WriteString(` C:\dir\file `)
		case rng.Intn(61) == 0:
			sb.WriteString(" caf\u00e9 \u2014 ")
		default:
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

// perf18Unesc turns the paragraph sign into a backslash, so test sources can spell JSON escapes
// (for example the key "§u006futput_config") without writing the escape sequence itself.
func perf18Unesc(s string) string { return strings.ReplaceAll(s, "§", `\`) }

func perf18Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func perf18Base64(rng *rand.Rand, n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return string(out)
}

// perf18JSONString quotes s the way encoding/json would (escapes ", \, control
// characters; leaves non-ASCII as is).
func perf18JSONString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 16)
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// perf18Sizes are the three body sizes every benchmark runs at.
var perf18Sizes = []struct {
	name string
	size int
}{
	{"10KB", 10 << 10},
	{"200KB", 200 << 10},
	{"2MB", 2 << 20},
}
