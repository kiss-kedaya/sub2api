package service

import (
	"fmt"
	"math"
	"math/rand"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/Wei-Shaw/sub2api/internal/testutil/jsonbody"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Differential tests for BodyTop: every answer must equal gjson.GetBytes on the same
// bytes, field by field (Type, Raw, Str, Num, Index), including on bodies gjson
// only tolerates (BOM, trailing garbage, duplicate and escaped keys, stray parens,
// bad numbers). Where the index declines to answer it must say so (handled=false)
// rather than guess.

// topSite is one migrated call site and the paths it asks for. The differential
// run below gives every site its own batch of random bodies.
type topSite struct {
	name  string
	paths []string
}

// topSites lists the migrated call sites; keep it in step with the code (the
// plain-path test fails when a site asks for a path the index would not handle).
var topSites = []topSite{
	{"handler.requestMaxOutputTokens", []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "generationConfig.maxOutputTokens", "generation_config.max_output_tokens"}},
	{"handler.parseOpenAICompatibleStream", []string{"stream"}},
	{"handler.Responses/Messages/ChatCompletions model + previous_response_id", []string{"model", "previous_response_id"}},
	{"handler.codexBootstrapInputLooksRelevant", []string{"input"}},
	{"service.ValidateOpenAIServiceTierField", []string{"service_tier"}},
	{"service.extractOpenAIServiceTierFromBody", []string{"service_tier"}},
	{"service.filterOpenAIResponsesNoneReasoningEffortForAccount", []string{"reasoning.effort", "reasoning_effort", "reasoning"}},
	{"service.explicitRequestedReasoningEffortFromBody", []string{"reasoning.effort", "reasoning_effort", "output_config.effort"}},
	{"service.extractOpenAIReasoningEffortFromBody", []string{"reasoning.effort", "reasoning_effort"}},
	{"service.CanonicalRequestedReasoningEffort", []string{"model"}},
	{"service.hasOpenAIResponsesNamespaceToolDeclaration", []string{"tools", "input"}},
	{"service.isOpenAIResponsesLiteWebSocketPayload", []string{"client_metadata.ws_request_header_x_openai_internal_codex_responses_lite"}},
	{"service.openAIRequestBodyImageGenerationToolNeedsNormalization", []string{"tools"}},
	{"service.openAIRequestBodyMayContainEmptyBase64InputImage", []string{"input"}},
	{"service.isOpenAICompatMessagesBridgeBody", []string{"prompt_cache_key"}},
	{"service.openAIRequestBodyHasTools", []string{"tools", "input"}},
	{"service.normalizeOpenAIParallelToolCallsWithoutTools", []string{"parallel_tool_calls"}},
	{"service.normalizeOpenAIAPIKeyStoreFalseReasoningReplay", []string{"store"}},
	{"service.setOpenAICodexRoutingHintFromBody", []string{"model", "service_tier"}},
	{"service.ForwardAsRawChatCompletions", []string{"model", "stream", "tools", "functions", "reasoning_effort"}},
	{"service.forwardOpenAIPassthrough / buildUpstreamRequestOpenAIPassthrough", []string{"stream", "model", "prompt_cache_key"}},
	{"handler.applyOpenAIReasoningEffortPolicyForRequest / bindOpenAIReasoningEffortPolicyForMessagesRequest", []string{"model"}},
	// No paths: these sites only share gjson.ValidBytes (BodyTop.Valid), which
	// topCompare checks on every body.
	{"validity memo: IsExplicitImageGenerationIntent, declaredClientFunctionToolNames, normalizeOpenAIResponsesLegacyIngress, handler entry points", nil},
}

// topDecoyPaths are paths the index must decline (gjson syntax it does not model).
var topDecoyPaths = []string{
	"", ".", "a.b.c", "reasoning.", ".effort", "tools.0", "input.#", "input.0.type", "tools.#.type",
	"reasoning.*", "re*", "mod?l", "@this", "@keys", "!true", "[model]", "{model}", "..0", "model|x",
	"input.#(type==\"x\")", `mo\del`, "0", "123", "a b", "model ", "é",
}

