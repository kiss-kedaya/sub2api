//go:build unit

package repository

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

//go:noinline
func runMidStreamDoRequest(t *testing.T, upstream service.HTTPUpstream, url string, useTLSProfile bool, useBytesReader bool) (*http.Response, weak.Pointer[byte]) {
	t.Helper()
	payload := []byte(`{"model":"gpt-5","input":"` + strings.Repeat("x", 256<<10) + `"}`)
	weakBuf := weak.Make(&payload[0])
	var req *http.Request
	var err error
	if useBytesReader {
		req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/v1/responses", bytes.NewReader(payload))
		require.NoError(t, err)
	} else {
		body := httputil.NewPrereadBody(payload)
		req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/v1/responses", body)
		require.NoError(t, err)
		req.ContentLength = int64(len(payload))
		req.GetBody = func() (io.ReadCloser, error) {
			return httputil.NewPrereadBody(payload), nil
		}
	}
	var resp *http.Response
	var doErr error
	if useTLSProfile {
		resp, doErr = upstream.DoWithTLS(req, "", 1, 1, &tlsfingerprint.Profile{Name: "chrome_120"})
	} else {
		resp, doErr = upstream.Do(req, "", 1, 1)
	}
	require.NoError(t, doErr)
	require.Nil(t, req.Body, "expected req.Body to be nil on Do/DoWithTLS return")
	require.Nil(t, req.GetBody, "expected req.GetBody to be nil on Do/DoWithTLS return")
	return resp, weakBuf
}

func TestHTTPUpstreamDo_ReleasesRequestBodyMidStream(t *testing.T) {
	for _, tc := range []struct {
		name           string
		useTLSProfile  bool
		useBytesReader bool
	}{
		{name: "Do_PrereadBody_with_GetBody", useTLSProfile: false, useBytesReader: false},
		{name: "Do_BytesReader_with_auto_GetBody", useTLSProfile: false, useBytesReader: true},
		{name: "DoWithTLS_plain_HTTP_fallback", useTLSProfile: true, useBytesReader: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			releasePipe := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				close(started)
				<-releasePipe
			}))
			t.Cleanup(srv.Close)
			defer close(releasePipe)

			upstream := NewHTTPUpstream(nil)
			resp, weakBuf := runMidStreamDoRequest(t, upstream, srv.URL, tc.useTLSProfile, tc.useBytesReader)
			require.NotNil(t, resp)
			defer func() { _ = resp.Body.Close() }()

			<-started
			if resp.Request != nil {
				require.Nil(t, resp.Request.Body)
				require.Nil(t, resp.Request.GetBody)
			}

			require.Eventually(t, func() bool {
				runtime.GC()
				return weakBuf.Value() == nil
			}, 3*time.Second, 5*time.Millisecond, "request body must be collectible while streaming response is still open")
		})
	}
}

type trackingSlowBody struct {
	firstSent chan struct{}
	unblock   chan struct{}
	chunks    [][]byte
	idx       int
	closes    atomic.Int32
}

func (b *trackingSlowBody) Read(p []byte) (int, error) {
	if b.idx == 0 {
		n := copy(p, b.chunks[0])
		b.chunks[0] = b.chunks[0][n:]
		if len(b.chunks[0]) == 0 {
			b.idx++
			close(b.firstSent)
		}
		return n, nil
	}
	if b.idx == 1 {
		<-b.unblock
	}
	if b.idx >= len(b.chunks) {
		return 0, io.EOF
	}
	n := copy(p, b.chunks[b.idx])
	b.chunks[b.idx] = b.chunks[b.idx][n:]
	if len(b.chunks[b.idx]) == 0 {
		b.idx++
	}
	return n, nil
}

func (b *trackingSlowBody) Close() error {
	b.closes.Add(1)
	return nil
}

func TestHTTPUpstreamDo_HTTP2EarlyResponseDoesNotTruncateOrSkipClose(t *testing.T) {
	expectedTail := strings.Repeat("Z", 64<<10)
	expectedFull := "head-" + expectedTail
	gotBodyCh := make(chan string, 1)
	headersSent := make(chan struct{})

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reply with 200 headers BEFORE draining the HTTP/2 POST body.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(headersSent)
		data, _ := io.ReadAll(r.Body)
		gotBodyCh <- string(data)
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	slow := &trackingSlowBody{
		firstSent: make(chan struct{}),
		unblock:   make(chan struct{}),
		chunks:    [][]byte{[]byte("head-"), []byte(expectedTail)},
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/v1/responses", slow)
	require.NoError(t, err)
	req.ContentLength = int64(len(expectedFull))

	wrappedBody, releaseWrapped := wrapUpstreamRequestPayloadForRelease(req)
	resp, err := observedUpstreamRequest(srv.Client(), req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	<-headersSent
	require.Equal(t, 2, resp.ProtoMajor, "test must exercise HTTP/2")

	// Simulate Do returning while upload is still blocked mid-body.
	releaseWrapped()
	require.False(t, wrappedBody.IsClosed(), "body must stay open while HTTP/2 upload is still streaming")

	close(slow.unblock)
	require.Equal(t, expectedFull, <-gotBodyCh, "HTTP/2 early-200 release must not truncate the remaining upload")
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return slow.closes.Load() == 1 && wrappedBody.IsClosed()
	}, 3*time.Second, 5*time.Millisecond, "underlying request body Close must be called once when transport finishes")
}
