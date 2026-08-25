package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type usageBalanceSettlementRepositorySpy struct {
	mu            sync.Mutex
	flushLimit    int
	flushDeadline bool
	cleanupBefore time.Time
	cleanupLimit  int
}

func (s *usageBalanceSettlementRepositorySpy) FlushPendingBalanceSettlements(ctx context.Context, limit int) ([]UsageBalanceSettlementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLimit = limit
	_, s.flushDeadline = ctx.Deadline()
	return []UsageBalanceSettlementResult{}, nil
}

func (s *usageBalanceSettlementRepositorySpy) DeleteAppliedBalanceSettlements(_ context.Context, before time.Time, limit int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupBefore = before
	s.cleanupLimit = limit
	return 0, nil
}

func TestUsageBalanceSettlementWorkerUsesBoundedBatches(t *testing.T) {
	repo := &usageBalanceSettlementRepositorySpy{}
	worker := &UsageBalanceSettlementWorker{repo: repo}

	worker.flush(context.Background())
	cleanupStarted := time.Now()
	worker.cleanup(context.Background())
	cleanupFinished := time.Now()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, usageBalanceSettlementBatchSize, repo.flushLimit)
	require.True(t, repo.flushDeadline)
	require.Equal(t, usageBalanceSettlementCleanupBatch, repo.cleanupLimit)
	require.False(t, repo.cleanupBefore.Before(cleanupStarted.Add(-usageBalanceSettlementRetention-time.Second)))
	require.False(t, repo.cleanupBefore.After(cleanupFinished.Add(-usageBalanceSettlementRetention+time.Second)))
}

func TestUsageBalanceSettlementWorkerStartStopIsIdempotent(t *testing.T) {
	worker := &UsageBalanceSettlementWorker{repo: &usageBalanceSettlementRepositorySpy{}}
	worker.Start()
	worker.Start()
	worker.Stop()
	worker.Stop()
}
