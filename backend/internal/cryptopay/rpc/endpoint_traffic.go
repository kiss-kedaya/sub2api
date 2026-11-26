package rpc

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Per-endpoint request pacing and traffic accounting for the public RPC client.
//
// Pacing is a token bucket whose refill rate follows AIMD: an HTTP 429 halves the
// rate (and honours Retry-After), a stretch without one climbs back to the
// ceiling a little at a time. It exists because a rate-limited provider used to
// be hit again at full speed: every call to it failed with 429, the pool cooled
// it down for a few seconds, probed it again, and so on.
//
// What it deliberately is NOT:
//   - A fixed spacing. rpc_throttle_ms (EndpointThrottle) is that, it is opt-in,
//     and a default of 150 ms once stopped every chain (2026-11-24). Here a healthy
//     endpoint is never delayed below the ceiling, and a burst up to rateBurst
//     goes through at once.
//   - An unbounded wait. A call waits at most limiterMaxWait (and at most half of
//     what is left of its own deadline); beyond that it fails at once with a
//     429-shaped Failure, which the pool already answers by rotating to another
//     endpoint and the scanner by retrying the batch later. One slow endpoint can
//     therefore never hold the scan lock for longer than that.

const (
	// defaultRatePerSec is the ceiling of a healthy endpoint, in requests/second.
	// Steady scanning is a few requests per second per endpoint; this only ever
	// binds on a sustained flood.
	defaultRatePerSec = 20.0
	// MaxRatePerSec is the largest ceiling an operator may configure. Set it to
	// remove the ceiling in practice; 429 handling still applies.
	MaxRatePerSec = 1000
	// rateBurst is how many calls may start at once: the per-host connection cap,
	// i.e. the most one endpoint can have in flight anyway.
	rateBurst = float64(maxConnsPerHost)
	// rateFloor keeps a throttled endpoint probed (one call per two seconds) so it
	// can be seen recovering.
	rateFloor = 0.5
	// rateRecoverySpan is how long a stretch of answers takes to climb from zero
	// back to the ceiling, additively: ceiling/60 per second.
	rateRecoverySpan = 60 * time.Second
	// maxRetryAfter caps what a provider may ask us to wait. A bad header must not
	// park an endpoint for a day.
	maxRetryAfter = 60 * time.Second

	trafficWindow       = time.Minute
	trafficIdleEvict    = 10 * time.Minute
	trafficMaxEndpoints = 512
)

// limiterMaxWait is the longest a call waits for its turn. A variable so tests can
// shrink it.
var limiterMaxWait = 2 * time.Second

// errLocalRateLimit is returned by the transport when an endpoint is over its
// current rate and the wait would exceed the bound. Nothing was sent.
var errLocalRateLimit = errors.New("rpc endpoint is over its request rate")

// EndpointTraffic is what one endpoint saw in the current window. It carries
// numbers only: no URL, host or key.
type EndpointTraffic struct {
	WindowSeconds int64   `json:"window_seconds"`
	Requests      int64   `json:"requests"`
	OK            int64   `json:"ok"`
	HTTP429       int64   `json:"http_429"`
	HTTP402       int64   `json:"http_402"`
	HTTPOther     int64   `json:"http_other"`
	Timeouts      int64   `json:"timeouts"`
	ConnErrors    int64   `json:"conn_errors"`
	Throttled     int64   `json:"throttled"`
	StaleRetries  int64   `json:"stale_retries"`
	NewConns      int64   `json:"new_conns"`
	AvgMS         int64   `json:"avg_ms"`
	MaxMS         int64   `json:"max_ms"`
	RatePerSec    float64 `json:"rate_per_sec"`
}

type trafficCounters struct {
	start        time.Time
	requests     int64
	ok           int64
	r429         int64
	r402         int64
	other        int64
	timeouts     int64
	connErrors   int64
	throttled    int64
	staleRetries int64
	newConns     int64
	latencySum   time.Duration
	latencyN     int64
	latencyMax   time.Duration
}

// endpointTraffic is the pacing and accounting state of one endpoint (one URL:
// providers meter per key, and the key is in the URL).
type endpointTraffic struct {
	host  string // hostname only, for logs
	clock func() time.Time
	sink  func(host string, traffic EndpointTraffic)

	mu           sync.Mutex
	ceiling      float64
	rate         float64
	tokens       float64
	refilledAt   time.Time
	blockedUntil time.Time // Retry-After
	lastCut      time.Time
	lastRaise    time.Time
	lastUsed     time.Time
	win          trafficCounters
}

func newEndpointTraffic(host string, ceiling float64, clock func() time.Time, sink func(string, EndpointTraffic)) *endpointTraffic {
	now := clock()
	return &endpointTraffic{
		host: host, clock: clock, sink: sink,
		ceiling: ceiling, rate: ceiling, tokens: rateBurst,
		refilledAt: now, lastRaise: now, lastUsed: now,
	}
}

