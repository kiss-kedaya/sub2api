package rpc

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync"
	"testing"
	"time"
)

func newTestEndpoint(clock *fakeClock, ceiling float64) *endpointTraffic {
	return newEndpointTraffic("rpc.example.test", ceiling, clock.Now, nil)
}

func mustReserve(t *testing.T, e *endpointTraffic, clock *fakeClock, maxWait time.Duration) time.Duration {
	t.Helper()
	wait, ok := e.reserve(clock.Now(), maxWait)
	if !ok {
		t.Fatalf("reserve refused at %v", clock.Now())
	}
	return wait
}

func approx(got, want time.Duration, slack time.Duration) bool {
	return got >= want-slack && got <= want+slack
}

// A burst up to rateBurst starts at once; after that calls are spaced at the rate.
func TestLimiterAllowsABurstThenPacesAtTheRate(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	for i := 0; i < int(rateBurst); i++ {
		if wait := mustReserve(t, e, clock, 2*time.Second); wait != 0 {
			t.Fatalf("call %d of the burst waited %v", i, wait)
		}
	}
	for i := 1; i <= 5; i++ {
		wait := mustReserve(t, e, clock, 2*time.Second)
		if want := time.Duration(i) * 50 * time.Millisecond; !approx(wait, want, time.Millisecond) {
			t.Fatalf("call %d past the burst waits %v, want %v (20/s)", i, wait, want)
		}
	}
	// Time passing refills: two seconds earn forty calls' worth, capped at the burst.
	clock.Advance(2 * time.Second)
	for i := 0; i < int(rateBurst); i++ {
		if wait := mustReserve(t, e, clock, 2*time.Second); wait != 0 {
			t.Fatalf("after refilling, call %d waited %v", i, wait)
		}
	}
	if wait := mustReserve(t, e, clock, 2*time.Second); wait == 0 {
		t.Fatal("the bucket held more than the burst")
	}
}

// The wait is bounded: past the bound the call is refused and claims nothing.
func TestLimiterWaitIsBoundedAndRefusalClaimsNothing(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 10)
	admitted := 0
	for {
		if _, ok := e.reserve(clock.Now(), 2*time.Second); !ok {
			break
		}
		admitted++
		if admitted > 1000 {
			t.Fatal("the limiter never refused")
		}
	}
	// The burst, then 10 calls/s for the 2 seconds a call may wait.
	if want := int(rateBurst) + 20; admitted != want {
		t.Fatalf("admitted %d calls before refusing, want %d", admitted, want)
	}
	e.mu.Lock()
	before := e.tokens
	e.mu.Unlock()
	if _, ok := e.reserve(clock.Now(), 2*time.Second); ok {
		t.Fatal("a refused call was admitted the second time")
	}
	e.mu.Lock()
	after := e.tokens
	e.mu.Unlock()
	if before != after {
		t.Fatalf("a refused call changed the balance: %v -> %v", before, after)
	}
}

// 429s that were already in flight when the first one arrived are one event, not
// sixteen: the rate halves once.
func TestRateLimitedHalvesOncePerEvent(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	sentAt := clock.Now()
	clock.Advance(100 * time.Millisecond)
	for i := 0; i < int(rateBurst); i++ {
		e.rateLimited(sentAt, clock.Now(), 0)
	}
	e.mu.Lock()
	rate := e.rate
	e.mu.Unlock()
	if rate != 10 {
		t.Fatalf("rate after a burst of 429s = %v, want 10 (halved once)", rate)
	}
	// A call sent AFTER the cut that is still refused is a new event.
	clock.Advance(100 * time.Millisecond)
	e.rateLimited(clock.Now().Add(-10*time.Millisecond), clock.Now(), 0)
	e.mu.Lock()
	rate = e.rate
	e.mu.Unlock()
	if rate != 5 {
		t.Fatalf("rate after a second event = %v, want 5", rate)
	}
}

