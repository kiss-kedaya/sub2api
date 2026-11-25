package service

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/testutil/jsonbody"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Function-level differential test for every migrated call site.
//
// Each function runs twice on the same random body: with a nil gin context, where
// BodyTop forwards every question to gjson.GetBytes / gjson.ValidBytes (exactly the
// code path that existed before the index), and with a fresh context, where the
// top-level index answers. Everything observable must be identical: returned bodies
// and flags, errors, extracted fields, headers written.

func topOutputOfPtr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return "&" + *p
}

func topOutputOfBody(b []byte, changed bool, err error) string {
	return fmt.Sprintf("%q|%v|%v", b, changed, err)
}

func topOutputOfSet(m map[string]struct{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v|nil=%v", keys, m == nil)
}

func topTestAccounts() (apiKey, oauth *Account) {
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://compat.example/v1"}},
		&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
}

func TestMigratedSitesMatchGJSON(t *testing.T) {
	withBodyTopMinBytes(t, 0)
	apiKey, oauth := topTestAccounts()

	sites := []struct {
		name string
		run  func(c *gin.Context, body []byte) string
	}{
		{"filterOpenAIResponsesNoneReasoningEffortForAccount", func(c *gin.Context, body []byte) string {
			out, err := filterOpenAIResponsesNoneReasoningEffortForAccount(c, apiKey, body)
			return topOutputOfBody(out, false, err)
		}},
		{"extractOpenAIReasoningEffortFromBody", func(c *gin.Context, body []byte) string {
			return topOutputOfPtr(extractOpenAIReasoningEffortFromBody(c, body, "gpt-5.5", "o3")) + "/" +
				topOutputOfPtr(extractOpenAIReasoningEffortFromBody(c, body))
		}},
		{"explicitRequestedReasoningEffortFromBody", func(c *gin.Context, body []byte) string {
			return explicitRequestedReasoningEffortFromBody(c, body)
		}},
		{"CanonicalRequestedReasoningEffort", func(c *gin.Context, body []byte) string {
			return topOutputOfPtr(CanonicalRequestedReasoningEffort(c, body)) + "/" +
				topOutputOfPtr(CanonicalRequestedReasoningEffort(c, body, "gpt-5.5-high"))
		}},
		{"extractOpenAIServiceTierFromBody", func(c *gin.Context, body []byte) string {
			return topOutputOfPtr(extractOpenAIServiceTierFromBody(c, body))
		}},
		{"ValidateOpenAIServiceTierField", func(c *gin.Context, body []byte) string {
			tier, err := ValidateOpenAIServiceTierField(c, body)
			return fmt.Sprintf("%q|%v", tier, err)
		}},
		{"hasOpenAIResponsesNamespaceToolDeclaration", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(hasOpenAIResponsesNamespaceToolDeclaration(c, body))
		}},
		{"shouldKeepOpenAIResponsesToolCallNamespaces", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(shouldKeepOpenAIResponsesToolCallNamespaces(c, apiKey, OpenAIUpstreamTransportHTTPSSE, false, false, body))
		}},
		{"isOpenAIResponsesLiteWebSocketPayload", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(isOpenAIResponsesLiteWebSocketPayload(c, body))
		}},
		{"openAIRequestBodyImageGenerationToolNeedsNormalization", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(openAIRequestBodyImageGenerationToolNeedsNormalization(c, body))
		}},
		{"openAIRequestBodyMayContainEmptyBase64InputImage", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(openAIRequestBodyMayContainEmptyBase64InputImage(c, body))
		}},
		{"sanitizeEmptyBase64InputImagesInOpenAIBody", func(c *gin.Context, body []byte) string {
			out, changed, err := sanitizeEmptyBase64InputImagesInOpenAIBody(c, body)
			return topOutputOfBody(out, changed, err)
		}},
		{"isOpenAICompatMessagesBridgeBody", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(isOpenAICompatMessagesBridgeBody(c, body))
		}},
		{"openAIRequestBodyHasTools", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(openAIRequestBodyHasTools(c, body))
		}},
		{"normalizeOpenAIParallelToolCallsWithoutTools", func(c *gin.Context, body []byte) string {
			out, changed, err := normalizeOpenAIParallelToolCallsWithoutTools(c, body, false)
			return topOutputOfBody(out, changed, err)
		}},
		{"normalizeOpenAIAPIKeyStoreFalseReasoningReplay", func(c *gin.Context, body []byte) string {
			out, changed, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, body, false)
			return topOutputOfBody(out, changed, err)
		}},
		{"normalizeOpenAIAPIKeyStoreFalseReasoningReplay/known", func(c *gin.Context, body []byte) string {
			out, changed, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, body, true)
			return topOutputOfBody(out, changed, err)
		}},
		{"normalizeOpenAIResponsesLegacyIngress", func(c *gin.Context, body []byte) string {
			out, changed, err := normalizeOpenAIResponsesLegacyIngress(c, body)
			return topOutputOfBody(out, changed, err)
		}},
		{"declaredClientFunctionToolNames", func(c *gin.Context, body []byte) string {
			return topOutputOfSet(declaredClientFunctionToolNames(c, body))
		}},
		{"IsExplicitImageGenerationIntent", func(c *gin.Context, body []byte) string {
			return fmt.Sprint(IsExplicitImageGenerationIntent(c, "/v1/responses", "gpt-5.5", body))
		}},
		{"setOpenAICodexRoutingHintFromBody", func(c *gin.Context, body []byte) string {
			h := http.Header{}
			setOpenAICodexRoutingHintFromBody(c, h, oauth, body)
			return fmt.Sprint(h)
		}},
	}

	for si, site := range sites {
		site := site
		t.Run(site.name, func(t *testing.T) {
			g := jsonbody.New(int64(5000 + si))
			indexed := 0
			for i := 0; i < topDiffBodiesPerSite; i++ {
				body := g.Body()
				want := site.run(nil, body)
				c := newTopTestContext()
				got := site.run(c, body)
				if _, ok := c.Get(bodyTopMemoKey); ok {
					indexed++
				}
				if got != want {
					t.Fatalf("%s, body #%d\n  gjson path: %s\n  index path: %s\n  body: %q", site.name, i, want, got, topLogBody(body))
				}
			}
			// The comparison above says nothing if the index never ran (a context
			// RequestBodyTop leaves alone). Some functions return before asking when a
			// byte prefilter rules the body out, hence the modest floor.
			require.Greaterf(t, indexed*100, topDiffBodiesPerSite*30, "%s asked the index for only %d of %d bodies", site.name, indexed, topDiffBodiesPerSite)
		})
	}
}