// advanceLocked adds the tokens earned since the last call. Nothing is earned
// while a Retry-After is in force, so the burst that piled up during the wait is
// not released the moment it ends.
func (e *endpointTraffic) advanceLocked(now time.Time) {
	from := e.refilledAt
	if e.blockedUntil.After(from) {
		from = e.blockedUntil
	}
	if now.After(from) {
		e.tokens = math.Min(rateBurst, e.tokens+e.rate*now.Sub(from).Seconds())
	}
	if now.After(e.refilledAt) {
		e.refilledAt = now
	}
}

// reserve claims one call. It returns how long the caller must wait before
// sending, or false (claiming nothing) when that wait exceeds maxWait. The token
// balance may go negative: that is the queue of calls already promised a slot.
func (e *endpointTraffic) reserve(now time.Time, maxWait time.Duration) (time.Duration, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastUsed = now
	e.advanceLocked(now)
	var wait time.Duration
	if now.Before(e.blockedUntil) {
		wait = e.blockedUntil.Sub(now)
	}
	if e.tokens < 1 {
		wait += time.Duration((1 - e.tokens) / e.rate * float64(time.Second))
	}
	if wait > maxWait {
		return 0, false
	}
	e.tokens--
	return wait, true
}

// unreserve returns a claimed call that was never sent (the caller gave up while
// waiting), so abandoned waits cannot drain the bucket.
func (e *endpointTraffic) unreserve() {
	e.mu.Lock()
	e.tokens = math.Min(rateBurst, e.tokens+1)
	e.mu.Unlock()
}

// rateLimited records an HTTP 429. The rate is halved once per congestion event:
// a burst of calls that were already in flight when the first 429 arrived all
// come back 429, and counting each would collapse the rate to the floor in one
// round trip. A call sent after the last cut that still gets 429 is a new event.
func (e *endpointTraffic) rateLimited(sentAt, now time.Time, retryAfter time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advanceLocked(now)
	if e.lastCut.IsZero() || sentAt.After(e.lastCut) {
		e.rate = math.Max(rateFloor, e.rate/2)
		e.lastCut, e.lastRaise = now, now
		if e.tokens > 0 {
			e.tokens = 0 // no bursting out of a congested state
		}
	}
	if retryAfter > 0 {
		retryAfter = min(retryAfter, maxRetryAfter)
		if until := now.Add(retryAfter); until.After(e.blockedUntil) {
			e.blockedUntil = until
		}
		if e.tokens > 0 {
			e.tokens = 0
		}
	}
}

// answered records a successful answer: the rate climbs back towards the ceiling
// by ceiling/rateRecoverySpan per second elapsed since the last change.
func (e *endpointTraffic) answered(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.advanceLocked(now)
	if e.rate >= e.ceiling || now.Before(e.blockedUntil) {
		return
	}
	elapsed := now.Sub(e.lastRaise)
	if elapsed <= 0 {
		return
	}
	e.rate = math.Min(e.ceiling, e.rate+e.ceiling*elapsed.Seconds()/rateRecoverySpan.Seconds())
	e.lastRaise = now
}

func (e *endpointTraffic) setCeiling(perSec float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	atCeiling := e.rate >= e.ceiling
	e.ceiling = perSec
	if e.rate > perSec || atCeiling {
		e.rate = perSec
	}
}

