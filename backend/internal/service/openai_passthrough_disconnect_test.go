//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Owner decision 2026-11-06: when the client leaves, the upstream connection is
// closed at once, on the passthrough path too. What the upstream already produced
// is billed from an estimate (95% of the input priced as cache read). These tests
// drive the real Forward of a passthrough account against an upstream whose body
// the test controls, then play the handler's RecordUsage step.

const (
	passthroughDisconnectStreamBody   = `{"model":"gpt-5.1","instructions":"You are a careful assistant.","input":"Write a very long story about a fox who learns to code.","stream":true}`
	passthroughDisconnectBufferedBody = `{"model":"gpt-5.1","instructions":"You are a careful assistant.","input":"Write a very long story about a fox who learns to code.","stream":false}`
)

type disconnectOutcome struct {
	result *OpenAIForwardResult
	err    error
}

// asIncomplete adapts the outcome to the shared billing assertions.
func (o disconnectOutcome) asIncomplete() incompleteStreamOutcome {
	return incompleteStreamOutcome{result: o.result, err: o.err}
}

type disconnectPipeHarness struct {
	t        *testing.T
	ctx      context.Context
	cancel   context.CancelFunc
	recorder *openAIResponseFlushRecorder
	upstream *httpUpstreamRecorder
	pipeW    *io.PipeWriter
	tracked  *lifecycleTrackingBody
	done     chan disconnectOutcome
}

func newPassthroughDisconnectContext(t *testing.T, ctx context.Context, body []byte) (*gin.Context, *openAIResponseFlushRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := newOpenAIResponseFlushRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	return c, recorder
}

func newPassthroughDisconnectService(upstream HTTPUpstream) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	}
}

// startPassthroughDisconnectForward starts Forward for a passthrough account whose
// upstream body is a pipe the test writes to. streaming selects the upstream
// content type; the request body decides whether the client asked for a stream.
func startPassthroughDisconnectForward(t *testing.T, body string, streaming bool) *disconnectPipeHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	c, recorder := newPassthroughDisconnectContext(t, ctx, []byte(body))
	resp, tracked, pw := newLifecycleTrackingResponse()
	contentType := "application/json"
	if streaming {
		contentType = "text/event-stream"
	}
	resp.Header = http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"rid-passthrough-disconnect"}}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := newPassthroughDisconnectService(upstream)

	h := &disconnectPipeHarness{
		t: t, ctx: ctx, cancel: cancel, recorder: recorder, upstream: upstream, pipeW: pw, tracked: tracked,
		done: make(chan disconnectOutcome, 1),
	}
	done := h.done
	go func() {
		result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), []byte(body))
		done <- disconnectOutcome{result: result, err: err}
	}()
	return h
}

// write pushes text through the upstream pipe. The write returns once the gateway
// has consumed it, so a stuck reader fails the test instead of hanging it.
func (h *disconnectPipeHarness) write(frames ...string) {
	h.t.Helper()
	payload := strings.Join(frames, "")
	errCh := make(chan error, 1)
	go func() {
		_, err := io.WriteString(h.pipeW, payload)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		require.NoError(h.t, err)
	case <-time.After(3 * time.Second):
		h.t.Fatal("gateway did not read the upstream bytes")
	}
}

// failClientWrites makes every later write to the client fail, as a vanished
// client does, without canceling the request context.
func (h *disconnectPipeHarness) failClientWrites() {
	h.recorder.mu.Lock()
	h.recorder.failAfterWrites = h.recorder.writes
	h.recorder.mu.Unlock()
}

func (h *disconnectPipeHarness) await(timeout time.Duration) disconnectOutcome {
	h.t.Helper()
	select {
	case out := <-h.done:
		return out
	case <-time.After(timeout):
		h.t.Fatal("Forward did not finish")
		return disconnectOutcome{}
	}
}

func (h *disconnectPipeHarness) requireUpstreamClosed() {
	h.t.Helper()
	select {
	case <-h.tracked.closed:
	default:
		h.t.Fatal("the upstream body was left open after the client left")
	}
}

// The client leaves after receiving output while the upstream goes quiet. The old
// code kept scanning a body nobody closed, so Forward never returned and the
// output was never billed. Now the upstream is closed at once and the delivered
// output is billed from the estimate.
func TestOpenAIPassthroughStream_ClientLeavesAfterDeliveredOutputIsBilledAtOnce(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	canceledAt := time.Now()
	h.cancel()

	out := h.await(3 * time.Second)

	require.Less(t, time.Since(canceledAt), 2*time.Second)
	require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
	require.Len(t, h.upstream.requests, 1, "a canceled client must not trigger a retry")
	h.requireUpstreamClosed()
	requireIncompleteStreamBilled(t, out.asIncomplete(), true)
	require.True(t, out.result.Usage.CacheReadEstimated)
}

