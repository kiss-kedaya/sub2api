//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func awaitClientGone(t *testing.T, done <-chan headerCancelForwardResult, within time.Duration) headerCancelForwardResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(within):
		t.Fatal("Forward did not return after the client left")
		return headerCancelForwardResult{}
	}
}

func requireClientGoneNotFailover(t *testing.T, err error) {
	t.Helper()
	require.ErrorIs(t, err, context.Canceled)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "a client that left must never trigger failover")
}

// The client leaves after the request was dispatched and before any response: the
// upstream is canceled at once and the input is billed from the estimate (95% as
// cache read), sync or stream. No output is invented.
func TestOpenAIForward_ClientLeavesAfterDispatchIsBilledForInput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "sync"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			arrived, closed := make(chan struct{}), make(chan struct{})
			upstream, done := startHeaderCancelForward(t, ctx, stream, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(arrived)
				<-r.Context().Done()
				close(closed)
			})
			select {
			case <-arrived:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request did not arrive")
			}
			canceledAt := time.Now()
			cancel()
			got := awaitClientGone(t, done, 2*time.Second)

			require.Less(t, time.Since(canceledAt), 800*time.Millisecond, "no drain window may apply")
			requireClientGoneNotFailover(t, got.err)
			require.Equal(t, int32(1), upstream.calls.Load())
			require.NotNil(t, got.result, "a dispatched request must be billed for its input")
			require.True(t, got.result.ClientDisconnect)
			require.Equal(t, stream, got.result.Stream)
			require.Greater(t, got.result.Usage.InputTokens, 0)
			require.Zero(t, got.result.Usage.OutputTokens)
			require.Equal(t, incompleteCacheReadTokens(got.result.Usage.InputTokens), got.result.Usage.CacheReadInputTokens)
			require.True(t, got.result.Usage.CacheReadEstimated)
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("the upstream connection was not closed")
			}
			_, billing := billIncompleteStreamResult(t, got.result)
			require.NotNil(t, billing.lastCmd)
			require.Greater(t, billing.lastCmd.BalanceCost, 0.0)
		})
	}
}

// A client that was already gone before the request left the gateway costs nothing.
func TestOpenAIForward_ClientGoneBeforeSendIsNotBilled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	upstream, done := startHeaderCancelForward(t, ctx, false, func(http.ResponseWriter, *http.Request) {
		t.Error("the upstream must not be called")
	})
	got := awaitClientGone(t, done, 2*time.Second)

	requireClientGoneNotFailover(t, got.err)
	require.Nil(t, got.result)
	require.Zero(t, upstream.calls.Load())
}

// The upstream answered with an error status and the client left while the error
// body was being read: the upstream rejected the request, so nothing is billed.
func TestOpenAIForward_ClientLeavesWhileReadingErrorBodyIsNotBilled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	_, done := startHeaderCancelForward(t, ctx, false, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.(http.Flusher).Flush()
		close(arrived)
		<-r.Context().Done()
	})
	<-arrived
	time.Sleep(300 * time.Millisecond)
	cancel()
	got := awaitClientGone(t, done, 2*time.Second)

	requireClientGoneNotFailover(t, got.err)
	require.Nil(t, got.result)
}

// Headers arrived, then the client left while the sync body was being read: the
// body is closed at once and the input is billed from the estimate.
func TestOpenAIForward_ClientLeavesWhileReadingSyncBodyIsBilledForInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	_, done := startHeaderCancelForward(t, ctx, false, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_partial","status":"in_progress",`)
		w.(http.Flusher).Flush()
		close(arrived)
		<-r.Context().Done()
	})
	<-arrived
	time.Sleep(300 * time.Millisecond)
	canceledAt := time.Now()
	cancel()
	got := awaitClientGone(t, done, 2*time.Second)

	require.Less(t, time.Since(canceledAt), 800*time.Millisecond)
	requireClientGoneNotFailover(t, got.err)
	require.NotNil(t, got.result)
	require.True(t, got.result.ClientDisconnect)
	require.Greater(t, got.result.Usage.InputTokens, 0)
	require.Zero(t, got.result.Usage.OutputTokens)
	require.True(t, got.result.Usage.CacheReadEstimated)
}
