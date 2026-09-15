//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type headerCancelHTTPUpstream struct {
	client *http.Client
	target *url.URL
	calls  atomic.Int32
}

func (u *headerCancelHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	localReq := req.Clone(req.Context())
	localReq.URL.Scheme = u.target.Scheme
	localReq.URL.Host = u.target.Host
	return u.client.Do(localReq)
}

func (u *headerCancelHTTPUpstream) DoWithTLS(req *http.Request, proxy string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, accountID, concurrency)
}

type headerCancelForwardResult struct {
	result *OpenAIForwardResult
	err    error
}

type headerCancelUpstreamFunc func(*http.Request) (*http.Response, error)

func (f headerCancelUpstreamFunc) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return f(req)
}

func (f headerCancelUpstreamFunc) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f(req)
}

func TestOpenAIHeaderCancel_ClosesDiscardedResponse(t *testing.T) {
	for _, timedOut := range []bool{false, true} {
		name := "response_and_error"
		if timedOut {
			name = "response_after_cancel_budget"
		}
		t.Run(name, func(t *testing.T) {
			clientCtx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("test client stopped")
			resp, body, writer := newLifecycleTrackingResponse()
			defer func() { _ = body.Close() }()
			defer func() { _ = writer.Close() }()
			svc := &OpenAIGatewayService{
				cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}},
				httpUpstream: headerCancelUpstreamFunc(func(req *http.Request) (*http.Response, error) {
					if !timedOut {
						return resp, cause
					}
					cancel(cause)
					select {
					case <-req.Context().Done():
						return resp, nil
					case <-time.After(2 * time.Second):
						return resp, errors.New("header cancellation was not delivered")
					}
				}),
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			_, err := svc.doOpenAIUpstreamWithHeaderCancel(clientCtx, req, "", &Account{ID: 1})
			select {
			case <-body.closed:
			default:
				t.Error("helper returned a discarded response without closing its body")
			}
			require.ErrorIs(t, err, cause)
		})
	}
}

func startHeaderCancelForward(t *testing.T, ctx context.Context, handler http.HandlerFunc) (*headerCancelHTTPUpstream, <-chan headerCancelForwardResult) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() {
		server.CloseClientConnections()
		server.Close()
	})
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	upstream := &headerCancelHTTPUpstream{client: server.Client(), target: target}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 1,
			MaxLineSize:               defaultMaxLineSize,
		}},
		httpUpstream: upstream,
	}
	body := []byte(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":true}`)
	c, _ := newOpenAIPartialUsageContext(t, body)
	c.Request = c.Request.WithContext(ctx)
	done := make(chan headerCancelForwardResult, 1)
	go func() {
		// The handler's original context must be observed even if Forward's
		// execution context is detached by its caller.
		result, err := svc.Forward(context.WithoutCancel(ctx), c, newOpenAIPartialUsageAccount(false), body)
		done <- headerCancelForwardResult{result, err}
	}()
	return upstream, done
}

func TestOpenAIForwardHeaderCancel_BlocksUntilClientCancelBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	closed := make(chan struct{})
	upstream, done := startHeaderCancelForward(t, ctx, func(w http.ResponseWriter, r *http.Request) {
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
	select {
	case got := <-done:
		t.Fatalf("active request must not acquire a header deadline: %v", got.err)
	case <-time.After(1200 * time.Millisecond):
	}
	canceledAt := time.Now()
	cancel()
	select {
	case <-closed:
		t.Fatal("upstream was canceled without a drain budget")
	case <-time.After(150 * time.Millisecond):
	}
	select {
	case got := <-done:
		require.ErrorIs(t, got.err, context.Canceled)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(got.err, &failover))
		require.Nil(t, got.result)
		require.GreaterOrEqual(t, time.Since(canceledAt), 900*time.Millisecond)
		require.Equal(t, int32(1), upstream.calls.Load())
	case <-time.After(2 * time.Second):
		t.Fatal("canceled client still holds the response-header wait beyond the configured budget")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("header cancellation did not close the upstream HTTP connection")
	}
}

func TestOpenAIForwardHeaderCancel_HeadersHandOffToUsageDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	upstream, done := startHeaderCancelForward(t, ctx, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(arrived)
		<-ctx.Done()
		timer := time.NewTimer(600 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n")
		w.(http.Flusher).Flush()
		// Terminal arrives beyond the old header-cancel deadline, but within
		// the SSE handler's existing one-second drain window after headers.
		timer.Reset(650 * time.Millisecond)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":13,\"output_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":2}}}}\n\n")
	})
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not arrive")
	}
	cancel()
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
		require.True(t, got.result.ClientDisconnect)
		require.Equal(t, 13, got.result.Usage.InputTokens)
		require.Equal(t, 4, got.result.Usage.OutputTokens)
		require.Equal(t, 2, got.result.Usage.CacheReadInputTokens)
		require.Equal(t, int32(1), upstream.calls.Load())
	case <-time.After(3 * time.Second):
		t.Fatal("terminal usage did not finish the request")
	}
}

func TestOpenAIForwardHeaderCancel_ErrorResponseDoesNotFailover(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	arrived := make(chan struct{})
	upstream, done := startHeaderCancelForward(t, ctx, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(arrived)
		<-ctx.Done()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"service unavailable"}}`)
	})
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not arrive")
	}
	cancel()
	select {
	case got := <-done:
		require.ErrorIs(t, got.err, context.Canceled)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(got.err, &failover))
		require.Equal(t, int32(1), upstream.calls.Load())
	case <-time.After(2 * time.Second):
		t.Fatal("canceled error response did not return")
	}
}
