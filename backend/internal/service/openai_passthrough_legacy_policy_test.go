//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The shared passthrough response handlers keep their historic client-gone
// behaviour under the zero passthroughResponseOptions: keep reading the upstream
// until it answers so its usage can be billed. The Excel relay still depends on
// that (TestExcelBPSGatewayCancelledClientStillRecordsAcceptedUsage) and is
// migrated by its own change; forwardOpenAIPassthrough opts into closing the
// upstream instead. These two tests pin the contract the relay relies on.

func TestOpenAIPassthroughLegacyPolicy_StreamKeepsDrainingForUsageAfterClientLeaves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _ := newPassthroughDisconnectContext(t, ctx, []byte(passthroughDisconnectStreamBody))
	resp, tracked, pw := newLifecycleTrackingResponse()
	resp.Header = http.Header{"Content-Type": []string{"text/event-stream"}}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = tracked.Close()
	})
	svc := newPassthroughDisconnectService(&httpUpstreamRecorder{})

	done := make(chan disconnectOutcome, 1)
	var result *openaiStreamingResultPassthrough
	go func() {
		var err error
		result, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, newOpenAIPartialUsageAccount(true), time.Now(), "gpt-5.1", "gpt-5.1")
		done <- disconnectOutcome{err: err}
	}()
	select {
	case <-tracked.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never started reading the upstream body")
	}
	cancel()

	select {
	case <-done:
		t.Fatal("the legacy policy must keep reading the upstream after the client left")
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case <-tracked.closed:
		t.Fatal("the legacy policy must not close the upstream when the client leaves")
	default:
	}
	terminal := `data: {"type":"response.completed","response":{"id":"resp_done","usage":{"input_tokens":13,"output_tokens":4}}}` + "\n\n"
	h := &disconnectPipeHarness{t: t, pipeW: pw}
	h.write(terminal)
	require.NoError(t, pw.Close())

	select {
	case out := <-done:
		require.NoError(t, out.err)
		require.NotNil(t, result)
		require.True(t, result.clientDisconnect)
		require.Equal(t, 13, result.usage.InputTokens)
		require.Equal(t, 4, result.usage.OutputTokens)
		require.False(t, result.usage.CacheReadEstimated)
	case <-time.After(3 * time.Second):
		t.Fatal("the handler did not finish after the terminal frame")
	}
}

func TestOpenAIPassthroughLegacyPolicy_BufferedKeepsReadingForUsageAfterClientLeaves(t *testing.T) {
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
		result, err := svc.handleNonStreamingResponsePassthrough(ctx, resp, c, newOpenAIPartialUsageAccount(true), "gpt-5.1", "gpt-5.1")
		done <- outcome{result: result, err: err}
	}()
	select {
	case <-tracked.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the handler never started reading the upstream body")
	}
	cancel()

	select {
	case <-done:
		t.Fatal("the legacy policy must keep reading the upstream after the client left")
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case <-tracked.closed:
		t.Fatal("the legacy policy must not close the upstream when the client leaves")
	default:
	}
	h := &disconnectPipeHarness{t: t, pipeW: pw}
	h.write(`{"id":"resp_done","object":"response","status":"completed","output":[],"usage":{"input_tokens":11,"output_tokens":4}}`)
	require.NoError(t, pw.Close())

	select {
	case out := <-done:
		require.NoError(t, out.err)
		require.NotNil(t, out.result)
		require.Equal(t, 11, out.result.usage.InputTokens)
		require.Equal(t, 4, out.result.usage.OutputTokens)
	case <-time.After(3 * time.Second):
		t.Fatal("the handler did not finish after the body was complete")
	}
}