// acquire waits for this endpoint's turn. It returns nil when the call may be
// sent, errLocalRateLimit when the wait would be too long (nothing is claimed),
// or the context's error when the caller gave up while waiting.
func (e *endpointTraffic) acquire(ctx context.Context) error {
	maxWait := limiterMaxWait
	if deadline, ok := ctx.Deadline(); ok {
		// Never spend more than half of what the caller has left on waiting: the
		// request itself still needs the other half.
		maxWait = min(maxWait, time.Until(deadline)/2)
	}
	wait, ok := e.reserve(e.clock(), maxWait)
	if !ok {
		e.account(func(c *trafficCounters) { c.throttled++ })
		return errLocalRateLimit
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			e.unreserve()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

// account applies one change to the current window, first closing the window if
// it is older than trafficWindow: the summary of the finished window is emitted
// (only when it saw traffic) and a new one starts with this event. There is no
// background goroutine: an idle endpoint costs nothing and logs nothing.
func (e *endpointTraffic) account(apply func(*trafficCounters)) {
	now := e.clock()
	var summary *EndpointTraffic
	e.mu.Lock()
	if e.win.start.IsZero() {
		e.win.start = now
	}
	if now.Sub(e.win.start) >= trafficWindow {
		if e.win.requests+e.win.throttled > 0 {
			finished := e.snapshotLocked(now)
			summary = &finished
		}
		e.win = trafficCounters{start: now}
	}
	apply(&e.win)
	e.lastUsed = now
	e.mu.Unlock()
	if summary != nil && e.sink != nil {
		e.sink(e.host, *summary)
	}
}

func (e *endpointTraffic) snapshotLocked(now time.Time) EndpointTraffic {
	w := &e.win
	out := EndpointTraffic{
		WindowSeconds: int64(now.Sub(w.start).Round(time.Second) / time.Second),
		Requests:      w.requests, OK: w.ok, HTTP429: w.r429, HTTP402: w.r402, HTTPOther: w.other,
		Timeouts: w.timeouts, ConnErrors: w.connErrors, Throttled: w.throttled,
		StaleRetries: w.staleRetries, NewConns: w.newConns,
		MaxMS:      w.latencyMax.Milliseconds(),
		RatePerSec: math.Round(e.rate*10) / 10,
	}
	if w.latencyN > 0 {
		out.AvgMS = (w.latencySum / time.Duration(w.latencyN)).Milliseconds()
	}
	return out
}

// snapshot is the current (unfinished) window.
func (e *endpointTraffic) snapshot() EndpointTraffic {
	now := e.clock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.win.start.IsZero() {
		e.win.start = now
	}
	return e.snapshotLocked(now)
}

func (e *endpointTraffic) sent(staleRetry bool) {
	e.account(func(c *trafficCounters) {
		c.requests++
		if staleRetry {
			c.staleRetries++
		}
	})
}

// answeredWith classifies the outcome of one attempt that reached the network.
func (e *endpointTraffic) answeredWith(status int, err error, newConn bool) {
	e.account(func(c *trafficCounters) {
		if newConn {
			c.newConns++
		}
		switch {
		case err != nil:
			switch {
			case errors.Is(err, context.Canceled):
				// The caller gave up: says nothing about the endpoint.
			case isTimeoutError(err):
				c.timeouts++
			default:
				c.connErrors++
			}
		case status >= 200 && status < 300:
			c.ok++
		case status == http.StatusTooManyRequests:
			c.r429++
		case status == http.StatusPaymentRequired:
			c.r402++
		default:
			c.other++
		}
	})
}

func (e *endpointTraffic) finished(elapsed time.Duration) {
	e.account(func(c *trafficCounters) {
		c.latencySum += elapsed
		c.latencyN++
		if elapsed > c.latencyMax {
			c.latencyMax = elapsed
		}
	})
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// parseRetryAfter reads a Retry-After header: delta-seconds or an HTTP date.
func parseRetryAfter(header http.Header, now time.Time) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if wait := at.Sub(now); wait > 0 {
			return min(wait, maxRetryAfter)
		}
	}
	return 0
}

// logTraffic is the one-line-per-endpoint-per-minute summary. The host is the
// only identity written: the URL carries the provider key.
func logTraffic(host string, t EndpointTraffic) {
	slog.Info("[cryptopay] rpc endpoint traffic",
		"endpoint_host", host, "window_s", t.WindowSeconds,
		"requests", t.Requests, "ok", t.OK, "http_429", t.HTTP429, "http_402", t.HTTP402,
		"http_other", t.HTTPOther, "timeouts", t.Timeouts, "conn_errors", t.ConnErrors,
		"throttled_local", t.Throttled, "stale_retries", t.StaleRetries, "new_conns", t.NewConns,
		"avg_ms", t.AvgMS, "max_ms", t.MaxMS, "rate_per_s", t.RatePerSec)
}

// trafficRegistry holds the state of every endpoint seen by one transport.
type trafficRegistry struct {
	clock func() time.Time
	sink  func(host string, traffic EndpointTraffic)

	mu      sync.Mutex
	ceiling float64
	byKey   map[string]*endpointTraffic
}

func newTrafficRegistry(clock func() time.Time, sink func(string, EndpointTraffic), ceiling float64) *trafficRegistry {
	if ceiling <= 0 {
		ceiling = defaultRatePerSec
	}
	return &trafficRegistry{clock: clock, sink: sink, ceiling: ceiling, byKey: make(map[string]*endpointTraffic)}
}

// trafficKey identifies an endpoint. It may contain a provider key, so it stays in
// memory: nothing here is ever logged or returned.
func trafficKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	key := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimSuffix(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return key
}

// get returns the state of the endpoint, creating it on first use. It returns nil
// when the registry is full of recently used endpoints: such a call is simply not
// paced or counted, which is the behaviour from before this existed.
func (r *trafficRegistry) get(u *url.URL) *endpointTraffic {
	key := trafficKey(u)
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.byKey[key]; e != nil {
		return e
	}
	if len(r.byKey) >= trafficMaxEndpoints {
		cutoff := r.clock().Add(-trafficIdleEvict)
		for k, e := range r.byKey {
			e.mu.Lock()
			idle := e.lastUsed.Before(cutoff)
			e.mu.Unlock()
			if idle {
				delete(r.byKey, k)
			}
		}
		if len(r.byKey) >= trafficMaxEndpoints {
			return nil
		}
	}
	e := newEndpointTraffic(strings.ToLower(u.Hostname()), r.ceiling, r.clock, r.sink)
	r.byKey[key] = e
	return e
}

func (r *trafficRegistry) lookup(u *url.URL) (*endpointTraffic, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byKey[trafficKey(u)]
	return e, ok
}

func (r *trafficRegistry) setCeiling(perSec float64) {
	r.mu.Lock()
	r.ceiling = perSec
	all := make([]*endpointTraffic, 0, len(r.byKey))
	for _, e := range r.byKey {
		all = append(all, e)
	}
	r.mu.Unlock()
	for _, e := range all {
		e.setCeiling(perSec)
	}
}
