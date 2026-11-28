package repository

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
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

func TestBuildUpstreamTransportWithTLSFingerprint_EnforcesHandshakeTimeout(t *testing.T) {
	// Blackhole TCP listener: accepts connections and stalls forever during TLS handshake.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	var acceptedConns sync.Mutex
	var openConns []net.Conn
	defer func() {
		acceptedConns.Lock()
		for _, c := range openConns {
			_ = c.Close()
		}
		acceptedConns.Unlock()
	}()
	go func() {
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			acceptedConns.Lock()
			openConns = append(openConns, c)
			acceptedConns.Unlock()
		}
	}()

	settings := defaultPoolSettings(nil)
	profile := &tlsfingerprint.Profile{Name: "chrome_120"}
	const shortTimeout = 150 * time.Millisecond

	t.Run("direct DialTLSContext times out on stalled TLS peer", func(t *testing.T) {
		tr, err := buildUpstreamTransportWithTLSFingerprintTimeout(settings, nil, profile, shortTimeout)
		require.NoError(t, err)
		require.NotNil(t, tr.DialTLSContext)

		start := time.Now()
		conn, dialErr := tr.DialTLSContext(context.Background(), "tcp", ln.Addr().String())
		elapsed := time.Since(start)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, dialErr)
		require.Less(t, elapsed, 2*time.Second, "stalled TLS handshake should abort near TLSHandshakeTimeout")
	})

	t.Run("http proxy DialTLSContext times out on stalled TLS peer", func(t *testing.T) {
		// Minimal CONNECT tunnel that forwards to the blackhole TLS listener.
		proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = proxyLn.Close() }()
		go func() {
			for {
				pc, acceptErr := proxyLn.Accept()
				if acceptErr != nil {
					return
				}
				acceptedConns.Lock()
				openConns = append(openConns, pc)
				acceptedConns.Unlock()
				go func(c net.Conn) {
					buf := make([]byte, 1024)
					_, _ = c.Read(buf)
					_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
					// Keep tunnel open without speaking TLS.
				}(pc)
			}
		}()

		proxyURL, err := url.Parse("http://" + proxyLn.Addr().String())
		require.NoError(t, err)
		tr, err := buildUpstreamTransportWithTLSFingerprintTimeout(settings, proxyURL, profile, shortTimeout)
		require.NoError(t, err)
		require.NotNil(t, tr.DialTLSContext)

		start := time.Now()
		conn, dialErr := tr.DialTLSContext(context.Background(), "tcp", "example.com:443")
		elapsed := time.Since(start)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, dialErr)
		require.Less(t, elapsed, 2*time.Second, "proxied stalled TLS handshake should abort near TLSHandshakeTimeout")
	})

	t.Run("wrapDialTLSWithHandshakeTimeout releases timeout after dial returns", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = serverConn.Close() }()

		wrapped := wrapDialTLSWithHandshakeTimeout(func(ctx context.Context, _, _ string) (net.Conn, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline, "handshake context must carry timeout deadline")
			return clientConn, nil
		}, 40*time.Millisecond)

		gotConn, err := wrapped(context.Background(), "tcp", "example.com:443")
		require.NoError(t, err)
		// Wait past the handshake timeout and verify the returned conn still streams freely.
		time.Sleep(70 * time.Millisecond)
		go func() { _, _ = io.WriteString(serverConn, "stream-chunk") }()
		buf := make([]byte, 32)
		n, readErr := gotConn.Read(buf)
		require.NoError(t, readErr)
		require.Equal(t, "stream-chunk", string(buf[:n]))
	})
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
	require.Same(t, baseClient, gotNonGrok)

	// Grok CLI host request gets wrapped directly by httpClientForUpstreamRequest (used by Do & DoWithTLS).
	gotGrok := svc.httpClientForUpstreamRequest(baseClient, grokReq)
	require.NotSame(t, baseClient, gotGrok)
	_, isFallback := gotGrok.Transport.(*grokAccessDeniedFallbackTransport)
	require.True(t, isFallback)

	// Combining redirect-disable context with Grok host applies both in one clone.
	grokNoRedirectReq := grokReq.WithContext(service.WithHTTPUpstreamRedirectsDisabled(grokReq.Context()))
	gotGrokNoRedirect := svc.httpClientForUpstreamRequest(baseClient, grokNoRedirectReq)
	require.NotNil(t, gotGrokNoRedirect.CheckRedirect)
	require.ErrorIs(t, gotGrokNoRedirect.CheckRedirect(grokNoRedirectReq, nil), http.ErrUseLastResponse)
	_, isFallback = gotGrokNoRedirect.Transport.(*grokAccessDeniedFallbackTransport)
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
func decompressAndReleaseUpstreamDecoder(t *testing.T, encoding string) (weak.Pointer[byte], any) {
	t.Helper()
	payload := []byte(strings.Repeat("release-check-payload-", 256))
	var packed []byte
	switch encoding {
	case "gzip":
		packed = compressGzip(t, payload)
	case "br":
		packed = compressBrotli(t, payload)
	case "deflate":
		packed = compressDeflate(t, payload)
	case "zstd":
		packed = compressZstd(t, payload)
	default:
		t.Fatalf("unexpected encoding %q", encoding)
	}
	raw := append([]byte(nil), packed...)
	wp := weak.Make(&raw[0])
	src := bytes.NewReader(raw)

	// Acquire, read, and release directly so we can return the released pooled
	// decoder and keep it strongly reachable across runtime.GC() via KeepAlive.
	switch encoding {
	case "gzip":
		p, err := acquireUpstreamGzipReader(src)
		require.NoError(t, err)
		got, err := io.ReadAll(p.gr)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		releaseUpstreamGzipReader(p)
		return wp, p
	case "br":
		br := acquireUpstreamBrotliReader(src)
		got, err := io.ReadAll(br)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		releaseUpstreamBrotliReader(br)
		return wp, br
	case "deflate":
		fr := acquireUpstreamFlateReader(src)
		got, err := io.ReadAll(fr)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		releaseUpstreamFlateReader(fr)
		return wp, fr
	case "zstd":
		bufReader := acquireUpstreamBufioReader(src)
		zr, err := acquireUpstreamZstdDecoder(bufReader)
		require.NoError(t, err)
		got, err := io.ReadAll(zr)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		releaseUpstreamZstdDecoder(zr)
		releaseUpstreamBufioReader(bufReader)
		return wp, []any{zr, bufReader}
	default:
		return weak.Pointer[byte]{}, nil
	}
}

