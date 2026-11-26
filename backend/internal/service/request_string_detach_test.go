package service

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"weak"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// aliasingString returns a string that is a window into buf, the way gjson/parseRawJSONView hand out
// substrings of a request body.
func aliasingString(buf []byte, from, to int) string {
	return unsafe.String(&buf[from], to-from)
}

func requireBodyCollected(t *testing.T, w weak.Pointer[byte], msg string) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.GC()
		return w.Value() == nil
	}, 3*time.Second, 5*time.Millisecond, msg)
}

func newAliasedBody(size int) (buf []byte, w weak.Pointer[byte]) {
	buf = []byte(`{"model":"claude-opus-5-5","effort":"high"}` + strings.Repeat(" ", size))
	return buf, weak.Make(&buf[0])
}

// The strings of a usage result are queued for as long as billing is slow. If they point into the request
// body they pin all of it; DetachRequestStrings must leave them with memory of their own.
func TestDetachRequestStringsReleasesTheRequestBody(t *testing.T) {
	type kase struct {
		name   string
		detach func(buf []byte) (keep any)
	}
	cases := []kase{
		{"ForwardResult", func(buf []byte) any {
			effort := aliasingString(buf, 37, 41)
			r := &ForwardResult{
				Model:                       aliasingString(buf, 10, 25),
				UpstreamModel:               aliasingString(buf, 10, 25),
				UpstreamResponseServiceTier: aliasingString(buf, 37, 41),
				ReasoningEffort:             &effort,
				RequestedReasoningEffort:    &effort,
				ImageOutputSizes:            []string{aliasingString(buf, 10, 15)},
			}
			r.DetachRequestStrings()
			return r
		}},
		{"OpenAIForwardResult", func(buf []byte) any {
			tier := aliasingString(buf, 37, 41)
			r := &OpenAIForwardResult{
				Model:         aliasingString(buf, 10, 25),
				BillingModel:  aliasingString(buf, 10, 25),
				UpstreamModel: aliasingString(buf, 10, 25),
				ServiceTier:   &tier,
				RequestID:     aliasingString(buf, 2, 7),
			}
			r.DetachRequestStrings()
			return r
		}},
		{"ChannelUsageFields", func(buf []byte) any {
			f := ChannelUsageFields{OriginalModel: aliasingString(buf, 10, 25), ChannelMappedModel: aliasingString(buf, 10, 25), ModelMappingChain: aliasingString(buf, 10, 25)}.Detached()
			return f
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keep, w := func() (any, weak.Pointer[byte]) {
				buf, w := newAliasedBody(1 << 20)
				return tc.detach(buf), w
			}()
			requireBodyCollected(t, w, "a detached result still pins the 1 MiB request body")
			runtime.KeepAlive(keep)
		})
	}
}

func TestDetachRequestStringsKeepsEveryValue(t *testing.T) {
	buf, _ := newAliasedBody(16)
	effort := aliasingString(buf, 37, 41)
	r := &OpenAIForwardResult{
		Model: aliasingString(buf, 10, 25), RequestID: "req-1", ServiceTier: &effort,
		ImageOutputSizes: []string{"1K", aliasingString(buf, 10, 15)},
		Usage:            OpenAIUsage{InputTokens: 12, OutputTokens: 34},
		Duration:         5 * time.Second,
		ResponseHeaders:  http.Header{"X-Test": {"1"}},
	}
	r.DetachRequestStrings()
	require.Equal(t, "claude-opus-5-5", r.Model)
	require.Equal(t, "req-1", r.RequestID)
	require.NotNil(t, r.ServiceTier)
	require.Equal(t, "high", *r.ServiceTier)
	require.Equal(t, []string{"1K", "claud"}, r.ImageOutputSizes)
	require.Equal(t, 12, r.Usage.InputTokens)
	require.Equal(t, 34, r.Usage.OutputTokens)
	require.Equal(t, 5*time.Second, r.Duration)
	require.Equal(t, "1", r.ResponseHeaders.Get("X-Test"))
	// the *string pointee is replaced, not edited in place: whoever shared the old pointer still holds the old pointer
	require.NotSame(t, &effort, r.ServiceTier)
	var nilResult *OpenAIForwardResult
	nilResult.DetachRequestStrings() // must not panic
	var nilForward *ForwardResult
	nilForward.DetachRequestStrings()
}

// The request view hands its model / cache key / tier / effort strings to usage results and logs.
func TestOpenAIRequestViewStringsDoNotPinTheBody(t *testing.T) {
	view, w := func() (openAIRequestView, weak.Pointer[byte]) {
		body := []byte(`{"model":"deepseek-v4-flash","stream":true,"prompt_cache_key":"pck-123","previous_response_id":"resp_1","service_tier":"priority","reasoning":{"effort":"high"},"input":"` + strings.Repeat("p", 1<<20) + `"}`)
		v := newOpenAIRequestView(body)
		v.body = nil // the view's own body field is request-scoped; only the extracted strings outlive it
		return v, weak.Make(&body[0])
	}()
	requireBodyCollected(t, w, "newOpenAIRequestView strings point into the request body")
	require.Equal(t, "deepseek-v4-flash", view.Model)
	require.True(t, view.Stream)
	require.Equal(t, "pck-123", view.PromptCacheKey)
	require.Equal(t, "resp_1", view.PreviousResponseID)
	require.Equal(t, "priority", view.ServiceTier)
	require.Equal(t, "high", view.ReasoningEffort)
}

// After the handler chain is done nothing in the gin context may keep the body alive: the context goes back to
// gin's pool (not reset there), and billing/log tasks may carry it.
func TestReleaseRequestBodyReferencesLetsTheBodyBeCollected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := &Account{ID: 7}
	w := func() weak.Pointer[byte] {
		body := []byte(`{"model":"m","input":"` + strings.Repeat("p", 64<<10) + `"}`)
		require.Equal(t, "m", RequestBodyTop(c, body).Get("model").String())
		stashCompatUpstreamRequestBody(c, account, body)
		RememberInboundBody(c, body)
		return weak.Make(&body[0])
	}()
	if w.Value() == nil {
		t.Fatal("the context must keep the body reachable before the release (otherwise this test proves nothing)")
	}
	ReleaseRequestBodyReferences(c)
	requireBodyCollected(t, w, "gin context still pins the request body after ReleaseRequestBodyReferences")
	runtime.KeepAlive(c)

	// the cached values are only caches / diagnostics: asking again must work, just without the old data
	got, _ := compatUpstreamRequestBodyFor(c, account)
	require.Nil(t, got)
	require.Equal(t, "n", RequestBodyTop(c, []byte(`{"model":"n","input":"`+strings.Repeat("q", 64<<10)+`"}`)).Get("model").String())

	ReleaseRequestBodyReferences(nil)            // must not panic
	ReleaseRequestBodyReferences(&gin.Context{}) // a context without keys
}
