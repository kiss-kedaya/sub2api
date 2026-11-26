//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// indexByteScanJSONStringEnd is scanJSONStringEnd before the adaptive switch: IndexByte for every quote and a
// backslash count behind it, all the way. It is here to be benchmarked against.
func indexByteScanJSONStringEnd(body []byte, i int) (int, error) {
	start := i + 1
	j := start
	for {
		k := bytes.IndexByte(body[j:], '"')
		if k < 0 {
			return 0, fmt.Errorf("unterminated string at byte %d", i)
		}
		q := j + k
		backslashes := 0
		for p := q - 1; p >= start && body[p] == '\\'; p-- {
			backslashes++
		}
		if backslashes&1 == 0 {
			return q + 1, nil
		}
		j = q + 1
	}
}

// perf18StringEndUnits are repeated to the wanted length. They are the contents of JSON strings as they appear
// in the body (already escaped).
var perf18StringEndUnits = []struct{ shape, unit string }{
	{"prose", "The quick brown fox jumps over the lazy dog. "},
	{"code", `func handler(ctx context.Context) error { return nil }\n`},
	{"codeq", `func main() {\n\tfmt.Println(\"hello\")\n}\n// comment line here\n`},
	{"jsonesc", `{\"status\":\"ok\",\"id\":123,\"name\":\"abc\"},`},
	{"escq", `\"`},
	{"bsq", `\\\"`},
	{"bs", `\\`},
}

