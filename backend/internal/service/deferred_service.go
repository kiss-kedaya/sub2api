package service

import (
	"context"
	"log"
	"sync"
	"time"
)

// DeferredService provides deferred batch update functionality
type DeferredService struct {
	accountRepo AccountRepository
	// apiKeyRepo is optional. API-key authentication updates last_used_at on
	// every successful request; batching those writes here keeps that metadata
	// out of the request latency/connection budget without changing auth state.
	apiKeyRepo  APIKeyRepository
	timingWheel *TimingWheelService
	interval    time.Duration

	lastUsedUpdates       sync.Map
	apiKeyLastUsedUpdates sync.Map
}

// NewDeferredService creates a new DeferredService instance
func NewDeferredService(accountRepo AccountRepository, timingWheel *TimingWheelService, interval time.Duration) *DeferredService {
	return &DeferredService{
		accountRepo: accountRepo,
		timingWheel: timingWheel,
		interval:    interval,
	}
}

// Start starts the deferred service
func (s *DeferredService) Start() {
	if s == nil || s.timingWheel == nil || s.interval <= 0 {
		return
	}
	s.timingWheel.ScheduleRecurring("deferred:last_used", s.interval, s.flushLastUsed)
	log.Printf("[DeferredService] Started (interval: %v)", s.interval)
}

// Stop stops the deferred service
func (s *DeferredService) Stop() {
	if s == nil {
		return
	}
	if s.timingWheel != nil {
		s.timingWheel.Cancel("deferred:last_used")
	}
	s.flushLastUsed()
	log.Printf("[DeferredService] Service stopped")
}

func (s *DeferredService) ScheduleLastUsedUpdate(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.lastUsedUpdates.Store(accountID, time.Now())
}

// SetAPIKeyRepository enables batched API-key last-used updates. It is kept as
// a setter because the existing provider graph creates DeferredService after
// APIKeyService, and the dependency is optional for lightweight embedders.
func (s *DeferredService) SetAPIKeyRepository(repo APIKeyRepository) {
	if s != nil {
		s.apiKeyRepo = repo
	}
}

// ScheduleAPIKeyLastUsedUpdate records an API-key activity timestamp without
// touching PostgreSQL. The latest timestamp wins for each key.
func (s *DeferredService) ScheduleAPIKeyLastUsedUpdate(keyID int64) {
	if s == nil || keyID <= 0 || s.apiKeyRepo == nil {
		return
	}
	s.apiKeyLastUsedUpdates.Store(keyID, time.Now())
}

// ScheduleAPIKeyLastUsed is a short alias used by service wiring and keeps
// call sites aligned with ScheduleLastUsedUpdate.
func (s *DeferredService) ScheduleAPIKeyLastUsed(keyID int64) {
	s.ScheduleAPIKeyLastUsedUpdate(keyID)
}

func (s *DeferredService) flushLastUsed() {
	updates := drainDeferredTimeUpdates(&s.lastUsedUpdates)

	if len(updates) > 0 && s.accountRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := s.accountRepo.BatchUpdateLastUsed(ctx, updates); err != nil {
			log.Printf("[DeferredService] BatchUpdateLastUsed failed (%d accounts): %v", len(updates), err)
			restoreDeferredTimeUpdates(&s.lastUsedUpdates, updates)
		} else {
			log.Printf("[DeferredService] BatchUpdateLastUsed flushed %d accounts", len(updates))
		}
		cancel()
	}

	apiKeyUpdates := drainDeferredTimeUpdates(&s.apiKeyLastUsedUpdates)
	if len(apiKeyUpdates) == 0 || s.apiKeyRepo == nil {
		return
	}
	// Reuse the same deferred timestamp map for repositories that support the
	// optional batch interface. A missing batch method is a compatibility case;
	// production's SQL repository implements it.
	if batcher, ok := s.apiKeyRepo.(interface {
		BatchUpdateLastUsed(context.Context, map[int64]time.Time) error
	}); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := batcher.BatchUpdateLastUsed(ctx, apiKeyUpdates); err != nil {
			log.Printf("[DeferredService] BatchUpdateLastUsed API keys failed (%d keys): %v", len(apiKeyUpdates), err)
			restoreDeferredTimeUpdates(&s.apiKeyLastUsedUpdates, apiKeyUpdates)
		} else {
			log.Printf("[DeferredService] BatchUpdateLastUsed flushed %d API keys", len(apiKeyUpdates))
		}
		cancel()
		return
	}
	// Compatibility fallback for repositories that have not added the batch
	// method yet. This path is only used by non-production/lightweight adapters.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for id, ts := range apiKeyUpdates {
		if err := s.apiKeyRepo.UpdateLastUsed(ctx, id, ts); err != nil {
			log.Printf("[DeferredService] UpdateLastUsed API key failed (key=%d): %v", id, err)
			restoreDeferredTimeUpdates(&s.apiKeyLastUsedUpdates, map[int64]time.Time{id: ts})
		}
	}
}

func drainDeferredTimeUpdates(source *sync.Map) map[int64]time.Time {
	updates := make(map[int64]time.Time)
	if source == nil {
		return updates
	}
	source.Range(func(key, value any) bool {
		id, ok := key.(int64)
		if !ok {
			return true
		}
		ts, ok := value.(time.Time)
		if !ok {
			return true
		}
		updates[id] = ts
		// Do not delete a newer timestamp stored concurrently after Range read
		// the old value. CompareAndDelete leaves that newer value for the next
		// flush instead of losing the activity signal.
		source.CompareAndDelete(key, value)
		return true
	})
	return updates
}

// restoreDeferredTimeUpdates requeues failed writes without overwriting a
// newer timestamp that arrived while the batch was in flight.
func restoreDeferredTimeUpdates(source *sync.Map, updates map[int64]time.Time) {
	if source == nil {
		return
	}
	for id, ts := range updates {
		for {
			current, loaded := source.Load(id)
			if !loaded {
				if _, loaded = source.LoadOrStore(id, ts); !loaded {
					break
				}
				continue
			}
			currentTS, ok := current.(time.Time)
			if ok && currentTS.After(ts) {
				break
			}
			if source.CompareAndSwap(id, current, ts) {
				break
			}
		}
	}
}
