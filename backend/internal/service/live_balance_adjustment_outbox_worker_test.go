package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type liveBalanceOutboxRepoStub struct {
	mu             sync.Mutex
	events         []LiveBalanceAdjustmentEvent
	claimLimit     int
	marked         []int64
	markedBatches  [][]int64
	retried        []int64
	retriedBatches [][]int64
	retryError     string
	markFailures   int
	deleted        int64
	stats          LiveBalanceAdjustmentOutboxStats
}

func (r *liveBalanceOutboxRepoStub) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]LiveBalanceAdjustmentEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimLimit = limit
	return append([]LiveBalanceAdjustmentEvent(nil), r.events...), nil
}

func (r *liveBalanceOutboxRepoStub) MarkDelivered(_ context.Context, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.markFailures > 0 {
		r.markFailures--
		return errors.New("database unavailable")
	}
	r.marked = append(r.marked, id)
	return nil
}

func (r *liveBalanceOutboxRepoStub) MarkDeliveredBatch(_ context.Context, ids []int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.markFailures > 0 {
		r.markFailures--
		return errors.New("database unavailable")
	}
	r.markedBatches = append(r.markedBatches, append([]int64(nil), ids...))
	r.marked = append(r.marked, ids...)
	return nil
}

func (r *liveBalanceOutboxRepoStub) RetryClaimed(_ context.Context, id int64, _ string, _ time.Time, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = append(r.retried, id)
	r.retryError = lastError
	return nil
}

func (r *liveBalanceOutboxRepoStub) RetryClaimedBatch(_ context.Context, ids []int64, _ string, _ time.Time, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retriedBatches = append(r.retriedBatches, append([]int64(nil), ids...))
	r.retryError = lastError
	return nil
}

func (r *liveBalanceOutboxRepoStub) DeleteDelivered(context.Context, time.Time, int) (int64, error) {
	return r.deleted, nil
}

func (r *liveBalanceOutboxRepoStub) Stats(context.Context) (LiveBalanceAdjustmentOutboxStats, error) {
	return r.stats, nil
}

type liveBalanceOutboxApplierStub struct {
	mu           sync.Mutex
	err          error
	seen         map[string]int64
	balanceUnits int64
	calls        []string
	batches      [][]int64
}

func (a *liveBalanceOutboxApplierStub) ApplyExternalBalanceOutboxAdjustments(_ context.Context, events []LiveBalanceAdjustmentEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]int64, len(events))
	for i, event := range events {
		ids[i] = event.ID
	}
	a.batches = append(a.batches, ids)
	if a.err != nil {
		return a.err
	}
	if a.seen == nil {
		a.seen = make(map[string]int64)
	}
	for _, event := range events {
		eventID := event.RedisEventID()
		if _, exists := a.seen[eventID]; !exists {
			a.seen[eventID] = event.DeltaUnits
			a.balanceUnits += event.DeltaUnits
		}
	}
	return nil
}

func (a *liveBalanceOutboxApplierStub) ApplyExternalBalanceOutboxAdjustment(_ context.Context, event LiveBalanceAdjustmentEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	eventID := event.RedisEventID()
	a.calls = append(a.calls, eventID)
	if a.err != nil {
		return a.err
	}
	if a.seen == nil {
		a.seen = make(map[string]int64)
	}
	if _, exists := a.seen[eventID]; !exists {
		a.seen[eventID] = event.DeltaUnits
		a.balanceUnits += event.DeltaUnits
	}
	return nil
}

func TestLiveBalanceAdjustmentOutboxWorkerDeliversStableIdAndAcknowledges(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{}
	applier := &liveBalanceOutboxApplierStub{}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	event := LiveBalanceAdjustmentEvent{ID: 17, UserID: 42, DeltaUnits: 125000000}

	worker.processEvent(context.Background(), event)

	require.Equal(t, []string{"live-balance-outbox:17"}, applier.calls)
	require.Equal(t, []int64{17}, repo.marked)
	require.Equal(t, int64(125000000), applier.balanceUnits)
	require.Equal(t, uint64(1), worker.Health(context.Background()).Processed)
}

func TestLiveBalanceAdjustmentOutboxWorkerRetriesRedisFailure(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{}
	applier := &liveBalanceOutboxApplierStub{err: errors.New("redis unavailable")}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)

	worker.processEvent(context.Background(), LiveBalanceAdjustmentEvent{ID: 18, UserID: 42, DeltaUnits: -200000000, Attempts: 1})

	require.Equal(t, []int64{18}, repo.retried)
	require.Contains(t, repo.retryError, "redis unavailable")
	require.Empty(t, repo.marked)
	health := worker.Health(context.Background())
	require.Equal(t, uint64(1), health.Retries)
	require.Equal(t, uint64(1), health.Failures)
}

