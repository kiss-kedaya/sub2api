package rpc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pacedFixture is a pool over a production-shaped client whose pacing is the real
// default (ceiling 20/s, burst 16) unless a test says otherwise.
func (fx *tlsFixture) pacedPool(t testing.TB, mutate ...func(*publicTransportConfig)) (*Pool, *http.Client) {
	t.Helper()
	client := fx.client(15*time.Second, append([]func(*publicTransportConfig){func(c *publicTransportConfig) { c.ratePerSec = 0 }}, mutate...)...)
	t.Cleanup(client.CloseIdleConnections)
	return NewPoolWithHTTPClient(fx.url(), client), client
}

func useFakeClock(t *testing.T) *fakeClock {
	t.Helper()
	clock := newFakeClock()
	nowFunc = clock.Now
	t.Cleanup(func() { nowFunc = time.Now })
	return clock
}

func failureOf(t *testing.T, err error) *Failure {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not an rpc.Failure", err)
	}
	return failure
}

// A 429 with Retry-After pauses the endpoint for that long (the caller waits it out
// when it is within the bound) and halves the rate.
func TestPoolHonoursRetryAfterAndHalvesTheRate(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		first := len(arrivals) == 1
		mu.Unlock()
		if first {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, client := fx.pacedPool(t)
	_, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil)
	failure := failureOf(t, err)
	if failure.HTTPStatus != http.StatusTooManyRequests || failure.Throttled {
		t.Fatalf("first call: %+v, want the node's own 429", failure)
	}
	if got := trafficOf(t, client, fx.url()); got.RatePerSec != defaultRatePerSec/2 || got.HTTP429 != 1 {
		t.Fatalf("after the 429: %+v, want the rate halved to %v", got, defaultRatePerSec/2)
	}
	start := time.Now()
	if _, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil); err != nil {
		t.Fatalf("the call that waited out the Retry-After failed: %v", err)
	}
	if took := time.Since(start); took < 800*time.Millisecond || took > 1900*time.Millisecond {
		t.Fatalf("second call took %v, want about the 1s Retry-After", took)
	}
	mu.Lock()
	defer mu.Unlock()
	if gap := arrivals[1].Sub(arrivals[0]); gap < 900*time.Millisecond {
		t.Fatalf("the node saw the next request %v after the 429, want at least the 1s it asked for", gap)
	}
}

// Over the bound the call fails at once, locally, without asking the node; the
// pool rotates to the next endpoint and the throttled one is not asked again until
// its Retry-After is over. Nothing here puts it on a health cooldown of its own.
func TestRetryAfterBeyondTheBoundIsRefusedLocallyAndRotates(t *testing.T) {
	clock := useFakeClock(t)
	var aHits, bHits atomic.Int32
	var aOpen atomic.Bool
	a := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		aHits.Add(1)
		if aOpen.Load() {
			rpcResult(w, `"0x10"`)
			return
		}
		w.Header().Set("Retry-After", "30")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}))
	b := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		bHits.Add(1)
		rpcResult(w, `"0x10"`)
	}))
	client := a.client(15*time.Second, func(c *publicTransportConfig) {
		c.now = clock.Now
		c.ratePerSec = 0
		// One client reaches both servers: route by port.
		c.tlsConfig.RootCAs.AddCert(b.server.Certificate())
		inner := c.dial
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, ":"+b.port) {
				return (&net.Dialer{}).DialContext(ctx, network, b.addr())
			}
			return inner(ctx, network, address)
		}
	})
	defer client.CloseIdleConnections()
	pool := NewPoolWithHTTPClient(a.url()+","+b.url(), client)

	if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
		t.Fatalf("the first call must fail over to the healthy endpoint: %v", err)
	}
	if aHits.Load() != 1 {
		t.Fatalf("endpoint A was asked %d times, want 1", aHits.Load())
	}
	// Health cooldown (a few seconds) is over, the Retry-After (30s) is not.
	clock.Advance(10 * time.Second)
	for i := 0; i < 20; i++ {
		start := time.Now()
		if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("call %d took %v: a refused local call must not wait", i, took)
		}
	}
	if aHits.Load() != 1 {
		t.Fatalf("endpoint A was asked %d times during its Retry-After, want 1", aHits.Load())
	}
	_, err := pool.callOne(context.Background(), a.url(), "eth_blockNumber", nil)
	failure := failureOf(t, err)
	if !failure.Throttled || failure.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("local refusal = %+v, want a throttled 429", failure)
	}
	if CategoryOf(err) != CategoryTransport || !IsTransient(err) {
		t.Fatalf("a local refusal must read as a transient 429 (category %s)", CategoryOf(err))
	}
	if got := endpointCooldown(err); got != 0 {
		t.Fatalf("a local refusal put the node on a %v cooldown; it never asked the node", got)
	}
	if SafeError(err) != "RPC HTTP request failed (status 429)" {
		t.Fatalf("SafeError = %q", SafeError(err))
	}
	if traffic := trafficOf(t, client, a.url()); traffic.Throttled == 0 {
		t.Fatalf("traffic = %+v, want the local refusals counted", traffic)
	}
	// After the Retry-After the node is asked again.
	aOpen.Store(true)
	clock.Advance(40 * time.Second)
	for i := 0; i < 4 && aHits.Load() < 2; i++ {
		if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
			t.Fatal(err)
		}
	}
	if aHits.Load() < 2 {
		t.Fatal("endpoint A was never asked again after its Retry-After ended")
	}
}

