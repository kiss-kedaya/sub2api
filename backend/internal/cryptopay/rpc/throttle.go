package rpc

import (
	"context"
	"strings"
	"sync"
	"time"
)

// EndpointThrottle spaces calls to one endpoint.
//
// Public providers rate-limit per endpoint, and the scanner is bursty: one pass
// fires four batches per chain and several chains at once. Without pacing, a
// provider answers 429 for a while, and because Pool.Call rotates on failure,
// every endpoint gets burned in turn — so one burst degrades the whole pool
// rather than one node. Waiting a few milliseconds is far cheaper than a wasted
// round-trip plus a rejected sibling request.
//
// The interval is a floor between call *starts*, not a concurrency cap: calls
// still overlap, they just do not start together.
type EndpointThrottle struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

// NewEndpointThrottle builds a throttle with the given minimum spacing.
// A zero or negative interval disables pacing, which is the previous behaviour.
func NewEndpointThrottle(interval time.Duration) *EndpointThrottle {
	return &EndpointThrottle{interval: interval}
}

// Wait blocks until this endpoint may be called, or the context ends.
func (t *EndpointThrottle) Wait(ctx context.Context) error {
	if t == nil || t.interval <= 0 {
		return nil
	}
	t.mu.Lock()
	now := time.Now()
	start := t.next
	if start.Before(now) {
		start = now
	}
	t.next = start.Add(t.interval)
	t.mu.Unlock()

	delay := time.Until(start)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type throttleRegistry struct {
	mu         sync.Mutex
	perHost    map[string]*EndpointThrottle
	interval   time.Duration
	generation uint64
}

var endpointThrottles = &throttleRegistry{perHost: make(map[string]*EndpointThrottle)}

// SetEndpointThrottleInterval applies one interval to every endpoint. Existing
// throttles are replaced so a config change takes effect without a restart;
// an empty or non-positive interval disables pacing.
func SetEndpointThrottleInterval(interval time.Duration) {
	endpointThrottles.mu.Lock()
	defer endpointThrottles.mu.Unlock()
	endpointThrottles.interval = interval
	endpointThrottles.perHost = make(map[string]*EndpointThrottle)
	endpointThrottles.generation++
}

// ThrottleFor returns the throttle for one endpoint, creating it on first use.
func ThrottleFor(endpoint string) *EndpointThrottle {
	endpointThrottles.mu.Lock()
	defer endpointThrottles.mu.Unlock()
	if endpointThrottles.interval <= 0 {
		return nil
	}
	key := throttleKey(endpoint)
	if key == "" {
		return nil
	}
	throttle, ok := endpointThrottles.perHost[key]
	if !ok {
		throttle = NewEndpointThrottle(endpointThrottles.interval)
		endpointThrottles.perHost[key] = throttle
	}
	return throttle
}

// throttleKey normalises an endpoint so a trailing slash or case difference
// does not create two throttles for the same provider.
func throttleKey(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(trimmed), "/")
}
