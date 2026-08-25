package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	balancePreauthorizationRecoveryPollInterval = 2 * time.Second
	balancePreauthorizationFinalizationGrace    = 30 * time.Second
	balancePreauthorizationRecoveryBatchSize    = 500
	balancePreauthorizationRecoveryParallelism  = 16
	balancePreauthorizationRecoveryBatchTimeout = 20 * time.Second
)

type balancePreauthorizationRecoverySource interface {
	ListRecoverableBalancePreauthorizations(ctx context.Context, authorizationExpiredBefore, finalizationStaleBefore time.Time, limit int) ([]BalancePreauthorizationRecord, error)
}

type balancePreauthorizationRecoverer interface {
	RecoverBalancePreauthorization(ctx context.Context, record BalancePreauthorizationRecord) error
}

// BalancePreauthorizationRecoveryWorker repairs abandoned holds and interrupted
// Redis finalization. Multiple instances may run: source selection uses
// SKIP LOCKED and every recovery mutation is idempotent.
type BalancePreauthorizationRecoveryWorker struct {
	source    balancePreauthorizationRecoverySource
	recoverer balancePreauthorizationRecoverer

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func NewBalancePreauthorizationRecoveryWorker(
	source UsageBillingRepository,
	recoverer *BalancePreauthorizationService,
) *BalancePreauthorizationRecoveryWorker {
	worker := &BalancePreauthorizationRecoveryWorker{source: source, recoverer: recoverer}
	worker.Start()
	return worker
}

func (w *BalancePreauthorizationRecoveryWorker) Start() {
	if w == nil || w.source == nil || w.recoverer == nil || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go w.run(ctx)
}

func (w *BalancePreauthorizationRecoveryWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		w.wg.Wait()
	})
}

func (w *BalancePreauthorizationRecoveryWorker) run(ctx context.Context) {
	defer w.wg.Done()
	ticker := time.NewTicker(balancePreauthorizationRecoveryPollInterval)
	defer ticker.Stop()
	w.recoverBatch(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.recoverBatch(ctx)
		}
	}
}

func (w *BalancePreauthorizationRecoveryWorker) recoverBatch(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, balancePreauthorizationRecoveryBatchTimeout)
	defer cancel()
	now := time.Now()
	records, err := w.source.ListRecoverableBalancePreauthorizations(
		ctx,
		now,
		now.Add(-balancePreauthorizationFinalizationGrace),
		balancePreauthorizationRecoveryBatchSize,
	)
	if err != nil {
		logger.L().Warn("billing.balance_preauthorization_recovery_list_failed", zap.Error(err))
		return
	}
	if len(records) == 0 {
		return
	}

	workerCount := balancePreauthorizationRecoveryParallelism
	if len(records) < workerCount {
		workerCount = len(records)
	}
	jobs := make(chan BalancePreauthorizationRecord)
	var (
		workers    sync.WaitGroup
		failureMu  sync.Mutex
		failures   int
		firstError error
	)
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for record := range jobs {
				if err := w.recoverer.RecoverBalancePreauthorization(ctx, record); err != nil {
					failureMu.Lock()
					failures++
					if firstError == nil {
						firstError = err
					}
					failureMu.Unlock()
				}
			}
		}()
	}
sendLoop:
	for _, record := range records {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- record:
		}
	}
	close(jobs)
	workers.Wait()
	if failures > 0 {
		logger.L().Warn(
			"billing.balance_preauthorization_recovery_partial_failure",
			zap.Int("records", len(records)),
			zap.Int("failures", failures),
			zap.Error(firstError),
		)
	}
}
