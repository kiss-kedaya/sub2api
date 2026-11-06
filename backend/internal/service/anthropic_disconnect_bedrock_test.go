//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Bedrock streams AWS EventStream frames whose payload is {"bytes": base64(event)}.
// The Bedrock path never detaches the upstream context from the client, so a real
// transport fails the body read when the client leaves; the stub below does the
// same, and the gateway must still stop and bill by estimate.

func bedrockEventStreamFrame(event string) []byte {
	payload := []byte(`{"bytes":"` + base64.StdEncoding.EncodeToString([]byte(event)) + `"}`)
	var headers bytes.Buffer
	for _, h := range [][2]string{{":event-type", "chunk"}, {":message-type", "event"}} {
		_ = headers.WriteByte(byte(len(h[0])))
		_, _ = headers.WriteString(h[0])
		_ = headers.WriteByte(7)
		_ = binary.Write(&headers, binary.BigEndian, uint16(len(h[1])))
		_, _ = headers.WriteString(h[1])
	}
	var prelude bytes.Buffer
	_ = binary.Write(&prelude, binary.BigEndian, uint32(12+headers.Len()+len(payload)+4))
	_ = binary.Write(&prelude, binary.BigEndian, uint32(headers.Len()))
	table := crc32.MakeTable(crc32.IEEE)
	var frame bytes.Buffer
	_, _ = frame.Write(prelude.Bytes())
	_ = binary.Write(&frame, binary.BigEndian, crc32.Checksum(prelude.Bytes(), table))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payload)
	_ = binary.Write(&frame, binary.BigEndian, crc32.Checksum(frame.Bytes(), table))
	return frame.Bytes()
}

func bedrockFrames(events ...string) string {
	var out bytes.Buffer
	for _, event := range events {
		_, _ = out.Write(bedrockEventStreamFrame(event))
	}
	return out.String()
}

func bedrockStartEvent() string {
	return fmt.Sprintf(`{"type":"message_start","message":{"id":"msg_b","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":1}}}`,
		anthropicDisconnectInput, anthropicDisconnectCacheRead)
}

const bedrockBlockStartEvent = `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`

func bedrockDeltaEvent(text string) string {
	return `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + text + `"}}`
}

func bedrockFinalEvents(outputTokens int) []string {
	return []string{
		`{"type":"content_block_stop","index":0}`,
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, outputTokens),
		`{"type":"message_stop"}`,
	}
}

type bedrockStreamHarness struct {
	t        *testing.T
	cancel   context.CancelFunc
	recorder *anthropicDisconnectRecorder
	upstream *anthropicDisconnectUpstream
	body     *anthropicDisconnectBody
	pipeW    *io.PipeWriter
	done     <-chan anthropicDisconnectOutcome
}

// startBedrockStream starts a Bedrock stream forward. Like a real transport, the
// stub fails the body read with the context error once the request context ends.
func startBedrockStream(t *testing.T, failClientAfter int) *bedrockStreamHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	resp, body, pw := newAnthropicDisconnectSSEResponse()
	resp.Header = http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}, "X-Amzn-Requestid": {"req_bedrock_disconnect"}}
	stop := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() { close(stop) })
		_ = pw.Close()
	})
	upstream := newAnthropicDisconnectUpstream(func(req *http.Request) (*http.Response, error) {
		go func() {
			select {
			case <-req.Context().Done():
				_ = pw.CloseWithError(req.Context().Err())
			case <-stop:
			}
		}()
		return resp, nil
	})
	recorder := newAnthropicDisconnectRecorder(failClientAfter)
	svc := newAnthropicDisconnectService(upstream, 0)
	return &bedrockStreamHarness{
		t: t, cancel: cancel, recorder: recorder, upstream: upstream, body: body, pipeW: pw,
		done: startAnthropicDisconnectForward(t, ctx, anthropicKindBedrock, svc, recorder),
	}
}

func (h *bedrockStreamHarness) write(events ...string) {
	h.t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := io.WriteString(h.pipeW, bedrockFrames(events...))
		errCh <- err
	}()
	select {
	case err := <-errCh:
		require.NoError(h.t, err)
	case <-time.After(3 * time.Second):
		h.t.Fatal("the gateway did not read the upstream frames")
	}
}

func (h *bedrockStreamHarness) await(what string) anthropicDisconnectOutcome {
	h.t.Helper()
	return awaitAnthropicDisconnect(h.t, h.done, what)
}

func requireBedrockClientDisconnect(t *testing.T, out anthropicDisconnectOutcome) {
	t.Helper()
	require.NoError(t, out.err, "a departed Bedrock client keeps the success path of the handler")
	require.NotNil(t, out.result)
	require.True(t, out.result.ClientDisconnect)
	require.True(t, out.result.Stream)
}

// The client leaves after it received output and the upstream sent no usage after
// message_start (which only carries a provisional output of 1). The output the
// upstream really produced is billed by estimate; the real input is kept.
func TestAnthropicDisconnect_BedrockClientLeavesMidStreamBillsEstimatedOutput(t *testing.T) {
	h := startBedrockStream(t, -1)
	h.write(bedrockStartEvent(), bedrockBlockStartEvent, bedrockDeltaEvent(anthropicDisconnectTextOne), bedrockDeltaEvent(anthropicDisconnectTextTwo))
	h.recorder.waitForText(t, anthropicDisconnectTextOne)
	h.cancel()

	out := h.await("client left mid stream")

	requireBedrockClientDisconnect(t, out)
	require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
	require.Equal(t, anthropicDisconnectCacheRead, out.result.Usage.CacheReadInputTokens)
	require.False(t, out.result.Usage.CacheReadEstimated)
	require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne+anthropicDisconnectTextTwo), out.result.Usage.OutputTokens,
		"the provisional output of message_start must not stand in for the output that was delivered")
	require.Greater(t, billAnthropicDisconnectResult(t, out.result).lastCmd.BalanceCost, 0.0)
}

