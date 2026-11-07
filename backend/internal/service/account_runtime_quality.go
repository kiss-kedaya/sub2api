package service

import (
	"math"
	"sync"
	"time"
)

// Account-level runtime quality used inside one priority/load layer.
//
// The durable state already covers hard failures (rate limits, cooldown). This
// state is a soft signal for ordering candidates that are otherwise tied:
// repeated provider faults raise an exponentially decaying error rate, and time
// itself decays it back, so a recovered account is not penalized forever.
const (
	accountRuntimeQualityAlpha        = 0.3
	accountRuntimeQualityHalfLife     = 5 * time.Minute
	accountRuntimeQualityMaxEntries   = 50_000
	accountRuntimeQualityNeutralScore = 0.5
)

type accountRuntimeQualityEntry struct {
	errorRate float64
	updatedAt time.Time
}

type AccountRuntimeQuality struct {
	mu      sync.Mutex
	entries map[int64]*accountRuntimeQualityEntry
	now     func() time.Time
}

func NewAccountRuntimeQuality() *AccountRuntimeQuality {
	return &AccountRuntimeQuality{
		entries: make(map[int64]*accountRuntimeQualityEntry),
		now:     time.Now,
	}
}

func (q *AccountRuntimeQuality) clock() time.Time {
	if q == nil || q.now == nil {
		return time.Now()
	}
	return q.now()
}

// decayedLocked returns the error rate projected to now.
func (q *AccountRuntimeQuality) decayedLocked(entry *accountRuntimeQualityEntry, now time.Time) float64 {
	if entry == nil {
		return 0
	}
	age := now.Sub(entry.updatedAt)
	if age <= 0 {
		return entry.errorRate
	}
	factor := math.Pow(0.5, age.Seconds()/accountRuntimeQualityHalfLife.Seconds())
	return entry.errorRate * factor
}

// ObserveFailure records one provider fault for the account.
func (q *AccountRuntimeQuality) ObserveFailure(accountID int64) {
	if q == nil || accountID <= 0 {
		return
	}
	now := q.clock()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) >= accountRuntimeQualityMaxEntries {
		q.evictLocked(now)
	}
	entry := q.entries[accountID]
	previous := q.decayedLocked(entry, now)
	if entry == nil {
		entry = &accountRuntimeQualityEntry{}
		q.entries[accountID] = entry
	}
	entry.errorRate = accountRuntimeQualityAlpha + (1-accountRuntimeQualityAlpha)*previous
	entry.updatedAt = now
}

// ObserveSuccess records one successful request; it pulls the error rate down.
func (q *AccountRuntimeQuality) ObserveSuccess(accountID int64) {
	if q == nil || accountID <= 0 {
		return
	}
	now := q.clock()
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.entries[accountID]
	if entry == nil {
		return
	}
	entry.errorRate = (1 - accountRuntimeQualityAlpha) * q.decayedLocked(entry, now)
	entry.updatedAt = now
	if entry.errorRate < 1e-4 {
		delete(q.entries, accountID)
	}
}

// ErrorRate returns the decayed error rate, 0 when unknown.
func (q *AccountRuntimeQuality) ErrorRate(accountID int64) float64 {
	if q == nil || accountID <= 0 {
		return 0
	}
	now := q.clock()
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.decayedLocked(q.entries[accountID], now)
}

// Score blends the observed error rate with the current load into 0..1. An
// account with no observations and no load scores 0.5 + 0.5 = 1.0, so unknown
// accounts are preferred over known-bad ones but never over a known-good
// account at the same load.
func (q *AccountRuntimeQuality) Score(accountID int64, loadRate float64) float64 {
	health := 1 - clampUnit(q.ErrorRate(accountID))
	load := 1 - clampUnit(loadRate/100)
	return (health*0.5 + load*0.5)
}

func (q *AccountRuntimeQuality) evictLocked(now time.Time) {
	for id, entry := range q.entries {
		if now.Sub(entry.updatedAt) > 30*time.Minute {
			delete(q.entries, id)
		}
	}
}

// selectByRuntimeQuality picks the healthiest candidate inside one
// priority/load layer. Unknown accounts are neutral, and ties fall back to the
// existing LRU selection so behaviour is unchanged when no faults were seen.
func (s *GatewayService) selectByRuntimeQuality(candidates []accountWithLoad, preferOAuth bool) *accountWithLoad {
	if len(candidates) == 0 {
		return nil
	}
	if s == nil || s.rateLimitService == nil || s.rateLimitService.accountRuntime == nil {
		return selectByLRU(candidates, preferOAuth)
	}
	quality := s.rateLimitService.accountRuntime
	bestScore := math.Inf(-1)
	best := make([]accountWithLoad, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.account == nil {
			continue
		}
		loadRate := 0.0
		if candidate.loadInfo != nil {
			loadRate = float64(candidate.loadInfo.LoadRate)
		}
		score := quality.Score(candidate.account.ID, loadRate)
		switch {
		case score > bestScore+1e-9:
			bestScore = score
			best = append(best[:0], candidate)
		case math.Abs(score-bestScore) <= 1e-9:
			best = append(best, candidate)
		}
	}
	if len(best) == 0 {
		return selectByLRU(candidates, preferOAuth)
	}
	return selectByLRU(best, preferOAuth)
}
