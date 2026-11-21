//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Real OpenAI Responses streams report usage only on the terminal frame. Every
// earlier frame carries "usage":null or no usage at all, so a stream that is cut
// before the terminal frame leaves the parsed usage at zero.
const (
	incompleteStreamRequestBody = `{"model":"gpt-5.1","instructions":"You are a careful assistant.","input":"Write a very long story about a fox who learns to code.","stream":true}`

	incompleteStreamCreatedFrame = "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_incomplete","model":"gpt-5.1","status":"in_progress","output":[],"usage":null}}` + "\n\n"
	incompleteStreamDeltaOneText  = "Once upon a time a quick brown fox sat down at a keyboard "
	incompleteStreamDeltaTwoText  = "and began to write its very first program in Go."
	incompleteStreamDeltaOneFrame = "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"` + incompleteStreamDeltaOneText + `"}` + "\n\n"
	incompleteStreamDeltaTwoFrame = "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"` + incompleteStreamDeltaTwoText + `"}` + "\n\n"
	incompleteStreamFailedFrame = "event: response.failed\n" +
		`data: {"type":"response.failed","response":{"id":"resp_incomplete","status":"failed","error":{"code":"server_error","message":"The server had an error while processing your request"}}}` + "\n\n"
)

type incompleteStreamOutcome struct {
	result *OpenAIForwardResult
	err    error
}

// incompleteStreamHarness drives the real Forward path against an upstream whose
// body the test controls frame by frame.
type incompleteStreamHarness struct {
	t        *testing.T
	cancel   context.CancelFunc
	recorder *openAIResponseFlushRecorder
	upstream *httpUpstreamRecorder
	pipeW    *io.PipeWriter
	tracked  *lifecycleTrackingBody
	done     chan incompleteStreamOutcome
}

func startIncompleteStreamForward(t *testing.T, streamIntervalSeconds int) *incompleteStreamHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	body := []byte(incompleteStreamRequestBody)
	recorder := newOpenAIResponseFlushRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)

	resp, tracked, pw := newLifecycleTrackingResponse()
	resp.Header = http.Header{
		"Content-Type": []string{"text/event-stream"},
		"X-Request-Id": []string{"rid-incomplete-stream"},
	}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := newOpenAIPartialUsageService(upstream)
	svc.cfg.Gateway.StreamDataIntervalTimeout = streamIntervalSeconds

	h := &incompleteStreamHarness{
		t: t, cancel: cancel, recorder: recorder, upstream: upstream, pipeW: pw, tracked: tracked,
		done: make(chan incompleteStreamOutcome, 1),
	}
	go func() {
		result, err := svc.Forward(ctx, c, newOpenAIPartialUsageAccount(false), body)
		h.done <- incompleteStreamOutcome{result: result, err: err}
	}()
	return h
}

// write pushes SSE text through the upstream pipe. The write returns once the
// gateway scanner has consumed it, so a stuck reader fails the test instead of
// hanging it.
func (h *incompleteStreamHarness) write(frames ...string) {
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
		h.t.Fatal("gateway did not read the upstream frames")
	}
}

// closeUpstream ends the upstream body with a clean EOF.
func (h *incompleteStreamHarness) closeUpstream() {
	h.t.Helper()
	require.NoError(h.t, h.pipeW.Close())
}

func (h *incompleteStreamHarness) await(timeout time.Duration) incompleteStreamOutcome {
	h.t.Helper()
	select {
	case out := <-h.done:
		return out
	case <-time.After(timeout):
		h.t.Fatal("Forward did not finish")
		return incompleteStreamOutcome{}
	}
}

// billIncompleteStreamResult plays the handler's usage submission: a nil result
// is a no-op (submitResponsesUsage), anything else goes through RecordUsage.
func billIncompleteStreamResult(t *testing.T, result *OpenAIForwardResult) (*openAIRecordUsageLogRepoStub, *openAIRecordUsageBillingRepoStub) {
	t.Helper()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	if result == nil {
		return usageRepo, billingRepo
	}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil,
	)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result:  result,
		APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:    &User{ID: 2000},
		Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	return usageRepo, billingRepo
}