// The client leaves before any output was produced. The upstream had accepted the
// request, so the input is billed (95% at the cache price); there is no output.
func TestOpenAIPassthroughStream_ClientLeavesBeforeAnyOutputIsBilledForInputOnly(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame)
	h.cancel()

	out := h.await(3 * time.Second)

	require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
	require.NotNil(t, out.result, "the input of an accepted request must reach billing")
	require.True(t, out.result.ClientDisconnect)
	require.Zero(t, out.result.Usage.OutputTokens)
	require.Greater(t, out.result.Usage.InputTokens, 0)
	require.Equal(t, incompleteCacheReadTokens(out.result.Usage.InputTokens), out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	usageRepo, billingRepo := billIncompleteStreamResult(t, out.result)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, 0.0)
	body, _ := h.recorder.snapshot()
	require.Empty(t, body, "no output may be committed to a canceled client")
	h.requireUpstreamClosed()
}

// The client vanishes without the request context ending: the next write to it
// fails. The old code only logged "continue draining" and kept reading a quiet
// upstream forever. Now the failed write closes the upstream at once.
func TestOpenAIPassthroughStream_ClientWriteFailureClosesUpstreamAndBills(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 1)
	h.failClientWrites()
	h.write(incompleteStreamDeltaTwoFrame)

	out := h.await(3 * time.Second)

	require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
	require.Len(t, h.upstream.requests, 1)
	h.requireUpstreamClosed()
	requireIncompleteStreamBilled(t, out.asIncomplete(), true)
}

// The connection is cut (EOF) after output was delivered but before the terminal
// frame, with the client still attached. The old code returned an empty result
// and the delivered output was never billed.
func TestOpenAIPassthroughStream_EOFWithoutTerminalAfterDeliveredOutputIsBilled(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	require.NoError(t, h.pipeW.Close())

	out := h.await(5 * time.Second)

	require.ErrorContains(t, out.err, "missing terminal event")
	requireIncompleteStreamBilled(t, out.asIncomplete(), false)
}

// The upstream read fails after output was delivered.
func TestOpenAIPassthroughStream_ReadErrorAfterDeliveredOutputIsBilled(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	require.NoError(t, h.pipeW.CloseWithError(errors.New("connection reset by peer")))

	out := h.await(5 * time.Second)

	require.ErrorContains(t, out.err, "stream read error")
	requireIncompleteStreamBilled(t, out.asIncomplete(), false)
}

// With the client still attached, an upstream that is cut before producing anything
// is a replayable failure: it fails over and nothing is billed.
func TestOpenAIPassthroughStream_UpstreamCutBeforeAnyOutputStaysFree(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame)
	require.NoError(t, h.pipeW.Close())

	out := h.await(5 * time.Second)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, out.err, &failoverErr, "a silent upstream cut must still fail over")
	require.Nil(t, out.result)
	requireIncompleteStreamNotBilled(t, out.asIncomplete())
}

// An upstream failure frame that produced no output must never be charged.
func TestOpenAIPassthroughStream_UpstreamFailureWithoutOutputStaysFree(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(incompleteStreamCreatedFrame, incompleteStreamFailedFrame)
	require.NoError(t, h.pipeW.Close())

	out := h.await(5 * time.Second)

	requireIncompleteStreamNotBilled(t, out.asIncomplete())
}

// A real terminal usage frame is authoritative: no estimate may replace it.
func TestOpenAIPassthroughStream_RealTerminalUsageIsNeverReplacedByEstimate(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	completed := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_incomplete","model":"gpt-5.1","status":"completed","usage":{"input_tokens":13,"output_tokens":4}}}` + "\n\n"
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame, completed)
	require.NoError(t, h.pipeW.Close())

	out := h.await(5 * time.Second)

	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.Equal(t, 13, out.result.Usage.InputTokens)
	require.Equal(t, 4, out.result.Usage.OutputTokens)
	require.Zero(t, out.result.Usage.CacheReadInputTokens)
	require.False(t, out.result.Usage.CacheReadEstimated)
}

