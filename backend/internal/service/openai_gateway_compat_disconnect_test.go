//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The compat gateways (Chat Completions and Messages over Responses, raw Chat
// Completions, Responses over Chat Completions, native Anthropic) used to keep reading
// the upstream after the client left, and billed nothing when no usage frame arrived.
// These tests drive the real forward functions against an upstream the test controls,
// make the client leave or the upstream break, then bill the returned result the way
// the handlers do.

const (
	compatDisconnectPrompt = "Write a very long story about a fox who learns to code."
	compatDisconnectOutput = incompleteStreamDeltaOneText + incompleteStreamDeltaTwoText

	compatResponsesCompletedFrame = "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_incomplete","model":"gpt-5.1","status":"completed","usage":{"input_tokens":13,"output_tokens":4}}}` + "\n\n"

	compatCCRoleFrame = `data: {"id":"chatcmpl_x","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n\n"
	// The finish chunk carries the usage report, then the stream ends with [DONE].
	compatCCFinishFrames = `data: {"id":"chatcmpl_x","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}],"usage":{"prompt_tokens":17,"completion_tokens":8,"total_tokens":25}}` + "\n\n" +
		"data: [DONE]\n\n"

	compatAnthropicStartFrames = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.1","content":[],"stop_reason":null,"usage":{"input_tokens":93,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n"
	compatAnthropicPingFrame = "event: ping\n" + `data: {"type":"ping"}` + "\n\n"
	compatAnthropicEndFrames = "event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":16}}` + "\n\n" +
		"event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n"
)

func compatCCDeltaFrame(text string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl_x","object":"chat.completion.chunk","model":"gpt-5.1","choices":[{"index":0,"delta":{"content":%q}}]}`, text) + "\n\n"
}

func compatAnthropicDeltaFrame(text string) string {
	return "event: content_block_delta\n" +
		fmt.Sprintf(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text) + "\n\n"
}

// compatDisconnectPath describes one gateway path: how to call it, what its upstream
// sends, and what the client and the bill should look like.
type compatDisconnectPath struct {
	name         string
	url          string
	streamBody   string
	bufferedBody string
	account      func() *Account
	run          func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error)
	// intervals are the stream_data_interval_timeout settings to cover: 0 takes the
	// synchronous read loop where the path has one, 30 takes the select loop.
	intervals []int

	startFrames    string // frames before any output; may already report usage
	startInput     int    // input those frames report (0: none)
	freeFrames     string // frames that carry neither output nor usage
	outputFrames   [2]string
	terminalFrames string
	clientSees     string // text the client has received once the output frames passed
	terminalInput  int
	terminalOutput int

	// usageStartFrames report input before any output (empty: the path cannot).
	usageStartFrames string
	usageStartInput  int

	// bufferedSSE: a non-stream client still gets an SSE upstream (the gateway
	// buffers it). Otherwise the upstream answers with one JSON body.
	bufferedSSE bool
	// bufferedCtxAware: the upstream request carries the client context, so a real
	// transport aborts the body read when the client leaves.
	bufferedCtxAware bool
	// estimateFromClientBody: the input estimate is taken from the client request
	// because the upstream body is a different protocol.
	estimateFromClientBody bool
}

func compatDisconnectBody(shape string, stream bool) string {
	return fmt.Sprintf(shape, compatDisconnectPrompt, stream)
}

