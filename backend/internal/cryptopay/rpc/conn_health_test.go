package rpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// blackholeConn is a connection whose path has gone silent: what is written to it no
// longer reaches the server and nothing comes back, and it never reports an error
// (no FIN, no RST) - what a path black hole, or a NAT gateway that dropped the flow,
// looks like to the client.
type blackholeConn struct {
	net.Conn
	dead *atomic.Bool
}

func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.dead.Load() {
		return len(p), nil // the packet vanishes
	}
	return c.Conn.Write(p)
}

// A pooled HTTP/2 connection that goes silent must be abandoned by the health check.
// Without it every call reuses the dead connection and waits out its whole timeout,
// for as long as traffic keeps coming (a multiplexed connection is not dropped when
// one request on it times out, unlike an HTTP/1.1 one), until the kernel gives up on
// the retransmits - fifteen minutes in which a chain served by that endpoint stops.
func TestSilentHTTP2ConnectionIsAbandonedByTheHealthCheck(t *testing.T) {
	fx := newTLSFixture(t, true, blockNumberHandler())
	var dead atomic.Bool
	var dials atomic.Int32
	client := fx.client(400*time.Millisecond, func(c *publicTransportConfig) {
		c.h2SendPing, c.h2PingTimeout = 150*time.Millisecond, 150*time.Millisecond
		inner := c.dial
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := inner(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if dials.Add(1) == 1 { // only the first connection goes silent
				return &blackholeConn{Conn: conn, dead: &dead}, nil
			}
			return conn, nil
		}
	})
	t.Cleanup(client.CloseIdleConnections)
	pool := NewPoolWithHTTPClient(fx.url(), client)

	if err := callOneBlockNumber(pool, fx.url()); err != nil {
		t.Fatalf("warm-up call: %v", err)
	}
	dead.Store(true)
	failed, slowest := 0, time.Duration(0)
	for i := 0; i < 8; i++ {
		start := time.Now()
		if err := callOneBlockNumber(pool, fx.url()); err != nil {
			failed++
		}
		slowest = max(slowest, time.Since(start))
		time.Sleep(50 * time.Millisecond)
	}
	// The black hole costs ONE slow call: the check closes the silent connection and the
	// call that was waiting on it is repeated on a new one (the stale-connection retry).
	// Without the check every call waits out its whole timeout and fails.
	if slowest < 200*time.Millisecond {
		t.Fatalf("the slowest call took %v: the test does not exercise a silent connection", slowest)
	}
	if failed > 2 {
		t.Fatalf("after the connection went silent %d of 8 calls failed: the dead connection was not abandoned", failed)
	}
	// The original, the one-shot connection of the repeated call, and the pooled
	// replacement the calls after it use.
	if got := dials.Load(); got < 2 || got > 4 {
		t.Fatalf("connections dialled = %d, want 2-4 (the original and its replacements, not one per call)", got)
	}
}

// The production settings of the health check and of the idle timeout. Pinned: they
// are what the three numbers in the comments of http_client.go argue for.
func TestPooledTransportPingsSilentHTTP2Connections(t *testing.T) {
	for name, transport := range map[string]*publicTransport{
		"a new transport":   newPublicTransport(publicTransportConfig{}),
		"the shared one":    sharedPublicTransport(),
		"with test seams 0": newPublicTransport(publicTransportConfig{h2SendPing: 0, h2PingTimeout: 0}),
	} {
		h2 := transport.pooled.HTTP2
		if h2 == nil {
			t.Fatalf("%s: the pooled transport has no HTTP/2 health check", name)
		}
		if h2.SendPingTimeout != 15*time.Second || h2.PingTimeout != 10*time.Second {
			t.Fatalf("%s: SendPingTimeout=%v PingTimeout=%v, want 15s and 10s", name, h2.SendPingTimeout, h2.PingTimeout)
		}
	}
}

// 60 s is what load balancers and NAT gateways commonly use for an idle connection;
// a client that waits exactly as long races the other side.
func TestIdleConnTimeoutStaysBelowTheUsualSixtySeconds(t *testing.T) {
	if idleConnTimeout >= 60*time.Second {
		t.Fatalf("idleConnTimeout = %v, want below 60s", idleConnTimeout)
	}
	if got := newPublicTransport(publicTransportConfig{}).pooled.IdleConnTimeout; got != idleConnTimeout {
		t.Fatalf("the pooled transport's IdleConnTimeout = %v, want %v", got, idleConnTimeout)
	}
	if got := sharedPublicTransport().pooled.IdleConnTimeout; got != 45*time.Second {
		t.Fatalf("the shared transport's IdleConnTimeout = %v, want 45s", got)
	}
}
