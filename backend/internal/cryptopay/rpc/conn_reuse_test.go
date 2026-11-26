package rpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func callBlockNumber(t testing.TB, pool *Pool, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := pool.BlockNumber(context.Background()); err != nil {
			t.Fatalf("call %d failed: %v", i, err)
		}
	}
}

// Many calls to one endpoint share a handful of connections, over HTTP/1.1 and
// over HTTP/2. This is the fix for a connection (TCP + TLS handshake, then a
// TIME_WAIT) per call.
func TestPooledClientReusesConnections(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", http2), func(t *testing.T) {
			fx := newTLSFixture(t, http2, blockNumberHandler())
			callBlockNumber(t, fx.pool(t), 200)
			if got := fx.conns.Load(); got < 1 || got > 2 {
				t.Fatalf("200 sequential calls opened %d connections, want 1-2", got)
			}
		})
	}
}

// The harness itself: the client as it was opens one connection per call.
func TestLegacyClientOpensOneConnectionPerCall(t *testing.T) {
	fx := newTLSFixture(t, false, blockNumberHandler())
	client := fx.legacyClient(0)
	defer client.CloseIdleConnections()
	callBlockNumber(t, NewPoolWithHTTPClient(fx.url(), client), 50)
	if got := fx.conns.Load(); got != 50 {
		t.Fatalf("legacy client opened %d connections for 50 calls, want 50", got)
	}
}

func TestIdleConnectionsAreRebuiltAfterTheIdleTimeout(t *testing.T) {
	fx := newTLSFixture(t, false, blockNumberHandler())
	pool := fx.pool(t, func(c *publicTransportConfig) { c.idleTimeout = 60 * time.Millisecond })
	callBlockNumber(t, pool, 3)
	if got := fx.conns.Load(); got != 1 {
		t.Fatalf("connections before idling = %d, want 1", got)
	}
	time.Sleep(400 * time.Millisecond)
	callBlockNumber(t, pool, 3)
	if got := fx.conns.Load(); got != 2 {
		t.Fatalf("connections after the idle timeout = %d, want 2 (the idle one was closed and rebuilt)", got)
	}
}

// A burst far above the cap is served on at most maxConnsPerHost connections per
// host (the rest wait for a free one); nothing is refused or lost.
func TestConnectionsPerHostAreBounded(t *testing.T) {
	var inFlight, peak atomic.Int64
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		rpcResult(w, `"0x10"`)
	}))
	pool := fx.pool(t)
	var wg sync.WaitGroup
	var failures atomic.Int64
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.BlockNumber(context.Background()); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d of 64 calls failed", failures.Load())
	}
	if got := fx.conns.Load(); got > maxConnsPerHost {
		t.Fatalf("connections = %d, want at most %d", got, maxConnsPerHost)
	}
	if peak.Load() > maxConnsPerHost {
		t.Fatalf("peak concurrent requests = %d, want at most %d on HTTP/1.1", peak.Load(), maxConnsPerHost)
	}
}

func TestHTTP2MultiplexesConcurrentCallsOnFewConnections(t *testing.T) {
	fx := newTLSFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		rpcResult(w, `"0x10"`)
	}))
	pool := fx.pool(t)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.BlockNumber(context.Background()); err != nil {
				t.Errorf("call failed: %v", err)
			}
		}()
	}
	wg.Wait()
	callBlockNumber(t, pool, 100)
	if got := fx.conns.Load(); got > maxConnsPerHost {
		t.Fatalf("connections = %d, want at most %d", got, maxConnsPerHost)
	}
	before := fx.conns.Load()
	callBlockNumber(t, pool, 100)
	if fx.conns.Load() != before {
		t.Fatalf("warm HTTP/2 calls opened %d new connections", fx.conns.Load()-before)
	}
}

// A chunked answer must still be fully consumed, or HTTP/1.1 drops the connection:
// the JSON decoder stops at the closing brace, before the terminating chunk.
func TestChunkedAnswersKeepTheConnection(t *testing.T) {
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x10"}`))
		w.(http.Flusher).Flush() // headers + first chunk go out before the handler returns
		_, _ = w.Write([]byte("\n"))
	}))
	callBlockNumber(t, fx.pool(t), 30)
	if got := fx.conns.Load(); got > 2 {
		t.Fatalf("30 chunked answers opened %d connections, want 1-2", got)
	}
}