// 402 means the allowance or the prepaid balance is spent, whatever the body says
// (some providers send none): the long quota cooldown applies and the rotation
// moves on, instead of asking again every few seconds.
func TestPaymentRequiredCoolsTheEndpointForFifteenMinutes(t *testing.T) {
	for name, body := range map[string]string{"empty body": "", "unrelated body": "nope"} {
		t.Run(name, func(t *testing.T) {
			clock := useFakeClock(t)
			var aHits, bHits atomic.Int32
			a := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				aHits.Add(1)
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(body))
			}))
			b := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = requestMethod(r)
				bHits.Add(1)
				rpcResult(w, `"0x10"`)
			}))
			client := a.client(15*time.Second, func(c *publicTransportConfig) {
				c.tlsConfig.RootCAs.AddCert(b.server.Certificate())
				inner := c.dial
				c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
					if strings.HasSuffix(address, ":"+b.port) {
						return (&net.Dialer{}).DialContext(ctx, network, b.addr())
					}
					return inner(ctx, network, address)
				}
			})
			defer client.CloseIdleConnections()
			pool := NewPoolWithHTTPClient(a.url()+","+b.url(), client)

			if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
				t.Fatalf("the first call must rotate to the healthy endpoint: %v", err)
			}
			for i := 0; i < 50; i++ {
				if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
					t.Fatal(err)
				}
			}
			if aHits.Load() != 1 {
				t.Fatalf("the exhausted endpoint was asked %d times, want 1", aHits.Load())
			}
			clock.Advance(14 * time.Minute)
			for i := 0; i < 5; i++ {
				_, _ = pool.Call(context.Background(), "eth_blockNumber", nil)
			}
			if aHits.Load() != 1 {
				t.Fatalf("asked again after 14 minutes (%d)", aHits.Load())
			}
			clock.Advance(90 * time.Second)
			for i := 0; i < 5; i++ {
				_, _ = pool.Call(context.Background(), "eth_blockNumber", nil)
			}
			if aHits.Load() != 2 {
				t.Fatalf("after the 15 minute cooldown the endpoint was asked %d times in total, want 2", aHits.Load())
			}
			// On its own, the failure says why.
			_, err := NewPoolWithHTTPClient(a.url(), client).callOne(context.Background(), a.url(), "eth_blockNumber", nil)
			if f := failureOf(t, err); !f.Quota || f.HTTPStatus != http.StatusPaymentRequired {
				t.Fatalf("failure = %+v, want a quota-exhausted 402", f)
			}
			if got := endpointCooldown(err); got != quotaEndpointCooldown {
				t.Fatalf("cooldown = %v, want %v", got, quotaEndpointCooldown)
			}
		})
	}
}

