package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type recordedUpstreamAttempt struct {
	ctx       context.Context
	accountID int64
	status    int
	err       error
}

type upstreamAttemptRecorder struct {
	mu       sync.Mutex
	attempts []recordedUpstreamAttempt
}

func (r *upstreamAttemptRecorder) ObserveUpstreamAttempt(ctx context.Context, accountID int64, statusCode int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, recordedUpstreamAttempt{ctx: ctx, accountID: accountID, status: statusCode, err: err})
}

func (r *upstreamAttemptRecorder) snapshot() []recordedUpstreamAttempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedUpstreamAttempt(nil), r.attempts...)
}

// observableOf 取出真实传输层的观察者注册接口（断言失败直接让测试失败，不能悄悄跳过）。
func observableOf(t *testing.T, up service.HTTPUpstream) service.UpstreamAttemptObservable {
	t.Helper()
	observable, ok := up.(service.UpstreamAttemptObservable)
	require.True(t, ok, "the real transport must accept an attempt observer")
	return observable
}

func newObservedUpstream(t *testing.T) (service.HTTPUpstream, *upstreamAttemptRecorder) {
	t.Helper()
	up := NewHTTPUpstream(nil)
	rec := &upstreamAttemptRecorder{}
	observableOf(t, up).SetUpstreamAttemptObserver(rec)
	return up, rec
}

func gatewayRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	ctx := context.WithValue(t.Context(), ctxkey.UserID, int64(9))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	return req
}

func TestHTTPUpstreamObservesEveryAttemptExactlyOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	up, rec := newObservedUpstream(t)

	// Do
	resp, err := up.Do(gatewayRequest(t, srv.URL), "", 7, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, rec.snapshot(), 1)

	// DoWithTLS without a profile delegates to Do: still exactly one more observation, not two.
	resp, err = up.DoWithTLS(gatewayRequest(t, srv.URL), "", 8, 1, nil)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, rec.snapshot(), 2)

	// Plain HTTP with a profile also delegates to Do.
	resp, err = up.DoWithTLS(gatewayRequest(t, srv.URL), "", 9, 1, &tlsfingerprint.Profile{Name: "unused-for-http"})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	got := rec.snapshot()
	require.Len(t, got, 3, "each upstream attempt must be observed exactly once")
	for i, want := range []int64{7, 8, 9} {
		require.Equal(t, want, got[i].accountID)
		require.Equal(t, http.StatusBadGateway, got[i].status)
		require.NoError(t, got[i].err)
		require.Equal(t, int64(9), got[i].ctx.Value(ctxkey.UserID), "the observer must see the request context (gateway marker)")
	}
}

func TestHTTPUpstreamObservesTransportErrors(t *testing.T) {
	// 取一个确定没人监听的端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	up, rec := newObservedUpstream(t)

	resp, err := up.Do(gatewayRequest(t, "http://"+addr), "", 11, 1)
	require.Error(t, err)
	require.Nil(t, resp)

	// 带 TLS 指纹的 https 请求有自己的发送路径：同样只观察一次。
	resp, err = up.DoWithTLS(gatewayRequest(t, "https://"+addr), "", 12, 1, &tlsfingerprint.Profile{Name: "test"})
	require.Error(t, err)
	require.Nil(t, resp)

	got := rec.snapshot()
	require.Len(t, got, 2)
	require.Equal(t, int64(11), got[0].accountID)
	require.Zero(t, got[0].status)
	require.Error(t, got[0].err)
	require.Equal(t, int64(12), got[1].accountID)
	require.Zero(t, got[1].status)
	require.Error(t, got[1].err)
}

func TestHTTPUpstreamSuccessIsObservedToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)
	up, rec := newObservedUpstream(t)
	resp, err := up.Do(gatewayRequest(t, srv.URL), "", 5, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	got := rec.snapshot()
	require.Len(t, got, 1)
	require.Equal(t, http.StatusOK, got[0].status)
}

type panickingObserver struct{}

func (panickingObserver) ObserveUpstreamAttempt(context.Context, int64, int, error) { panic("boom") }