func compatDisconnectPaths() []compatDisconnectPath {
	const (
		chatShape      = `{"model":"gpt-5.1","messages":[{"role":"user","content":"%s"}],"stream":%t}`
		anthropicShape = `{"model":"gpt-5.1","max_tokens":64,"messages":[{"role":"user","content":"%s"}],"stream":%t}`
		responsesShape = `{"model":"gpt-5.1","input":"%s","stream":%t}`
	)
	responsesOutput := [2]string{incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame}
	ccOutput := [2]string{compatCCDeltaFrame(incompleteStreamDeltaOneText), compatCCDeltaFrame(incompleteStreamDeltaTwoText)}
	anthropicOutput := [2]string{compatAnthropicDeltaFrame(incompleteStreamDeltaOneText), compatAnthropicDeltaFrame(incompleteStreamDeltaTwoText)}
	usageStart := partialOpenAIResponsesSSE(false) + "\n"

	return []compatDisconnectPath{
		{
			name: "chat_over_responses", url: "/v1/chat/completions",
			streamBody:   compatDisconnectBody(chatShape, true),
			bufferedBody: compatDisconnectBody(chatShape, false),
			account:      rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "gpt-5.1")
			},
			intervals:   []int{0, 30},
			startFrames: incompleteStreamCreatedFrame, freeFrames: incompleteStreamCreatedFrame,
			outputFrames: responsesOutput, terminalFrames: compatResponsesCompletedFrame,
			clientSees: "very first program", terminalInput: 13, terminalOutput: 4,
			usageStartFrames: usageStart, usageStartInput: 13,
			bufferedSSE: true,
		},
		{
			name: "messages_over_responses", url: "/v1/messages",
			streamBody:   compatDisconnectBody(anthropicShape, true),
			bufferedBody: compatDisconnectBody(anthropicShape, false),
			account:      rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(c.Request.Context(), c, account, body, "", "")
			},
			intervals:   []int{0, 30},
			startFrames: incompleteStreamCreatedFrame, freeFrames: incompleteStreamCreatedFrame,
			outputFrames: responsesOutput, terminalFrames: compatResponsesCompletedFrame,
			clientSees: "very first program", terminalInput: 13, terminalOutput: 4,
			usageStartFrames: usageStart, usageStartInput: 13,
			bufferedSSE: true,
		},
		{
			name: "raw_chat", url: "/v1/chat/completions",
			streamBody:   compatDisconnectBody(chatShape, true),
			bufferedBody: compatDisconnectBody(chatShape, false),
			account:      rawChatCompletionsTestAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				// ForwardAsChatCompletions detaches the upstream from the client context first.
				return svc.forwardAsRawChatCompletions(context.WithoutCancel(c.Request.Context()), c, account, body, "")
			},
			intervals:   []int{0},
			startFrames: compatCCRoleFrame, freeFrames: compatCCRoleFrame,
			outputFrames: ccOutput, terminalFrames: compatCCFinishFrames,
			clientSees: "very first program", terminalInput: 17, terminalOutput: 8,
		},
		{
			name: "responses_over_chat", url: "/v1/responses",
			streamBody:   compatDisconnectBody(responsesShape, true),
			bufferedBody: compatDisconnectBody(responsesShape, false),
			account:      forceChatResponsesFallbackAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(c.Request.Context(), c, account, body)
			},
			intervals:   []int{0},
			startFrames: compatCCRoleFrame, freeFrames: compatCCRoleFrame,
			outputFrames: ccOutput, terminalFrames: compatCCFinishFrames,
			clientSees: "very first program", terminalInput: 17, terminalOutput: 8,
			estimateFromClientBody: true,
		},
		{
			name: "native_anthropic", url: "/v1/messages",
			streamBody:   compatDisconnectBody(anthropicShape, true),
			bufferedBody: compatDisconnectBody(anthropicShape, false),
			account:      nativeAnthropicTestAccount,
			run: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(c.Request.Context(), c, account, body, "", "")
			},
			intervals:   []int{0, 30},
			startFrames: compatAnthropicStartFrames, startInput: 93, freeFrames: compatAnthropicPingFrame,
			outputFrames: anthropicOutput, terminalFrames: compatAnthropicEndFrames,
			clientSees: "very first program", terminalInput: 93, terminalOutput: 16,
			usageStartFrames: compatAnthropicStartFrames, usageStartInput: 93,
			bufferedCtxAware: true,
		},
	}
}

// compatCtxAbortUpstream mimics a real transport: when the request context ends, the
// response body read fails with the context error.
type compatCtxAbortUpstream struct {
	inner *httpUpstreamRecorder
	pipeW *io.PipeWriter
}

func (u *compatCtxAbortUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	resp, err := u.inner.Do(req, proxyURL, accountID, accountConcurrency)
	if ctx := req.Context(); ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			_ = u.pipeW.CloseWithError(ctx.Err())
		}()
	}
	return resp, err
}

func (u *compatCtxAbortUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type compatDisconnectOpts struct {
	body             string
	interval         int
	jsonUpstream     bool // the upstream answers with one JSON body instead of SSE
	ctxAware         bool // a canceled request context aborts the upstream body read
	failClientWrites bool // every write to the client fails (the client is gone)
}

func startCompatDisconnect(t *testing.T, p compatDisconnectPath, opts compatDisconnectOpts) *incompleteStreamHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	body := []byte(opts.body)
	recorder := newOpenAIResponseFlushRecorder()
	if opts.failClientWrites {
		recorder.failAfterWrites = 0
	}
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, p.url, bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	resp, tracked, pw := newLifecycleTrackingResponse()
	contentType := "text/event-stream"
	if opts.jsonUpstream {
		contentType = "application/json"
	}
	resp.Header = http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"rid-compat-disconnect"}}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	recorderUpstream := &httpUpstreamRecorder{resp: resp}
	var upstream HTTPUpstream = recorderUpstream
	if opts.ctxAware {
		upstream = &compatCtxAbortUpstream{inner: recorderUpstream, pipeW: pw}
	}
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.StreamDataIntervalTimeout = opts.interval
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	h := &incompleteStreamHarness{
		t: t, cancel: cancel, recorder: recorder, upstream: recorderUpstream, pipeW: pw, tracked: tracked,
		done: make(chan incompleteStreamOutcome, 1),
	}
	account := p.account()
	go func() {
		result, err := p.run(svc, c, account, body)
		h.done <- incompleteStreamOutcome{result: result, err: err}
	}()
	return h
}