// With the default pacing a normal burst (a scan pass of up to the burst size at
// once) is not delayed at all: the limiter only ever binds on a sustained flood.
func TestHealthyBurstIsNotDelayed(t *testing.T) {
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		time.Sleep(20 * time.Millisecond)
		rpcResult(w, `"0x10"`)
	}))
	pool, client := fx.pacedPool(t)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < int(rateBurst); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := pool.BlockNumber(context.Background()); err != nil {
				t.Errorf("call failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if took := time.Since(start); took > 400*time.Millisecond {
		t.Fatalf("a burst of %d took %v with 20ms answers: the limiter delayed a healthy endpoint", int(rateBurst), took)
	}
	if got := trafficOf(t, client, fx.url()); got.Throttled != 0 || got.RatePerSec != defaultRatePerSec {
		t.Fatalf("traffic = %+v", got)
	}
}

// One throttled endpoint costs the others nothing.
func TestThrottledEndpointDoesNotSlowOtherEndpoints(t *testing.T) {
	a := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	b := newTLSFixture(t, false, blockNumberHandler())
	client := a.client(15*time.Second, func(c *publicTransportConfig) {
		c.ratePerSec = 0
		c.tlsConfig.RootCAs.AddCert(b.server.Certificate())
		inner := c.dial
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, ":"+b.port) {
				return (&net.Dialer{}).DialContext(ctx, network, b.addr())
			}
			return inner(ctx, network, address)
		}
	})
	defer client.CloseIdleConnections()
	pool := NewPoolWithHTTPClient(a.url(), client)
	if _, err := pool.callOne(context.Background(), a.url(), "eth_blockNumber", nil); err == nil {
		t.Fatal("expected the 429")
	}
	poolB := NewPoolWithHTTPClient(b.url(), client)
	start := time.Now()
	for i := 0; i < int(rateBurst); i++ {
		if _, err := poolB.callOne(context.Background(), b.url(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("call to the healthy endpoint %d: %v", i, err)
		}
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the healthy endpoint took %v while another was throttled", took)
	}
}

// The wait is bounded by limiterMaxWait and by the caller's own deadline.
func TestPoolCallWaitsAtMostTheBound(t *testing.T) {
	old := limiterMaxWait
	limiterMaxWait = 60 * time.Millisecond
	t.Cleanup(func() { limiterMaxWait = old })
	fx := newTLSFixture(t, false, blockNumberHandler())
	pool, _ := fx.pacedPool(t, func(c *publicTransportConfig) { c.ratePerSec = 2 }) // one call per 500ms
	for i := 0; i < int(rateBurst); i++ {
		if _, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	_, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil)
	if f := failureOf(t, err); !f.Throttled {
		t.Fatalf("failure = %+v, want a local refusal", f)
	}
	if took := time.Since(start); took > 40*time.Millisecond {
		t.Fatalf("a refused call took %v: the wait was not bounded", took)
	}
}

