package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type usageBalanceSettlementStatsSpy struct {
	mu      sync.Mutex
	results []UsageBalanceSettlementResult
	err     error
	flushes int
}

func (s *usageBalanceSettlementStatsSpy) FlushPendingBalanceSettlements(context.Context, int) ([]UsageBalanceSettlementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	if s.err != nil {
		return nil, s.err
	}
	return s.results, nil
}

func (s *usageBalanceSettlementStatsSpy) DeleteAppliedBalanceSettlements(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestUsageBalanceSettlementWorkerStatsTrackFlushedEvents(t *testing.T) {
	spy := &usageBalanceSettlementStatsSpy{
		results: []UsageBalanceSettlementResult{
			{UserID: 1, EventCount: 5, Amount: 1.5, NewBalance: 98.5},
			{UserID: 2, EventCount: 3, Amount: 0.5, NewBalance: 10.5},
		},
	}
	worker := &UsageBalanceSettlementWorker{repo: spy}

	worker.flush(context.Background())
	stats := worker.Stats()
	require.Equal(t, uint64(1), stats.Flushes)
	require.Equal(t, uint64(8), stats.Events)
	require.Equal(t, uint64(2), stats.Users)
	require.Equal(t, uint64(0), stats.Failures)

	// An empty flush is not a settlement transaction and must not move the counters.
	spy.mu.Lock()
	spy.results = nil
	spy.mu.Unlock()
	worker.flush(context.Background())
	require.Equal(t, uint64(1), worker.Stats().Flushes)

	// A failed flush is counted, and logStats must stay quiet (it only runs from
	// the periodic cleanup tick).
	spy.mu.Lock()
	spy.err = errors.New("db unavailable")
	spy.mu.Unlock()
	worker.flush(context.Background())
	require.Equal(t, uint64(1), worker.Stats().Failures)
	worker.logStats()
}

func TestUsageBalanceSettlementConsumerRunningFollowsStartStop(t *testing.T) {
	worker := &UsageBalanceSettlementWorker{repo: &usageBalanceSettlementStatsSpy{}}
	worker.Start()
	require.True(t, UsageBalanceSettlementConsumerRunning())
	worker.Stop()
	require.False(t, UsageBalanceSettlementConsumerRunning())
}