// Real input usage reported by an early frame is kept; only the missing output
// side is estimated.
func TestOpenAIPassthroughStream_EstimateOnlyFillsOutputWhenInputWasReported(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectStreamBody, true)
	h.write(partialOpenAIResponsesSSE(false)+"\n", incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	require.NoError(t, h.pipeW.Close())

	out := h.await(5 * time.Second)

	require.ErrorContains(t, out.err, "missing terminal event")
	require.NotNil(t, out.result)
	require.Equal(t, 13, out.result.Usage.InputTokens, "a reported input count must not be replaced by an estimate")
	require.Zero(t, out.result.Usage.CacheReadInputTokens, "reported input is never re-split")
	require.False(t, out.result.Usage.CacheReadEstimated)
	require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), out.result.Usage.OutputTokens)
}

// passthroughBlockingUpstream never answers: Do returns only when the request
// context ends or the test releases it. It models an upstream that is still
// working on the response headers when the client leaves.
type passthroughBlockingUpstream struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func newPassthroughBlockingUpstream() *passthroughBlockingUpstream {
	return &passthroughBlockingUpstream{started: make(chan struct{}, 4), release: make(chan struct{})}
}

func (u *passthroughBlockingUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	u.started <- struct{}{}
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-u.release:
		return nil, errors.New("released by the test")
	}
}

func (u *passthroughBlockingUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, concurrency)
}

func (u *passthroughBlockingUpstream) unblock() {
	u.once.Do(func() { close(u.release) })
}

// The client leaves while the upstream is still working on the response headers.
// The old code detached the upstream request from the client, so the call kept
// running; now it is canceled at once. Nothing was produced, nothing is billed,
// and a departed client must not trigger a failover.
func TestOpenAIPassthrough_ClientLeavesBeforeResponseHeadersCancelsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "stream", body: passthroughDisconnectStreamBody},
		{name: "buffered", body: passthroughDisconnectBufferedBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			c, _ := newPassthroughDisconnectContext(t, ctx, []byte(tc.body))
			upstream := newPassthroughBlockingUpstream()
			t.Cleanup(upstream.unblock)
			svc := newPassthroughDisconnectService(upstream)

			done := make(chan disconnectOutcome, 1)
			go func() {
				result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(true), []byte(tc.body))
				done <- disconnectOutcome{result: result, err: err}
			}()
			select {
			case <-upstream.started:
			case <-time.After(3 * time.Second):
				t.Fatal("the request never reached the upstream")
			}
			canceledAt := time.Now()
			cancel()

			select {
			case out := <-done:
				require.Less(t, time.Since(canceledAt), 2*time.Second)
				require.ErrorIs(t, out.err, context.Canceled)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(out.err, &failoverErr), "a departed client must not fail over")
				require.Nil(t, out.result)
				require.EqualValues(t, 1, upstream.calls.Load())
				requireIncompleteStreamNotBilled(t, out.asIncomplete())
			case <-time.After(3 * time.Second):
				t.Fatal("the upstream request outlived the departed client")
			}
		})
	}
}

// The client leaves while a non-stream response body is still arriving. The
// upstream is closed at once; the request was accepted, so its input is billed
// from the estimate. The old code read the whole body regardless of the client.
func TestOpenAIPassthroughBuffered_ClientLeavesWhileBodyArrivesBillsInputOnly(t *testing.T) {
	h := startPassthroughDisconnectForward(t, passthroughDisconnectBufferedBody, false)
	h.write(`{"id":"resp_buffered","object":"response","status":"completed","output":[`)
	canceledAt := time.Now()
	h.cancel()

	out := h.await(3 * time.Second)

	require.Less(t, time.Since(canceledAt), 2*time.Second)
	require.Error(t, out.err)
	require.Len(t, h.upstream.requests, 1)
	h.requireUpstreamClosed()
	require.NotNil(t, out.result, "the input of an accepted request must reach billing")
	require.True(t, out.result.ClientDisconnect)
	require.Zero(t, out.result.Usage.OutputTokens)
	require.Greater(t, out.result.Usage.InputTokens, 0)
	require.Equal(t, incompleteCacheReadTokens(out.result.Usage.InputTokens), out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	usageRepo, billingRepo := billIncompleteStreamResult(t, out.result)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, 0.0)
	body, _ := h.recorder.snapshot()
	require.Empty(t, body, "no response may be committed to a canceled client")
}