func TestLiveBalanceAdjustmentOutboxWorkerAckCrashReplayDoesNotDoubleApply(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{markFailures: 1}
	applier := &liveBalanceOutboxApplierStub{}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	event := LiveBalanceAdjustmentEvent{ID: 19, UserID: 42, DeltaUnits: 300000000}

	worker.processEvent(context.Background(), event)
	worker.processEvent(context.Background(), event)

	require.Equal(t, []string{"live-balance-outbox:19", "live-balance-outbox:19"}, applier.calls)
	require.Equal(t, int64(300000000), applier.balanceUnits, "stable event id must make post-Redis crash replay idempotent")
	require.Equal(t, []int64{19}, repo.marked)
}

func TestLiveBalanceAdjustmentOutboxWorkerBatchHealthCleanupAndLifecycle(t *testing.T) {
	oldest := time.Now().UTC().Add(-time.Minute)
	repo := &liveBalanceOutboxRepoStub{
		events: []LiveBalanceAdjustmentEvent{
			{ID: 1, UserID: 7, DeltaUnits: 100000000},
			{ID: 2, UserID: 8, DeltaUnits: -100000000},
		},
		deleted: 4,
		stats: LiveBalanceAdjustmentOutboxStats{
			Pending: 2, Delivered: 9, OldestCreatedAt: &oldest, MaxAttempts: 3, LastError: "prior failure",
		},
	}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, &liveBalanceOutboxApplierStub{})
	require.NoError(t, worker.processBatch(context.Background()))
	require.Equal(t, liveBalanceOutboxBatchSize, repo.claimLimit)
	require.Len(t, repo.marked, 2)

	worker.cleanup(context.Background())
	health := worker.Health(context.Background())
	require.Equal(t, int64(2), health.Pending)
	require.Equal(t, int64(9), health.Delivered)
	require.Equal(t, uint64(4), health.Cleaned)
	require.GreaterOrEqual(t, health.OldestLag, time.Minute)
	require.Equal(t, "prior failure", health.LastError)

	worker.Start()
	require.Eventually(t, func() bool { return worker.Health(context.Background()).Running }, time.Second, 10*time.Millisecond)
	require.NotPanics(t, func() { worker.Stop(); worker.Stop() })
	require.False(t, worker.Health(context.Background()).Running)
}

func TestLiveBalanceAdjustmentOutboxWorkerAppliesAndAcknowledgesPerUserPrefix(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{events: []LiveBalanceAdjustmentEvent{
		{ID: 10, UserID: 7, PredecessorID: 8, DeltaUnits: 100},
		{ID: 11, UserID: 7, PredecessorID: 10, DeltaUnits: -25},
		{ID: 20, UserID: 9, PredecessorID: 18, DeltaUnits: 50},
	}}
	applier := &liveBalanceOutboxApplierStub{}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	processed, err := worker.processBatchLimit(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, 3, processed)
	require.Len(t, applier.batches, 2)
	require.Len(t, repo.markedBatches, 2)
	require.Equal(t, int64(125), applier.balanceUnits)
	require.Equal(t, uint64(3), worker.Health(context.Background()).Processed)
}

func TestLiveBalanceAdjustmentOutboxBatchAckCrashReplayDoesNotDoubleApply(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{markFailures: 1}
	applier := &liveBalanceOutboxApplierStub{}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	events := []LiveBalanceAdjustmentEvent{
		{ID: 10, UserID: 7, PredecessorID: 8, DeltaUnits: 100},
		{ID: 11, UserID: 7, PredecessorID: 10, DeltaUnits: -25},
	}
	worker.processEventBatch(context.Background(), events)
	require.Empty(t, repo.marked)
	worker.processEventBatch(context.Background(), events)
	require.Equal(t, int64(75), applier.balanceUnits)
	require.Equal(t, []int64{10, 11}, repo.marked)
	require.Len(t, applier.batches, 2)
	require.Equal(t, uint64(2), worker.processed.Load())
}

func TestLiveBalanceAdjustmentOutboxBatchRedisFailureKeepsAllEventsPending(t *testing.T) {
	repo := &liveBalanceOutboxRepoStub{}
	applier := &liveBalanceOutboxApplierStub{err: errors.New("redis unavailable")}
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	worker.processEventBatch(context.Background(), []LiveBalanceAdjustmentEvent{
		{ID: 10, UserID: 7, PredecessorID: 8, DeltaUnits: 100},
		{ID: 11, UserID: 7, PredecessorID: 10, DeltaUnits: -25},
	})
	require.Empty(t, repo.marked)
	require.Equal(t, [][]int64{{10, 11}}, repo.retriedBatches)
	require.Equal(t, int64(0), applier.balanceUnits)
	require.Equal(t, uint64(2), worker.retries.Load())
}

func TestLiveBalanceOutboxRetryDelayIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 20; attempt++ {
		delay := liveBalanceOutboxRetryDelay(attempt)
		require.GreaterOrEqual(t, delay, 800*time.Millisecond)
		require.LessOrEqual(t, delay, 308*time.Second)
	}
}