// requireIncompleteStreamBilled asserts the fixed behaviour: output the upstream
// already produced is recorded with estimated usage and produces a real charge.
func requireIncompleteStreamBilled(t *testing.T, out incompleteStreamOutcome, wantClientDisconnect bool) {
	t.Helper()
	t.Logf("Forward returned: result_nil=%t err=%v", out.result == nil, out.err)
	if out.result != nil {
		t.Logf("usage=%+v client_disconnect=%t", out.result.Usage, out.result.ClientDisconnect)
	}
	require.Error(t, out.err)
	require.NotNil(t, out.result, "delivered output must reach billing even though the terminal frame never arrived")
	require.Equal(t, wantClientDisconnect, out.result.ClientDisconnect)
	require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), out.result.Usage.OutputTokens)
	require.Greater(t, out.result.Usage.InputTokens, 0, "input is estimated from the request when no usage frame exists")
	require.Zero(t, out.result.Usage.CacheReadInputTokens,
		"an estimate never invents a cache discount: the whole input bills at the plain price")
	require.True(t, out.result.Usage.CacheReadEstimated)

	usageRepo, billingRepo := billIncompleteStreamResult(t, out.result)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, 0.0, "delivered output must not be free")
}

// requireIncompleteStreamNotBilled asserts nothing is charged when the upstream
// failed without producing any output.
func requireIncompleteStreamNotBilled(t *testing.T, out incompleteStreamOutcome) {
	t.Helper()
	t.Logf("Forward returned: result_nil=%t err=%v", out.result == nil, out.err)
	if out.result != nil {
		t.Logf("usage=%+v", out.result.Usage)
	}
	require.Error(t, out.err)
	_, billingRepo := billIncompleteStreamResult(t, out.result)
	if billingRepo.lastCmd != nil {
		require.Zero(t, billingRepo.lastCmd.BalanceCost, "an upstream error that delivered nothing must stay free")
	}
}

// The client leaves after receiving output and the upstream never sent a terminal
// frame. The upstream is closed at once (no drain window) and the delivered output
// is billed from the estimate.
func TestOpenAIForward_ClientLeavesAfterDeliveredOutputIsBilledAtOnce(t *testing.T) {
	// A long idle interval: the old drain window would have kept this open.
	h := startIncompleteStreamForward(t, 30)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	canceledAt := time.Now()
	h.cancel()

	out := h.await(3 * time.Second)

	require.Less(t, time.Since(canceledAt), 2*time.Second)
	require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
	require.Len(t, h.upstream.requests, 1, "a canceled client must not trigger a retry")
	select {
	case <-h.tracked.closed:
	default:
		t.Fatal("the upstream body was left open after the client left")
	}
	requireIncompleteStreamBilled(t, out, true)
}

// The connection is cut (EOF) after output was delivered but before the terminal
// frame, while the client is still attached.
func TestOpenAIForward_EOFWithoutTerminalAfterDeliveredOutputIsBilled(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	h.closeUpstream()

	out := h.await(5 * time.Second)

	require.ErrorContains(t, out.err, "missing terminal event")
	requireIncompleteStreamBilled(t, out, false)
}

// The upstream stalls after output was delivered and the idle watchdog gives up.
func TestOpenAIForward_IdleStallAfterDeliveredOutputIsBilled(t *testing.T) {
	h := startIncompleteStreamForward(t, 1)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)

	out := h.await(8 * time.Second)

	require.ErrorContains(t, out.err, "stream data interval timeout")
	requireIncompleteStreamBilled(t, out, false)
}

// Real input usage reported by an early frame is kept; only the missing output
// side is estimated.
func TestOpenAIForward_EstimateOnlyFillsOutputWhenInputWasReported(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	h.write(partialOpenAIResponsesSSE(false)+"\n", incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	h.closeUpstream()

	out := h.await(5 * time.Second)

	require.ErrorContains(t, out.err, "missing terminal event")
	t.Logf("Forward returned: result_nil=%t err=%v", out.result == nil, out.err)
	require.NotNil(t, out.result)
	t.Logf("usage=%+v", out.result.Usage)
	require.Equal(t, 13, out.result.Usage.InputTokens, "a reported input count must not be replaced by an estimate")
	require.Equal(t, estimateTokensForText(incompleteStreamDeltaOneText+incompleteStreamDeltaTwoText), out.result.Usage.OutputTokens)
}

// A real terminal usage frame is authoritative: no estimate may replace it.
func TestOpenAIForward_RealTerminalUsageIsNeverReplacedByEstimate(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	completed := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_incomplete","model":"gpt-5.1","status":"completed","usage":{"input_tokens":13,"output_tokens":4}}}` + "\n\n"
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame, completed)

	out := h.await(5 * time.Second)

	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.Equal(t, 13, out.result.Usage.InputTokens)
	require.Equal(t, 4, out.result.Usage.OutputTokens)
}

