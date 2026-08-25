package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type balancePreauthorizationRecoverySourceStub struct {
	records                    []BalancePreauthorizationRecord
	err                        error
	authorizationExpiredBefore time.Time
	finalizationStaleBefore    time.Time
	limit                      int
}

func (s *balancePreauthorizationRecoverySourceStub) ListRecoverableBalancePreauthorizations(
	_ context.Context,
	authorizationExpiredBefore time.Time,
	finalizationStaleBefore time.Time,
	limit int,
) ([]BalancePreauthorizationRecord, error) {
	s.authorizationExpiredBefore = authorizationExpiredBefore
	s.finalizationStaleBefore = finalizationStaleBefore
	s.limit = limit
	return append([]BalancePreauthorizationRecord(nil), s.records...), s.err
}

type balancePreauthorizationRecovererStub struct {
	mu        sync.Mutex
	recovered []string
	failFor   map[string]error
}

func (s *balancePreauthorizationRecovererStub) RecoverBalancePreauthorization(_ context.Context, record BalancePreauthorizationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recovered = append(s.recovered, record.RequestID)
	return s.failFor[record.RequestID]
}

func TestBalancePreauthorizationRecoveryWorkerUsesBoundedStaleBatch(t *testing.T) {
	source := &balancePreauthorizationRecoverySourceStub{records: []BalancePreauthorizationRecord{
		{RequestID: "one"},
		{RequestID: "two"},
		{RequestID: "three"},
	}}
	recoverer := &balancePreauthorizationRecovererStub{failFor: map[string]error{"two": errors.New("redis unavailable")}}
	worker := &BalancePreauthorizationRecoveryWorker{source: source, recoverer: recoverer}

	started := time.Now()
	worker.recoverBatch(context.Background())
	finished := time.Now()
	require.Equal(t, balancePreauthorizationRecoveryBatchSize, source.limit)
	require.False(t, source.authorizationExpiredBefore.Before(started))
	require.False(t, source.authorizationExpiredBefore.After(finished))
	require.InDelta(t,
		source.authorizationExpiredBefore.Add(-balancePreauthorizationFinalizationGrace).UnixNano(),
		source.finalizationStaleBefore.UnixNano(),
		float64(time.Millisecond),
	)
	recoverer.mu.Lock()
	require.ElementsMatch(t, []string{"one", "two", "three"}, recoverer.recovered)
	recoverer.mu.Unlock()
}

func TestBalancePreauthorizationRecoveryWorkerStartStopIsIdempotent(t *testing.T) {
	worker := &BalancePreauthorizationRecoveryWorker{
		source:    &balancePreauthorizationRecoverySourceStub{},
		recoverer: &balancePreauthorizationRecovererStub{},
	}
	worker.Start()
	worker.Start()
	worker.Stop()
	worker.Stop()
}
