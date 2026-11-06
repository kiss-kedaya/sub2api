//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
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
			name = "response_after_cancel"
		}
		t.Run(name, func(t *testing.T) {
			clientCtx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("test client stopped")
			resp, body, writer := newLifecycleTrackingResponse()
			defer func() { _ = body.Close() }()
			defer func() { _ = writer.Close() }()
			svc := &OpenAIGatewayService{
				cfg: &config.Config{},
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

func startHeaderCancelForward(t *testing.T, ctx context.Context, stream bool, handler http.HandlerFunc) (*headerCancelHTTPUpstream, <-chan headerCancelForwardResult) {
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
	body := []byte(fmt.Sprintf(`{"model":"gpt-5.4","instructions":"answer","input":"hello","stream":%t}`, stream))
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
