package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type deferredAPIKeyRepoStub struct {
	APIKeyRepository
	mu      sync.Mutex
	updates []map[int64]time.Time
	nextErr error
}

func (s *deferredAPIKeyRepoStub) BatchUpdateLastUsed(_ context.Context, updates map[int64]time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nextErr != nil {
		err := s.nextErr
		s.nextErr = nil
		return err
	}
	copy := make(map[int64]time.Time, len(updates))
	for id, ts := range updates {
		copy[id] = ts
	}
	s.updates = append(s.updates, copy)
	return nil
}

func (s *deferredAPIKeyRepoStub) updateCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.updates)
}

func TestDeferredServiceBatchesAPIKeyLastUsedUpdates(t *testing.T) {
	repo := &deferredAPIKeyRepoStub{}
	deferred := &DeferredService{apiKeyRepo: repo}
	deferred.ScheduleAPIKeyLastUsed(11)
	deferred.ScheduleAPIKeyLastUsed(22)
	deferred.ScheduleAPIKeyLastUsed(11)

	deferred.flushLastUsed()

	require.Equal(t, 1, repo.updateCount())
	repo.mu.Lock()
	got := repo.updates[0]
	repo.mu.Unlock()
	require.Len(t, got, 2)
	require.Contains(t, got, int64(11))
	require.Contains(t, got, int64(22))
}

func TestDeferredServiceRetriesFailedAPIKeyBatch(t *testing.T) {
	repo := &deferredAPIKeyRepoStub{nextErr: errors.New("database unavailable")}
	deferred := &DeferredService{apiKeyRepo: repo}
	deferred.ScheduleAPIKeyLastUsed(31)

	deferred.flushLastUsed()
	require.Zero(t, repo.updateCount())

	deferred.flushLastUsed()
	require.Equal(t, 1, repo.updateCount())
}

func TestAPIKeyServiceScheduledLastUsedKeepsPerKeyDebounce(t *testing.T) {
	var scheduled int
	svc := &APIKeyService{}
	svc.SetLastUsedScheduler(func(int64) { scheduled++ })

	require.NoError(t, svc.TouchLastUsed(context.Background(), 77))
	require.NoError(t, svc.TouchLastUsed(context.Background(), 77))
	require.Equal(t, 1, scheduled)
}

func TestDrainDeferredTimeUpdatesPreservesConcurrentReplacement(t *testing.T) {
	var source sync.Map
	old := time.Now().Add(-time.Minute)
	fresh := time.Now()
	source.Store(int64(1), old)
	updates := drainDeferredTimeUpdates(&source)
	require.Equal(t, old, updates[1])

	// A replacement after the drain must survive for the next flush. This
	// mirrors the CompareAndDelete guarantee used by concurrent request writes.
	source.Store(int64(1), fresh)
	next := drainDeferredTimeUpdates(&source)
	require.Equal(t, fresh, next[1])
}