func compatWaitClientSaw(h *incompleteStreamHarness, text string) {
	h.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if body, _ := h.recorder.snapshot(); strings.Contains(body, text) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("the client never received %q", text)
}

func compatWaitUpstreamRead(h *incompleteStreamHarness) {
	h.t.Helper()
	select {
	case <-h.tracked.readStarted:
	case <-time.After(3 * time.Second):
		h.t.Fatal("the gateway never started reading the upstream body")
	}
}

func compatRequireUpstreamClosed(h *incompleteStreamHarness) {
	h.t.Helper()
	select {
	case <-h.tracked.closed:
	default:
		h.t.Fatal("the upstream body was left open")
	}
}

// compatBilled is what a result must carry once billed.
type compatBilled struct {
	disconnect    bool   // the client left; otherwise the upstream broke and an error is returned
	output        int    // exact output tokens, -1 for "some"
	reportedInput int    // input the upstream really reported; 0 means estimated from the request
	clientBody    string // the client request, for paths that estimate from it
}

func requireCompatBilled(t *testing.T, h *incompleteStreamHarness, p compatDisconnectPath, out incompleteStreamOutcome, want compatBilled) {
	t.Helper()
	t.Logf("Forward returned: result_nil=%t err=%v", out.result == nil, out.err)
	if out.result != nil {
		t.Logf("usage=%+v client_disconnect=%t", out.result.Usage, out.result.ClientDisconnect)
	}
	if want.disconnect {
		require.NoError(t, out.err, "a client that left is not an upstream failure")
	} else {
		require.Error(t, out.err)
	}
	require.NotNil(t, out.result, "what the upstream already produced and was sent must reach billing")
	require.Equal(t, want.disconnect, out.result.ClientDisconnect)

	usage := out.result.Usage
	if want.output < 0 {
		require.Greater(t, usage.OutputTokens, 0)
	} else {
		require.Equal(t, want.output, usage.OutputTokens)
	}
	if want.reportedInput > 0 {
		require.Equal(t, want.reportedInput, usage.InputTokens, "a reported input count must not be replaced by an estimate")
		require.Zero(t, usage.CacheReadInputTokens)
		require.False(t, usage.CacheReadEstimated)
	} else {
		estimateBody := h.upstream.lastBody
		if p.estimateFromClientBody {
			estimateBody = []byte(want.clientBody)
		}
		require.Greater(t, usage.InputTokens, 0, "input is estimated from the request when none was reported")
		require.Equal(t, estimateRequestInputTokens(estimateBody), usage.InputTokens)
		require.Equal(t, incompleteCacheReadTokens(usage.InputTokens), usage.CacheReadInputTokens, "95% of the estimated input is priced as cache read")
		require.True(t, usage.CacheReadEstimated)
	}

	usageRepo, billingRepo := billIncompleteStreamResult(t, out.result)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, 0.0)
}

// The client leaves after receiving output and the upstream never sent a terminal
// frame: the upstream is closed at once and the delivered output is billed from the
// estimate.
func TestCompatDisconnect_ClientLeavesMidStreamClosesUpstreamAndBillsEstimate(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval})
				h.write(p.startFrames, p.outputFrames[0], p.outputFrames[1])
				compatWaitClientSaw(h, p.clientSees)
				canceledAt := time.Now()
				h.cancel()

				out := h.await(3 * time.Second)

				require.Less(t, time.Since(canceledAt), 2*time.Second)
				compatRequireUpstreamClosed(h)
				requireCompatBilled(t, h, p, out, compatBilled{
					disconnect: true, output: estimateTokensForText(compatDisconnectOutput),
					reportedInput: p.startInput, clientBody: p.streamBody,
				})
			})
		}
	}
}

