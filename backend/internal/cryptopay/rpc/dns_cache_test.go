package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func publicIP(s string) []net.IPAddr { return []net.IPAddr{{IP: net.ParseIP(s)}} }

func TestDNSCacheServesFreshAnswersWithoutLookingAgain(t *testing.T) {
	clock := newFakeClock()
	var lookups atomic.Int32
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) {
		lookups.Add(1)
		return publicIP("1.1.1.1"), nil
	}, clock.Now, 45*time.Second)
	for i := 0; i < 100; i++ {
		addrs, err := cache.resolve(context.Background(), "rpc.example.test")
		if err != nil || len(addrs) != 1 {
			t.Fatalf("resolve = %v, %v", addrs, err)
		}
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("lookups = %d, want 1 for 100 resolutions", got)
	}
	// Host names are case-insensitive.
	if _, err := cache.resolve(context.Background(), "RPC.Example.Test"); err != nil || lookups.Load() != 1 {
		t.Fatalf("a differently cased host looked up again (lookups=%d, err=%v)", lookups.Load(), err)
	}
}

func TestDNSCacheExpires(t *testing.T) {
	clock := newFakeClock()
	var lookups atomic.Int32
	answer := "1.1.1.1"
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) {
		lookups.Add(1)
		return publicIP(answer), nil
	}, clock.Now, 45*time.Second)
	if addrs, _ := cache.resolve(context.Background(), "h.example.test"); addrs[0].IP.String() != "1.1.1.1" {
		t.Fatalf("first answer = %v", addrs)
	}
	clock.Advance(44 * time.Second)
	cache.resolve(context.Background(), "h.example.test")
	if lookups.Load() != 1 {
		t.Fatalf("looked up again before the TTL (%d)", lookups.Load())
	}
	answer = "8.8.8.8"
	clock.Advance(2 * time.Second)
	addrs, _ := cache.resolve(context.Background(), "h.example.test")
	if lookups.Load() != 2 || addrs[0].IP.String() != "8.8.8.8" {
		t.Fatalf("after the TTL: lookups=%d answer=%v, want a fresh lookup returning 8.8.8.8", lookups.Load(), addrs)
	}
}

// A failed or empty lookup is never stored: the next call asks again, and a
// provider whose DNS was down for a second is not written off for the TTL.
func TestDNSCacheDoesNotStoreFailures(t *testing.T) {
	clock := newFakeClock()
	var lookups atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) {
		lookups.Add(1)
		if fail.Load() {
			return nil, errors.New("servfail")
		}
		return publicIP("1.1.1.1"), nil
	}, clock.Now, 45*time.Second)
	for i := 0; i < 3; i++ {
		if _, err := cache.resolve(context.Background(), "h.example.test"); err == nil {
			t.Fatal("a failing lookup succeeded")
		}
	}
	if lookups.Load() != 3 {
		t.Fatalf("lookups = %d, want 3 (failures must not be cached)", lookups.Load())
	}
	fail.Store(false)
	if _, err := cache.resolve(context.Background(), "h.example.test"); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	// An empty answer is a failure too.
	empty := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }, clock.Now, time.Minute)
	for i := 0; i < 2; i++ {
		if _, err := empty.resolve(context.Background(), "e.example.test"); err == nil {
			t.Fatal("an empty answer must be an error")
		}
	}
}

