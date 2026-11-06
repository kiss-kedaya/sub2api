//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// anthropicDisconnectRecorder is a goroutine-safe client writer. Once failAfter
// writes have succeeded every further write fails, which is how a client that
// closed its socket looks to the gateway. failAfter < 0 never fails.
type anthropicDisconnectRecorder struct {
	mu        sync.Mutex
	header    http.Header
	body      bytes.Buffer
	status    int
	writes    int
	failAfter int
}

func newAnthropicDisconnectRecorder(failAfter int) *anthropicDisconnectRecorder {
	return &anthropicDisconnectRecorder{header: make(http.Header), failAfter: failAfter}
}

func (w *anthropicDisconnectRecorder) Header() http.Header { return w.header }

func (w *anthropicDisconnectRecorder) WriteHeader(statusCode int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *anthropicDisconnectRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAfter >= 0 && w.writes >= w.failAfter {
		return 0, errors.New("client disconnected")
	}
	w.writes++
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *anthropicDisconnectRecorder) Flush() {}

// breakWrites makes every further write fail, like a client whose socket just broke.
func (w *anthropicDisconnectRecorder) breakWrites() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failAfter = w.writes
}

func (w *anthropicDisconnectRecorder) snapshot() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// waitForText blocks until the client has received text, so a test cancels the
// client only after it really saw output.
func (w *anthropicDisconnectRecorder) waitForText(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.snapshot(), text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the client never received %q; got %q", text, w.snapshot())
}

// anthropicDisconnectUpstream is a controllable HTTPUpstream stub.
type anthropicDisconnectUpstream struct {
	mu       sync.Mutex
	calls    int
	bodies   [][]byte
	called   chan struct{}
	respond  func(req *http.Request) (*http.Response, error)
	callOnce sync.Once
}

func newAnthropicDisconnectUpstream(respond func(req *http.Request) (*http.Response, error)) *anthropicDisconnectUpstream {
	return &anthropicDisconnectUpstream{called: make(chan struct{}), respond: respond}
}

func (u *anthropicDisconnectUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body []byte
	if req != nil && req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	u.mu.Lock()
	u.calls++
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()
	u.callOnce.Do(func() { close(u.called) })
	return u.respond(req)
}

func (u *anthropicDisconnectUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

func (u *anthropicDisconnectUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func (u *anthropicDisconnectUpstream) lastBody() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.bodies) == 0 {
		return nil
	}
	return u.bodies[len(u.bodies)-1]
}

// anthropicDisconnectBody is a pipe-backed upstream body that reports when the
// gateway closed it.
type anthropicDisconnectBody struct {
	reader    *io.PipeReader
	closed    chan struct{}
	closeOnce sync.Once
}

func (b *anthropicDisconnectBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *anthropicDisconnectBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return b.reader.Close()
}

func newAnthropicDisconnectSSEResponse() (*http.Response, *anthropicDisconnectBody, *io.PipeWriter) {
	reader, writer := io.Pipe()
	body := &anthropicDisconnectBody{reader: reader, closed: make(chan struct{})}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"req_disconnect"},
		},
		Body: body,
	}, body, writer
}

const (
	anthropicDisconnectInput     = 11
	anthropicDisconnectCacheRead = 7
	anthropicDisconnectTextOne   = "Once upon a time a quick brown fox sat down at a keyboard"
	anthropicDisconnectTextTwo   = " and began to write its very first program in Go."
)

func anthropicDisconnectStartFrame(usageJSON string) string {
	return "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_disc","type":"message","role":"assistant","model":"claude-sonnet-4","content":[],"usage":` + usageJSON + `}}` + "\n\n"
}

// The real upstream reports input and cache usage on message_start and only a
// provisional output count of 1.
func anthropicDisconnectRealStartFrame() string {
	return anthropicDisconnectStartFrame(fmt.Sprintf(`{"input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":1}`,
		anthropicDisconnectInput, anthropicDisconnectCacheRead))
}

func anthropicDisconnectBlockStartFrame() string {
	return "event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n"
}

func anthropicDisconnectDeltaFrame(text string) string {
	return "event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}` + "\n\n"
}

func anthropicDisconnectFinalFrames(outputTokens int) string {
	return "event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\ndata: " + fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, outputTokens) + "\n\n" +
		"event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
}

type anthropicDisconnectKind string

const (
	anthropicKindNative            anthropicDisconnectKind = "native"
	anthropicKindPassthrough       anthropicDisconnectKind = "passthrough"
	anthropicKindChatStream        anthropicDisconnectKind = "chat_stream"
	anthropicKindChatBuffered      anthropicDisconnectKind = "chat_buffered"
	anthropicKindResponsesStream   anthropicDisconnectKind = "responses_stream"
	anthropicKindResponsesBuffered anthropicDisconnectKind = "responses_buffered"
	// Non-stream Messages requests: the answer is one JSON body.
	anthropicKindNativeJSON      anthropicDisconnectKind = "native_json"
	anthropicKindPassthroughJSON anthropicDisconnectKind = "passthrough_json"
	// Bedrock accounts: the upstream speaks AWS EventStream (stream) or one JSON body.
	anthropicKindBedrock     anthropicDisconnectKind = "bedrock"
	anthropicKindBedrockJSON anthropicDisconnectKind = "bedrock_json"

	anthropicDisconnectPrompt = "Write a very long story about a fox who learns to code, with a twist at the end."
)