// The client socket breaks (writes fail) while the request context is alive. The
// upstream is closed on the failed write instead of being read to the end.
func TestAnthropicDisconnect_BedrockClientWriteFailureClosesUpstreamAtOnce(t *testing.T) {
	h := startBedrockStream(t, 0)
	h.write(bedrockStartEvent(), bedrockBlockStartEvent, bedrockDeltaEvent(anthropicDisconnectTextOne))

	select {
	case <-h.body.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the client write failed but the upstream kept streaming")
	}
	out := h.await("client write failed")

	requireBedrockClientDisconnect(t, out)
	require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
	require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne), out.result.Usage.OutputTokens)
}

// The client leaves before any frame arrived. The request was dispatched and the
// upstream answered 200, so its input is billed from the estimate.
func TestAnthropicDisconnect_BedrockClientLeavesBeforeAnyFrameBillsEstimatedInput(t *testing.T) {
	h := startBedrockStream(t, -1)
	select {
	case <-h.upstream.called:
	case <-time.After(3 * time.Second):
		t.Fatal("the upstream request never arrived")
	}
	h.cancel()

	out := h.await("client left before the first frame")

	requireBedrockClientDisconnect(t, out)
	split := splitIncompleteInput(estimateRequestInputTokens(anthropicKindBedrock.requestBody()))
	require.Greater(t, split.Total, 0)
	require.Equal(t, split.Fresh, out.result.Usage.InputTokens)
	require.Equal(t, split.CacheRead, out.result.Usage.CacheReadInputTokens)
	require.True(t, out.result.Usage.CacheReadEstimated)
	require.Zero(t, out.result.Usage.OutputTokens)
	require.Greater(t, billAnthropicDisconnectResult(t, out.result).lastCmd.BalanceCost, 0.0)
}

// The upstream cut the connection after output was delivered but before the
// terminal frame, with the client still attached.
func TestAnthropicDisconnect_BedrockEOFWithoutTerminalAfterOutputBillsEstimatedOutput(t *testing.T) {
	h := startBedrockStream(t, -1)
	h.write(bedrockStartEvent(), bedrockBlockStartEvent, bedrockDeltaEvent(anthropicDisconnectTextOne), bedrockDeltaEvent(anthropicDisconnectTextTwo))
	h.recorder.waitForText(t, anthropicDisconnectTextOne)
	require.NoError(t, h.pipeW.Close())

	out := h.await("upstream ended without a terminal frame")

	require.NotNil(t, out.result)
	require.False(t, out.result.ClientDisconnect)
	require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
	require.Equal(t, estimateTokensForText(anthropicDisconnectTextOne+anthropicDisconnectTextTwo), out.result.Usage.OutputTokens)
}

// A terminal frame that carried the final usage is authoritative: no estimate may
// replace it, even when the client write failed.
func TestAnthropicDisconnect_BedrockRealTerminalUsageIsNeverReplacedByEstimate(t *testing.T) {
	const reportedOutput = 19
	h := startBedrockStream(t, 0)
	events := append([]string{bedrockStartEvent(), bedrockBlockStartEvent,
		bedrockDeltaEvent(anthropicDisconnectTextOne), bedrockDeltaEvent(anthropicDisconnectTextTwo)}, bedrockFinalEvents(reportedOutput)...)
	h.write(events...)
	require.NoError(t, h.pipeW.Close())

	out := h.await("terminal frame already delivered")

	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
	require.Equal(t, reportedOutput, out.result.Usage.OutputTokens, "the reported output must not be replaced by an estimate")
	require.False(t, out.result.Usage.CacheReadEstimated)
}

// A Bedrock stream that reached message_stop is never estimated, even when no
// message_delta reported the cumulative output.
func TestAnthropicDisconnect_BedrockTerminalFrameWithoutOutputUsageIsNotEstimated(t *testing.T) {
	h := startBedrockStream(t, -1)
	h.write(bedrockStartEvent(), bedrockBlockStartEvent, bedrockDeltaEvent(anthropicDisconnectTextOne), `{"type":"message_stop"}`)
	require.NoError(t, h.pipeW.Close())

	out := h.await("terminal frame without output usage")

	require.NoError(t, out.err)
	require.NotNil(t, out.result)
	require.False(t, out.result.ClientDisconnect)
	require.Equal(t, anthropicDisconnectInput, out.result.Usage.InputTokens)
	require.Equal(t, 1, out.result.Usage.OutputTokens, "a finished stream keeps what the upstream reported")
}

// An upstream that is cut before it produced anything is not estimated.
func TestAnthropicDisconnect_BedrockUpstreamBreakBeforeAnyOutputStaysFree(t *testing.T) {
	h := startBedrockStream(t, -1)
	require.NoError(t, h.pipeW.Close())

	out := h.await("upstream ended with nothing produced")

	require.NotNil(t, out.result)
	require.False(t, out.result.ClientDisconnect)
	require.Equal(t, ClaudeUsage{}, out.result.Usage, "an upstream break that produced nothing must stay free")
	require.False(t, strings.Contains(fmt.Sprint(out.err), "incomplete"))
}