func TestRateNeverDropsBelowTheFloor(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	for i := 0; i < 30; i++ {
		clock.Advance(time.Second)
		e.rateLimited(clock.Now(), clock.Now(), 0)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rate != rateFloor {
		t.Fatalf("rate = %v, want the floor %v", e.rate, rateFloor)
	}
}

// After a 429 nothing bursts out of the bucket.
func TestRateLimitedDrainsTheBucket(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 0)
	if wait := mustReserve(t, e, clock, 2*time.Second); wait == 0 {
		t.Fatal("a call right after a 429 went out without waiting")
	}
}

func TestRetryAfterBlocksTheEndpointAndIsCapped(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 30*time.Second)
	if _, ok := e.reserve(clock.Now(), 2*time.Second); ok {
		t.Fatal("a call was admitted during a 30s Retry-After under a 2s bound")
	}
	wait, ok := e.reserve(clock.Now(), time.Hour)
	if !ok || wait < 29*time.Second {
		t.Fatalf("wait = %v (ok=%v), want about 30s", wait, ok)
	}
	// A hostile or buggy header cannot park an endpoint for longer than the cap.
	hostile := newTestEndpoint(clock, 20)
	hostile.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 24*time.Hour)
	wait, ok = hostile.reserve(clock.Now(), 24*time.Hour)
	if !ok || wait > maxRetryAfter+time.Second {
		t.Fatalf("wait = %v, want at most the %v cap", wait, maxRetryAfter)
	}
	// Once it has passed, calls flow again (at the halved rate).
	clock.Advance(31 * time.Second)
	if wait := mustReserve(t, e, clock, 2*time.Second); wait > time.Second {
		t.Fatalf("after the Retry-After, wait = %v", wait)
	}
}

// Nothing is earned during a Retry-After, so the burst does not pile up behind it
// and get released the moment it ends.
func TestNoBurstIsReleasedWhenARetryAfterEnds(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 10*time.Second)
	clock.Advance(10*time.Second + 10*time.Millisecond)
	immediate := 0
	for i := 0; i < 16; i++ {
		wait, ok := e.reserve(clock.Now(), 2*time.Second)
		if ok && wait == 0 {
			immediate++
		}
	}
	if immediate > 1 {
		t.Fatalf("%d calls started at once when the Retry-After ended, want at most 1", immediate)
	}
}

// Answers without a 429 bring the rate back to the ceiling, a little at a time,
// and never above it.
func TestRateRecoversOnSuccessUpToTheCeiling(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 0) // 20 -> 10
	clock.Advance(time.Millisecond)
	rateNow := func() float64 { e.mu.Lock(); defer e.mu.Unlock(); return e.rate }
	if got := rateNow(); got != 10 {
		t.Fatalf("rate = %v, want 10", got)
	}
	var last float64 = 10
	for second := 1; second <= 45; second++ {
		clock.Advance(time.Second)
		e.answered(clock.Now())
		got := rateNow()
		if got < last-1e-9 {
			t.Fatalf("the rate fell during a stretch of answers: %v -> %v", last, got)
		}
		if got > 20+1e-9 {
			t.Fatalf("the rate rose above the ceiling: %v", got)
		}
		last = got
		if second == 5 && !(got > 10 && got < 12.5) {
			t.Fatalf("after 5s of answers rate = %v, want a slow climb (about +1.7)", got)
		}
	}
	if math.Abs(last-20) > 1e-9 {
		t.Fatalf("after 45s of answers rate = %v, want back at the ceiling 20", last)
	}
}

// A 429 sets the rate back immediately; recovery is not allowed during a Retry-After.
func TestNoRecoveryDuringARetryAfter(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 30*time.Second)
	clock.Advance(10 * time.Second)
	e.answered(clock.Now())
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rate != 10 {
		t.Fatalf("rate recovered during a Retry-After: %v", e.rate)
	}
}

