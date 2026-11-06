//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These cases drive the real forward entry points against an upstream whose body
// the test controls frame by frame. They pin the owner's rule: the moment the
// client is gone the upstream connection is closed (no drain window), and what
// was already produced is billed from an estimate while anything the upstream
// really reported is kept as is.

var anthropicDisconnectAllKinds = []anthropicDisconnectKind{
	anthropicKindNative, anthropicKindPassthrough,
	anthropicKindChatStream, anthropicKindChatBuffered,
	anthropicKindResponsesStream, anthropicKindResponsesBuffered,
}

var anthropicDisconnectClientStreamKinds = []anthropicDisconnectKind{
	anthropicKindNative, anthropicKindPassthrough,
	anthropicKindChatStream, anthropicKindResponsesStream,
}

type anthropicDisconnectStreamHarness struct {
	t        *testing.T
	kind     anthropicDisconnectKind
	cancel   context.CancelFunc
	recorder *anthropicDisconnectRecorder
	upstream *anthropicDisconnectUpstream
	body     *anthropicDisconnectBody
	pipeW    *io.PipeWriter
	done     <-chan anthropicDisconnectOutcome
}

// startAnthropicDisconnectStream starts a forward whose upstream answers 200 with
// an SSE body the test feeds. failClientAfter is the number of client writes that
// succeed before the client socket "breaks" (-1 never breaks). A long idle
// interval keeps the old drain-until-idle behaviour visible as a hang.
func startAnthropicDisconnectStream(t *testing.T, kind anthropicDisconnectKind, failClientAfter, streamIntervalSeconds int) *anthropicDisconnectStreamHarness {
	t.Helper()
	return startAnthropicDisconnectStreamWith(t, kind, failClientAfter, streamIntervalSeconds, nil)
}

// startAnthropicDisconnectStreamWith is startAnthropicDisconnectStream with a hook
// to adjust the service (for example to enable the downstream keepalive).
func startAnthropicDisconnectStreamWith(t *testing.T, kind anthropicDisconnectKind, failClientAfter, streamIntervalSeconds int, tweak func(*GatewayService)) *anthropicDisconnectStreamHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resp, body, pw := newAnthropicDisconnectSSEResponse()
	t.Cleanup(func() { _ = pw.Close() })
	upstream := newAnthropicDisconnectUpstream(func(*http.Request) (*http.Response, error) { return resp, nil })
	recorder := newAnthropicDisconnectRecorder(failClientAfter)
	svc := newAnthropicDisconnectService(upstream, streamIntervalSeconds)
	if tweak != nil {
		tweak(svc)
	}
	return &anthropicDisconnectStreamHarness{
		t: t, kind: kind, cancel: cancel, recorder: recorder, upstream: upstream, body: body, pipeW: pw,
		done: startAnthropicDisconnectForward(t, ctx, kind, svc, recorder),
	}
}

// write pushes SSE text through the upstream pipe. The write returns once the
// gateway reader consumed it, so a stuck reader fails the test instead of hanging.
func (h *anthropicDisconnectStreamHarness) write(frames ...string) {
	h.t.Helper()
	require.NoError(h.t, h.tryWrite(frames...))
}

// tryWrite is write for a client that is expected to be gone: the upstream may
// already be closed by the time the frames are pushed.
func (h *anthropicDisconnectStreamHarness) tryWrite(frames ...string) error {
	h.t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := io.WriteString(h.pipeW, strings.Join(frames, ""))
		errCh <- err
	}()
	select {
	case err := <-errCh:
		return err
	case <-time.After(3 * time.Second):
		h.t.Fatal("the gateway did not read the upstream frames")
		return nil
	}
}

func (h *anthropicDisconnectStreamHarness) awaitUpstream() {
	h.t.Helper()
	select {
	case <-h.upstream.called:
	case <-time.After(3 * time.Second):
		h.t.Fatal("the upstream request never arrived")
	}
}

// awaitClientText waits until a streaming client received text. A buffered
// client receives nothing until the end, so there is nothing to wait for.
func (h *anthropicDisconnectStreamHarness) awaitClientText(text string) {
	h.t.Helper()
	if h.kind.clientStream() {
		h.recorder.waitForText(h.t, text)
	}
}

func (h *anthropicDisconnectStreamHarness) closeUpstream() {
	h.t.Helper()
	require.NoError(h.t, h.pipeW.Close())
}

func (h *anthropicDisconnectStreamHarness) await(what string, timeout ...time.Duration) anthropicDisconnectOutcome {
	h.t.Helper()
	return awaitAnthropicDisconnect(h.t, h.done, what, timeout...)
}

