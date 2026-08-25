package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	usageBalanceSettlementPollInterval = 250 * time.Millisecond
	usageBalanceSettlementBatchSize    = 2000
	usageBalanceSettlementRetention    = 15 * time.Minute
	usageBalanceSettlementCleanupEvery = time.Minute
	usageBalanceSettlementCleanupBatch = 20000
)

// UsageBalanceSettlementWorker drains compact billing events. Every process may
// run a worker; the repository uses a PostgreSQL advisory transaction lock so
// exactly one instance coalesces a batch at a time.
type UsageBalanceSettlementWorker struct {
	repo UsageBalanceSettlementRepository

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

func NewUsageBalanceSettlementWorker(
	repo UsageBalanceSettlementRepository,
) *UsageBalanceSettlementWorker {
	worker := &UsageBalanceSettlementWorker{repo: repo}
	worker.Start()
	return worker
}

func (w *UsageBalanceSettlementWorker) Start() {
	if w == nil || w.repo == nil || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go w.run(ctx)
}

func (w *UsageBalanceSettlementWorker) Stop() {
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

func (w *UsageBalanceSettlementWorker) run(ctx context.Context) {
	defer w.wg.Done()
	pollTicker := time.NewTicker(usageBalanceSettlementPollInterval)
	cleanupTicker := time.NewTicker(usageBalanceSettlementCleanupEvery)
	defer pollTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			w.flush(ctx)
		case <-cleanupTicker.C:
			w.cleanup(ctx)
		}
	}
}

func (w *UsageBalanceSettlementWorker) flush(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	_, err := w.repo.FlushPendingBalanceSettlements(ctx, usageBalanceSettlementBatchSize)
	if err != nil {
		logger.L().Warn("billing.balance_settlement_flush_failed", zap.Error(err))
		return
	}
}

func (w *UsageBalanceSettlementWorker) cleanup(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	_, err := w.repo.DeleteAppliedBalanceSettlements(
		ctx,
		time.Now().Add(-usageBalanceSettlementRetention),
		usageBalanceSettlementCleanupBatch,
	)
	if err != nil {
		logger.L().Warn("billing.balance_settlement_cleanup_failed", zap.Error(err))
	}
}
