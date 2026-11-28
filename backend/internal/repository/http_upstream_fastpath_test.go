package repository

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestBuildUpstreamTransports_BufferSizesAndTLSHandshakeTimeout(t *testing.T) {
	settings := defaultPoolSettings(nil)

	tr, err := buildUpstreamTransport(settings, nil, upstreamProtocolModeDefault)
	require.NoError(t, err)
	require.Equal(t, 32*1024, tr.ReadBufferSize)
	require.Equal(t, 32*1024, tr.WriteBufferSize)
	require.Equal(t, defaultUpstreamTLSHandshakeTimeout, tr.TLSHandshakeTimeout)

	tlsTr, err := buildUpstreamTransportWithTLSFingerprint(settings, nil, &tlsfingerprint.Profile{Name: "chrome_120"})
	require.NoError(t, err)
	require.Equal(t, 32*1024, tlsTr.ReadBufferSize)
	require.Equal(t, 32*1024, tlsTr.WriteBufferSize)
	require.Equal(t, defaultUpstreamTLSHandshakeTimeout, tlsTr.TLSHandshakeTimeout)
}

type lockCheckingRoundTripper struct {
	mu           *sync.RWMutex
	closeCalls   atomic.Int32
	unlockedSeen atomic.Bool
}

func (rt *lockCheckingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
}

func (rt *lockCheckingRoundTripper) CloseIdleConnections() {
	rt.closeCalls.Add(1)
	if rt.mu.TryLock() {
		rt.unlockedSeen.Store(true)
		rt.mu.Unlock()
	}
}

func TestHTTPUpstreamEviction_ClosesIdleConnectionsOutsideGlobalLock(t *testing.T) {
	svc, ok := NewHTTPUpstream(&config.Config{
		Gateway: config.GatewayConfig{
			ConnectionPoolIsolation: config.ConnectionPoolIsolationAccount,
			MaxUpstreamClients:      2,
			ClientIdleTTLSeconds:    1,
		},
	}).(*httpUpstreamService)
	require.True(t, ok)

	rt := &lockCheckingRoundTripper{mu: &svc.mu}
	stale := &upstreamClientEntry{
		client:   &http.Client{Transport: rt},
		proxyKey: directProxyKey,
		poolKey:  "stale",
	}
	atomic.StoreInt64(&stale.lastUsed, time.Now().Add(-10*time.Second).UnixNano())

	svc.mu.Lock()
	svc.clients["stale-key"] = stale
	svc.mu.Unlock()

	entry, err := svc.acquireClient("", 1, 1)
	require.NoError(t, err)
	require.NotNil(t, entry)
	atomic.AddInt64(&entry.inFlight, -1)

	require.Equal(t, int32(1), rt.closeCalls.Load(), "evicted client must have CloseIdleConnections called")
	require.True(t, rt.unlockedSeen.Load(), "CloseIdleConnections must run outside s.mu.Lock()")
}

type fastpathRoundTripFunc func(*http.Request) (*http.Response, error)

func (f fastpathRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPUpstreamDo_WrapsGrokFallbackTransportOnlyForGrokCLIHost(t *testing.T) {
	nonGrokReq := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
	require.False(t, isGrokCLIProxyRequest(nonGrokReq))

	grokReq := httptest.NewRequest(http.MethodPost, "https://"+grokCLIProxyHost+"/v1/responses", nil)
	require.True(t, isGrokCLIProxyRequest(grokReq))

	baseClient := &http.Client{Transport: fastpathRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})}
	svc, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)

	// Non-Grok request must keep baseClient directly without allocating a fallback wrapper.
	gotNonGrok := svc.httpClientForUpstreamRequest(baseClient, nonGrokReq)
	if isGrokCLIProxyRequest(nonGrokReq) {
		gotNonGrok = httpClientWithGrokAccessDeniedFallback(gotNonGrok)
	}
	require.Same(t, baseClient, gotNonGrok)

	// Grok CLI host request gets wrapped.
	gotGrok := svc.httpClientForUpstreamRequest(baseClient, grokReq)
	if isGrokCLIProxyRequest(grokReq) {
		gotGrok = httpClientWithGrokAccessDeniedFallback(gotGrok)
	}
	_, isFallback := gotGrok.Transport.(*grokAccessDeniedFallbackTransport)
	require.True(t, isFallback)
}