func (h *anthropicDisconnectStreamHarness) requireUpstreamClosed(what string) {
	h.t.Helper()
	select {
	case <-h.body.closed:
	case <-time.After(3 * time.Second):
		h.t.Fatalf("the upstream connection was left open: %s", what)
	}
}

// The client leaves after it received output and the upstream sent no usage after
// message_start. The upstream is closed at once (the old code kept reading until
// the idle watchdog), the real input side is kept and the missing output is
// estimated from what the upstream really produced.
func TestAnthropicDisconnect_ClientLeavesMidStreamClosesUpstreamAndBillsEstimate(t *testing.T) {
	for _, kind := range anthropicDisconnectAllKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 30)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne), anthropicDisconnectDeltaFrame(anthropicDisconnectTextTwo))
			h.awaitClientText(anthropicDisconnectTextOne)
			canceledAt := time.Now()
			h.cancel()

			out := h.await("client left mid stream")

			require.Less(t, time.Since(canceledAt), 2*time.Second)
			requireAnthropicClientGone(t, out.err)
			require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
			require.Equal(t, 1, h.upstream.callCount(), "a departed client must not trigger a retry")
			h.requireUpstreamClosed("no drain window after the client left")
			require.NotNil(t, out.result, "output the upstream already produced must reach billing")
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens, "reported input is never replaced")
			require.Equal(t, anthropicDisconnectCacheRead, out.result.Usage.CacheReadInputTokens, "reported cache read is never replaced")
			require.False(t, out.result.Usage.CacheReadEstimated, "nothing on the input side was estimated")
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne+anthropicDisconnectTextTwo), out.result.Usage.OutputTokens)
			require.Greater(t, billAnthropicDisconnectResult(t, out.result).lastCmd.BalanceCost, 0.0)
		})
	}
}

// The client leaves before any frame arrived. The request was dispatched and
// accepted (the upstream answered 200), so the input is estimated from the request
// with 95% priced as cache read. There is no output to bill.
func TestAnthropicDisconnect_ClientLeavesBeforeAnyFrameBillsEstimatedInput(t *testing.T) {
	for _, kind := range anthropicDisconnectAllKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 30)
			h.awaitUpstream()
			h.cancel()

			out := h.await("client left before the first frame")

			requireAnthropicClientGone(t, out.err)
			h.requireUpstreamClosed("no drain window after the client left")
			require.NotNil(t, out.result, "the input of a dispatched request must reach billing")
			require.True(t, out.result.ClientDisconnect)
			total := estimateRequestInputTokens(h.upstream.lastBody())
			require.Greater(t, total, 0)
			split := splitIncompleteInput(total)
			require.Equal(t, split.Fresh, out.result.Usage.InputTokens, "Anthropic InputTokens is the fresh input only")
			require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
			require.True(t, out.result.Usage.CacheReadEstimated)
			require.Zero(t, out.result.Usage.OutputTokens)
			require.Greater(t, billAnthropicDisconnectResult(t, out.result).lastCmd.BalanceCost, 0.0)
		})
	}
}

// The client socket breaks (writes fail) while the request context is still alive.
// The old code only noted the failure and kept reading the upstream until it ended
// by itself; now the upstream is closed on the failed write.
func TestAnthropicDisconnect_ClientWriteFailureClosesUpstreamAtOnce(t *testing.T) {
	for _, kind := range anthropicDisconnectClientStreamKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, 0, 30)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne))

			h.requireUpstreamClosed("the client write failed but the upstream kept streaming")
			out := h.await("client write failed")

			require.Error(t, out.err)
			require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, out.err, &failover)
			require.Equal(t, 1, h.upstream.callCount())
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, anthropicDisconnectCacheRead, out.result.Usage.CacheReadInputTokens)
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne), out.result.Usage.OutputTokens)
		})
	}
}

// A terminal frame carrying the final usage reached the gateway before the client
// write failed. The upstream's own numbers are authoritative: no estimate may
// replace them and the request is not an incomplete one.
func TestAnthropicDisconnect_RealTerminalUsageIsNeverReplacedByEstimate(t *testing.T) {
	const reportedOutput = 19
	for _, kind := range anthropicDisconnectClientStreamKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, 0, 30)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne), anthropicDisconnectDeltaFrame(anthropicDisconnectTextTwo),
				anthropicDisconnectFinalFrames(reportedOutput))
			// A real upstream closes the connection after message_stop.
			h.closeUpstream()

			out := h.await("terminal frame already delivered")

			require.NoError(t, out.err)
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, anthropicDisconnectCacheRead, out.result.Usage.CacheReadInputTokens)
			require.Equal(t, reportedOutput, out.result.Usage.OutputTokens, "the reported output must not be replaced by an estimate")
			require.False(t, out.result.Usage.CacheReadEstimated)
		})
	}
}