// The client leaves before any output was produced. The upstream had accepted the
// request, so the input is billed (95% at the cache price); there is no output.
func TestOpenAIForward_ClientLeavesBeforeAnyOutputIsBilledForInputOnly(t *testing.T) {
	h := startIncompleteStreamForward(t, 30)
	h.write(incompleteStreamCreatedFrame)
	h.cancel()

	out := h.await(3 * time.Second)

	require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
	require.NotNil(t, out.result, "the input of an accepted request must reach billing")
	require.True(t, out.result.ClientDisconnect)
	require.Zero(t, out.result.Usage.OutputTokens)
	require.Greater(t, out.result.Usage.InputTokens, 0)
	require.Zero(t, out.result.Usage.CacheReadInputTokens)
	usageRepo, billingRepo := billIncompleteStreamResult(t, out.result)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Greater(t, billingRepo.lastCmd.BalanceCost, 0.0)
	body, _ := h.recorder.snapshot()
	require.Empty(t, body, "no output may be committed to a canceled client")
}

// With the client still attached, an upstream that is cut before producing anything
// is still replayable, but the upstream accepted the request and may already have
// billed it. The attempt keeps an input estimate that the handler settles only when
// no retry succeeds (issue #456).
func TestOpenAIForward_UpstreamCutBeforeAnyOutputRetainsInputEstimate(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	h.write(incompleteStreamCreatedFrame)
	h.closeUpstream()

	out := h.await(5 * time.Second)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, out.err, &failoverErr)
	require.True(t, failoverErr.StreamEndedWithoutTerminal, "the attempt must stay replayable but retain its usage")
	require.NotNil(t, out.result, "an accepted request that may already be billed upstream must reach the handler")
	require.Zero(t, out.result.Usage.OutputTokens)
	require.Equal(t, estimateRequestInputTokens(h.upstream.lastBody), out.result.Usage.InputTokens,
		"input is estimated from the request body that went upstream")
	require.Zero(t, out.result.Usage.CacheReadInputTokens, "no cache discount is invented")
	require.True(t, out.result.Usage.CacheReadEstimated)
}

// By default an estimated input bills at the plain input price, matching new-api's
// abnormal-end fallback (issue #456). Raising the configured share restores the old
// cache-read heuristic and lowers the bill.
func TestOpenAIForward_EstimatedInputBillsAtPlainInputPriceByDefault(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	h.write(incompleteStreamCreatedFrame, incompleteStreamDeltaOneFrame, incompleteStreamDeltaTwoFrame)
	waitOpenAIResponseFlushCount(t, h.recorder, 2)
	h.closeUpstream()

	out := h.await(5 * time.Second)
	require.NotNil(t, out.result)
	require.Zero(t, out.result.Usage.CacheReadInputTokens)

	_, plain := billIncompleteStreamResult(t, out.result)
	require.NotNil(t, plain.lastCmd)

	withOpenAIIncompleteInputCacheReadPercent(t, 95)
	discountedResult := *out.result
	discountedResult.Usage.CacheReadInputTokens = openAIIncompleteCacheReadTokens(discountedResult.Usage.InputTokens)
	discountedResult.Usage.CacheReadEstimated = true
	_, discounted := billIncompleteStreamResult(t, &discountedResult)

	require.NotNil(t, discounted.lastCmd)
	require.Greater(t, discounted.lastCmd.BalanceCost, 0.0)
	require.Less(t, discounted.lastCmd.BalanceCost, plain.lastCmd.BalanceCost,
		"the configured cache share must lower the bill")
}

// An upstream error that produced no output must never be charged or estimated.
func TestOpenAIForward_UpstreamFailureWithoutOutputStaysFree(t *testing.T) {
	h := startIncompleteStreamForward(t, 0)
	h.write(incompleteStreamCreatedFrame, incompleteStreamFailedFrame)
	h.closeUpstream()

	out := h.await(5 * time.Second)

	requireIncompleteStreamNotBilled(t, out)
}
