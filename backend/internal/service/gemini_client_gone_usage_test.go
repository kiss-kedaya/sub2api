//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Native Gemini and Code Assist: when the client leaves, the upstream connection is
// closed at once, and a request the upstream had accepted is billed from what was
// observed, with the missing parts estimated (incomplete_request_usage.go).

// geminiGoneRedirectUpstream sends the gateway's request to a local httptest server over
// a real http.Client, so cancellation travels through the real transport.
// onAccepted fires once the upstream has answered with its response headers.
type geminiGoneRedirectUpstream struct {
	target     *url.URL
	client     *http.Client
	onAccepted func()
}

func (u *geminiGoneRedirectUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host, out.Host, out.RequestURI = u.target.Scheme, u.target.Host, u.target.Host, ""
	resp, err := u.client.Do(out)
	if err == nil && u.onAccepted != nil {
		u.onAccepted()
	}
	return resp, err
}

func (u *geminiGoneRedirectUpstream) DoWithTLS(req *http.Request, proxyURL string, id int64, conc int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, id, conc)
}

// geminiGoneRecorder lets the test read the bytes already relayed to the client while the
// gateway goroutine is still writing. failWrites makes every client write fail.
type geminiGoneRecorder struct {
	*httptest.ResponseRecorder
	mu         sync.Mutex
	failWrites bool
}

func (r *geminiGoneRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failWrites {
		return 0, io.ErrClosedPipe
	}
	return r.ResponseRecorder.Write(p)
}

func (r *geminiGoneRecorder) WriteString(s string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failWrites {
		return 0, io.ErrClosedPipe
	}
	return r.ResponseRecorder.WriteString(s)
}

func (r *geminiGoneRecorder) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}

// geminiGoneUpstream is a real upstream: it writes the given SSE events, flushes, then
// holds the connection open until the gateway hangs up (gone) or the test ends.
type geminiGoneUpstream struct {
	server      *httptest.Server
	gone        chan struct{}
	release     chan struct{}
	goneOnce    sync.Once
	releaseOnce sync.Once
}

func newGeminiGoneUpstream(t *testing.T, events ...string) *geminiGoneUpstream {
	t.Helper()
	up := &geminiGoneUpstream{gone: make(chan struct{}), release: make(chan struct{})}
	up.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "upstream-gone-1")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		for _, event := range events {
			_, _ = io.WriteString(w, event)
			if flusher != nil {
				flusher.Flush()
			}
		}
		select {
		case <-r.Context().Done():
			up.goneOnce.Do(func() { close(up.gone) })
		case <-up.release:
		}
	}))
	t.Cleanup(func() {
		up.releaseOnce.Do(func() { close(up.release) })
		up.server.CloseClientConnections()
		up.server.Close()
	})
	return up
}

func geminiGoneAPIKeyAccount() *Account {
	return &Account{
		ID: 8101, Name: "gemini-gone-apikey", Platform: PlatformGemini, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test-key"}, Concurrency: 1,
	}
}

func geminiGoneOAuthAccount() *Account {
	return &Account{
		ID: 8102, Name: "gemini-gone-codeassist", Platform: PlatformGemini, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "ya29.test-token", "project_id": "project-1"}, Concurrency: 1,
	}
}

func newGeminiGoneService(upstream HTTPUpstream) *GeminiMessagesCompatService {
	return &GeminiMessagesCompatService{
		tokenProvider:    &GeminiTokenProvider{},
		httpUpstream:     upstream,
		cfg:              &config.Config{},
		rateLimitService: NewRateLimitService(&errorPolicyRepoStub{}, nil, &config.Config{}, nil, nil),
	}
}

// 400 ASCII characters estimate to 100 tokens: 95 priced as cache read, 5 as normal input.
var geminiGoneRequest = `{"contents":[{"role":"user","parts":[{"text":"` + strings.Repeat("abcd", 100) + `"}]}]}`

const (
	// usageMetadata is cumulative in a Gemini stream: after event 2, input=120 and output=9+4.
	geminiGoneEvent1 = `data: {"candidates":[{"content":{"parts":[{"text":"Once upon"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":125}}` + "\n\n"
	geminiGoneEvent2 = `data: {"candidates":[{"content":{"parts":[{"text":" a time"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":120,"candidatesTokenCount":9,"thoughtsTokenCount":4,"totalTokenCount":133}}` + "\n\n"
	// The same two chunks without any usage figure.
	geminiGoneBareEvent1 = `data: {"candidates":[{"content":{"parts":[{"text":"Once upon"}],"role":"model"}}]}` + "\n\n"
	geminiGoneBareEvent2 = `data: {"candidates":[{"content":{"parts":[{"text":" a time"}],"role":"model"}}]}` + "\n\n"
	geminiGoneErrEvent   = `data: {"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}` + "\n\n"
)

