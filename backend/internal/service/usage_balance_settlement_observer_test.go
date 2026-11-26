//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type settlementObserverSpy struct {
	mu      sync.Mutex
	batches [][]UsageBalanceSettlementResult
	panics  atomic.Int32 // number of upcoming calls that must panic
	calls   atomic.Int64
}

func (s *settlementObserverSpy) ObserveBalanceSettlements(results []UsageBalanceSettlementResult) {
	s.calls.Add(1)
	if s.panics.Add(-1) >= 0 {
		panic("observer boom")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, results)
}

func (s *settlementObserverSpy) received() [][]UsageBalanceSettlementResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]UsageBalanceSettlementResult(nil), s.batches...)
}

func newObservedWorker(repo UsageBalanceSettlementRepository, observer UsageBalanceSettlementObserver, merge bool) *UsageBalanceSettlementWorker {
	// Built the way production does it, minus the background start.
	w := &UsageBalanceSettlementWorker{repo: repo, opts: UsageBalanceSettlementWorkerOptions{MergeEnabled: merge, Observer: observer}}
	if merge && observer != nil {
		w.observeCh = make(chan []UsageBalanceSettlementResult, usageBalanceSettlementObserveBuffer)
	}
	return w
}

func TestSettlementObserver_OnlyCommittedFlushesAreObserved(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	w := newObservedWorker(repo, &settlementObserverSpy{}, true)

	// Failed flush: nothing was committed, nothing may be observed.
	repo.flushErr = errors.New("db unavailable")
	w.flush(context.Background())
	require.Len(t, w.observeCh, 0)

	// Empty flush (nothing to settle, or this process is not the leader).
	repo.flushErr = nil
	repo.results = nil
	w.flush(context.Background())
	require.Len(t, w.observeCh, 0)

	repo.results = []UsageBalanceSettlementResult{{UserID: 7, EventCount: 2, Amount: 1.5, NewBalance: 8.5}}
	w.flush(context.Background())
	require.Len(t, w.observeCh, 1)
	got := <-w.observeCh
	require.Equal(t, repo.results, got)
}

func TestSettlementObserver_NeverBlocksTheFlushLoop(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{results: []UsageBalanceSettlementResult{{UserID: 1, EventCount: 1, Amount: 1, NewBalance: 9}}}
	w := newObservedWorker(repo, &settlementObserverSpy{}, true)

	for i := 0; i < usageBalanceSettlementObserveBuffer; i++ {
		w.flush(context.Background())
	}
	require.Len(t, w.observeCh, usageBalanceSettlementObserveBuffer)
	require.Zero(t, w.observerDropped.Load())

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.flush(context.Background())
		w.flush(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a full observer buffer must not block settlement")
	}
	require.Equal(t, uint64(2), w.observerDropped.Load())
	require.Equal(t, uint64(usageBalanceSettlementObserveBuffer+2), w.Stats().Flushes, "dropped alerts never undo or skip a settlement")
	require.Equal(t, uint64(usageBalanceSettlementObserveBuffer+2), w.Stats().Events)
}

func TestSettlementObserver_NotArmedWithTheSwitchOff(t *testing.T) {
	resetSettlementProcessState(t)
	spy := &settlementObserverSpy{}
	worker := NewUsageBalanceSettlementWorkerWithOptions(
		&guardTestRepo{results: []UsageBalanceSettlementResult{{UserID: 1, EventCount: 1, Amount: 1, NewBalance: 9}}},
		UsageBalanceSettlementWorkerOptions{MergeEnabled: false, Observer: spy},
	)
	t.Cleanup(worker.Stop)
	require.Nil(t, worker.observeCh)
	require.False(t, UsageBalanceSettlementNotifierActive(), "request-path alerts must stay in charge when the worker does not run the observer")

	worker.flush(context.Background())
	require.Zero(t, spy.calls.Load())
}

func TestSettlementObserver_DeliveredByTheRunningWorker(t *testing.T) {
	resetSettlementProcessState(t)
	spy := &settlementObserverSpy{}
	repo := &guardTestRepo{results: []UsageBalanceSettlementResult{{UserID: 4, EventCount: 3, Amount: 2.5, NewBalance: 7.5}}}
	worker := NewUsageBalanceSettlementWorkerWithOptions(repo, UsageBalanceSettlementWorkerOptions{MergeEnabled: true, Observer: spy})
	t.Cleanup(worker.Stop)
	require.True(t, UsageBalanceSettlementNotifierActive())

	// The poll interval is 250ms; drive a flush directly instead of waiting for it.
	worker.flush(context.Background())
	require.Eventually(t, func() bool { return len(spy.received()) == 1 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, repo.results, spy.received()[0])

	worker.Stop()
	require.False(t, UsageBalanceSettlementNotifierActive())
}

func TestSettlementObserver_PanicDoesNotStopLaterDeliveries(t *testing.T) {
	resetSettlementProcessState(t)
	spy := &settlementObserverSpy{}
	spy.panics.Store(1)
	repo := &guardTestRepo{results: []UsageBalanceSettlementResult{{UserID: 4, EventCount: 1, Amount: 1, NewBalance: 5}}}
	worker := NewUsageBalanceSettlementWorkerWithOptions(repo, UsageBalanceSettlementWorkerOptions{MergeEnabled: true, Observer: spy})
	t.Cleanup(worker.Stop)

	worker.flush(context.Background())
	require.Eventually(t, func() bool { return spy.calls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	worker.flush(context.Background())
	require.Eventually(t, func() bool { return len(spy.received()) == 1 }, 2*time.Second, 5*time.Millisecond,
		"a panicking observer must not take the delivery goroutine down")
}
