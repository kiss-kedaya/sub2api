package rpc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// busyEndpoint puts an endpoint's bucket in debt so that the NEXT call to it has to
// wait `wait` for its turn: a rate of 5/s with the tokens already promised. With a
// static fake clock in the transport the queue never drains while the test runs.
func busyEndpoint(t *testing.T, state *endpointTraffic, wait time.Duration) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.rate = 5
	state.tokens = 1 - 5*wait.Seconds()
}

// One endpoint with a queue (it was throttled to 5 calls/s) must not set the pace of
// the pool: while another endpoint is left to try, the busy one is skipped after a
// short wait instead of being queued behind. Before, a call waited up to two seconds
// at whichever endpoint the rotation had picked, and a pool of three endpoints
// delivered a quarter of its throughput with one of them cut.
func TestBusyEndpointIsSkippedWhileAnotherOneIsLeftToTry(t *testing.T) {
	var aHits, bHits atomic.Int32
	a := newTLSFixture(t, false, countingHandler(&aHits))
	b := newTLSFixture(t, false, countingHandler(&bHits))
	clock := newFakeClock() // static: the queue below does not drain during the test
	client := twoNodeClient(a, b, func(c *publicTransportConfig) { c.now = clock.Now; c.ratePerSec = 0 })
	t.Cleanup(client.CloseIdleConnections)
	pool := NewPoolWithHTTPClient(a.url()+","+b.url(), client)
	for _, endpoint := range []string{a.url(), b.url()} { // create both states
		if _, err := pool.callOne(t.Context(), endpoint, "eth_blockNumber", nil); err != nil {
			t.Fatal(err)
		}
	}
	busyEndpoint(t, endpointOf(t, client, a.url()), 800*time.Millisecond)
	aBefore := aHits.Load()
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, err := pool.Call(t.Context(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if took := time.Since(start); took > 400*time.Millisecond {
			t.Fatalf("call %d took %v: it waited for a busy endpoint while a free one was left to try", i, took)
		}
	}
	if got := aHits.Load() - aBefore; got != 0 {
		t.Fatalf("the busy endpoint was asked %d times; calls must go to the free one", got)
	}
	if got := trafficOf(t, client, a.url()); got.Throttled == 0 {
		t.Fatalf("traffic = %+v: the skips were not counted as local refusals", got)
	}
}

// The LAST candidate (here the only one) has nobody to fall back on: it waits out
// its queue, up to the full bound, as before.
func TestLastCandidateWaitsOutItsQueue(t *testing.T) {
	t.Run("a pool of one", func(t *testing.T) {
		var hits atomic.Int32
		fx := newTLSFixture(t, false, countingHandler(&hits))
		clock := newFakeClock()
		pool, client := fx.pacedPool(t, func(c *publicTransportConfig) { c.now = clock.Now })
		if _, err := pool.callOne(t.Context(), fx.url(), "eth_blockNumber", nil); err != nil {
			t.Fatal(err)
		}
		busyEndpoint(t, endpointOf(t, client, fx.url()), 800*time.Millisecond)
		start := time.Now()
		if _, err := pool.Call(t.Context(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("the only endpoint was refused instead of waited for: %v", err)
		}
		if took := time.Since(start); took < 600*time.Millisecond || took > 3*time.Second {
			t.Fatalf("the call took %v, want about its 800ms turn", took)
		}
	})
	t.Run("every endpoint busy", func(t *testing.T) {
		var aHits, bHits atomic.Int32
		a := newTLSFixture(t, false, countingHandler(&aHits))
		b := newTLSFixture(t, false, countingHandler(&bHits))
		clock := newFakeClock()
		client := twoNodeClient(a, b, func(c *publicTransportConfig) { c.now = clock.Now; c.ratePerSec = 0 })
		t.Cleanup(client.CloseIdleConnections)
		pool := NewPoolWithHTTPClient(a.url()+","+b.url(), client)
		for _, endpoint := range []string{a.url(), b.url()} {
			if _, err := pool.callOne(t.Context(), endpoint, "eth_blockNumber", nil); err != nil {
				t.Fatal(err)
			}
			busyEndpoint(t, endpointOf(t, client, endpoint), 800*time.Millisecond)
		}
		before := aHits.Load() + bHits.Load()
		start := time.Now()
		if _, err := pool.Call(t.Context(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("with every endpoint busy the call must wait for the last one, not fail: %v", err)
		}
		if took := time.Since(start); took < 600*time.Millisecond || took > 3*time.Second {
			t.Fatalf("the call took %v, want about the 800ms turn of the last candidate", took)
		}
		if got := aHits.Load() + bHits.Load() - before; got != 1 {
			t.Fatalf("the nodes saw %d calls, want exactly 1", got)
		}
	})
}

// The caller's own bound on the wait: shorter than the default is honoured, longer
// than the default is not (the default stays the ceiling).
func TestAcquireHonoursTheCallersBound(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 10)
	for i := 0; i < int(rateBurst); i++ {
		if err := e.acquire(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
	}
	// The next call needs 100ms.
	start := time.Now()
	if err := e.acquire(context.Background(), 50*time.Millisecond); err != errLocalRateLimit {
		t.Fatalf("acquire with a 50ms bound = %v, want errLocalRateLimit", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a refused call took %v", time.Since(start))
	}
	if err := e.acquire(context.Background(), 0); err != nil { // the default bound lets it wait
		t.Fatalf("acquire with the default bound: %v", err)
	}
	// A queue of 2.6s is over the default bound; asking for more must not raise it.
	e.mu.Lock()
	e.tokens = -25
	e.mu.Unlock()
	if err := e.acquire(context.Background(), time.Hour); err != errLocalRateLimit {
		t.Fatalf("a bound above the default raised it: %v", err)
	}
}
