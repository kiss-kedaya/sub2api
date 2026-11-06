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
	maxCallRounds             = 3
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
}

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
	state.failures++
	delay := cooldown
	for i := 1; i < state.failures && delay < maxEndpointCooldown; i++ {
		delay *= 2
	}
	if delay > maxEndpointCooldown {
		delay = maxEndpointCooldown
	}
	state.until = nowFunc().Add(delay)
}

func (t *endpointHealthTracker) succeed(endpoint string) {
	t.mu.Lock()
	delete(t.states, endpoint)
	t.mu.Unlock()
}

func endpointCooldown(err error) time.Duration {
	switch CategoryOf(err) {
	case CategoryAuthConfig:
		return authEndpointCooldown
	case CategoryTransport, CategoryTimeout:
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