func TestSetCeilingLowersAndRaisesAHealthyRate(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	e.setCeiling(5)
	e.mu.Lock()
	if e.rate != 5 {
		t.Fatalf("rate = %v after lowering the ceiling to 5", e.rate)
	}
	e.mu.Unlock()
	e.setCeiling(200)
	e.mu.Lock()
	if e.rate != 200 {
		t.Fatalf("a healthy endpoint did not follow a raised ceiling: %v", e.rate)
	}
	e.mu.Unlock()
	// A throttled endpoint keeps its rate and climbs on its own.
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 0)
	e.setCeiling(400)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rate != 100 {
		t.Fatalf("a throttled endpoint jumped to the new ceiling: %v", e.rate)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	date := now.Add(20 * time.Second).UTC().Format(http.TimeFormat)
	past := now.Add(-time.Hour).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 0}, {"0", 0}, {"-5", 0}, {"7", 7 * time.Second}, {" 7 ", 7 * time.Second},
		{"3600", maxRetryAfter},
		// A number too large for an int64 is a very long wait, capped like any other,
		// not a missing header.
		{"99999999999999999999", maxRetryAfter}, {"18446744073709551616", maxRetryAfter},
		{"+5", 5 * time.Second}, {"-0", 0}, {"1.5", 0}, {"1e3", 0},
		{date, 20 * time.Second}, {past, 0}, {"soon", 0},
	} {
		header := http.Header{}
		if tc.value != "" {
			header.Set("Retry-After", tc.value)
		}
		if got := parseRetryAfter(header, now); got != tc.want {
			t.Errorf("Retry-After %q = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// acquire spends at most half of what the caller has left on waiting.
func TestAcquireNeverSpendsMoreThanHalfTheCallersDeadline(t *testing.T) {
	clock := newFakeClock() // static: the bucket does not refill while the test runs
	e := newEndpointTraffic("h", 10, clock.Now, nil)
	for i := 0; i < int(rateBurst); i++ {
		if err := e.acquire(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
	}
	// The next call needs 100ms. With 150ms left that is more than half: refuse.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := e.acquire(ctx, 0); err != errLocalRateLimit {
		t.Fatalf("acquire = %v, want errLocalRateLimit", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a refused call took %v", time.Since(start))
	}
	// With plenty of time it waits (the refusal claimed nothing) and then goes.
	start = time.Now()
	if err := e.acquire(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 50*time.Millisecond || took > 3*time.Second {
		t.Fatalf("a call that needed 100ms took %v", took)
	}
}

func TestAcquireGivesTheTokenBackWhenTheCallerGivesUp(t *testing.T) {
	clock := newFakeClock() // static: no refill, so the balance must come back exactly
	e := newEndpointTraffic("h", 10, clock.Now, nil)
	for i := 0; i < int(rateBurst); i++ {
		_ = e.acquire(context.Background(), 0)
	}
	e.mu.Lock()
	before := e.tokens
	e.mu.Unlock()
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- e.acquire(ctx, 0) }() // needs 100ms, well inside the bound
		time.Sleep(5 * time.Millisecond)
		cancel()
		if err := <-done; err != context.Canceled {
			t.Fatalf("acquire = %v, want the caller's cancellation", err)
		}
	}
	e.mu.Lock()
	after := e.tokens
	e.mu.Unlock()
	if after != before {
		t.Fatalf("abandoned waits changed the balance: %v -> %v", before, after)
	}
}

func TestLimiterIsSafeUnderConcurrency(t *testing.T) {
	e := newEndpointTraffic("h", 1e6, time.Now, func(string, EndpointTraffic) {})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := e.acquire(context.Background(), 0); err != nil {
					continue
				}
				e.sent(false)
				switch i % 7 {
				case 0:
					e.rateLimited(time.Now(), time.Now(), 0)
				case 1:
					e.answeredWith(429, nil, false)
				default:
					e.answered(time.Now())
					e.answeredWith(200, nil, i%5 == 0)
					e.finished(time.Millisecond)
				}
				_ = e.snapshot()
			}
		}()
	}
	wg.Wait()
}

// --- the defaults -----------------------------------------------------------------------

// The defaults are decisions with numbers behind them (see defaultRatePerSec and
// rateBurst); pinned, so that changing one is deliberate.
func TestDefaultPacingKeepsHeadroomForAScanPass(t *testing.T) {
	if defaultRatePerSec != 40 {
		t.Fatalf("defaultRatePerSec = %v, want 40", defaultRatePerSec)
	}
	if rateBurst != 32 {
		t.Fatalf("rateBurst = %v, want 32", rateBurst)
	}
	if rateBurst <= maxConnsPerHost {
		t.Fatalf("rateBurst = %v is not above the connection cap %d: calls beyond the cap queue for a connection, pacing must not delay them too", rateBurst, maxConnsPerHost)
	}
	if limiterMaxWait != 2*time.Second || rotationMaxWait != 250*time.Millisecond {
		t.Fatalf("waits = %v / %v, want 2s / 250ms", limiterMaxWait, rotationMaxWait)
	}
}

