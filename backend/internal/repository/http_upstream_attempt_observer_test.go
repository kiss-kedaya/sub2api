package repository

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
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

type oneShotErrorBody struct {
	data   []byte
	err    error
	reads  int
	closed bool
}

func (b *oneShotErrorBody) Read(p []byte) (int, error) {
	b.reads++
	if len(b.data) > 0 {
		n := copy(p, b.data)
		b.data = b.data[n:]
		return n, nil
	}
	if b.err != nil {
		err := b.err
		b.err = nil
		return 0, err
	}
	return 0, io.EOF
}

func (b *oneShotErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestPeekResponseBodyReplaysOneShotReadErrorAndDoesNotReread(t *testing.T) {
	boom := errors.New("stream reset once")
	raw := &oneShotErrorBody{data: []byte("partial-400"), err: boom}
	resp := &http.Response{Body: raw}

	prefix := peekResponseBody(resp, service.UpstreamAttemptBodyPeekLimit)
	require.Equal(t, []byte("partial-400"), prefix)
	readsAfterPeek := raw.reads

	got, err := io.ReadAll(resp.Body)
	require.Equal(t, []byte("partial-400"), got)
	require.ErrorIs(t, err, boom, "one-shot read error must not be swallowed into io.EOF")
	require.Equal(t, readsAfterPeek, raw.reads, "downstream read must not invoke the failed underlying reader again")
	require.NoError(t, resp.Body.Close())
	require.True(t, raw.closed, "closing the restored body must still close the original reader")
}

func TestHTTPUpstream400TruncatedBodyKeepsReadErrorAndStaysOK(t *testing.T) {
	partial := `{"type":"error","error":{"message":"partial truncated`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer does not support hijacking")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 500\r\n\r\n" + partial)
		_ = buf.Flush()
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)

	breaker := service.NewAccountCircuitBreaker(service.DefaultAccountCircuitBreakerSettings(), nil)
	breaker.RunPersistSynchronouslyForTest()
	up := NewHTTPUpstream(nil)
	observableOf(t, up).SetUpstreamAttemptObserver(breaker)

	for i := 0; i < 25; i++ {
		resp, err := up.Do(gatewayRequestForAccount(t, srv.URL, 77, service.PlatformAnthropic), "", 77, 1)
		require.NoError(t, err)
		got, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, partial, string(got))
		require.ErrorIs(t, readErr, io.ErrUnexpectedEOF, "truncated 400 body must remain an error for downstream callers")
	}
	require.False(t, breaker.IsOpen(77), "truncated/unreadable non-matching 400 must stay attemptOK")
	require.Equal(t, int64(25), breaker.Stats().Observed)
	require.Zero(t, breaker.Stats().Failures)
}