// A client that asked for a plain JSON reply leaves while the gateway waits for the
// upstream. The upstream is closed at once; whatever the upstream already produced is
// billed from the estimate, and nothing is written to the departed client.
func TestCompatDisconnect_BufferedClientLeavesClosesUpstreamAndBillsEstimate(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		t.Run(p.name, func(t *testing.T) {
			h := startCompatDisconnect(t, p, compatDisconnectOpts{
				body: p.bufferedBody, interval: 30, jsonUpstream: !p.bufferedSSE, ctxAware: p.bufferedCtxAware,
			})
			wantOutput := 0
			if p.bufferedSSE {
				h.write(p.freeFrames, p.outputFrames[0], p.outputFrames[1])
				wantOutput = estimateTokensForText(compatDisconnectOutput)
			}
			compatWaitUpstreamRead(h)
			canceledAt := time.Now()
			h.cancel()

			out := h.await(3 * time.Second)

			require.Less(t, time.Since(canceledAt), 2*time.Second)
			compatRequireUpstreamClosed(h)
			requireCompatBilled(t, h, p, out, compatBilled{disconnect: true, output: wantOutput, clientBody: p.bufferedBody})
			body, _ := h.recorder.snapshot()
			require.Empty(t, body, "nothing may be written to a client that left")
		})
	}
}

// The client is still connected in the transport sense, but a write to it fails. That
// is the same as leaving: the upstream is closed instead of drained.
func TestCompatDisconnect_ClientWriteFailureClosesUpstream(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval, failClientWrites: true})
				h.write(p.startFrames, p.outputFrames[0])

				out := h.await(3 * time.Second)

				compatRequireUpstreamClosed(h)
				requireCompatBilled(t, h, p, out, compatBilled{
					disconnect: true, output: -1, reportedInput: p.startInput, clientBody: p.streamBody,
				})
			})
		}
	}
}

// The request was dispatched and accepted, then the client left before the upstream
// sent a single frame. The input is still billed (95% as cache read), output is zero.
func TestCompatDisconnect_ClientLeavesBeforeAnyFrameBillsInputOnly(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval})
				compatWaitUpstreamRead(h)
				h.cancel()

				out := h.await(3 * time.Second)

				compatRequireUpstreamClosed(h)
				requireCompatBilled(t, h, p, out, compatBilled{disconnect: true, output: 0, clientBody: p.streamBody})
			})
		}
	}
}

// Input the upstream really reported before the stream broke is never replaced or
// re-split by the estimate; only the missing output side is filled.
func TestCompatDisconnect_ReportedInputIsKept(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		if p.usageStartFrames == "" {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: 30})
			h.write(p.usageStartFrames, p.outputFrames[0], p.outputFrames[1])
			compatWaitClientSaw(h, p.clientSees)
			h.cancel()

			out := h.await(3 * time.Second)

			requireCompatBilled(t, h, p, out, compatBilled{
				disconnect: true, output: estimateTokensForText(compatDisconnectOutput),
				reportedInput: p.usageStartInput, clientBody: p.streamBody,
			})
		})
	}
}

// The upstream breaks after it produced output while the client is still attached:
// the output is billed from the estimate and the error is still reported.
func TestCompatDisconnect_UpstreamCutAfterOutputIsBilled(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval})
				h.write(p.startFrames, p.outputFrames[0], p.outputFrames[1])
				compatWaitClientSaw(h, p.clientSees)
				require.NoError(t, h.pipeW.CloseWithError(io.ErrUnexpectedEOF))

				out := h.await(5 * time.Second)

				requireCompatBilled(t, h, p, out, compatBilled{
					disconnect: false, output: estimateTokensForText(compatDisconnectOutput),
					reportedInput: p.startInput, clientBody: p.streamBody,
				})
			})
		}
	}
}

// The upstream breaks before it produced anything and the client is still attached:
// nothing is billed and the attempt stays replayable.
func TestCompatDisconnect_UpstreamCutBeforeOutputStaysFree(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval})
				h.write(p.freeFrames)
				require.NoError(t, h.pipeW.CloseWithError(io.ErrUnexpectedEOF))

				out := h.await(5 * time.Second)

				requireIncompleteStreamNotBilled(t, out)
			})
		}
	}
}

// A usage report that really arrived is authoritative, whether or not the client
// left right after it.
func TestCompatDisconnect_TerminalUsageIsNeverReplacedByEstimate(t *testing.T) {
	for _, p := range compatDisconnectPaths() {
		for _, interval := range p.intervals {
			t.Run(fmt.Sprintf("%s/interval_%d", p.name, interval), func(t *testing.T) {
				h := startCompatDisconnect(t, p, compatDisconnectOpts{body: p.streamBody, interval: interval})
				h.write(p.startFrames, p.outputFrames[0], p.outputFrames[1], p.terminalFrames)
				require.NoError(t, h.pipeW.Close())
				h.cancel()

				out := h.await(5 * time.Second)

				require.NoError(t, out.err)
				require.NotNil(t, out.result)
				require.Equal(t, p.terminalInput, out.result.Usage.InputTokens)
				require.Equal(t, p.terminalOutput, out.result.Usage.OutputTokens)
				require.False(t, out.result.Usage.CacheReadEstimated)
			})
		}
	}
}