// Many callers, 429s mixed in: no race, nothing hangs, every call ends.
func TestPoolUnderConcurrencyWithRateLimits(t *testing.T) {
	old := limiterMaxWait
	limiterMaxWait = 50 * time.Millisecond // keep the throttled stretch short
	t.Cleanup(func() { limiterMaxWait = old })
	var n atomic.Int64
	fx := newTLSFixture(t, true, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		if n.Add(1)%9 == 0 {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, client := fx.pacedPool(t, func(c *publicTransportConfig) { c.ratePerSec = 1000 })
	var wg sync.WaitGroup
	var ok, limited, refused atomic.Int64
	start := time.Now()
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil)
				switch {
				case err == nil:
					ok.Add(1)
				case failureOf(t, err).Throttled:
					refused.Add(1)
				default:
					limited.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("320 calls took %v", took)
	}
	if ok.Load()+limited.Load()+refused.Load() != 320 {
		t.Fatalf("calls accounted for = %d, want 320", ok.Load()+limited.Load()+refused.Load())
	}
	if ok.Load() == 0 || limited.Load() == 0 {
		t.Fatalf("ok=%d limited=%d refused=%d: the scenario did not exercise both paths", ok.Load(), limited.Load(), refused.Load())
	}
	_ = trafficOf(t, client, fx.url())
}

// A stretch of good answers after a 429 brings the rate back to the ceiling.
func TestRateRecoversThroughThePool(t *testing.T) {
	clock := newFakeClock()
	var first atomic.Bool
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		if first.CompareAndSwap(false, true) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, client := fx.pacedPool(t, func(c *publicTransportConfig) { c.now = clock.Now })
	_, _ = pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil)
	if got := trafficOf(t, client, fx.url()); got.RatePerSec != defaultRatePerSec/2 {
		t.Fatalf("rate after the 429 = %v", got.RatePerSec)
	}
	for i := 0; i < 40; i++ {
		clock.Advance(time.Second)
		if _, err := pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := trafficOf(t, client, fx.url()); got.RatePerSec != defaultRatePerSec {
		t.Fatalf("rate after 40s of good answers = %v, want back at %v", got.RatePerSec, defaultRatePerSec)
	}
}

// --- traffic summary --------------------------------------------------------------------

func TestTrafficCountsEachOutcome(t *testing.T) {
	var n atomic.Int64
	release := make(chan struct{})
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		switch n.Add(1) {
		case 1, 2:
			rpcResult(w, `"0x10"`)
		case 3:
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		case 4:
			w.WriteHeader(http.StatusPaymentRequired)
		case 5:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}))
	defer close(release)
	pool, client := fx.pacedPool(t, func(c *publicTransportConfig) {
		c.ratePerSec = 1000
		inner := c.dial
		c.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, ":1") {
				return nil, errors.New("connection refused")
			}
			return inner(ctx, network, address)
		}
	})
	for i := 0; i < 5; i++ {
		_, _ = pool.callOne(context.Background(), fx.url(), "eth_blockNumber", nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, err := pool.callOne(ctx, fx.url(), "eth_blockNumber", nil)
	cancel()
	if CategoryOf(err) != CategoryTimeout {
		t.Fatalf("the hanging call: %v", err)
	}
	got := trafficOf(t, client, fx.url())
	if got.Requests != 6 || got.OK != 2 || got.HTTP429 != 1 || got.HTTP402 != 1 || got.HTTPOther != 1 || got.Timeouts != 1 || got.ConnErrors != 0 {
		t.Fatalf("traffic = %+v", got)
	}
	if got.NewConns != 1 {
		t.Fatalf("new connections = %d, want 1", got.NewConns)
	}
	if got.MaxMS < 100 || got.AvgMS <= 0 {
		t.Fatalf("latency avg=%dms max=%dms, want the 150ms timeout reflected", got.AvgMS, got.MaxMS)
	}
	// A refused dial is a connection error of its own endpoint.
	dead := "https://example.com:1"
	_, _ = pool.callOne(context.Background(), dead, "eth_blockNumber", nil)
	if got := trafficOf(t, client, dead); got.ConnErrors != 1 || got.Requests != 1 {
		t.Fatalf("dead endpoint traffic = %+v", got)
	}
}

func TestTrafficSummaryIsEmittedOncePerWindowAndOnlyWithTraffic(t *testing.T) {
	clock := newFakeClock()
	var mu sync.Mutex
	var summaries []EndpointTraffic
	var hosts []string
	e := newEndpointTraffic("rpc.example.test", 20, clock.Now, func(host string, s EndpointTraffic) {
		mu.Lock()
		defer mu.Unlock()
		hosts = append(hosts, host)
		summaries = append(summaries, s)
	})
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(summaries) }
	for i := 0; i < 5; i++ {
		e.sent(false)
		e.answeredWith(200, nil, i == 0)
	}
	clock.Advance(59 * time.Second)
	e.sent(false)
	if count() != 0 {
		t.Fatal("a summary was emitted before the window was over")
	}
	clock.Advance(2 * time.Second)
	e.sent(false) // closes the first window, opens the next
	if count() != 1 {
		t.Fatalf("summaries = %d, want 1", count())
	}
	if s := summaries[0]; s.Requests != 6 || s.OK != 5 || s.NewConns != 1 || s.WindowSeconds != 61 || hosts[0] != "rpc.example.test" {
		t.Fatalf("summary = %+v host=%s", s, hosts[0])
	}
	// Idle: nothing is emitted, however long it lasts, until there is traffic again.
	clock.Advance(10 * time.Minute)
	if count() != 1 {
		t.Fatal("an idle endpoint emitted a summary")
	}
	// A window that only saw a stray latency record from before is not "traffic".
	clock.Advance(61 * time.Second)
	e.finished(time.Millisecond)
	clock.Advance(61 * time.Second)
	e.finished(time.Millisecond)
	if count() != 2 {
		t.Fatalf("summaries = %d, want 2 (the second window had one request)", count())
	}
}