// The same functions on large Codex-shaped bodies, at the real size threshold and
// with the variations the gateway cares about (service tier, effort, store=false,
// duplicate keys appended at the end).
func TestMigratedSitesMatchGJSONOnCodexBodies(t *testing.T) {
	apiKey, oauth := topTestAccounts()
	tails := []string{
		``,
		`,"service_tier":"fast"`, `,"service_tier":"bogus"`, `,"max_output_tokens":4096`, `,"reasoning_effort":"none"`,
		`,"reasoning":{"effort":"none"}`, `,"reasoning":{"effort":"high"},"reasoning":{"effort":"low"}`,
		`,"model":"second"`, `,"stream":"yes"`, `,"previous_response_id":"resp_1"`, `,"prompt_cache_key":"anthropic-cache-1"`,
		`,"client_metadata":{"ws_request_header_x_openai_internal_codex_responses_lite":"true"}`, `,"parallel_tool_calls":false,"tools":[]`,
	}
	for _, size := range []int{20 << 10, 120 << 10, 400 << 10} {
		base := codexBenchBody(size, int64(size))
		for _, tail := range tails {
			body := append([]byte(nil), base[:len(base)-1]...)
			body = append(body, tail...)
			body = append(body, '}')
			c := newTopTestContext()
			if RequestBodyTop(c, body).idx == nil {
				t.Fatalf("a %d byte body must be indexed at the default threshold", len(body))
			}
			check := func(name, want, got string) {
				t.Helper()
				if got != want {
					t.Fatalf("%s on %dKB body with tail %q\n  gjson: %s\n  index: %s", name, len(body)>>10, tail, want, got)
				}
			}
			out, _ := filterOpenAIResponsesNoneReasoningEffortForAccount(nil, apiKey, body)
			out2, _ := filterOpenAIResponsesNoneReasoningEffortForAccount(c, apiKey, body)
			check("filterNone", string(out), string(out2))
			check("reasoning", topOutputOfPtr(extractOpenAIReasoningEffortFromBody(nil, body, "gpt-5.5")), topOutputOfPtr(extractOpenAIReasoningEffortFromBody(c, body, "gpt-5.5")))
			check("canonical", topOutputOfPtr(CanonicalRequestedReasoningEffort(nil, body)), topOutputOfPtr(CanonicalRequestedReasoningEffort(c, body)))
			check("tier", topOutputOfPtr(extractOpenAIServiceTierFromBody(nil, body)), topOutputOfPtr(extractOpenAIServiceTierFromBody(c, body)))
			t1, e1 := ValidateOpenAIServiceTierField(nil, body)
			t2, e2 := ValidateOpenAIServiceTierField(c, body)
			check("validate tier", fmt.Sprint(t1, e1), fmt.Sprint(t2, e2))
			check("namespace", fmt.Sprint(hasOpenAIResponsesNamespaceToolDeclaration(nil, body)), fmt.Sprint(hasOpenAIResponsesNamespaceToolDeclaration(c, body)))
			check("lite", fmt.Sprint(isOpenAIResponsesLiteWebSocketPayload(nil, body)), fmt.Sprint(isOpenAIResponsesLiteWebSocketPayload(c, body)))
			check("hastools", fmt.Sprint(openAIRequestBodyHasTools(nil, body)), fmt.Sprint(openAIRequestBodyHasTools(c, body)))
			check("bridge", fmt.Sprint(isOpenAICompatMessagesBridgeBody(nil, body)), fmt.Sprint(isOpenAICompatMessagesBridgeBody(c, body)))
			check("imagetool", fmt.Sprint(openAIRequestBodyImageGenerationToolNeedsNormalization(nil, body)), fmt.Sprint(openAIRequestBodyImageGenerationToolNeedsNormalization(c, body)))
			check("base64", fmt.Sprint(openAIRequestBodyMayContainEmptyBase64InputImage(nil, body)), fmt.Sprint(openAIRequestBodyMayContainEmptyBase64InputImage(c, body)))
			n1, ch1, er1 := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(nil, body, true)
			n2, ch2, er2 := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(c, body, true)
			check("storefalse", topOutputOfBody(n1, ch1, er1), topOutputOfBody(n2, ch2, er2))
			l1, lc1, le1 := normalizeOpenAIResponsesLegacyIngress(nil, body)
			l2, lc2, le2 := normalizeOpenAIResponsesLegacyIngress(c, body)
			check("legacy ingress", topOutputOfBody(l1, lc1, le1), topOutputOfBody(l2, lc2, le2))
			check("declared tools", topOutputOfSet(declaredClientFunctionToolNames(nil, body)), topOutputOfSet(declaredClientFunctionToolNames(c, body)))
			check("explicit image intent", fmt.Sprint(IsExplicitImageGenerationIntent(nil, "/v1/responses", "gpt-5.5", body)), fmt.Sprint(IsExplicitImageGenerationIntent(c, "/v1/responses", "gpt-5.5", body)))
			h1, h2 := http.Header{}, http.Header{}
			setOpenAICodexRoutingHintFromBody(nil, h1, oauth, body)
			setOpenAICodexRoutingHintFromBody(c, h2, oauth, body)
			check("routing hint", fmt.Sprint(h1), fmt.Sprint(h2))
			if strings.Contains(tail, "reasoning") {
				n1, e1 := filterOpenAIResponsesNoneReasoningEffortForAccount(nil, apiKey, body)
				n2, e2 := filterOpenAIResponsesNoneReasoningEffortForAccount(c, apiKey, body)
				check("filterNone again", fmt.Sprint(len(n1), e1), fmt.Sprint(len(n2), e2))
			}
		}
	}
}

// A rewrite of the body inside one request must not be answered from the index of
// the body before it.
func TestMigratedSitesSeeRewrittenBody(t *testing.T) {
	apiKey, _ := topTestAccounts()
	base := string(codexBenchBody(40<<10, 3))
	base = strings.Replace(base, `"reasoning":{"effort":"high","summary":"auto"}`, `"reasoning":{"effort":"none"}`, 1)
	body := []byte(base[:len(base)-1] + `,"reasoning_effort":"none"}`)
	c := newTopTestContext()

	require.NotNil(t, RequestBodyTop(c, body).idx)
	require.Equal(t, "none", RequestBodyTop(c, body).Get("reasoning.effort").String())

	out, err := filterOpenAIResponsesNoneReasoningEffortForAccount(c, apiKey, body)
	require.NoError(t, err)
	require.NotEqual(t, len(body), len(out), "the none placeholders are removed")
	require.False(t, RequestBodyTop(c, out).Get("reasoning.effort").Exists(), "the rewritten body is asked, not the old one")
	require.False(t, RequestBodyTop(c, out).Get("reasoning_effort").Exists())
	require.Equal(t, "none", RequestBodyTop(c, body).Get("reasoning.effort").String(), "the original body is untouched and still answers")
}