func TestHTTPUpstreamObserverCannotBreakForwarding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(srv.Close)

	up := NewHTTPUpstream(nil)
	observableOf(t, up).SetUpstreamAttemptObserver(panickingObserver{})
	require.NotPanics(t, func() {
		resp, err := up.Do(gatewayRequest(t, srv.URL), "", 1, 1)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	})

	// 没有注册观察者 / 注销观察者：照常工作
	observableOf(t, up).SetUpstreamAttemptObserver(nil)
	resp, err := up.Do(gatewayRequest(t, srv.URL), "", 1, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	resp, err = NewHTTPUpstream(nil).Do(gatewayRequest(t, srv.URL), "", 1, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// bodyAwareObserver 同时实现 service.UpstreamAttemptBodyObserver：只对指定状态码要响应体前缀。
type bodyAwareObserver struct {
	*upstreamAttemptRecorder
	wantStatus int
	mu         sync.Mutex
	prefixes   [][]byte
}

func (o *bodyAwareObserver) WantsAttemptBody(_ context.Context, _ int64, status int) bool {
	return status == o.wantStatus
}

func (o *bodyAwareObserver) ObserveUpstreamAttemptBody(_ context.Context, _ int64, _ int, _ error, prefix []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.prefixes = append(o.prefixes, append([]byte(nil), prefix...))
}

func TestHTTPUpstreamPeeksTheBodyOnlyWhenAskedAndHandsItBackIntact(t *testing.T) {
	small := []byte(`{"error":{"code":"content_policy_violation"}}`)
	big := bytes.Repeat([]byte("x"), 3*service.UpstreamAttemptBodyPeekLimit+17)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(small)
		case "/big":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(big)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(small)
		}
	}))
	t.Cleanup(srv.Close)

	up := NewHTTPUpstream(nil)
	obs := &bodyAwareObserver{upstreamAttemptRecorder: &upstreamAttemptRecorder{}, wantStatus: http.StatusForbidden}
	observableOf(t, up).SetUpstreamAttemptObserver(obs)

	read := func(path string) []byte {
		resp, err := up.Do(gatewayRequest(t, srv.URL+path), "", 1, 1)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return got
	}
	require.Equal(t, small, read("/small"), "the caller still sees the whole body")
	require.Equal(t, big, read("/big"), "a body larger than the peek limit is handed back whole too")
	require.Equal(t, small, read("/ok"))

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Len(t, obs.prefixes, 2, "only the responses the observer asked about were peeked")
	require.Equal(t, small, obs.prefixes[0])
	require.Len(t, obs.prefixes[1], service.UpstreamAttemptBodyPeekLimit, "the peek is bounded")
	require.Equal(t, big[:service.UpstreamAttemptBodyPeekLimit], obs.prefixes[1])
	require.Len(t, obs.snapshot(), 1, "the 200 went through the plain observation path")
}

type erroringBody struct {
	data []byte
	err  error
}

func (b *erroringBody) Read(p []byte) (int, error) {
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	return 0, b.err
}

func (b *erroringBody) Close() error { return nil }

func TestPeekResponseBodyKeepsReadErrorsVisibleToTheCaller(t *testing.T) {
	boom := errors.New("connection reset by peer")
	resp := &http.Response{Body: &erroringBody{data: []byte("partial"), err: boom}}
	prefix := peekResponseBody(resp, service.UpstreamAttemptBodyPeekLimit)
	require.Equal(t, []byte("partial"), prefix)

	got, err := io.ReadAll(resp.Body)
	require.Equal(t, []byte("partial"), got, "the bytes consumed by the peek are handed back")
	require.ErrorIs(t, err, boom, "a failing body keeps failing for the caller")
}

func TestPeekResponseBodyIgnoresNilEmptyAndZeroLimit(t *testing.T) {
	require.Nil(t, peekResponseBody(nil, 10))
	require.Nil(t, peekResponseBody(&http.Response{}, 10))
	empty := &http.Response{Body: http.NoBody}
	require.Nil(t, peekResponseBody(empty, 10))
	require.True(t, empty.Body == http.NoBody, "an empty body is left untouched")
	body := io.NopCloser(strings.NewReader("abc"))
	zero := &http.Response{Body: body}
	require.Nil(t, peekResponseBody(zero, 0))
	require.True(t, zero.Body == body, "a zero limit reads nothing")
}

func gatewayRequestForAccount(t *testing.T, url string, accountID int64, platform string) *http.Request {
	t.Helper()
	ctx := context.WithValue(t.Context(), ctxkey.UserID, int64(9))
	ctx = context.WithValue(ctx, ctxkey.AccountID, accountID)
	ctx = context.WithValue(ctx, ctxkey.Platform, platform)
	ctx = context.WithValue(ctx, ctxkey.AccountName, "acct")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	return req
}

func gzipBytesForUpstreamTest(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestHTTPUpstreamPeeks400BodyAndPreservesItForCaller(t *testing.T) {
	small := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	big := append(append([]byte(nil), small...), bytes.Repeat([]byte("y"), 2*service.UpstreamAttemptBodyPeekLimit+31)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(small)
		case "/big":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(big)
		case "/empty":
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(small)
		}
	}))
	t.Cleanup(srv.Close)

	up := NewHTTPUpstream(nil)
	obs := &bodyAwareObserver{upstreamAttemptRecorder: &upstreamAttemptRecorder{}, wantStatus: http.StatusBadRequest}
	observableOf(t, up).SetUpstreamAttemptObserver(obs)

	read := func(path string) (int, []byte) {
		resp, err := up.Do(gatewayRequestForAccount(t, srv.URL+path, 1, service.PlatformAnthropic), "", 1, 1)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, got
	}

	status, gotSmall := read("/small")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, small, gotSmall, "downstream caller must receive the complete small 400 body")

	status, gotBig := read("/big")
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, big, gotBig, "downstream caller must receive the complete >8KB 400 body intact")

	status, gotEmpty := read("/empty")
	require.Equal(t, http.StatusBadRequest, status)
	require.Empty(t, gotEmpty)

	status, gotOK := read("/ok")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, small, gotOK)

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Len(t, obs.prefixes, 3, "WantsAttemptBody + ObserveUpstreamAttemptBody must be invoked for HTTP 400 responses")
	require.Equal(t, small, obs.prefixes[0])
	require.Len(t, obs.prefixes[1], service.UpstreamAttemptBodyPeekLimit)
	require.Equal(t, big[:service.UpstreamAttemptBodyPeekLimit], obs.prefixes[1])
	require.Empty(t, obs.prefixes[2])
	require.Len(t, obs.snapshot(), 1, "200 must stay on the plain observation path")
}

