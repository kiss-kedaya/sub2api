package service

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Model-level transient cooldown state.
//
// The durable model rate-limit state already covers 429 responses. This state
// machine covers provider faults that are not rate limits (5xx, timeouts,
// broken streams): the first failure only counts, the second pauses the
// (account, model) pair for a short window, and the third and later failures
// pause it for a longer window. A success (or an expired window) clears the
// streak, so intermittent failures do not accumulate into a permanent ban.
const (
	modelTransientShortCooldown = 10 * time.Second
	modelTransientLongCooldown  = 45 * time.Second
	modelTransientRetention     = 30 * time.Minute
	modelTransientMaxEntries    = 50_000
)

type modelTransientEntry struct {
	streak        int
	cooldownUntil time.Time
	lastSeen      time.Time
}

// ModelTransientCooldown is a bounded in-memory state machine. It is
// deliberately not persisted: a process restart starts with a clean slate,
// matching how upstream blips behave.
type ModelTransientCooldown struct {
	mu         sync.Mutex
	entries    map[string]*modelTransientEntry
	now        func() time.Time
	short      time.Duration
	long       time.Duration
	retention  time.Duration
	maxEntries int
}

// NewModelTransientCooldown builds the default state machine.
func NewModelTransientCooldown() *ModelTransientCooldown {
	return &ModelTransientCooldown{
		entries:    make(map[string]*modelTransientEntry),
		now:        time.Now,
		short:      modelTransientShortCooldown,
		long:       modelTransientLongCooldown,
		retention:  modelTransientRetention,
		maxEntries: modelTransientMaxEntries,
	}
}

func modelTransientKey(accountID int64, model string) string {
	return strings.TrimSpace(strings.ToLower(model)) + "\x00" + strconv.FormatInt(accountID, 10)
}

// Allow reports whether the account may be selected for the model right now.
// An expired window is treated as a fresh start.
func (c *ModelTransientCooldown) Allow(accountID int64, model string) bool {
	if c == nil {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" || accountID <= 0 {
		return true
	}
	key := modelTransientKey(accountID, model)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return true
	}
	entry.lastSeen = now
	if now.Before(entry.cooldownUntil) {
		return false
	}
	// Only an *expired* window resets the streak. A plain probe between a
	// first and second failure must not erase the pending count.
	if !entry.cooldownUntil.IsZero() {
		entry.streak = 0
		entry.cooldownUntil = time.Time{}
	}
	return true
}

// ObserveFailure records one provider fault for (account, model). The caller
// must only pass faults attributable to the upstream/model (5xx, timeouts,
// broken streams), never client errors or local policy rejections.
func (c *ModelTransientCooldown) ObserveFailure(accountID int64, model string) {
	if c == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" || accountID <= 0 {
		return
	}
	key := modelTransientKey(accountID, model)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxEntries {
		c.evictLocked(now)
	}
	entry, ok := c.entries[key]
	if !ok {
		entry = &modelTransientEntry{}
		c.entries[key] = entry
	}
	entry.streak++
	entry.lastSeen = now
	switch {
	case entry.streak >= 3:
		entry.cooldownUntil = now.Add(c.long)
	case entry.streak == 2:
		entry.cooldownUntil = now.Add(c.short)
	default:
		entry.cooldownUntil = time.Time{}
	}
}

// ObserveSuccess clears the streak after a successful request.
func (c *ModelTransientCooldown) ObserveSuccess(accountID int64, model string) {
	if c == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" || accountID <= 0 {
		return
	}
	key := modelTransientKey(accountID, model)
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// CooldownUntil exposes the current pause end for diagnostics/tests.
func (c *ModelTransientCooldown) CooldownUntil(accountID int64, model string) time.Time {
	if c == nil {
		return time.Time{}
	}
	key := modelTransientKey(accountID, model)
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok {
		return entry.cooldownUntil
	}
	return time.Time{}
}

func (c *ModelTransientCooldown) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.Sub(entry.lastSeen) > c.retention {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}
	// Still full: drop the oldest quarter rather than growing without bound.
	target := len(c.entries) - c.maxEntries + c.maxEntries/4
	if target <= 0 {
		return
	}
	type kv struct {
		key  string
		seen time.Time
	}
	oldest := make([]kv, 0, len(c.entries))
	for key, entry := range c.entries {
		oldest = append(oldest, kv{key: key, seen: entry.lastSeen})
	}
	sort.Slice(oldest, func(i, j int) bool { return oldest[i].seen.Before(oldest[j].seen) })
	for i := 0; i < target && i < len(oldest); i++ {
		delete(c.entries, oldest[i].key)
	}
}
