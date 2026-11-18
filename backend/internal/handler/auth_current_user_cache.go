package handler

import (
	"os"
	"strings"
	"sync"
	"time"
)

// authCurrentUserCacheTTL is how long one /api/v1/auth/me answer may be reused.
// The endpoint is the single most requested API route (a quarter of all API
// traffic is browser polling), and every page navigates through it. A few
// seconds of staleness on the profile payload is invisible, while the saving is
// one backend query per open tab per navigation.
const authCurrentUserCacheTTL = 5 * time.Second

// authCurrentUserCacheOffEnv lets production turn the reuse off without a new
// build if a profile field ever needs to be strictly immediate.
const authCurrentUserCacheOffEnv = "AUTH_ME_CACHE_DISABLED"

type authCurrentUserCacheEntry struct {
	value   *currentUserResponse
	expires time.Time
}

// authCurrentUserCache is a tiny process-local TTL map. It is deliberately not
// shared between instances: a miss elsewhere only costs one query, and nothing
// here is a source of truth.
type authCurrentUserCache struct {
	mu      sync.Mutex
	entries map[int64]authCurrentUserCacheEntry
	now     func() time.Time
}

func (c *authCurrentUserCache) enabled() bool {
	raw := strings.TrimSpace(os.Getenv(authCurrentUserCacheOffEnv))
	return !strings.EqualFold(raw, "true") && raw != "1"
}

func (c *authCurrentUserCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// get returns a cached payload that has not expired yet.
func (c *authCurrentUserCache) get(userID int64) (*currentUserResponse, bool) {
	if !c.enabled() {
		return nil, false
	}
	c.mu.Lock()
	entry, ok := c.entries[userID]
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	if !c.clock().Before(entry.expires) {
		return nil, false
	}
	return entry.value, true
}

// put stores a payload for the TTL window, dropping expired entries when the
// map grows so a long-lived process cannot accumulate dead users forever.
func (c *authCurrentUserCache) put(userID int64, value *currentUserResponse) {
	if !c.enabled() || value == nil {
		return
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[int64]authCurrentUserCacheEntry, 64)
	}
	if len(c.entries) >= 4096 {
		for id, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, id)
			}
		}
	}
	c.entries[userID] = authCurrentUserCacheEntry{value: value, expires: now.Add(authCurrentUserCacheTTL)}
}