func TestDNSCacheSharesOneLookupAmongConcurrentCallers(t *testing.T) {
	clock := newFakeClock()
	var lookups atomic.Int32
	release := make(chan struct{})
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) {
		lookups.Add(1)
		<-release
		return publicIP("1.1.1.1"), nil
	}, clock.Now, 45*time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.resolve(context.Background(), "h.example.test"); err != nil {
				t.Errorf("resolve: %v", err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := lookups.Load(); got != 1 {
		t.Fatalf("20 concurrent resolutions did %d lookups, want 1", got)
	}
}

func TestDNSCacheStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	cache := newDNSCache(func(ctx context.Context, _ string) ([]net.IPAddr, error) {
		<-block
		return publicIP("1.1.1.1"), nil
	}, nil, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := cache.resolve(ctx, "slow.example.test"); err == nil {
		t.Fatal("expected the caller's deadline")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the caller was held past its own deadline")
	}
}

func TestDNSCacheIsBounded(t *testing.T) {
	clock := newFakeClock()
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) { return publicIP("1.1.1.1"), nil }, clock.Now, time.Minute)
	for i := 0; i < dnsCacheMaxHosts+50; i++ {
		if _, err := cache.resolve(context.Background(), fmt.Sprintf("h%d.example.test", i)); err != nil {
			t.Fatal(err)
		}
	}
	cache.mu.Lock()
	size := len(cache.entries)
	cache.mu.Unlock()
	if size > dnsCacheMaxHosts {
		t.Fatalf("cache holds %d hosts, bound is %d", size, dnsCacheMaxHosts)
	}
	// Expired entries make room.
	clock.Advance(2 * time.Minute)
	if _, err := cache.resolve(context.Background(), "late.example.test"); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	_, stored := cache.entries["late.example.test"]
	cache.mu.Unlock()
	if !stored {
		t.Fatal("a new host was not cached although every old entry had expired")
	}
}

func TestDNSCacheRotatesTheFirstAddress(t *testing.T) {
	cache := newDNSCache(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("1.0.0.1")}}, nil
	}, nil, time.Minute)
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		addrs, err := cache.resolve(context.Background(), "multi.example.test")
		if err != nil {
			t.Fatal(err)
		}
		seen[addrs[0].IP.String()] = true
	}
	if len(seen) != 2 {
		t.Fatalf("first addresses seen = %v, want both", seen)
	}
}

// --- the cache inside the transport -------------------------------------------------

// A host name is resolved once for many calls, and the checked address is the one
// that is dialled.
func TestTransportResolvesOncePerTTLForManyCalls(t *testing.T) {
	var lookups atomic.Int32
	fx := newTLSFixture(t, false, blockNumberHandler())
	client := fx.client(5*time.Second, func(c *publicTransportConfig) {
		c.idleTimeout = time.Millisecond // force a dial per call: the cache must still answer
		inner := c.lookup
		c.lookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
			lookups.Add(1)
			return inner(ctx, host)
		}
	})
	defer client.CloseIdleConnections()
	pool := NewPoolWithHTTPClient(fx.url(), client)
	for i := 0; i < 20; i++ {
		callBlockNumber(t, pool, 1)
		time.Sleep(5 * time.Millisecond)
	}
	if got := lookups.Load(); got != 1 {
		t.Fatalf("DNS lookups = %d over 20 calls, want 1", got)
	}
	if fx.conns.Load() < 2 {
		t.Fatalf("test setup: only %d connections, the cache was not exercised across dials", fx.conns.Load())
	}
}

// --- security equivalence: reuse and caching must not weaken the address check ----

// DNS rebinding: a name that answered with a public address and later answers with
// a private one. Connections already open keep talking to the address that was
// checked when they were dialled (a TCP connection cannot change its peer); every
// NEW connection is checked against the current answer and refused.
func TestRebindingToAPrivateAddressIsRefusedOnTheNextDial(t *testing.T) {
	fx := newTLSFixture(t, false, blockNumberHandler())
	clock := newFakeClock()
	var privateNow atomic.Bool
	var dials atomic.Int32
	var lastDialed atomic.Value
	client := fx.client(5*time.Second, func(c *publicTransportConfig) {
		c.now = clock.Now
		c.dnsTTL = 45 * time.Second
		c.lookup = func(context.Context, string) ([]net.IPAddr, error) {
			if privateNow.Load() {
				return publicIP("10.0.0.7"), nil
			}
			return publicIP("1.1.1.1"), nil
		}
		inner := c.dial
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			lastDialed.Store(address)
			return inner(ctx, network, address)
		}
	})
	defer client.CloseIdleConnections()
	pool := NewPoolWithHTTPClient(fx.url(), client)

	callBlockNumber(t, pool, 3) // validated, dialled once, kept alive
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want 1", dials.Load())
	}
	if addr, _ := lastDialed.Load().(string); !strings.HasPrefix(addr, "1.1.1.1:") {
		t.Fatalf("dialled %q, want the validated address", addr)
	}

	privateNow.Store(true)
	clock.Advance(2 * time.Minute) // the cached answer expires
	callBlockNumber(t, pool, 3)    // the open connection is still the validated peer: still fine
	if dials.Load() != 1 {
		t.Fatalf("a call after the DNS change dialled again (%d)", dials.Load())
	}

	client.CloseIdleConnections() // the next call needs a new connection
	if _, err := pool.BlockNumber(context.Background()); err == nil {
		t.Fatal("a call to a name that now resolves to a private address succeeded")
	}
	if dials.Load() != 1 {
		t.Fatalf("a private address was dialled (%d dials)", dials.Load())
	}
}

