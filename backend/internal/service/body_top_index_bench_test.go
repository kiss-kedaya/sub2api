package service

import (
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Synthetic request bodies shaped like the traffic the gateway sees most:
// Codex /v1/responses (hundreds of reasoning / function_call / function_call_output
// items, a tool list, instructions) and Claude Code /v1/messages (system blocks,
// tool_use / tool_result turns, tools, metadata).

func benchText(r *rand.Rand, n int) string {
	const words = "lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua "
	var b strings.Builder
	for b.Len() < n {
		_, _ = b.WriteString(words[r.Intn(len(words)-16):][:8+r.Intn(8)])
	}
	return b.String()[:n]
}

// benchJSONText escapes s the way a client serializes a string with newlines,
// quotes and a backslash in it (tool output, code, diffs).
func benchJSONText(r *rand.Rand, n int) string {
	t := benchText(r, n)
	t = strings.ReplaceAll(t, " sed ", " \\\"sed\\\"\\n")
	t = strings.ReplaceAll(t, " do ", "\\n\\tdo ")
	t = strings.ReplaceAll(t, " ut ", " C:\\\\path ")
	return t
}

func benchBase64(r *rand.Rand, n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func codexBenchBody(target int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	var b strings.Builder
	_, _ = b.WriteString(`{"model":"gpt-5.5","instructions":"` + benchJSONText(r, 9000) + `","input":[`)
	first := true
	add := func(item string) {
		if !first {
			_ = b.WriteByte(',')
		}
		first = false
		_, _ = b.WriteString(item)
	}
	tools := `],"tools":[`
	toolsTail := ""
	for i := 0; i < 14; i++ {
		if i > 0 {
			toolsTail += ","
		}
		toolsTail += `{"type":"function","name":"tool_` + strconv.Itoa(i) + `","description":"` + benchJSONText(r, 300) +
			`","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"},"description":"` +
			benchJSONText(r, 120) + `"},"workdir":{"type":"string"}},"required":["command"],"additionalProperties":false}}`
	}
	budget := target - 9000 - len(toolsTail) - 600
	n := 0
	for b.Len() < budget {
		id := strconv.Itoa(n)
		n++
		switch n % 5 {
		case 0:
			add(`{"type":"message","role":"user","content":[{"type":"input_text","text":"` + benchJSONText(r, 200+r.Intn(1200)) + `"}]}`)
		case 1:
			add(`{"type":"reasoning","id":"rs_` + id + `","summary":[],"encrypted_content":"` + benchBase64(r, 1800+r.Intn(2400)) + `"}`)
		case 2:
			add(`{"type":"function_call","id":"fc_` + id + `","call_id":"call_` + id + `","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"` + benchJSONText(r, 80) + `\"]}"}`)
		case 3:
			add(`{"type":"function_call_output","call_id":"call_` + id + `","output":"` + benchJSONText(r, 200+r.Intn(3000)) + `"}`)
		default:
			add(`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + benchJSONText(r, 150+r.Intn(900)) + `"}]}`)
		}
	}
	_, _ = b.WriteString(tools + toolsTail)
	_, _ = b.WriteString(`],"tool_choice":"auto","parallel_tool_calls":true,"reasoning":{"effort":"high","summary":"auto"},"store":false,"stream":true,` +
		`"include":["reasoning.encrypted_content"],"prompt_cache_key":"0b1f6a50-6a0e-4f0e-9c52-3f8d2a7c9e11","text":{"verbosity":"medium"},` +
		`"client_metadata":{"x-codex-installation-id":"8c4b8d0e-1f5c-4a77-9a5e-0e6f4b2f7a10"}}`)
	return []byte(b.String())
}

func claudeCodeBenchBody(target int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	var b strings.Builder
	_, _ = b.WriteString(`{"model":"claude-sonnet-4-5","max_tokens":32000,"system":[{"type":"text","text":"` + benchJSONText(r, 6000) +
		`","cache_control":{"type":"ephemeral"}}],"messages":[`)
	toolsTail := ""
	for i := 0; i < 20; i++ {
		if i > 0 {
			toolsTail += ","
		}
		toolsTail += `{"name":"Tool` + strconv.Itoa(i) + `","description":"` + benchJSONText(r, 600) +
			`","input_schema":{"type":"object","properties":{"path":{"type":"string","description":"` + benchJSONText(r, 100) + `"}},"required":["path"]}}`
	}
	budget := target - 6000 - len(toolsTail) - 400
	n := 0
	for b.Len() < budget {
		if n > 0 {
			_ = b.WriteByte(',')
		}
		id := strconv.Itoa(n)
		switch n % 3 {
		case 0:
			_, _ = b.WriteString(`{"role":"user","content":[{"type":"text","text":"` + benchJSONText(r, 150+r.Intn(900)) + `"}]}`)
		case 1:
			_, _ = b.WriteString(`{"role":"assistant","content":[{"type":"thinking","thinking":"` + benchJSONText(r, 300) + `","signature":"` + benchBase64(r, 400) +
				`"},{"type":"tool_use","id":"toolu_` + id + `","name":"Read","input":{"path":"/src/file` + id + `.go"}}]}`)
		default:
			_, _ = b.WriteString(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_` + id + `","content":"` + benchJSONText(r, 300+r.Intn(3000)) + `"}]}`)
		}
		n++
	}
	_, _ = b.WriteString(`],"tools":[` + toolsTail + `],"metadata":{"user_id":"user_abc_account__session_0b1f6a50"},"thinking":{"type":"enabled","budget_tokens":16000},"stream":true}`)
	return []byte(b.String())
}

var benchShapes = []struct {
	name string
	gen  func(int, int64) []byte
	late string // a field that sits at the end of the body
}{
	{"codex", codexBenchBody, "stream"},
	{"claudecode", claudeCodeBenchBody, "stream"},
}

var benchSizes = []int{50 << 10, 300 << 10, 1 << 20}

func benchLabel(shape string, size int) string {
	return fmt.Sprintf("%s/%dKB", shape, size>>10)
}

var benchSink gjson.Result
var benchSinkN int

// BenchmarkTopIndexBuild: one pass over the top level.
func BenchmarkTopIndexBuild(b *testing.B) {
	for _, sh := range benchShapes {
		for _, size := range benchSizes {
			body := sh.gen(size, 1)
			b.Run(benchLabel(sh.name, len(body)), func(b *testing.B) {
				b.SetBytes(int64(len(body)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					idx := &bodyTopIndex{body: body}
					// An absent key makes the pass run to the end of the object.
					if _, handled := idx.get("absent_key"); !handled || !idx.scan.done {
						b.Fatal("scan declined a well-formed body")
					}
					benchSinkN += len(idx.members)
				}
			})
		}
	}
}

// BenchmarkTopLookup compares one lookup of a field near the start of the body
// (model), near the end (stream / reasoning) and of an absent field (service_tier:
// a full scan in gjson): gjson.GetBytes against an index that has already scanned
// that far.
func BenchmarkTopLookup(b *testing.B) {
	for _, sh := range benchShapes {
		for _, size := range benchSizes {
			body := sh.gen(size, 1)
			idx := &bodyTopIndex{body: body}
			top := BodyTop{body: body, idx: idx}
			for _, key := range []string{sh.late, "model", "service_tier", "reasoning.effort"} {
				name := benchLabel(sh.name, len(body)) + "/" + key
				b.Run(name+"/gjson", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchSink = gjson.GetBytes(body, key)
					}
				})
				b.Run(name+"/index", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchSink = top.Get(key)
					}
				})
			}
		}
	}
}

func TestBenchBodiesAreWellFormed(t *testing.T) {
	for _, sh := range benchShapes {
		for _, size := range benchSizes {
			body := sh.gen(size, 7)
			if !gjson.ValidBytes(body) {
				t.Fatalf("%s: generated body is not valid JSON", benchLabel(sh.name, size))
			}
			if _, handled := (&bodyTopIndex{body: body}).get("absent_key"); !handled {
				t.Fatalf("%s: index declined a valid body", benchLabel(sh.name, size))
			}
			if len(body) < size*8/10 || len(body) > size*13/10 {
				t.Fatalf("%s: body is %d bytes, wanted about %d", benchLabel(sh.name, size), len(body), size)
			}
		}
	}
}

// topOp is one body question: gjson.ValidBytes (valid) or gjson.GetBytes(path).
type topOp struct {
	valid bool
	path  string
}

// codexRequestOps is the chain of top-level questions one /v1/responses request
// from Codex asks through the migrated call sites, in the order the handler and
// Forward ask them (a body that carries a backslash defeats the byte prefilters,
// as every real Codex body with a newline does). The names are the call sites.
var codexRequestOps = []topOp{
	// handler: Responses
	{valid: true},          // Responses: validity gate
	{path: "model"},        // Responses: model
	{path: "input"},        // codexBootstrapInputLooksRelevant
	{path: "stream"},       // parseOpenAICompatibleStream
	{path: "service_tier"}, // ValidateOpenAIServiceTierField
	{path: "previous_response_id"},
	{valid: true},                                                                      // IsExplicitImageGenerationIntent
	{path: "max_tokens"}, {path: "max_completion_tokens"}, {path: "max_output_tokens"}, // requestMaxOutputTokens
	{path: "generationConfig.maxOutputTokens"}, {path: "generation_config.max_output_tokens"},
	{path: "model"}, {path: "reasoning.effort"}, // applyOpenAIReasoningEffortPolicyForRequest / CanonicalRequestedReasoningEffort
	// service: Forward
	{path: "reasoning.effort"}, {path: "reasoning_effort"}, {path: "reasoning"}, // filterOpenAIResponsesNoneReasoningEffortForAccount
	{valid: true},                                                                                     // declaredClientFunctionToolNames
	{valid: true},                                                                                     // normalizeOpenAIResponsesLegacyIngress
	{valid: true}, {path: "client_metadata.ws_request_header_x_openai_internal_codex_responses_lite"}, // isOpenAIResponsesLiteWebSocketPayload
	{path: "tools"}, {path: "input"}, // hasOpenAIResponsesNamespaceToolDeclaration
	{path: "parallel_tool_calls"}, {path: "tools"}, // normalizeOpenAIParallelToolCallsWithoutTools / openAIRequestBodyHasTools
	{path: "store"}, {valid: true}, // normalizeOpenAIAPIKeyStoreFalseReasoningReplay
	{path: "prompt_cache_key"},     // isOpenAICompatMessagesBridgeBody
	{valid: true}, {path: "tools"}, // openAIRequestBodyImageGenerationToolNeedsNormalization
	{path: "reasoning.effort"}, {path: "service_tier"}, // result bookkeeping: extractOpenAIReasoningEffortFromBody / extractOpenAIServiceTierFromBody
	{path: "model"}, {path: "service_tier"}, // setOpenAICodexRoutingHintFromBody
	{path: "model"}, {path: "service_tier"}, // logOpenAIRoutingDiagnosticsFromBody
	{path: "reasoning.effort"}, {path: "service_tier"}, // result bookkeeping again (failover / usage paths)
	{path: "prompt_cache_key"}, // isOpenAICompatMessagesBridgeBody (second call)
}

func BenchmarkRequestLookupChain(b *testing.B) {
	for _, size := range benchSizes {
		body := codexBenchBody(size, 1)
		label := fmt.Sprintf("codex/%dKB/%dlookups", len(body)>>10, len(codexRequestOps))
		b.Run(label+"/gjson", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, op := range codexRequestOps {
					if op.valid {
						benchSinkN += btoi(gjson.ValidBytes(body))
					} else {
						benchSink = gjson.GetBytes(body, op.path)
					}
				}
			}
		})
		b.Run(label+"/index", func(b *testing.B) {
			c := newTopTestContext()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.Keys = nil // a new request: no memo yet
				for _, op := range codexRequestOps {
					top := RequestBodyTop(c, body)
					if op.valid {
						benchSinkN += btoi(top.Valid())
					} else {
						benchSink = top.Get(op.path)
					}
				}
			}
		})
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