func TestDecompressResponseBody_PooledDecodersReuseRecoverAndRelease(t *testing.T) {
	payloadA := []byte(strings.Repeat(`{"chunk":"alpha-1234567890"}`, 128))
	payloadB := []byte(strings.Repeat(`{"chunk":"beta-abcdefghij"}`, 128))

	encodings := []struct {
		name     string
		encoding string
		compress func(*testing.T, []byte) []byte
	}{
		{"gzip", "gzip", compressGzip},
		{"br", "br", compressBrotli},
		{"deflate", "deflate", compressDeflate},
		{"zstd", "zstd", compressZstd},
	}

	for _, enc := range encodings {
		t.Run(enc.name, func(t *testing.T) {
			compA := enc.compress(t, payloadA)
			compB := enc.compress(t, payloadB)

			// Run concurrent decompressions + corrupt payload recovery to verify pool safety.
			var wg sync.WaitGroup
			for g := 0; g < 8; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					for i := 0; i < 20; i++ {
						want := payloadA
						packed := compA
						if (id+i)%2 == 1 {
							want = payloadB
							packed = compB
						}
						resp := newEncodedResponse(enc.encoding, packed)
						decompressResponseBody(resp)
						got, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						require.Equal(t, want, got)
						require.NoError(t, resp.Body.Close())
						// Second Close must be a safe no-op (no double-Put).
						require.NoError(t, resp.Body.Close())
					}
				}(g)
			}
			wg.Wait()

			// Corrupt payload followed by valid payload must still succeed cleanly.
			badResp := newEncodedResponse(enc.encoding, []byte("corrupt-stream-bytes"))
			decompressResponseBody(badResp)
			_, _ = io.ReadAll(badResp.Body)
			require.NoError(t, badResp.Body.Close())

			goodResp := newEncodedResponse(enc.encoding, compA)
			decompressResponseBody(goodResp)
			got, err := io.ReadAll(goodResp.Body)
			require.NoError(t, err)
			require.Equal(t, payloadA, got)
			require.NoError(t, goodResp.Body.Close())
		})
	}
}

func TestDecompressResponseBody_ZstdConcurrencyOneDoesNotLeakGoroutines(t *testing.T) {
	payload := []byte(strings.Repeat("hello zstd pooled decoder ", 64))
	packed := compressZstd(t, payload)

	// Warm up pool.
	for i := 0; i < 4; i++ {
		resp := newEncodedResponse("zstd", packed)
		decompressResponseBody(resp)
		_, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 50; i++ {
		resp := newEncodedResponse("zstd", packed)
		decompressResponseBody(resp)
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		require.NoError(t, resp.Body.Close())
	}

	after := runtime.NumGoroutine()
	require.LessOrEqual(t, after, before+4, "zstd WithDecoderConcurrency(1) must not spawn persistent worker goroutines")
}

//go:noinline
func decompressAndCloseGzipBody(t *testing.T) weak.Pointer[byte] {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte("release-check-payload"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	raw := append([]byte(nil), buf.Bytes()...)
	wp := weak.Make(&raw[0])

	resp := newEncodedResponse("gzip", raw)
	decompressResponseBody(resp)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "release-check-payload", string(got))
	require.NoError(t, resp.Body.Close())
	return wp
}

func TestDecompressResponseBody_PooledGzipReleasesUnderlyingBodyReference(t *testing.T) {
	wp := decompressAndCloseGzipBody(t)
	require.Eventually(t, func() bool {
		runtime.GC()
		return wp.Value() == nil
	}, 2*time.Second, 5*time.Millisecond, "pooled gzip reader must not retain closed response body bytes")
}