// perf18StringEndDoc is a document of about 1 MiB made of strings of n bytes of one shape, and where they start.
func perf18StringEndDoc(unit string, n int) ([]byte, []int) {
	content := strings.Repeat(unit, n/len(unit)+1)[:n]
	if k := len(content) - len(strings.TrimRight(content, `\`)); k%2 == 1 {
		content = content[:len(content)-1] // never cut an escape pair in half
	}
	var doc bytes.Buffer
	var starts []int
	for doc.Len() < 1<<20 {
		starts = append(starts, doc.Len())
		doc.WriteString(`"` + content + `",`)
	}
	return doc.Bytes(), starts
}

// Same strings through the three implementations in one binary: legacy (byte loop), indexbyte (the version before
// the adaptive switch), adaptive (production). ns/op is a pass over the whole document, so compare across the
// implementations of one shape and length.
func BenchmarkPerf18StringEnd(b *testing.B) {
	impls := []struct {
		name string
		fn   func([]byte, int) (int, error)
	}{
		{"legacy", legacyScanJSONStringEnd},
		{"indexbyte", indexByteScanJSONStringEnd},
		{"adaptive", scanJSONStringEnd},
	}
	for _, u := range perf18StringEndUnits {
		for _, n := range []int{3, 8, 40, 200, 2000, 1 << 20} {
			doc, starts := perf18StringEndDoc(u.unit, n)
			for _, impl := range impls {
				b.Run(fmt.Sprintf("%s/%d/%s", u.shape, n, impl.name), func(b *testing.B) {
					b.SetBytes(int64(len(doc)))
					sum := 0
					for i := 0; i < b.N; i++ {
						for _, s := range starts {
							end, _ := impl.fn(doc, s)
							sum += end
						}
					}
					perf18Sink = sum
				})
			}
		}
	}
}

var perf18Sink int

func perf18Quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// perf18ReviewShapeBody is the request shapes the independent review of the IndexByte string scan used: messages
// whose content is one kind of string (prose, code without quotes, JSON text inside a string, tiny "x" strings,
// many small text blocks), of about the given size.
func perf18ReviewShapeBody(shape string, size int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"claude-sonnet-4-5","max_tokens":8192,"stream":true,"output_config":{"effort":"high"},`)
	sb.WriteString(`"system":[{"type":"text","text":"You are Claude Code, a helpful coding agent.","cache_control":{"type":"ephemeral"}}],`)
	sb.WriteString(`"tools":[{"name":"bash","description":"run","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}],"messages":[`)
	for n := 0; sb.Len() < size; n++ {
		if n > 0 {
			sb.WriteByte(',')
		}
		var content string
		switch shape {
		case "prose":
			content = perf18Quote(strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40))
		case "code":
			content = perf18Quote(strings.Repeat("def f(x):\n    return x + 1  # comment\n\n", 30))
		case "jsonesc":
			content = perf18Quote(strings.Repeat(`{"status":"ok","id":123,"name":"abc","items":[1,2,3]},`, 30))
		case "tiny":
			content = `"x"`
		case "blocks":
			content = "[" + strings.TrimSuffix(strings.Repeat(`{"type":"text","text":"a"},`, 20), ",") + "]"
		}
		role := "user"
		if n%2 == 1 {
			role = "assistant"
		}
		fmt.Fprintf(&sb, `{"role":%q,"content":%s}`, role, content)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

// perf18ReviewAgentBody is a Claude Code style request: 3 long system blocks, 25 tools with 8 parameters each, and
// turns of user text, assistant tool_use and a tool_result that is Go code (a quote every ~30 bytes, escaped) or,
// every jsonEvery-th turn (0: never), JSON text.
func perf18ReviewAgentBody(turns, jsonEvery int) []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"claude-sonnet-4-5","max_tokens":32000,"stream":true,"thinking":{"type":"enabled","budget_tokens":16000},"output_config":{"effort":"high"},"metadata":{"user_id":"user_abc_account_def_session_123"},"system":[`)
	for i := 0; i < 3; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(`{"type":"text","text":` + perf18Quote(strings.Repeat("You are an interactive CLI tool that helps users with software engineering tasks. ", 60)) + `,"cache_control":{"type":"ephemeral"}}`)
	}
	sb.WriteString(`],"tools":[`)
	for i := 0; i < 25; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		var props []string
		for p := 0; p < 8; p++ {
			props = append(props, fmt.Sprintf(`"param_%d":{"type":"string","description":"The parameter number %d used by this tool to do things"}`, p, p))
		}
		fmt.Fprintf(&sb, `{"name":"tool_%d","description":%s,"input_schema":{"type":"object","properties":{%s},"required":["param_0","param_1"]}}`, i, perf18Quote(strings.Repeat("Does something useful for the agent. ", 8)), strings.Join(props, ","))
	}
	sb.WriteString(`],"messages":[`)
	for t := 0; t < turns; t++ {
		if t > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, `{"role":"user","content":[{"type":"text","text":%s}]},`, perf18Quote(strings.Repeat("Please look at the file and fix the failing test. ", 6)))
		fmt.Fprintf(&sb, `{"role":"assistant","content":[{"type":"text","text":"Let me look."},{"type":"tool_use","id":"toolu_%04d","name":"tool_3","input":{"param_0":"src/main.go","param_1":"1","param_2":"true"}}]},`, t)
		res := perf18Quote(strings.Repeat("func main() {\n\tfmt.Println(\"hello\")\n}\n// comment line here\n", 30))
		if jsonEvery > 0 && t%jsonEvery == 0 {
			res = perf18Quote(strings.Repeat(`{"status":"ok","id":123,"name":"abc","items":[1,2,3],"nested":{"a":"b"}},`, 40))
		}
		fmt.Fprintf(&sb, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%04d","content":%s}]}`, t, res)
	}
	sb.WriteString(`]}`)
	return []byte(sb.String())
}

// First parse of the review shapes: the whole-request effect of the string scan, on the same bodies for any commit
// (only ParseGatewayRequest is used).
func BenchmarkPerf18ParseReviewShapes(b *testing.B) {
	type bodyCase struct {
		name string
		body []byte
	}
	var cases []bodyCase
	for _, shape := range []string{"prose", "code", "jsonesc", "tiny", "blocks"} {
		for _, size := range []struct {
			name string
			n    int
		}{{"4KB", 4 << 10}, {"300KB", 300 << 10}, {"3MB", 3 << 20}} {
			cases = append(cases, bodyCase{shape + "/" + size.name, perf18ReviewShapeBody(shape, size.n)})
		}
	}
	cases = append(cases,
		bodyCase{"agent-60turns-nojson", perf18ReviewAgentBody(60, 0)},
		bodyCase{"agent-60turns-json1of4", perf18ReviewAgentBody(60, 4)},
		bodyCase{"agent-60turns-jsonall", perf18ReviewAgentBody(60, 1)},
		bodyCase{"agent-250turns-json1of4", perf18ReviewAgentBody(250, 4)},
	)
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.body)))
			for i := 0; i < b.N; i++ {
				if _, err := ParseGatewayRequest(NewRequestBodyRef(c.body), PlatformAnthropic); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