// TestRequestLookupChainAgrees makes the benchmark's two sides comparable: same
// answers for every question in the chain.
func TestRequestLookupChainAgrees(t *testing.T) {
	for _, size := range benchSizes {
		body := codexBenchBody(size, 1)
		c := newTopTestContext()
		for _, op := range codexRequestOps {
			top := RequestBodyTop(c, body)
			if op.valid {
				if top.Valid() != gjson.ValidBytes(body) {
					t.Fatalf("valid mismatch on %dKB", size>>10)
				}
				continue
			}
			if d := topSamePath(gjson.GetBytes(body, op.path), top.Get(op.path)); d != "" {
				t.Fatalf("%dKB %s: %s", size>>10, op.path, d)
			}
		}
	}
}

// BenchmarkMigratedFunctionsChain runs the real migrated functions one after the
// other on a Codex body: without a gin context every lookup is plain gjson (the code
// before the index), with one the index answers. Unlike the lookup chain above it
// includes the per-item work those functions still do themselves (ForEach over
// input, store=false reasoning replay ...), so the ratio is smaller.
func BenchmarkMigratedFunctionsChain(b *testing.B) {
	apiKey, oauth := topTestAccounts()
	for _, size := range benchSizes {
		body := codexBenchBody(size, 1)
		run := func(c *gin.Context) {
			_, _ = ValidateOpenAIServiceTierField(c, body)
			benchSinkN += btoi(IsExplicitImageGenerationIntent(c, "/v1/responses", "gpt-5.5", body))
			_ = CanonicalRequestedReasoningEffort(c, body)
			_, _ = filterOpenAIResponsesNoneReasoningEffortForAccount(c, apiKey, body)
			_ = declaredClientFunctionToolNames(c, body)
			_, _, _ = normalizeOpenAIResponsesLegacyIngress(c, body)
			benchSinkN += btoi(isOpenAIResponsesLiteWebSocketPayload(c, body))
			benchSinkN += btoi(shouldKeepOpenAIResponsesToolCallNamespaces(c, apiKey, OpenAIUpstreamTransportHTTPSSE, false, false, body))
			_, _, _ = normalizeOpenAIParallelToolCallsWithoutTools(c, body, false)
			_, _, _ = normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, body, false)
			benchSinkN += btoi(isOpenAICompatMessagesBridgeBody(c, body))
			benchSinkN += btoi(openAIRequestBodyImageGenerationToolNeedsNormalization(c, body))
			benchSinkN += btoi(openAIRequestBodyMayContainEmptyBase64InputImage(c, body))
			for i := 0; i < 3; i++ {
				_ = extractOpenAIReasoningEffortFromBody(c, body, "gpt-5.5")
				_ = extractOpenAIServiceTierFromBody(c, body)
			}
			setOpenAICodexRoutingHintFromBody(c, http.Header{}, oauth, body)
		}
		label := fmt.Sprintf("codex/%dKB", len(body)>>10)
		b.Run(label+"/gjson", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				run(nil)
			}
		})
		b.Run(label+"/index", func(b *testing.B) {
			c := newTopTestContext()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.Keys = nil
				run(c)
			}
		})
	}
}

// BenchmarkTopLookupCold: the first question about a body, on a fresh index. For a
// field near the start (model) the lazy pass stops there, like gjson; for a field
// near the end (stream) or an absent one it scans the object once.
func BenchmarkTopLookupCold(b *testing.B) {
	for _, size := range benchSizes {
		body := codexBenchBody(size, 1)
		for _, key := range []string{"model", "stream", "service_tier"} {
			name := fmt.Sprintf("codex/%dKB/%s", len(body)>>10, key)
			b.Run(name+"/gjson", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					benchSink = gjson.GetBytes(body, key)
				}
			})
			b.Run(name+"/index", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					benchSink = BodyTop{body: body, idx: &bodyTopIndex{body: body}}.Get(key)
				}
			})
		}
	}
}