func topSamePath(a, b gjson.Result) string {
	switch {
	case a.Type != b.Type:
		return fmt.Sprintf("Type %v != %v", a.Type, b.Type)
	case a.Raw != b.Raw:
		return fmt.Sprintf("Raw %q != %q", a.Raw, b.Raw)
	case a.Str != b.Str:
		return fmt.Sprintf("Str %q != %q", a.Str, b.Str)
	case math.Float64bits(a.Num) != math.Float64bits(b.Num):
		return fmt.Sprintf("Num %v != %v", a.Num, b.Num)
	case a.Index != b.Index:
		return fmt.Sprintf("Index %d != %d", a.Index, b.Index)
	case len(a.Indexes) != len(b.Indexes):
		return "Indexes differ"
	case a.Exists() != b.Exists() || a.String() != b.String() || a.Int() != b.Int() || a.Uint() != b.Uint() ||
		a.Bool() != b.Bool() || a.IsObject() != b.IsObject() || a.IsArray() != b.IsArray():
		return "accessor differs"
	}
	return ""
}

// topCompare asks the index and gjson the same question and reports a mismatch.
func topCompare(body []byte, paths []string) (handledN int, mismatch string) {
	idx := &bodyTopIndex{body: body}
	top := BodyTop{body: body, idx: idx}
	for _, path := range paths {
		want := gjson.GetBytes(body, path)
		got := top.Get(path)
		if _, handled := idx.get(path); handled {
			handledN++
		}
		if d := topSamePath(want, got); d != "" {
			return handledN, fmt.Sprintf("path %q: %s\nbody: %q", path, d, topLogBody(body))
		}
	}
	if top.Valid() != gjson.ValidBytes(body) {
		return handledN, fmt.Sprintf("Valid mismatch\nbody: %q", topLogBody(body))
	}
	return handledN, ""
}

func topLogBody(b []byte) string {
	if len(b) > 600 {
		return string(b[:600]) + "..."
	}
	return string(b)
}

