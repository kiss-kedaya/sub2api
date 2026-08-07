package service

import (
	"strings"
	"sync"
	"time"
)

// Lightweight free-usage recovery hint: after free cool ends, keep a short
// "probe preferred" window so the next scheduling pass can prefer accounts that
// have waited out free-usage without immediately re-entering a tight 429 loop.
// Full async quota recovery queues (grok2api) are intentionally not ported.
type grokFreeRecoveryHint struct {
	CoolUntil  time.Time
	ProbeUntil time.Time
}

type grokFreeRecoveryStore struct {
	mu    sync.Mutex
	items map[int64]grokFreeRecoveryHint
}

var globalGrokFreeRecovery = &grokFreeRecoveryStore{
	items: make(map[int64]grokFreeRecoveryHint),
}

const grokFreeRecoveryProbeWindow = 30 * time.Minute

// markGrokFreeUsageRecovery records that account left free-usage cool and should
// be treated carefully until ProbeUntil.
func markGrokFreeUsageRecovery(accountID int64, coolUntil time.Time) {
	if accountID <= 0 {
		return
	}
	now := time.Now()
	if coolUntil.IsZero() || !coolUntil.After(now) {
		coolUntil = now.Add(2 * time.Hour)
	}
	globalGrokFreeRecovery.mu.Lock()
	defer globalGrokFreeRecovery.mu.Unlock()
	globalGrokFreeRecovery.items[accountID] = grokFreeRecoveryHint{
		CoolUntil:  coolUntil,
		ProbeUntil: coolUntil.Add(grokFreeRecoveryProbeWindow),
	}
}

// isGrokFreeUsageCooling is true while still inside the free-usage cool window.
func isGrokFreeUsageCooling(accountID int64, now time.Time) bool {
	if accountID <= 0 {
		return false
	}
	globalGrokFreeRecovery.mu.Lock()
	defer globalGrokFreeRecovery.mu.Unlock()
	h, ok := globalGrokFreeRecovery.items[accountID]
	if !ok {
		return false
	}
	if now.After(h.ProbeUntil) {
		delete(globalGrokFreeRecovery.items, accountID)
		return false
	}
	return now.Before(h.CoolUntil)
}

// isGrokFreeUsageProbePreferred is true after cool ends but before probe window
// expires — account is schedulable but may still be near free-usage edges.
func isGrokFreeUsageProbePreferred(accountID int64, now time.Time) bool {
	if accountID <= 0 {
		return false
	}
	globalGrokFreeRecovery.mu.Lock()
	defer globalGrokFreeRecovery.mu.Unlock()
	h, ok := globalGrokFreeRecovery.items[accountID]
	if !ok {
		return false
	}
	if now.After(h.ProbeUntil) {
		delete(globalGrokFreeRecovery.items, accountID)
		return false
	}
	return !now.Before(h.CoolUntil) && now.Before(h.ProbeUntil)
}

// reasonIsGrokFreeUsage detects free-usage cool reasons written by our handlers.
func reasonIsGrokFreeUsage(reason string) bool {
	return strings.Contains(strings.ToLower(reason), "free usage")
}
