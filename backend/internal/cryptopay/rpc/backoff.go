package rpc

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Public RPC providers rate-limit and time out under the scanner's batch load.
// The pool therefore remembers recent endpoint failures: a node that just
// failed is skipped for a short cooldown, and a transient failure of the whole
// rotation is retried with exponential backoff instead of failing the batch
// immediately (which used to happen on every scan cycle).
const (
	transientEndpointCooldown = 3 * time.Second
	authEndpointCooldown      = 5 * time.Minute
	maxEndpointCooldown       = 2 * time.Minute
	// quotaEndpointCooldown keeps an endpoint whose daily/monthly allowance or
	// prepaid balance is used up out of rotation. Allowances reset at most once a
	// day, so probing every few seconds only burns requests and hides the nodes
	// that still work behind a wall of identical refusals.
	quotaEndpointCooldown = 15 * time.Minute
	maxCallRounds         = 3
)

var (
	// Variables so tests can shrink the delays without sleeping for seconds.
	callRetryBaseDelay = 200 * time.Millisecond
	callRetryMaxDelay  = 2 * time.Second
	nowFunc            = time.Now
)

type endpointHealth struct {
	failures int
	until    time.Time
	// maxSpan is the largest eth_getLogs block span this endpoint said it
	// accepts, valid until spanUntil. It is independent of the failure cooldown:
	// a node that refuses wide queries is healthy for narrow ones.
	maxSpan   uint64
	spanUntil time.Time
}

// learnedSpanTTL bounds how long a node's stated block-range limit is trusted.
// Operators upgrade plans, so a limit must expire rather than pin a node to a
// narrow window forever.
const learnedSpanTTL = 30 * time.Minute

type endpointHealthTracker struct {
	mu     sync.Mutex
	states map[string]*endpointHealth
}

func newEndpointHealthTracker() *endpointHealthTracker {
	return &endpointHealthTracker{states: make(map[string]*endpointHealth)}
}

// eligible drops endpoints inside their cooldown window. When every endpoint
// is cooling down it returns the full list: probing one node is better than
// stalling the scanner until a cooldown expires.
func (t *endpointHealthTracker) eligible(endpoints []string) []string {
	now := nowFunc()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if state := t.states[endpoint]; state == nil || !now.Before(state.until) {
			out = append(out, endpoint)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), endpoints...)
	}
	return out
}

func (t *endpointHealthTracker) fail(endpoint string, err error) {
	cooldown := endpointCooldown(err)
	if cooldown <= 0 {
		// Request-specific rejections (nonce too low, already known, ...) say
		// nothing about node health; only transport/auth categories cool down.
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.states[endpoint]
	if state == nil {
		state = &endpointHealth{}
		t.states[endpoint] = state
	}
	if nowFunc().Before(state.until) {
		// The endpoint is already cooling down: this failure belongs to a call that
		// started before the cooldown. A burst of N concurrent calls hitting one
		// rate limit is one event, not N, and counting it N times would push the
		// cooldown from seconds to the two minute cap in a single burst.
		return
	}
	state.failures++
	limit := maxEndpointCooldown
	if cooldown > limit {
		limit = cooldown
	}
	delay := cooldown
	for i := 1; i < state.failures && delay < limit; i++ {
		delay *= 2
	}
	if delay > limit {
		delay = limit
	}
	state.until = nowFunc().Add(delay)
}

func (t *endpointHealthTracker) succeed(endpoint string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.states[endpoint]
	if state == nil {
		return
	}
	// Recovery clears the failure streak only. A stated block-range limit is a
	// capability, not a fault, and one narrow success must not erase it.
	state.failures = 0
	state.until = time.Time{}
	if state.maxSpan == 0 || !nowFunc().Before(state.spanUntil) {
		delete(t.states, endpoint)
	}
}

// learnSpan records the block span an endpoint said it accepts.
func (t *endpointHealthTracker) learnSpan(endpoint string, limit uint64) {
	if limit == 0 {
		return
	}
	now := nowFunc()
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.states[endpoint]
	if state == nil {
		state = &endpointHealth{}
		t.states[endpoint] = state
	}
	if state.maxSpan == 0 || !now.Before(state.spanUntil) || limit < state.maxSpan {
		state.maxSpan = limit
	}
	state.spanUntil = now.Add(learnedSpanTTL)
}

// spanAllowedLocked reports whether endpoint may be asked for span blocks.
func (t *endpointHealthTracker) spanAllowedLocked(endpoint string, span uint64, now time.Time) bool {
	state := t.states[endpoint]
	if state == nil || state.maxSpan == 0 || !now.Before(state.spanUntil) {
		return true
	}
	return span <= state.maxSpan
}

// capableOfSpan keeps the endpoints not known to refuse a query of span blocks,
// ignoring cooldowns: whether to wait for a cooling node is the caller's call.
func (t *endpointHealthTracker) capableOfSpan(endpoints []string, span uint64) []string {
	now := nowFunc()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if t.spanAllowedLocked(endpoint, span, now) {
			out = append(out, endpoint)
		}
	}
	return out
}

// widestLimit is the largest stated block-span limit among the endpoints, i.e.
// the widest query any of them still accepts. It is only a hint for the failure
// that asks the caller to split; zero when any endpoint stated no limit.
func (t *endpointHealthTracker) widestLimit(endpoints []string) uint64 {
	now := nowFunc()
	t.mu.Lock()
	defer t.mu.Unlock()
	var widest uint64
	for _, endpoint := range endpoints {
		state := t.states[endpoint]
		if state == nil || state.maxSpan == 0 || !now.Before(state.spanUntil) {
			// An endpoint without a stated limit has none to report.
			return 0
		}
		if state.maxSpan > widest {
			widest = state.maxSpan
		}
	}
	return widest
}

func endpointCooldown(err error) time.Duration {
	var failure *Failure
	if errors.As(err, &failure) && failure.Quota {
		return quotaEndpointCooldown
	}
	switch CategoryOf(err) {
	case CategoryAuthConfig:
		return authEndpointCooldown
	case CategoryTransport, CategoryTimeout, CategoryStale:
		return transientEndpointCooldown
	default:
		return 0
	}
}

// callRetryDelay is an exponential backoff with jitter in [d/2, d].
func callRetryDelay(round int) time.Duration {
	delay := callRetryBaseDelay
	for i := 0; i < round; i++ {
		delay *= 2
	}
	if delay > callRetryMaxDelay {
		delay = callRetryMaxDelay
	}
	jitter := time.Duration(rand.Int63n(int64(delay/2) + 1))
	return delay/2 + jitter
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// isRetryableWithoutResponse reports failures where no HTTP answer arrived
// (dial errors, timeouts, broken reads). HTTP rate limits and 5xx responses
// come from a reachable node: retrying them inline only burns quota, so the
// caller (scanner) retries the batch later instead.
func isRetryableWithoutResponse(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Kind == "transport" && failure.IsTransient()
}