type geminiGoneOutcome struct {
	result *ForwardResult
	err    error
	body   string
	up     *geminiGoneUpstream
}

// requireGeminiGoneBilled feeds the result through the real RecordUsage and checks the
// charge against a hand computed amount. The test pricing has no cache read price, so an
// estimated cache read share is billed at the input price.
func requireGeminiGoneBilled(t *testing.T, result *ForwardResult, account *Account, wantInput, wantOutput int) {
	t.Helper()
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	repo := &openAIRecordUsageBillingRepoStub{}
	billing := NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-2.5-flash": {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
	}})
	gateway := &GatewayService{usageLogRepo: logs, usageBillingRepo: repo, billingService: billing,
		billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, gateway.RecordUsage(context.WithoutCancel(canceled), &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 7}, User: &User{ID: 42}, Account: account,
	}))
	want := float64(wantInput)*1e-6 + float64(wantOutput)*2e-6
	require.Equal(t, 1, repo.calls, "the balance is charged once")
	require.Equal(t, 1, logs.calls, "one usage log")
	require.NoError(t, repo.lastCtxErr)
	require.InDelta(t, want, repo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, want, logs.lastLog.ActualCost, 1e-12)
}

// geminiGoneCodeAssist wraps a plain Gemini SSE event the way Code Assist sends it.
func geminiGoneCodeAssist(event string) string {
	payload := strings.TrimSuffix(strings.TrimPrefix(event, "data: "), "\n\n")
	return "data: {\"response\":" + payload + ",\"traceId\":\"t1\"}\n\n"
}

// driveGeminiGone runs ForwardNative against a held upstream and makes the client leave:
// by cancelling the request context once waitEvents events reached the client, or, with
// failWrites, by failing every client write while the context stays alive.
func driveGeminiGone(t *testing.T, account *Account, events []string, stream bool, action string, waitEvents int, failWrites bool) geminiGoneOutcome {
	t.Helper()
	up := newGeminiGoneUpstream(t, events...)
	target, err := url.Parse(up.server.URL)
	require.NoError(t, err)
	accepted, once := make(chan struct{}), sync.Once{}
	svc := newGeminiGoneService(&geminiGoneRedirectUpstream{target: target, client: up.server.Client(),
		onAccepted: func() { once.Do(func() { close(accepted) }) }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &geminiGoneRecorder{ResponseRecorder: httptest.NewRecorder(), failWrites: failWrites}
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:"+action+"?alt=sse", strings.NewReader(geminiGoneRequest)).WithContext(ctx)
	done := make(chan geminiGoneOutcome, 1)
	go func() {
		result, err := svc.ForwardNative(ctx, c, account, "gemini-2.5-flash", action, stream, []byte(geminiGoneRequest))
		done <- geminiGoneOutcome{result: result, err: err}
	}()
	if !failWrites {
		select {
		case <-accepted:
		case <-time.After(5 * time.Second):
			t.Fatal("the upstream was never reached")
		}
		if waitEvents > 0 {
			require.Eventually(t, func() bool { return strings.Count(rec.Snapshot(), "data:") >= waitEvents },
				5*time.Second, 5*time.Millisecond, "the events should reach the client")
		}
		cancel()
	}
	select {
	case out := <-done:
		out.body, out.up = rec.Snapshot(), up
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("ForwardNative did not return after the client left")
	}
	return geminiGoneOutcome{}
}

// Usage the upstream already reported is kept exactly: not overwritten, not re-split, and
// no estimate is added on top.
func TestGeminiForwardNative_ClientGoneKeepsReportedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		account func() *Account
		wrap    func(string) string
	}{
		{"api_key", geminiGoneAPIKeyAccount, func(s string) string { return s }},
		{"oauth_codeassist", geminiGoneOAuthAccount, geminiGoneCodeAssist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := tc.account()
			out := driveGeminiGone(t, account, []string{tc.wrap(geminiGoneEvent1), tc.wrap(geminiGoneEvent2)}, true, "streamGenerateContent", 2, false)
			require.NoErrorf(t, out.err, "a client leaving is not an upstream failure; result=%v", out.result)
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, 120, out.result.Usage.InputTokens)
			require.Equal(t, 13, out.result.Usage.OutputTokens)
			require.Zero(t, out.result.Usage.CacheReadInputTokens)
			require.False(t, out.result.Usage.CacheReadEstimated, "reported usage is never turned into an estimate")
			select {
			case <-out.up.gone:
			case <-time.After(5 * time.Second):
				t.Fatal("the upstream connection was not closed")
			}
			requireGeminiGoneBilled(t, out.result, account, 120, 13)
		})
	}
}

