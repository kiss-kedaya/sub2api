package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// reportCacheTTL keeps the expensive admin reports warm across normal panel
	// refreshes. The underlying data is still refreshed on the next cache miss.
	reportCacheTTL         = 2 * time.Minute
	reportCacheLoadTimeout = 20 * time.Second
	reportCacheMaxEntries  = 256
	reportCacheMaxLoads    = 4
	reportCacheSweepEvery  = 30 * time.Second
)

// All report caches share one budget. Without a process-wide limit, five
// independent caches can each start expensive scans at the same time.
var reportCacheLoadSlots = make(chan struct{}, reportCacheMaxLoads)

// reportCacheLoadContext lets an in-flight report finish after a browser aborts
// so a retry can reuse the result instead of cancelling the expensive SQL scan.
func reportCacheLoadContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	return context.WithTimeout(base, reportCacheLoadTimeout)
}

type snapshotCacheEntry struct {
	ETag      string
	Payload   any
	ExpiresAt time.Time
}

type snapshotCache struct {
	mu          sync.RWMutex
	ttl         time.Duration
	items       map[string]snapshotCacheEntry
	sf          singleflight.Group
	sweepEvery  time.Duration
	lastSweepAt time.Time
}

type snapshotCacheLoadResult struct {
	Entry snapshotCacheEntry
	Hit   bool
}

func newSnapshotCache(ttl time.Duration) *snapshotCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &snapshotCache{
		ttl:         ttl,
		items:       make(map[string]snapshotCacheEntry),
		sweepEvery:  reportCacheSweepEvery,
		lastSweepAt: time.Now(),
	}
}

// sweepLocked removes expired entries at a bounded cadence. It is performed
// on cache access rather than in a goroutine so each cache has no shutdown
// lifecycle and tests do not leak background workers.
func (c *snapshotCache) sweepLocked(now time.Time) {
	if c == nil || c.sweepEvery <= 0 || now.Sub(c.lastSweepAt) < c.sweepEvery {
		return
	}
	for key, entry := range c.items {
		if !now.Before(entry.ExpiresAt) {
			delete(c.items, key)
		}
	}
	c.lastSweepAt = now
}

func (c *snapshotCache) Get(key string) (snapshotCacheEntry, bool) {
	if c == nil || key == "" {
		return snapshotCacheEntry{}, false
	}
	now := time.Now()

	c.mu.Lock()
	c.sweepLocked(now)
	entry, ok := c.items[key]
	c.mu.Unlock()
	if !ok {
		return snapshotCacheEntry{}, false
	}
	if now.After(entry.ExpiresAt) {
		c.mu.Lock()
		// Avoid deleting a newer value inserted after the initial read.
		if current, exists := c.items[key]; exists &&
			current.ExpiresAt.Equal(entry.ExpiresAt) && current.ETag == entry.ETag {
			delete(c.items, key)
		}
		c.mu.Unlock()
		return snapshotCacheEntry{}, false
	}
	return entry, true
}

func (c *snapshotCache) Set(key string, payload any) snapshotCacheEntry {
	if c == nil {
		return snapshotCacheEntry{}
	}
	entry := snapshotCacheEntry{
		ETag:      buildETagFromAny(payload),
		Payload:   payload,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	if key == "" {
		return entry
	}
	c.mu.Lock()
	c.sweepLocked(time.Now())
	if len(c.items) >= reportCacheMaxEntries {
		now := time.Now()
		for existingKey, existing := range c.items {
			if now.After(existing.ExpiresAt) {
				delete(c.items, existingKey)
			}
		}
		// Keep the cache bounded even when every key is still fresh. The oldest
		// entry is a conservative eviction choice and avoids unbounded RSS growth
		// from arbitrary date/filter combinations.
		if len(c.items) >= reportCacheMaxEntries {
			oldestKey := ""
			var oldest time.Time
			for existingKey, existing := range c.items {
				if oldestKey == "" || existing.ExpiresAt.Before(oldest) {
					oldestKey = existingKey
					oldest = existing.ExpiresAt
				}
			}
			if oldestKey != "" {
				delete(c.items, oldestKey)
			}
		}
	}
	c.items[key] = entry
	c.mu.Unlock()
	return entry
}

func (c *snapshotCache) GetOrLoad(key string, load func() (any, error)) (snapshotCacheEntry, bool, error) {
	return c.GetOrLoadContext(context.Background(), key, load)
}

func (c *snapshotCache) GetOrLoadContext(ctx context.Context, key string, load func() (any, error)) (snapshotCacheEntry, bool, error) {
	if load == nil {
		return snapshotCacheEntry{}, false, nil
	}
	if entry, ok := c.Get(key); ok {
		return entry, true, nil
	}
	if c == nil || key == "" {
		payload, err := load()
		if err != nil {
			return snapshotCacheEntry{}, false, err
		}
		return c.Set(key, payload), false, nil
	}

	value, err, _ := c.sf.Do(key, func() (any, error) {
		if entry, ok := c.Get(key); ok {
			return snapshotCacheLoadResult{Entry: entry, Hit: true}, nil
		}
		slotCtx, cancel := context.WithTimeout(context.Background(), reportCacheLoadTimeout)
		defer cancel()
		select {
		case reportCacheLoadSlots <- struct{}{}:
			defer func() { <-reportCacheLoadSlots }()
		case <-slotCtx.Done():
			return nil, slotCtx.Err()
		}
		payload, err := load()
		if err != nil {
			return nil, err
		}
		return snapshotCacheLoadResult{Entry: c.Set(key, payload), Hit: false}, nil
	})
	if err != nil {
		return snapshotCacheEntry{}, false, err
	}
	result, ok := value.(snapshotCacheLoadResult)
	if !ok {
		return snapshotCacheEntry{}, false, nil
	}
	return result.Entry, result.Hit, nil
}

func buildETagFromAny(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "\"" + hex.EncodeToString(sum[:]) + "\""
}

func parseBoolQueryWithDefault(raw string, def bool) bool {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return def
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}