func TestHTTPUpstream400AnthropicOrgDisabledEndToEndWithCircuitBreaker(t *testing.T) {
	orgDisabled := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	orgDisabledLarge := append(append([]byte(nil), orgDisabled...), bytes.Repeat([]byte("z"), service.UpstreamAttemptBodyPeekLimit+128)...)
	normal400 := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: field required"}}`)
	gzippedOrgDisabled := gzipBytesForUpstreamTest(t, orgDisabled)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/400-org-disabled":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(orgDisabled)
		case "/400-org-disabled-large":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(orgDisabledLarge)
		case "/400-org-disabled-gzip":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(gzippedOrgDisabled)
		case "/400-org-disabled-unknown-encoding":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "x-custom")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(gzippedOrgDisabled)
		case "/400-normal":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(normal400)
		case "/400-empty":
			w.WriteHeader(http.StatusBadRequest)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"upstream overloaded"}`))
		}
	}))
	t.Cleanup(srv.Close)

	t.Run("anthropic mixed 503 and 400 org disabled trips breaker while preserving bodies", func(t *testing.T) {
		var trips []service.AccountCircuitTrip
		breaker := service.NewAccountCircuitBreaker(service.DefaultAccountCircuitBreakerSettings(), func(_ context.Context, trip service.AccountCircuitTrip) error {
			trips = append(trips, trip)
			return nil
		})
		breaker.RunPersistSynchronouslyForTest()

		up := NewHTTPUpstream(nil)
		observableOf(t, up).SetUpstreamAttemptObserver(breaker)

		call := func(path string, acceptGzip bool) []byte {
			req := gatewayRequestForAccount(t, srv.URL+path, 1, service.PlatformAnthropic)
			if acceptGzip {
				req.Header.Set("Accept-Encoding", "gzip")
			}
			resp, err := up.Do(req, "", 1, 1)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			return body
		}

		for i := 0; i < 10; i++ {
			_ = call("/503", false)
		}
		for i := 0; i < 6; i++ {
			require.Equal(t, orgDisabled, call("/400-org-disabled", false))
		}
		for i := 0; i < 2; i++ {
			require.Equal(t, orgDisabledLarge, call("/400-org-disabled-large", false))
		}
		for i := 0; i < 2; i++ {
			require.Equal(t, orgDisabled, call("/400-org-disabled-gzip", true))
		}

		require.True(t, breaker.IsOpen(1), "10x 503 + 10x Anthropic 400 org-disabled must trip the breaker")
		require.Len(t, trips, 1)
		require.Equal(t, 20, trips[0].Attempts)
		require.Equal(t, 20, trips[0].Failures)
	})

	t.Run("normal 400, empty 400, undecodable compressed 400, and non-anthropic 400 do not trip", func(t *testing.T) {
		breaker := service.NewAccountCircuitBreaker(service.DefaultAccountCircuitBreakerSettings(), nil)
		breaker.RunPersistSynchronouslyForTest()
		up := NewHTTPUpstream(nil)
		observableOf(t, up).SetUpstreamAttemptObserver(breaker)

		call := func(accountID int64, platform, path string) []byte {
			req := gatewayRequestForAccount(t, srv.URL+path, accountID, platform)
			resp, err := up.Do(req, "", accountID, 1)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			return body
		}

		for i := 0; i < 25; i++ {
			require.Equal(t, normal400, call(10, service.PlatformAnthropic, "/400-normal"))
			require.Empty(t, call(11, service.PlatformAnthropic, "/400-empty"))
			require.Equal(t, gzippedOrgDisabled, call(12, service.PlatformAnthropic, "/400-org-disabled-unknown-encoding"))
			require.Equal(t, orgDisabled, call(13, service.PlatformOpenAI, "/400-org-disabled"))
		}

		for _, id := range []int64{10, 11, 12, 13} {
			require.False(t, breaker.IsOpen(id), "account %d must not trip", id)
		}
		stats := breaker.Stats()
		require.Equal(t, int64(100), stats.Observed)
		require.Zero(t, stats.Failures)
	})
}