// The shared buffered handler keeps its contract when the client leaves mid-body:
// it closes the upstream and returns a nil result with the abandoned marker, like
// every other read failure. The Excel relay relies on the nil result to fall back
// to its own usage tap, so the estimate for this case is made by the caller.
func TestOpenAIPassthroughBuffered_AbandonedBodyKeepsNilResultContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _ := newPassthroughDisconnectContext(t, ctx, []byte(passthroughDisconnectBufferedBody))
	resp, tracked, pw := newLifecycleTrackingResponse()
	resp.Header = http.Header{"Content-Type": []string{"application/json"}}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	svc := newPassthroughDisconnectService(&httpUpstreamRecorder{})

	type outcome struct {
		result *openaiNonStreamingResultPassthrough
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.handleNonStreamingResponsePassthroughWithOptions(ctx, resp, c, newOpenAIPartialUsageAccount(true), "gpt-5.1", "gpt-5.1", passthroughClosesUpstreamOnClientGone)
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-tracked.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never started reading the upstream body")
	}
	cancel()

	select {
	case out := <-done:
		require.Nil(t, out.result, "the shared handler must keep returning a nil result on a failed read")
		require.ErrorIs(t, out.err, errOpenAIBufferedResponseAbandoned)
		require.ErrorIs(t, out.err, io.ErrClosedPipe, "the read failure the closed upstream caused stays in the chain")
	case <-time.After(3 * time.Second):
		t.Fatal("the handler kept reading after the client left")
	}
	select {
	case <-tracked.closed:
	default:
		t.Fatal("the upstream body was left open after the client left")
	}
}

// A terminal frame the gateway already read before noticing the client is gone
// keeps its real usage; nothing is estimated over it.
func TestOpenAIPassthroughStream_TerminalUsageAlreadyReadSurvivesClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := newPassthroughDisconnectContext(t, ctx, []byte(passthroughDisconnectStreamBody))
	frames := incompleteStreamDeltaOneFrame +
		`data: {"type":"response.completed","response":{"id":"resp_done","usage":{"input_tokens":11,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}}}` + "\n\n"
	// A body whose Close does nothing, so everything written is certainly read.
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}
	svc := newPassthroughDisconnectService(&httpUpstreamRecorder{})

	result, err := svc.handleStreamingResponsePassthroughWithOptions(ctx, resp, c, newOpenAIPartialUsageAccount(true), time.Now(), "gpt-5.1", "gpt-5.1", passthroughClosesUpstreamOnClientGone)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.Equal(t, 11, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Equal(t, 2, result.usage.CacheReadInputTokens)
	require.False(t, result.usage.CacheReadEstimated)
}

// The client left and the upstream then ended cleanly without a terminal frame.
// The reader sees a plain EOF, not an error, so the old code reported a zero-usage
// "success" and the output was never billed. It must report an incomplete stream.
func TestOpenAIPassthroughStream_CleanEOFAfterClientLeftIsReportedIncomplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := newPassthroughDisconnectContext(t, ctx, []byte(passthroughDisconnectStreamBody))
	frames := incompleteStreamDeltaOneFrame + incompleteStreamDeltaTwoFrame
	// A body whose Close does nothing, so the reader ends with a plain EOF.
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}
	svc := newPassthroughDisconnectService(&httpUpstreamRecorder{})

	result, err := svc.handleStreamingResponsePassthroughWithOptions(ctx, resp, c, newOpenAIPartialUsageAccount(true), time.Now(), "gpt-5.1", "gpt-5.1", passthroughClosesUpstreamOnClientGone)

	require.ErrorContains(t, err, "stream usage incomplete after disconnect")
	require.NotNil(t, result)
	require.True(t, result.clientDisconnect)
	require.True(t, result.incompleteStream)
	require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), result.incompleteOutputTokens)
}

// A non-stream body that was fully read before the cancellation was noticed keeps
// its real usage.
func TestOpenAIPassthroughBuffered_BodyAlreadyReadKeepsRealUsageAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := newPassthroughDisconnectContext(t, ctx, []byte(passthroughDisconnectBufferedBody))
	respBody := `{"id":"resp_done","object":"response","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}}`
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(respBody))}
	svc := newPassthroughDisconnectService(&httpUpstreamRecorder{})

	result, err := svc.handleNonStreamingResponsePassthroughWithOptions(ctx, resp, c, newOpenAIPartialUsageAccount(true), "gpt-5.1", "gpt-5.1", passthroughClosesUpstreamOnClientGone)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 11, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Equal(t, 2, result.usage.CacheReadInputTokens)
}