// The cache stores what DNS said, not a verdict: a private answer that is cached
// is refused again on every use, not let through the second time.
func TestCachedPrivateAnswerIsRefusedOnEveryUse(t *testing.T) {
	var lookups, dials atomic.Int32
	client := newPublicHTTPClient(time.Second,
		func(context.Context, string) ([]net.IPAddr, error) {
			lookups.Add(1)
			return publicIP("192.168.1.10"), nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("must not dial")
		})
	defer client.CloseIdleConnections()
	for i := 0; i < 5; i++ {
		_, err := client.Get("http://rpc.example.test:8545/")
		if err == nil || !strings.Contains(err.Error(), "rpc destination is not public") {
			t.Fatalf("attempt %d: error = %v, want a refusal", i, err)
		}
	}
	if dials.Load() != 0 {
		t.Fatalf("dials = %d, want 0", dials.Load())
	}
	if lookups.Load() != 1 {
		t.Fatalf("lookups = %d, want the one answer cached and refused every time", lookups.Load())
	}
}

// A mixed answer (one public, one private) is refused as a whole, cached or not.
func TestCachedMixedAnswerIsRefusedOnEveryUse(t *testing.T) {
	var dials atomic.Int32
	client := newPublicHTTPClient(time.Second,
		func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("must not dial")
		})
	defer client.CloseIdleConnections()
	for i := 0; i < 3; i++ {
		if _, err := client.Get("http://rpc.example.test:8545/"); err == nil || !strings.Contains(err.Error(), "rpc destination is not public") {
			t.Fatalf("attempt %d: error = %v", i, err)
		}
	}
	if dials.Load() != 0 {
		t.Fatalf("dials = %d, want 0", dials.Load())
	}
}

// IP literals skip DNS but not the check.
func TestPrivateIPLiteralIsRefusedWithoutLookup(t *testing.T) {
	var lookups, dials atomic.Int32
	client := newPublicHTTPClient(time.Second,
		func(context.Context, string) ([]net.IPAddr, error) { lookups.Add(1); return nil, nil },
		func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("must not dial")
		})
	defer client.CloseIdleConnections()
	for _, target := range []string{"http://127.0.0.1:8545/", "http://10.1.2.3/", "http://[::1]:8545/", "http://169.254.169.254/latest"} {
		if _, err := client.Get(target); err == nil || !strings.Contains(err.Error(), "rpc destination is not public") {
			t.Fatalf("%s: error = %v", target, err)
		}
	}
	if lookups.Load() != 0 || dials.Load() != 0 {
		t.Fatalf("lookups=%d dials=%d, want neither", lookups.Load(), dials.Load())
	}
}

// The production transport refuses the loopback server through every Pool path.
func TestSharedPoolRefusesLoopbackEvenWhenWarm(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		rpcResult(w, `"0x1"`)
	}))
	defer server.Close()
	pool := NewPool(server.URL)
	for i := 0; i < 3; i++ {
		if _, err := pool.BlockNumber(context.Background()); err == nil {
			t.Fatal("loopback endpoint unexpectedly succeeded")
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("loopback server calls = %d, want 0", calls.Load())
	}
}