func TestDecompressResponseBody_PooledDecodersReleaseUnderlyingBodyReference(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wp, holder := decompressAndReleaseUpstreamDecoder(t, encoding)
			defer runtime.KeepAlive(holder)
			require.Eventually(t, func() bool {
				runtime.GC()
				return wp.Value() == nil
			}, 2*time.Second, 5*time.Millisecond, "pooled %s reader must not retain closed response body bytes", encoding)
			runtime.KeepAlive(holder)
		})
	}
}

func TestDecompressResponseBody_MidStreamEarlyCloseReusesPoolCleanly(t *testing.T) {
	payloadLarge := []byte(strings.Repeat(`{"chunk":"mid-stream-early-close-1234567890"}`, 512))
	payloadNext := []byte(strings.Repeat(`{"chunk":"subsequent-clean-payload-abcdef"}`, 128))

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
			packedLarge := enc.compress(t, payloadLarge)
			packedNext := enc.compress(t, payloadNext)

			// Partially read and close mid-stream (before EOF) multiple times, then
			// confirm subsequent full reads from pooled decoders decode cleanly.
			for i := 0; i < 8; i++ {
				resp := newEncodedResponse(enc.encoding, packedLarge)
				decompressResponseBody(resp)
				var partial [16]byte
				n, err := io.ReadFull(resp.Body, partial[:])
				require.NoError(t, err)
				require.Equal(t, 16, n)
				require.Equal(t, payloadLarge[:16], partial[:])
				require.NoError(t, resp.Body.Close())

				nextResp := newEncodedResponse(enc.encoding, packedNext)
				decompressResponseBody(nextResp)
				got, err := io.ReadAll(nextResp.Body)
				require.NoError(t, err)
				require.Equal(t, payloadNext, got)
				require.NoError(t, nextResp.Body.Close())
			}
		})
	}
}