func gzipBytes(t testing.TB, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGzipAnswersAreAskedForAndDecoded(t *testing.T) {
	var sawAcceptEncoding atomic.Value
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAcceptEncoding.Store(r.Header.Get("Accept-Encoding"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipBytes(t, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x2a"}`)))
	}))
	got, err := fx.pool(t).BlockNumber(context.Background())
	if err != nil || got != 0x2a {
		t.Fatalf("BlockNumber = %d, %v", got, err)
	}
	if v, _ := sawAcceptEncoding.Load().(string); !strings.Contains(v, "gzip") {
		t.Fatalf("Accept-Encoding = %q, want gzip", v)
	}
}

// What is read is bounded by the DECOMPRESSED size. This server streams a gzip
// body that never ends; reading it all would run until the call timed out (and
// fill memory on the way). Hitting the cap makes the call fail at once instead.
func TestCompressedAnswerIsBoundedByItsDecompressedSize(t *testing.T) {
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		zw, _ := gzip.NewWriterLevel(w, gzip.BestCompression)
		_, _ = zw.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"`))
		chunk := bytes.Repeat([]byte("0"), 1<<20)
		for {
			if _, err := zw.Write(chunk); err != nil {
				return // the client hung up
			}
			if err := zw.Flush(); err != nil {
				return
			}
		}
	}))
	start := time.Now()
	_, err := fx.pool(t).Call(context.Background(), "eth_getLogs", []any{})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "response" {
		t.Fatalf("error = %v, want a response failure", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the oversized answer took %v: the read was not bounded", took)
	}
}

// The cap on one answer is 8 MiB decoded, as it always was; a bigger one fails as
// "response", which the scanner answers by splitting the query. Pinned here so
// that raising or removing it is a decision, not an accident.
func TestAnswerSizeCap(t *testing.T) {
	big := strings.Repeat("a", maxResponseBytes+1024)
	small := strings.Repeat("a", maxResponseBytes-1024)
	for _, tc := range []struct {
		name    string
		payload string
		wantErr bool
	}{{"under the cap", small, false}, {"over the cap", big, true}} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = requestMethod(r)
				rpcResult(w, `"`+tc.payload+`"`)
			}))
			_, err := fx.pool(t).Call(context.Background(), "eth_blockNumber", nil)
			if tc.wantErr {
				var failure *Failure
				if !errors.As(err, &failure) || failure.Kind != "response" {
					t.Fatalf("error = %v, want a response failure", err)
				}
			} else if err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Before/after for the same 1000 calls: the connections opened and the time taken,
// with a local TLS server. The dial delay stands in for the round trips and CPU a
// handshake costs on a busy host. Run: RPC_CONN_REPORT=1 go test -run TestConnectionReuseReport -v
func TestConnectionReuseReport(t *testing.T) {
	if os.Getenv("RPC_CONN_REPORT") == "" {
		t.Skip("measurement: set RPC_CONN_REPORT=1 and run with -v to print the before/after table")
	}
	type scenario struct {
		name      string
		workers   int
		dialDelay time.Duration
	}
	scenarios := []scenario{
		{"1 worker, no dial latency", 1, 0},
		{"1 worker, 5ms dial latency", 1, 5 * time.Millisecond},
		{"8 workers, no dial latency", 8, 0},
		{"8 workers, 5ms dial latency", 8, 5 * time.Millisecond},
	}
	const total = 1000
	run := func(fx *tlsFixture, client *http.Client, workers int) (int64, time.Duration) {
		defer client.CloseIdleConnections()
		fx.conns.Store(0)
		pool := NewPoolWithHTTPClient(fx.url(), client)
		var wg sync.WaitGroup
		start := time.Now()
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < total/workers; i++ {
					if _, err := pool.BlockNumber(context.Background()); err != nil {
						t.Errorf("call failed: %v", err)
						return
					}
				}
			}()
		}
		wg.Wait()
		return fx.conns.Load(), time.Since(start)
	}
	for _, http2 := range []bool{false, true} {
		fx := newTLSFixture(t, http2, blockNumberHandler())
		for _, sc := range scenarios {
			delay := sc.dialDelay
			beforeConns, beforeTime := run(fx, fx.legacyClient(delay), sc.workers)
			afterConns, afterTime := run(fx, fx.client(15*time.Second, func(c *publicTransportConfig) {
				inner := c.dial
				c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
					time.Sleep(delay)
					return inner(ctx, network, address)
				}
			}), sc.workers)
			t.Logf("http2=%-5v %-28s before: %4d conns %8v | after: %2d conns %8v",
				http2, sc.name, beforeConns, beforeTime.Round(time.Millisecond), afterConns, afterTime.Round(time.Millisecond))
			if afterConns > maxConnsPerHost {
				t.Errorf("%s: %d connections after the change", sc.name, afterConns)
			}
		}
	}
}