// The upstream cut the connection after output was delivered but before any usage
// frame, with the client still attached. message_start only carries a provisional
// output count, so the real output the upstream produced is billed by estimate.
func TestAnthropicDisconnect_EOFWithoutTerminalAfterOutputBillsEstimatedOutput(t *testing.T) {
	for _, kind := range anthropicDisconnectAllKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 0)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne), anthropicDisconnectDeltaFrame(anthropicDisconnectTextTwo))
			h.awaitClientText(anthropicDisconnectTextOne)
			h.closeUpstream()

			out := h.await("upstream ended without a terminal frame")

			if kind == anthropicKindNative || kind == anthropicKindPassthrough {
				require.ErrorContains(t, out.err, "missing terminal event")
			}
			require.NotNil(t, out.result)
			require.False(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, anthropicDisconnectCacheRead, out.result.Usage.CacheReadInputTokens)
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne+anthropicDisconnectTextTwo), out.result.Usage.OutputTokens,
				"the provisional output of message_start must not stand in for the output that was delivered")
		})
	}
}

// With the client attached, an upstream that is cut before it produced anything is
// a replayable failure: nothing is estimated and nothing is billed.
func TestAnthropicDisconnect_UpstreamBreakBeforeAnyOutputStaysFree(t *testing.T) {
	for _, kind := range anthropicDisconnectAllKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 0)
			h.write("event: ping\ndata: {\"type\": \"ping\"}\n\n")
			h.closeUpstream()

			out := h.await("upstream ended with nothing produced")

			require.False(t, out.result != nil && out.result.ClientDisconnect)
			if out.result != nil {
				require.Equal(t, ClaudeUsage{}, out.result.Usage, "an upstream break that produced nothing must stay free")
			}
		})
	}
}

// The upstream goes silent after output was delivered and the idle watchdog gives
// up while the client is still attached.
func TestAnthropicDisconnect_IdleStallAfterOutputBillsEstimatedOutput(t *testing.T) {
	for _, kind := range anthropicDisconnectClientStreamKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 1)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne), anthropicDisconnectDeltaFrame(anthropicDisconnectTextTwo))
			h.awaitClientText(anthropicDisconnectTextOne)

			out := h.await("idle watchdog", 8*time.Second)

			require.Error(t, out.err)
			require.NotNil(t, out.result)
			require.False(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne+anthropicDisconnectTextTwo), out.result.Usage.OutputTokens)
		})
	}
}

// The client socket breaks while the upstream is quiet, so the first write that
// notices is the downstream keepalive ping. The upstream is closed on that failed
// ping too (the old code only noted it and kept reading).
func TestAnthropicDisconnect_KeepalivePingWriteFailureClosesUpstreamAtOnce(t *testing.T) {
	for _, kind := range []anthropicDisconnectKind{anthropicKindNative, anthropicKindPassthrough} {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStreamWith(t, kind, -1, 0, func(svc *GatewayService) {
				svc.cfg.Gateway.StreamKeepaliveInterval = 1
			})
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne))
			h.awaitClientText(anthropicDisconnectTextOne)
			h.recorder.breakWrites()

			select {
			case <-h.body.closed:
			case <-time.After(6 * time.Second):
				t.Fatal("the keepalive ping failed but the upstream kept streaming")
			}
			out := h.await("keepalive ping failed")

			require.ErrorContains(t, out.err, "stream usage incomplete after disconnect")
			require.NotNil(t, out.result)
			require.True(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne), out.result.Usage.OutputTokens)
		})
	}
}

// A stream that reached its terminal frame is never estimated, even when no
// message_delta reported the cumulative output: the upstream finished, and what it
// reported (here only the provisional count of message_start) stands.
func TestAnthropicDisconnect_TerminalFrameWithoutOutputUsageIsNotEstimated(t *testing.T) {
	for _, kind := range anthropicDisconnectAllKinds {
		t.Run(string(kind), func(t *testing.T) {
			h := startAnthropicDisconnectStream(t, kind, -1, 0)
			h.write(anthropicDisconnectRealStartFrame(), anthropicDisconnectBlockStartFrame(),
				anthropicDisconnectDeltaFrame(anthropicDisconnectTextOne),
				"event: message_stop\ndata: "+`{"type":"message_stop"}`+"\n\n")
			h.closeUpstream()

			out := h.await("terminal frame without output usage")

			require.NoError(t, out.err)
			require.NotNil(t, out.result)
			require.False(t, out.result.ClientDisconnect)
			require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
			require.Equal(t, 1, out.result.Usage.OutputTokens, "a finished stream keeps what the upstream reported")
			require.False(t, out.result.Usage.CacheReadEstimated)
		})
	}
}