func TestHTTPUpstreamDoWithTLSHTTPS400OrgDisabledPeeksAndPreservesBody(t *testing.T) {
	orgDisabled := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	orgDisabledLarge := append(append([]byte(nil), orgDisabled...), bytes.Repeat([]byte("w"), service.UpstreamAttemptBodyPeekLimit+97)...)
	gzippedOrgDisabled := gzipBytesForUpstreamTest(t, orgDisabled)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/400-org-disabled":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(orgDisabled)
		case "/400-org-disabled-large":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(orgDisabledLarge)
		case "/400-org-disabled-gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(gzippedOrgDisabled)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"overloaded"}`))
		}
	}))
	t.Cleanup(srv.Close)

	var trips []service.AccountCircuitTrip
	breaker := service.NewAccountCircuitBreaker(service.DefaultAccountCircuitBreakerSettings(), func(_ context.Context, trip service.AccountCircuitTrip) error {
		trips = append(trips, trip)
		return nil
	})
	breaker.RunPersistSynchronouslyForTest()

	up := NewHTTPUpstream(nil)
	observableOf(t, up).SetUpstreamAttemptObserver(breaker)

	// Seed the TLS cache entry with srv.Client() (which trusts the httptest TLS cert)
	// so DoWithTLS executes its HTTPS branch (lines 336-380) without delegating to Do.
	svc, ok := up.(*httpUpstreamService)
	require.True(t, ok)
	const accountID int64 = 55
	isolation := svc.getIsolationMode()
	settings := svc.applyProfilePoolSettings(svc.resolvePoolSettings(isolation, 1), service.HTTPUpstreamProfileDefault)
	cacheKey := "tls:" + buildCacheKey(isolation, directProxyKey, accountID, upstreamProtocolModeDefault)
	poolKey := buildPoolKey(settings, upstreamProtocolModeDefault) + ":tls"
	svc.mu.Lock()
	svc.clients[cacheKey] = &upstreamClientEntry{
		client:   srv.Client(),
		proxyKey: directProxyKey,
		poolKey:  poolKey,
	}
	svc.mu.Unlock()

	profile := &tlsfingerprint.Profile{Name: "test-https-400"}
	call := func(path string, acceptGzip bool) []byte {
		req := gatewayRequestForAccount(t, srv.URL+path, accountID, service.PlatformAnthropic)
		if acceptGzip {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		resp, err := up.DoWithTLS(req, "", accountID, 1, profile)
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

	require.True(t, breaker.IsOpen(accountID), "HTTPS DoWithTLS 400 org-disabled + 503 must trip the circuit breaker")
	require.Len(t, trips, 1)
	require.Equal(t, service.PlatformAnthropic, trips[0].Platform)
	require.Equal(t, 20, trips[0].Attempts)
	require.Equal(t, 20, trips[0].Failures)
}

type upstreamTestCaptureStore struct {
	mu      sync.Mutex
	tasks   map[string][]byte
	records map[string][]byte
}

func newUpstreamTestCaptureStore() *upstreamTestCaptureStore {
	return &upstreamTestCaptureStore{tasks: map[string][]byte{}, records: map[string][]byte{}}
}

func (s *upstreamTestCaptureStore) SaveTask(_ context.Context, task *requestcapture.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(task)
	s.tasks[task.ID] = b
	return err
}

func (s *upstreamTestCaptureStore) Task(_ context.Context, instance, id string) (*requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.tasks[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var task requestcapture.Task
	if err := json.Unmarshal(b, &task); err != nil {
		return nil, err
	}
	if task.InstanceID != instance {
		return nil, sql.ErrNoRows
	}
	return &task, nil
}

func (s *upstreamTestCaptureStore) Tasks(_ context.Context, instance string, _, _ int) ([]requestcapture.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []requestcapture.Task
	for _, b := range s.tasks {
		var task requestcapture.Task
		_ = json.Unmarshal(b, &task)
		if task.InstanceID == instance {
			out = append(out, task)
		}
	}
	return out, nil
}

func (s *upstreamTestCaptureStore) SaveRecord(_ context.Context, rec *requestcapture.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[rec.TaskID]; !ok {
		return sql.ErrNoRows
	}
	b, err := json.Marshal(rec)
	s.records[rec.ID] = b
	return err
}

func (s *upstreamTestCaptureStore) Records(_ context.Context, taskID, requestID string, _ bool, _, _ int) ([]requestcapture.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []requestcapture.Record
	for _, b := range s.records {
		var rec requestcapture.Record
		_ = json.Unmarshal(b, &rec)
		if rec.TaskID == taskID && (requestID == "" || rec.Meta.RequestID == requestID) {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *upstreamTestCaptureStore) Record(_ context.Context, taskID, id string) (*requestcapture.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.records[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var rec requestcapture.Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	if rec.TaskID != taskID {
		return nil, sql.ErrNoRows
	}
	return &rec, nil
}

func (s *upstreamTestCaptureStore) DeleteRecord(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, id)
	return nil
}

func (s *upstreamTestCaptureStore) DeleteTask(_ context.Context, _, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	return nil
}

func TestHTTPUpstream400PeekWithRequestCapturePreservesBodyWithoutDuplication(t *testing.T) {
	small := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled."}}`)
	large := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"This organization has been disabled.","pad":"` + strings.Repeat("q", service.UpstreamAttemptBodyPeekLimit+123) + `"}}`)
	gzipped := gzipBytesForUpstreamTest(t, small)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/small":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(small)
		case "/large":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(large)
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(gzipped)
		}
	}))
	t.Cleanup(srv.Close)

	store := newUpstreamTestCaptureStore()
	mgr, err := requestcapture.New(store, t.TempDir(), requestcapture.Config{Enabled: true, QuotaMiB: 128, RetentionDays: 7})
	require.NoError(t, err)
	t.Cleanup(mgr.Close)

	task, err := mgr.Create(context.Background(), requestcapture.CreateTask{
		TargetType: "user", TargetID: 9, DurationMinutes: 10,
	}, "test")
	require.NoError(t, err)

	breaker := service.NewAccountCircuitBreaker(service.DefaultAccountCircuitBreakerSettings(), nil)
	breaker.RunPersistSynchronouslyForTest()
	up := NewHTTPUpstream(nil)
	observableOf(t, up).SetUpstreamAttemptObserver(breaker)

	cases := []struct {
		name       string
		path       string
		acceptGzip bool
		want       []byte
	}{
		{"small 400", "/small", false, small},
		{"large >8KB 400", "/large", false, large},
		{"gzip 400", "/gzip", true, small},
	}

	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqID := "cap-400-" + tc.name
			session := mgr.Begin(requestcapture.Meta{UserID: 9, RequestID: reqID})
			require.NotNil(t, session)
			session.ClientRequest([]byte(`{"model":"claude-sonnet-4-5"}`), "application/json", nil)

			req := gatewayRequestForAccount(t, srv.URL+tc.path, 88, service.PlatformAnthropic)
			req = req.WithContext(requestcapture.WithSession(req.Context(), session))
			if tc.acceptGzip {
				req.Header.Set("Accept-Encoding", "gzip")
			}

			resp, err := up.Do(req, "", 88, 1)
			require.NoError(t, err)
			got, readErr := io.ReadAll(resp.Body)
			require.NoError(t, readErr)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.want, got, "downstream caller must receive exact complete body")

			session.Finish(http.StatusBadRequest)
			require.Eventually(t, func() bool { return mgr.Stats().ActiveRequests == 0 }, 5*time.Second, 10*time.Millisecond)

			records, err := mgr.Records(context.Background(), task.ID, reqID, true, 10, 0)
			require.NoError(t, err)
			require.Len(t, records, 1)
			var captured []byte
			for _, part := range records[0].Parts {
				if part.Stage == "upstream_response" {
					f, _, openErr := mgr.OpenPart(context.Background(), task.ID, records[0].ID, part.Name)
					require.NoError(t, openErr)
					captured, err = io.ReadAll(f)
					_ = f.Close()
					require.NoError(t, err)
				}
			}
			require.Equal(t, tc.want, captured, "requestcapture must record the peeked 400 body once without duplication or loss")
			require.Equal(t, int64(idx+1), breaker.Stats().Failures, "each org-disabled 400 must be recorded as a failure")
		})
	}
}
