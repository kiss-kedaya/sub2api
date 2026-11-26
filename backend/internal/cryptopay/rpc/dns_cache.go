package rpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// dnsCacheTTL is how long one resolution is reused. Without it every RPC call
	// (a scan pass is hundreds) paid a DNS round trip on the host that was already
	// the busiest machine in the pool. It is short on purpose: a provider that moves
	// is followed within a minute, and the public-address check below does not rely
	// on the cache being fresh.
	dnsCacheTTL = 45 * time.Second
	// dnsCacheMaxHosts bounds the map. Hosts come from operator configuration and
	// the public node registry, so a few dozen is normal; the bound only keeps a
	// pathological list from growing it without limit.
	dnsCacheMaxHosts = 256
	// dnsLookupTimeout bounds one shared lookup. It is detached from the caller that
	// started it (others may be waiting on the same answer), so it needs its own.
	dnsLookupTimeout = 10 * time.Second
)

// dnsCache remembers resolved addresses per host for dnsCacheTTL.
//
// It caches ANSWERS, never decisions: the caller still checks every address for
// being public on every use (publicRPCNetwork.dialContext), so a cached private
// answer is refused exactly like a fresh one and nothing here can approve an
// address. A failed lookup is not stored: the next call asks again.
type dnsCache struct {
	lookup lookupIPAddrFunc
	now    func() time.Time
	ttl    time.Duration
	flight singleflight.Group

	mu      sync.Mutex
	entries map[string]*dnsEntry
}

type dnsEntry struct {
	addrs   []net.IPAddr
	expires time.Time
	// turn moves the first address tried on every use, so a provider with several
	// A records still gets its connections spread over them as before.
	turn int
}

func newDNSCache(lookup lookupIPAddrFunc, now func() time.Time, ttl time.Duration) *dnsCache {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = dnsCacheTTL
	}
	return &dnsCache{lookup: lookup, now: now, ttl: ttl, entries: make(map[string]*dnsEntry)}
}

var errDNSLookupFailed = errors.New("dns lookup failed")

// resolve returns the addresses of host. Concurrent callers for the same host
// share one lookup, so the moment a cached entry expires costs one query, not one
// per waiting call.
func (c *dnsCache) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	key := strings.ToLower(host)
	if addrs, ok := c.fresh(key); ok {
		return addrs, nil
	}
	result := c.flight.DoChan(key, func() (any, error) {
		if addrs, ok := c.fresh(key); ok {
			return addrs, nil
		}
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dnsLookupTimeout)
		defer cancel()
		addrs, err := c.lookup(lookupCtx, host)
		if err != nil || len(addrs) == 0 {
			return nil, errDNSLookupFailed
		}
		c.store(key, addrs)
		return addrs, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case outcome := <-result:
		if outcome.Err != nil {
			return nil, outcome.Err
		}
		addrs, _ := outcome.Val.([]net.IPAddr)
		return addrs, nil
	}
}

// fresh returns a rotated copy of the cached addresses when they have not expired.
func (c *dnsCache) fresh(key string) ([]net.IPAddr, bool) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil || !now.Before(entry.expires) {
		return nil, false
	}
	n := len(entry.addrs)
	start := entry.turn % n
	entry.turn++
	out := make([]net.IPAddr, n)
	for i := range out {
		out[i] = entry.addrs[(start+i)%n]
	}
	return out, true
}

func (c *dnsCache) store(key string, addrs []net.IPAddr) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, known := c.entries[key]; !known && len(c.entries) >= dnsCacheMaxHosts {
		for host, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, host)
			}
		}
		if len(c.entries) >= dnsCacheMaxHosts {
			return // full of live entries: resolve this host every time instead
		}
	}
	c.entries[key] = &dnsEntry{addrs: append([]net.IPAddr(nil), addrs...), expires: now.Add(c.ttl)}
}