func topAllSitePaths() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range topSites {
		for _, p := range s.paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

const topDiffBodiesPerSite = 25000

func TestBodyTopMatchesGJSONRandomPerSite(t *testing.T) {
	for si, site := range topSites {
		site := site
		t.Run(site.name, func(t *testing.T) {
			g := jsonbody.New(int64(1000 + si))
			handled, asked := 0, 0
			for i := 0; i < topDiffBodiesPerSite; i++ {
				body := g.Body()
				// A fresh order for every body: the index resumes its pass between
				// lookups, so the order in which questions arrive must not matter.
				paths := append([]string(nil), site.paths...)
				g.Rand().Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
				n, mismatch := topCompare(body, paths)
				if mismatch != "" {
					t.Fatalf("site %s, body #%d: %s", site.name, i, mismatch)
				}
				handled += n
				asked += len(site.paths)
			}
			if asked > 0 {
				t.Logf("%s: %d random bodies, index answered %d of %d lookups (%d%%)", site.name, topDiffBodiesPerSite, handled, asked, handled*100/asked)
			}
			// The index must actually answer most questions; if it silently declined
			// everything the equality above would prove nothing about it.
			if asked > 0 {
				require.Greaterf(t, handled*100, asked*35, "%s: index answered only %d of %d lookups", site.name, handled, asked)
			}
		})
	}
}

func TestBodyTopDeclinesUnmodelledPaths(t *testing.T) {
	g := jsonbody.New(7)
	body := []byte(`{"model":"gpt","reasoning":{"effort":"high"},"tools":[1,2],"input":[{"type":"x"}]}`)
	idx := &bodyTopIndex{body: body}
	for _, p := range topDecoyPaths {
		_, handled := idx.get(p)
		require.Falsef(t, handled, "decoy path %q must be left to gjson", p)
	}
	for i := 0; i < 5000; i++ {
		_, mismatch := topCompare(g.Body(), topDecoyPaths)
		require.Empty(t, mismatch)
	}
}

func TestBodyTopSitePathsAreHandled(t *testing.T) {
	body := []byte(`{"model":"gpt","stream":true,"reasoning":{"effort":"high"},"tools":[1],"input":[],"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`)
	idx := &bodyTopIndex{body: body}
	for _, p := range topAllSitePaths() {
		_, handled := idx.get(p)
		require.Truef(t, handled, "migrated path %q is not a plain path: the call site would silently fall back to gjson", p)
	}
}

func TestBodyTopMatchesGJSONCases(t *testing.T) {
	bodies := []string{
		`{}`, ` { } `, `{"model":"a"}`, `{"model":"a","model":"b"}`, `{"model":1,"model":"b"}`, `{"Model":"a","model":"b"}`,
		`{"reasoning":{"effort":"x"},"reasoning":{"effort":"y"}}`,
		`{"reasoning":{"summary":"x"},"reasoning":{"effort":"y"}}`,
		`{"reasoning":[{"effort":"x"}],"reasoning":{"effort":"y"}}`,
		`{"reasoning":"str","reasoning":{"effort":"y"}}`,
		`{"reasoning":null,"reasoning":{"effort":"y"}}`,
		`{"reasoning":{"effort":null}}`, `{"reasoning":{"effort":{"a":1}}}`, `{"reasoning":{"effort":[1]}}`, `{"reasoning":{}}`,
		`{"reasoning":{"deep":{"effort":"z"}}}`,
		`{"input":[{"max_tokens":5}],"max_tokens":9}`, `{"input":{"max_tokens":5}}`,
		`{"a":"\"max_tokens\":5","max_tokens":7}`, `{"a":"x\\","max_tokens":7}`, `{"a":"x\\\\","max_tokens":7}`, `{"a":"x\\\"","max_tokens":7}`,
		`{"model":"esc","model":"plain"}`, `{"model":"esc"}`, `{"model":"esc é 😀 \n \" \\ \/"}`,
		`{"model":"\ud800"}`, `{"model":"bad \x escape"}`, `{"model":"` + "raw\ttab" + `"}`, "{\"model\":\"raw\nnewline\"}",
		`{"model":"ends escaped quote \""}`, `{"model":"ends backslashes \\\\"}`,
		"\ufeff" + `{"model":"bom"}`, "\xef\xbb\xbf" + `{"model":"bom"}`, `garbage{"model":"g"}`, `[{"model":"arr"}]`, `"model"`, `1`, `null`, ``, `   `,
		`{"model":"a"} trailing`, `{"model":"a"}{"model":"b"}`, `{"model":"a",}`, `{,"model":"a"}`, `{"model" "a"}`, `{"model":}`, `{model:"a"}`,
		`{"model":"unterminated`, `{"model":"a"`, `{"model":"a","x":[1,2}`, `{"x":[1,2],"model":"a"`, `{"x":{"y":1},"model":"a"}}`,
		`{"x":(1),"model":"a"}`, `{"x":{"a":(},"model":"m"}`, `{"x":[}],"model":"m"}`, `{"x":{"a":[}]},"model":"m"}`, `{"x":"(","model":"m"}`,
		`{"n":0,"model":"a"}`, `{"n":-0,"model":"a"}`, `{"n":01,"model":"a"}`, `{"n":+1,"model":"a"}`, `{"n":1.,"model":"a"}`, `{"n":.5,"model":"a"}`,
		`{"n":1e5,"model":"a"}`, `{"n":1E+5,"model":"a"}`, `{"n":NaN,"model":"a"}`, `{"n":Infinity,"model":"a"}`, `{"n":-Infinity,"model":"a"}`,
		`{"n":1e400,"max_tokens":1e400}`, `{"max_tokens":-1e400}`, `{"max_tokens":12345678901234567890}`, `{"max_tokens":9007199254740993}`,
		`{"max_tokens":1.5}`, `{"max_tokens":"5"}`, `{"max_tokens":0}`, `{"max_tokens":-0}`, `{"max_tokens":true}`, `{"max_tokens":null}`,
		`{"t":true,"f":false,"n":null,"model":"a"}`, `{"t":tru,"model":"a"}`, `{"t":truee,"model":"a"}`, `{"t":True,"model":"a"}`, `{"t":nul,"model":"a"}`,
		`{"t":nulll,"model":"a"}`, `{"stream":true}`, `{"stream":false}`, `{"stream":"true"}`, `{"stream":1}`, `{"stream":null}`, `{"stream":tru}`,
		`{"stream":trueX}`, `{"stream": true }`, "{\"stream\":\n\ttrue\n}", `{"stream":true,"stream":false}`,
		`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`,
		`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":true}}`,
		`{"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true","ws_request_header_x_openai_internal_codex_responses_lite":"false"}}`,
		`{"client_metadata":{"x":1},"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`,
		`{"client_metadata":{"a":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}}`,
		`{"client_metadata":"x","client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}}`,
		`{"generationConfig":{"maxOutputTokens":50},"generation_config":{"max_output_tokens":60}}`,
		`{"generationConfig":{"maxOutputTokens":50}`, `{"generationConfig":{"maxOutputTokens":50,}}`, `{"generationConfig":{"a":(},"maxOutputTokens":5}}`,
		`{"generationConfig":{"maxOutputTokens":"x\"}"}}`, `{"generationConfig":{"x":"\\","maxOutputTokens":5}}`,
		`{"tools":[],"input":[]}`, `{"tools":null}`, `{"tools":{}}`, `{"tools":"x"}`, `{"tools":[{"type":"namespace"}]}`,
		`{"type":"response.create","response":{"model":"m"}}`, `{"type":"response.create","response":"x"}`, `{"response":{"type":"response.x"}}`,
		`{"a":"` + strings.Repeat("x", 5000) + `","model":"far"}`, `{"a":"` + strings.Repeat(`\"`, 3000) + `","model":"far"}`,
		`{"a":"` + strings.Repeat(`\\`, 3001) + `","model":"far"}`, `{"a":[` + strings.Repeat(`{"b":[1,2,"]"]},`, 400) + `1],"model":"far"}`,
		`{"": "empty key","model":"a"}`, `{"é":1,"model":"a"}`, `{"a.b":1,"model":"a"}`, `{"model.x":1}`, `{"model":{"x":1}}`,
		"{\"model\":\"a\"}\x00", "\x00{\"model\":\"a\"}", "\x0b{\"model\":\"a\"}", "{\"model\":\"a\"}\x0c",
		`{"service_tier":"fast"}`, `{"service_tier":" Priority "}`, `{"service_tier":5}`, `{"service_tier":null}`, `{"service_tier":""}`,
		`{"store":false}`, `{"store":true}`, `{"store":"false"}`, `{"store":null}`, `{"prompt_cache_key":"anthropic-metadata-abc"}`,
	}
	paths := append(topAllSitePaths(), topDecoyPaths...)
	paths = append(paths, "Model", "MODEL", "model.x", "stream.x", "tools.type", "input.type", "reasoning.effort.x", "reasoning.summary", "store.x")
	for _, b := range bodies {
		_, mismatch := topCompare([]byte(b), paths)
		require.Empty(t, mismatch)
	}
	t.Logf("%d hand-written bodies x %d paths (site paths, decoys, extras)", len(bodies), len(paths))
}

// The pass stops where gjson would stop: a lookup that is answered by an early
// member never reads, and so never judges, the rest of the body.
func TestBodyTopScanIsLazy(t *testing.T) {
	body := []byte(`{"model":"early","stream":true,"input":[1,2,3],"max_tokens":9 garbage after the answer`)
	idx := &bodyTopIndex{body: body}
	top := BodyTop{body: body, idx: idx}

	require.Equal(t, "early", top.Get("model").String())
	idx.mu.Lock()
	require.Len(t, idx.members, 1, "only the member that answers is scanned")
	require.False(t, idx.scan.failed)
	idx.mu.Unlock()

	require.True(t, top.Get("stream").Bool())
	require.Equal(t, `[1,2,3]`, top.Get("input").Raw)
	require.Empty(t, topSamePath(gjson.GetBytes(body, "stream"), top.Get("stream")))

	// max_tokens is the last member before the damage and still answered; gjson
	// returns it too, it never looks past the member it returns.
	got, handled := idx.get("max_tokens")
	require.True(t, handled)
	require.Equal(t, int64(9), got.Int())
	require.Empty(t, topSamePath(gjson.GetBytes(body, "max_tokens"), top.Get("max_tokens")))
	// A question the scanned part cannot answer sends the pass into the damage:
	// the index declines and gjson answers.
	_, handled = idx.get("service_tier")
	require.False(t, handled)
	require.Empty(t, topSamePath(gjson.GetBytes(body, "service_tier"), top.Get("service_tier")))
	// What was answered before the damage is still answered from the table.
	_, handled = idx.get("model")
	require.True(t, handled)
}

func TestBodyTopLookupOrderDoesNotMatter(t *testing.T) {
	body := []byte(`{"a":1,"model":"m","reasoning":{"x":1},"stream":true,"reasoning":{"effort":"high"},"tail":[1,{"model":"nested"}]}`)
	paths := []string{"model", "reasoning.effort", "stream", "tail", "absent", "reasoning", "a", "reasoning.effort"}
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 500; i++ {
		order := append([]string(nil), paths...)
		r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		_, mismatch := topCompare(body, order)
		require.Empty(t, mismatch, "order %v", order)
	}
}

func TestBodyTopNilAndEmptyBodies(t *testing.T) {
	for _, body := range [][]byte{nil, {}, []byte(" ")} {
		top := BodyTop{body: body, idx: &bodyTopIndex{body: body}}
		for _, p := range []string{"model", "reasoning.effort"} {
			require.Empty(t, topSamePath(gjson.GetBytes(body, p), top.Get(p)))
		}
		require.Equal(t, gjson.ValidBytes(body), top.Valid())
	}
	require.Empty(t, topSamePath(gjson.GetBytes(nil, "model"), BodyTop{}.Get("model")))
}

// ---- memo on the gin context ----

func newTopTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func withBodyTopMinBytes(t *testing.T, n int64) {
	t.Helper()
	old := bodyTopMinBytes.Swap(n)
	t.Cleanup(func() { bodyTopMinBytes.Store(old) })
}

func TestRequestBodyTopMemoAndThreshold(t *testing.T) {
	small := []byte(`{"model":"a","stream":true}`)
	big := []byte(`{"model":"a","pad":"` + strings.Repeat("x", int(bodyTopDefaultMinBytes)) + `","stream":true}`)
	c := newTopTestContext()

	require.Nil(t, RequestBodyTop(nil, big).idx, "no gin context: no index")
	require.Nil(t, RequestBodyTop(c, small).idx, "below the threshold the original path is kept")
	first := RequestBodyTop(c, big)
	require.NotNil(t, first.idx)
	require.Same(t, first.idx, RequestBodyTop(c, big).idx, "same body slice, same index")
	require.Equal(t, "a", first.Get("model").String())
	require.True(t, first.Get("stream").Bool())

	// A rewrite is a different slice: new index; the old one stays usable (two slots).
	rewritten := append([]byte(nil), big...)
	second := RequestBodyTop(c, rewritten)
	require.NotSame(t, first.idx, second.idx)
	require.Same(t, first.idx, RequestBodyTop(c, big).idx)
	require.Same(t, second.idx, RequestBodyTop(c, rewritten).idx)

	// A third body evicts the older slot, never serves a stale answer.
	third := append([]byte(nil), big...)
	third = append(third[:len(third)-len(`"stream":true}`)], `"stream":false}`...)
	got := RequestBodyTop(c, third)
	require.False(t, got.Get("stream").Bool())
	require.True(t, RequestBodyTop(c, big).Get("stream").Bool())

	// Same pointer but shorter or longer is a different body.
	require.NotSame(t, RequestBodyTop(c, big).idx, RequestBodyTop(c, big[:len(big)-1]).idx)

	// A different request (fresh context) never sees this one's index.
	require.NotSame(t, first.idx, RequestBodyTop(newTopTestContext(), big).idx)

	withBodyTopMinBytes(t, 0)
	require.NotNil(t, RequestBodyTop(c, small).idx)
}

func TestRequestBodyTopConcurrent(t *testing.T) {
	big := []byte(`{"model":"a","pad":"` + strings.Repeat("x", int(bodyTopDefaultMinBytes)) + `","reasoning":{"effort":"high"},"max_tokens":77}`)
	c := newTopTestContext()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				body := big
				if (g+i)%3 == 0 {
					body = append([]byte(nil), big...)
				}
				top := RequestBodyTop(c, body)
				if top.Get("model").String() != "a" || top.Get("reasoning.effort").String() != "high" ||
					top.Get("max_tokens").Int() != 77 || !top.Valid() {
					t.Errorf("wrong answer from concurrent index")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestBodyTopLargeMemberTableFallsBack(t *testing.T) {
	var b strings.Builder
	_, _ = b.WriteString(`{`)
	for i := 0; i < bodyTopMaxMembers+5; i++ {
		_, _ = fmt.Fprintf(&b, `"k%d":%d,`, i, i)
	}
	_, _ = b.WriteString(`"model":"far"}`)
	body := []byte(b.String())
	idx := &bodyTopIndex{body: body}
	_, handled := idx.get("model")
	require.False(t, handled, "too many members: leave it to gjson")
	require.Equal(t, "far", BodyTop{body: body, idx: idx}.Get("model").String())
}

func TestBodyTopNestedLargeObjectFallsBack(t *testing.T) {
	body := []byte(`{"reasoning":{"pad":"` + strings.Repeat("x", bodyTopMaxNestedBytes) + `","effort":"high"}}`)
	idx := &bodyTopIndex{body: body}
	_, handled := idx.get("reasoning.effort")
	require.False(t, handled)
	require.Equal(t, "high", BodyTop{body: body, idx: idx}.Get("reasoning.effort").String())
}

func TestBodyTopScalarsDoNotPinTheBody(t *testing.T) {
	body := []byte(`{"service_tier":"priority","max_tokens":5,"reasoning":{"effort":"high"},"stream":true,"note":"a\nb"}`)
	top := BodyTop{body: body, idx: &bodyTopIndex{body: body}}
	for _, p := range []string{"service_tier", "max_tokens", "reasoning.effort", "stream", "note"} {
		r := top.Get(p)
		require.NotEmptyf(t, r.Raw, "%s missing", p)
		require.Falsef(t, aliasesBody(body, r.Raw), "%s: Raw points into the request body", p)
		require.Falsef(t, aliasesBody(body, r.Str), "%s: Str points into the request body", p)
	}
	// Composites are views by design.
	require.True(t, aliasesBody(body, top.Get("reasoning").Raw))
}

// aliasesBody reports whether s's bytes live inside body's memory.
func aliasesBody(body []byte, s string) bool {
	if len(s) == 0 || len(body) == 0 {
		return false
	}
	lo := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	p := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	return p >= lo && p < lo+uintptr(len(body))
}
