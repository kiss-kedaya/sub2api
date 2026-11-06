//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The client leaves after headers arrived: the buffered body is closed at once, no
// drain window. A sync request is billed for its input, an error status is not.
func TestOpenAILifecycleReview_ClientLeavingAfterHeadersClosesBufferedBodyAtOnce(t *testing.T) {
	for _, errorResponse := range []bool{false, true} {
		name, requestBody := "nonstream", `{"model":"gpt-5.4","input":"hello","stream":false}`
		if errorResponse {
			name, requestBody = "http_error", `{"model":"gpt-5.4","input":"hello","stream":true}`
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := []byte(requestBody)
			c, _ := newOpenAIPartialUsageContext(t, body)
			c.Request = c.Request.WithContext(ctx)
			resp, tracked, pw := newLifecycleTrackingResponse()
			if errorResponse {
				resp.StatusCode = http.StatusServiceUnavailable
			}
			upstream := &httpUpstreamRecorder{resp: resp}
			svc := newOpenAIPartialUsageService(upstream)
			done := make(chan headerCancelForwardResult, 1)
			go func() {
				result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
				done <- headerCancelForwardResult{result, err}
			}()
			defer func() { _ = tracked.Close(); _ = pw.Close() }()
			select {
			case <-tracked.readStarted:
			case <-time.After(2 * time.Second):
				_ = tracked.Close()
				cancel()
				<-done
				t.Fatal("Forward did not hand headers off to the body reader")
			}
			select {
			case got := <-done:
				t.Fatalf("an active body read returned on its own: %v", got.err)
			case <-time.After(300 * time.Millisecond):
			}
			canceledAt := time.Now()
			cancel()
			select {
			case got := <-done:
				require.Less(t, time.Since(canceledAt), 800*time.Millisecond)
				require.ErrorIs(t, got.err, context.Canceled)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(got.err, &failover))
				require.Len(t, upstream.requests, 1)
				if errorResponse {
					require.Nil(t, got.result, "an upstream error status is never billed")
				} else {
					require.NotNil(t, got.result, "the input of a dispatched sync request is billed")
					require.True(t, got.result.ClientDisconnect)
					require.Greater(t, got.result.Usage.InputTokens, 0)
				}
				select {
				case <-tracked.closed:
				default:
					t.Fatal("the canceled body reader left the response open")
				}
			case <-time.After(2 * time.Second):
				_ = tracked.Close()
				<-done
				t.Fatal("client cancellation left the buffered body read blocked")
			}
		})
	}
}

// The client leaves while the upstream is still answering: the original canceled
// request prevents the invalid-response failover, and the dispatched request is
// billed for its input.
func TestOpenAILifecycleReview_DetachedNonstreamCancellationSkipsInvalidFailover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	c, recorder := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	svc := &OpenAIGatewayService{
		cfg: &config.Config{},
		httpUpstream: headerCancelUpstreamFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("invalid response"))}, nil
		}),
	}
	result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Greater(t, result.Usage.InputTokens, 0)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "the original canceled request must prevent invalid-response failover")
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, recorder.Body.String())
}

// Once the client leaves the body is closed at once, so a usage report that only
// shows up afterwards is never read. The input is billed from the estimate.
func TestOpenAILifecycleReview_CanceledBufferedSuccessIsBilledFromTheEstimate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"gpt-5.4","input":"hello","stream":false}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	resp, tracked, pw := newLifecycleTrackingResponse()
	defer func() { _ = tracked.Close(); _ = pw.Close() }()
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := newOpenAIPartialUsageService(upstream)
	lateWrite := make(chan error, 1)
	go func() {
		defer func() { _ = pw.Close() }()
		<-tracked.readStarted
		cancel()
		<-tracked.closed
		_, err := io.WriteString(pw, `{"id":"resp_final","status":"completed","output":[],"usage":{"input_tokens":13,"output_tokens":7}}`)
		lateWrite <- err
	}()
	done := make(chan headerCancelForwardResult, 1)
	go func() {
		result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
		done <- headerCancelForwardResult{result, err}
	}()
	var got headerCancelForwardResult
	select {
	case got = <-done:
	case <-time.After(3 * time.Second):
		_ = tracked.Close()
		t.Fatal("client cancellation did not release the body read")
	}
	select {
	case err := <-lateWrite:
		require.Error(t, err, "the late usage report must find the body already closed")
	case <-time.After(2 * time.Second):
		t.Fatal("the producer never finished")
	}
	require.ErrorIs(t, got.err, context.Canceled)
	require.NotNil(t, got.result)
	require.True(t, got.result.ClientDisconnect)
	require.Greater(t, got.result.Usage.InputTokens, 0)
	require.NotEqual(t, 13, got.result.Usage.InputTokens, "the late real usage must not be read")
	require.Zero(t, got.result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 1)
}