func (k anthropicDisconnectKind) clientStream() bool {
	switch k {
	case anthropicKindChatBuffered, anthropicKindResponsesBuffered, anthropicKindNativeJSON, anthropicKindPassthroughJSON, anthropicKindBedrockJSON:
		return false
	}
	return true
}

func (k anthropicDisconnectKind) passthrough() bool {
	return k == anthropicKindPassthrough || k == anthropicKindPassthroughJSON
}

func (k anthropicDisconnectKind) bedrock() bool {
	return k == anthropicKindBedrock || k == anthropicKindBedrockJSON
}

// estimateBody is the body the input estimate is computed from. A Bedrock request
// loses its model field on the way upstream, so the client's body is the one.
func (k anthropicDisconnectKind) estimateBody(upstreamBody []byte) []byte {
	if k.bedrock() {
		return k.requestBody()
	}
	return upstreamBody
}

// requestBody is the body the client sent for this kind of entry point.
func (k anthropicDisconnectKind) requestBody() []byte {
	switch k {
	case anthropicKindChatStream, anthropicKindChatBuffered:
		return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4","stream":%t,"messages":[{"role":"user","content":%q}]}`, k.clientStream(), anthropicDisconnectPrompt))
	case anthropicKindResponsesStream, anthropicKindResponsesBuffered:
		return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4","stream":%t,"input":%q}`, k.clientStream(), anthropicDisconnectPrompt))
	case anthropicKindBedrock, anthropicKindBedrockJSON:
		return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-5","max_tokens":256,"stream":%t,"messages":[{"role":"user","content":%q}]}`, k.clientStream(), anthropicDisconnectPrompt))
	default:
		return []byte(fmt.Sprintf(`{"model":"claude-sonnet-4","max_tokens":256,"stream":%t,"messages":[{"role":"user","content":%q}]}`, k.clientStream(), anthropicDisconnectPrompt))
	}
}

func newAnthropicDisconnectAccount(kind anthropicDisconnectKind) *Account {
	if kind.bedrock() {
		return &Account{
			ID: 301, Name: "bedrock-disconnect", Platform: PlatformAnthropic, Type: AccountTypeBedrock,
			Concurrency: 1, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{"auth_mode": "apikey", "api_key": "bedrock-test-key", "aws_region": "us-east-1"},
		}
	}
	account := newAnthropicAPIKeyAccountForTest()
	if !kind.passthrough() {
		account.Extra = nil
	}
	return account
}

func newAnthropicDisconnectService(upstream HTTPUpstream, streamIntervalSeconds int) *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:               defaultMaxLineSize,
		StreamDataIntervalTimeout: streamIntervalSeconds,
	}}
	return &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
}

type anthropicDisconnectOutcome struct {
	result *ForwardResult
	err    error
}

// startAnthropicDisconnectForward drives the real forward entry point of the
// given kind. ctx is both the client request context and the execution context.
func startAnthropicDisconnectForward(t *testing.T, ctx context.Context, kind anthropicDisconnectKind, svc *GatewayService, writer http.ResponseWriter) <-chan anthropicDisconnectOutcome {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body := kind.requestBody()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
	account := newAnthropicDisconnectAccount(kind)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	done := make(chan anthropicDisconnectOutcome, 1)
	go func() {
		var result *ForwardResult
		var err error
		switch kind {
		case anthropicKindChatStream, anthropicKindChatBuffered:
			result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, parsed)
		case anthropicKindResponsesStream, anthropicKindResponsesBuffered:
			result, err = svc.ForwardAsResponses(ctx, c, account, body, parsed)
		default:
			result, err = svc.Forward(ctx, c, account, parsed)
		}
		done <- anthropicDisconnectOutcome{result: result, err: err}
	}()
	return done
}

func awaitAnthropicDisconnect(t *testing.T, done <-chan anthropicDisconnectOutcome, what string, timeout ...time.Duration) anthropicDisconnectOutcome {
	t.Helper()
	limit := 3 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	select {
	case out := <-done:
		t.Logf("Forward returned: result_nil=%t err=%v", out.result == nil, out.err)
		if out.result != nil {
			t.Logf("usage=%+v client_disconnect=%t", out.result.Usage, out.result.ClientDisconnect)
		}
		return out
	case <-time.After(limit):
		t.Fatalf("Forward kept running after the client left (%s)", what)
		return anthropicDisconnectOutcome{}
	}
}

// billAnthropicDisconnectResult plays the handler's usage submission through the
// real RecordUsage and returns what reached billing.
func billAnthropicDisconnectResult(t *testing.T, result *ForwardResult) *openAIRecordUsageBillingRepoStub {
	t.Helper()
	require.NotNil(t, result)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result:  result,
		APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2000},
		Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	return billingRepo
}

func requireAnthropicClientGone(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "a departed client is not an upstream fault")
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "a departed client must not trigger failover")
}