// The line carries the host name and numbers, never the URL: the path and query
// of an endpoint hold the provider key.
func TestTrafficLogLineNamesOnlyTheHost(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	clock := newFakeClock()
	fx := newTLSFixture(t, false, blockNumberHandler())
	endpoint := fx.url() + "/v1/rpc/SECRETKEY0123456789?apikey=SECRETQUERY"
	client := fx.client(5*time.Second, func(c *publicTransportConfig) { c.now = clock.Now })
	defer client.CloseIdleConnections()
	pool := NewPoolWithHTTPClient(endpoint, client)
	callBlockNumber(t, pool, 3)
	clock.Advance(2 * time.Minute)
	callBlockNumber(t, pool, 1) // closes the window and logs it

	out := buf.String()
	if !strings.Contains(out, "rpc endpoint traffic") || !strings.Contains(out, "endpoint_host=example.com") {
		t.Fatalf("no summary line for the host:\n%s", out)
	}
	for _, leak := range []string{"SECRETKEY", "SECRETQUERY", "/v1/rpc", "apikey", ":" + fx.port} {
		if strings.Contains(out, leak) {
			t.Fatalf("the summary leaked %q:\n%s", leak, out)
		}
	}
	for _, field := range []string{"requests=3", "ok=3", "http_429=0", "http_402=0", "timeouts=0", "conn_errors=0", "new_conns=1", "rate_per_s="} {
		if !strings.Contains(out, field) {
			t.Fatalf("summary has no %q:\n%s", field, out)
		}
	}
}

func TestEndpointTrafficForReadsTheSharedTransport(t *testing.T) {
	if _, ok := EndpointTrafficFor("https://never-used.example.test/key"); ok {
		t.Fatal("an unused endpoint has no traffic")
	}
	if _, ok := EndpointTrafficFor("://bad"); ok {
		t.Fatal("an unparsable endpoint has no traffic")
	}
	endpoint := "https://shared-transport-probe.example.test/rpc/key"
	parsed := mustParseURL(t, endpoint)
	state := sharedPublicTransport().traffic.get(parsed)
	state.sent(false)
	state.answeredWith(429, nil, true)
	got, ok := EndpointTrafficFor(endpoint + "/") // a trailing slash is the same endpoint
	if !ok || got.Requests != 1 || got.HTTP429 != 1 || got.NewConns != 1 {
		t.Fatalf("EndpointTrafficFor = %+v, %v", got, ok)
	}
}

func TestSetEndpointRateCeilingFollowsTheConfiguredValue(t *testing.T) {
	endpoint := "https://ceiling-probe.example.test/rpc"
	state := sharedPublicTransport().traffic.get(mustParseURL(t, endpoint))
	t.Cleanup(func() { SetEndpointRateCeiling(0) })
	SetEndpointRateCeiling(50)
	if got := state.snapshot().RatePerSec; got != 50 {
		t.Fatalf("rate = %v, want 50", got)
	}
	SetEndpointRateCeiling(5000) // beyond the maximum: clamped
	if got := state.snapshot().RatePerSec; got != MaxRatePerSec {
		t.Fatalf("rate = %v, want the maximum %d", got, MaxRatePerSec)
	}
	SetEndpointRateCeiling(0) // unset: the default
	if got := state.snapshot().RatePerSec; got != defaultRatePerSec {
		t.Fatalf("rate = %v, want the default %v", got, defaultRatePerSec)
	}
	if other := sharedPublicTransport().traffic.get(mustParseURL(t, "https://ceiling-probe-2.example.test/rpc")); other.snapshot().RatePerSec != defaultRatePerSec {
		t.Fatalf("an endpoint created later did not get the ceiling")
	}
}