// The load a BSC chain puts on its one log-serving endpoint - about 17-18 calls a
// second in steady state, a whole pass of 32 batches at once - never waits at the
// default, and still does not after the first 429 has halved the rate. At 20 calls a
// second (and a burst of 16) both did.
func TestScanLoadBelowTheDefaultCeilingNeverWaits(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, defaultRatePerSec)
	for i := 0; i < int(rateBurst); i++ { // one pass at once
		if wait := mustReserve(t, e, clock, time.Hour); wait != 0 {
			t.Fatalf("call %d of a pass waited %v", i, wait)
		}
	}
	step := time.Second / 30 // 30 calls a second, above the estimate
	for i := 0; i < 600; i++ {
		clock.Advance(step)
		if wait := mustReserve(t, e, clock, time.Hour); wait != 0 {
			t.Fatalf("call %d of a steady 30/s load waited %v at the default ceiling", i, wait)
		}
	}
	// A 429 halves the rate; the load of the estimate (18 a second) still fits.
	clock.Advance(time.Second)
	e.rateLimited(clock.Now(), clock.Now().Add(time.Millisecond), 0)
	clock.Advance(time.Second)
	for i := 0; i < 360; i++ {
		clock.Advance(time.Second / 18)
		if wait := mustReserve(t, e, clock, time.Hour); wait != 0 {
			t.Fatalf("call %d of a steady 18/s load waited %v after one halving of the default", i, wait)
		}
	}
}

// --- a context that has already run out is a timeout, not a rate limit ---------------

func TestAcquireWithAnExpiredContextIsATimeoutNotARateLimit(t *testing.T) {
	clock := newFakeClock()
	e := newTestEndpoint(clock, 20)
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := e.acquire(expired, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire with an expired deadline = %v, want context.DeadlineExceeded", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := e.acquire(canceled, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire with a canceled context = %v, want context.Canceled", err)
	}
	e.mu.Lock()
	tokens := e.tokens
	e.mu.Unlock()
	if tokens != rateBurst {
		t.Fatalf("tokens = %v: a call that never started claimed one", tokens)
	}
	if got := e.snapshot().Throttled; got != 0 {
		t.Fatalf("throttled = %d: the call was counted as a local rate limit", got)
	}
}

// --- Retry-After is a block, not recovery time ---------------------------------------

// The climb back starts when the block ends. Counted from the 429, the first answer
// after a 30 s Retry-After lifted the rate straight back to the ceiling and the
// halving was forgotten.
func TestRateAfterALongRetryAfterKeepsTheHalving(t *testing.T) {
	for name, retryAfter := range map[string]time.Duration{"30s": 30 * time.Second, "hostile 24h (capped)": 24 * time.Hour} {
		t.Run(name, func(t *testing.T) {
			clock := newFakeClock()
			e := newTestEndpoint(clock, 20)
			sent := clock.Now()
			clock.Advance(10 * time.Millisecond)
			e.rateLimited(sent, clock.Now(), retryAfter) // 20 -> 10
			blocked := min(retryAfter, maxRetryAfter)
			clock.Advance(blocked + time.Second) // one second after the block ended
			e.answered(clock.Now())
			e.mu.Lock()
			rate := e.rate
			e.mu.Unlock()
			// One second of recovery is ceiling/60 = 0.33; the whole block counted would be 10 or 20.
			if rate < 10 || rate > 10.5 {
				t.Fatalf("rate one second after the block = %v, want about 10.3 (the halving remembered)", rate)
			}
			// And it does climb afterwards.
			clock.Advance(30 * time.Second)
			e.answered(clock.Now())
			e.mu.Lock()
			rate = e.rate
			e.mu.Unlock()
			if rate < 19.5 {
				t.Fatalf("rate after 30 more seconds of answers = %v, want back near the ceiling", rate)
			}
		})
	}
}