// No usage figure at all: output is estimated from the content that reached the client,
// input from the request body, 95% of it as cache read.
func TestGeminiForwardNative_ClientGoneEstimatesMissingUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := geminiGoneAPIKeyAccount()
	out := driveGeminiGone(t, account, []string{geminiGoneBareEvent1, geminiGoneBareEvent2}, true, "streamGenerateContent", 2, false)
	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 5, out.result.Usage.InputTokens)
	require.Equal(t, 95, out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Equal(t, 4, out.result.Usage.OutputTokens) // "Once upon a time": 16 characters
	requireGeminiGoneBilled(t, out.result, account, 100, 4)
}

// The upstream accepted the request and the client left before any output: the request
// body was dispatched, so the estimated input is billed even though nothing was produced.
func TestGeminiForwardNative_ClientGoneBeforeOutputBillsEstimatedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := geminiGoneAPIKeyAccount()
	out := driveGeminiGone(t, account, nil, true, "streamGenerateContent", 0, false)
	require.NoErrorf(t, out.err, "result=%v", out.result)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 5, out.result.Usage.InputTokens)
	require.Equal(t, 95, out.result.Usage.CacheReadInputTokens)
	require.Zero(t, out.result.Usage.OutputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	requireGeminiGoneBilled(t, out.result, account, 100, 0)
}

// An in-band error envelope with nothing else is an upstream failure the client simply
// did not wait for: nothing is billed.
func TestGeminiForwardNative_ClientGoneAfterErrorEnvelopeStaysUnbilled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	out := driveGeminiGone(t, geminiGoneAPIKeyAccount(), []string{geminiGoneErrEvent}, true, "streamGenerateContent", 1, false)
	require.Nil(t, out.result)
	require.ErrorIs(t, out.err, context.Canceled)
}

// A failed client write is a disconnect too: the upstream is closed at once instead of
// being drained until it ends on its own (this upstream never ends by itself).
func TestGeminiForwardNative_ClientWriteFailureClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := geminiGoneAPIKeyAccount()
	out := driveGeminiGone(t, account, []string{geminiGoneEvent1, geminiGoneEvent2}, true, "streamGenerateContent", 0, true)
	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.Equal(t, 120, out.result.Usage.InputTokens, "the usage already parsed is kept")
	select {
	case <-out.up.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream connection was not closed")
	}
}

// Non-stream: the client leaves while the upstream body is being read. There is no drain
// window; the accepted request is billed from the request body estimate. The Code Assist
// account takes the aggregated path, where the upstream is read as a stream internally.
func TestGeminiForwardNative_NonStreamClientGoneBillsEstimatedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name    string
		account func() *Account
	}{
		{"api_key", geminiGoneAPIKeyAccount},
		{"oauth_codeassist_aggregated", geminiGoneOAuthAccount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := tc.account()
			out := driveGeminiGone(t, account, nil, false, "generateContent", 0, false)
			require.NoErrorf(t, out.err, "result=%v", out.result)
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.False(t, out.result.Stream)
			require.Equal(t, 5, out.result.Usage.InputTokens)
			require.Equal(t, 95, out.result.Usage.CacheReadInputTokens)
			require.True(t, out.result.Usage.CacheReadEstimated)
			require.Zero(t, out.result.Usage.OutputTokens)
			requireGeminiGoneBilled(t, out.result, account, 100, 0)
		})
	}
}

// The client left before the upstream answered: the request was never accepted, nothing
// is billed, and the retry loop must not keep going for a client that is gone.
func TestGeminiForwardNative_ClientGoneBeforeDispatchDoesNotRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &geminiCompatHTTPUpstreamStub{err: context.Canceled}
	svc := newGeminiGoneService(stub)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := newGeminiNativeTestContext(t)
	c.Request = c.Request.WithContext(ctx)
	started := time.Now()
	result, err := svc.ForwardNative(ctx, c, geminiGoneAPIKeyAccount(), "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiGoneRequest))
	require.Nil(t, result)
	require.Truef(t, errors.Is(err, context.Canceled), "err=%v", err)
	require.Equal(t, 1, stub.calls, "no retry for a client that is gone")
	require.Less(t, time.Since(started), 3*time.Second)
}
