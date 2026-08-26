package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// LiveBalanceAdjustmentOutboxWorker delivers PostgreSQL-committed external
// balance changes to Redis. Every instance may run a worker; leased SKIP LOCKED
// claims distribute work, while stable Redis event IDs make retries idempotent.
type LiveBalanceAdjustmentOutboxWorker struct {
	repo     LiveBalanceAdjustmentOutboxRepository
	applier  liveBalanceAdjustmentApplier
	workerID string

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
	stop      sync.Once
	running   atomic.Bool
	processed atomic.Uint64
	retries   atomic.Uint64
	failures  atomic.Uint64
	cleaned   atomic.Uint64
	lastError atomic.Value
}

func NewLiveBalanceAdjustmentOutboxWorker(
	repo LiveBalanceAdjustmentOutboxRepository,
	cache *BillingCacheService,
) *LiveBalanceAdjustmentOutboxWorker {
	return newLiveBalanceAdjustmentOutboxWorker(repo, cache)
}

// ProvideLiveBalanceAdjustmentOutboxWorker starts durable Redis delivery. A
// missing wallet is rebuilt from the same PostgreSQL balance/watermark snapshot
// used by request preauthorization before the claimed event is replayed.
func ProvideLiveBalanceAdjustmentOutboxWorker(
	repo LiveBalanceAdjustmentOutboxRepository,
	cache *BillingCacheService,
	billingRepo UsageBillingRepository,
) *LiveBalanceAdjustmentOutboxWorker {
	snapshotReader, _ := billingRepo.(balancePreauthorizationSnapshotReader)
	applier := newRecoveringLiveBalanceAdjustmentApplier(cache, snapshotReader)
	worker := newLiveBalanceAdjustmentOutboxWorker(repo, applier)
	worker.Start()
	return worker
}

func newLiveBalanceAdjustmentOutboxWorker(
	repo LiveBalanceAdjustmentOutboxRepository,
	applier liveBalanceAdjustmentApplier,
) *LiveBalanceAdjustmentOutboxWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &LiveBalanceAdjustmentOutboxWorker{
		repo: repo, applier: applier, workerID: uuid.NewString(), ctx: ctx, cancel: cancel,
	}
	worker.lastError.Store("")
	return worker
}

func (w *LiveBalanceAdjustmentOutboxWorker) Start() {
	if w == nil || w.repo == nil || w.applier == nil {
		return
	}
	w.start.Do(func() {
		w.running.Store(true)
		w.wg.Add(1)
		go w.run()
	})
}

func (w *LiveBalanceAdjustmentOutboxWorker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() {
		w.cancel()
		w.wg.Wait()
		w.running.Store(false)
	})
}

func (w *LiveBalanceAdjustmentOutboxWorker) run() {
	defer w.wg.Done()
	defer w.running.Store(false)
	pollTicker := time.NewTicker(liveBalanceOutboxPollInterval)
	cleanupTicker := time.NewTicker(liveBalanceOutboxCleanupInterval)
	defer pollTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		if err := w.processBatch(w.ctx); err != nil && w.ctx.Err() == nil {
			w.recordFailure("claim", 0, err)
		}
		select {
		case <-w.ctx.Done():
			return
		case <-pollTicker.C:
		case <-cleanupTicker.C:
			w.cleanup(w.ctx)
		}
	}
}

func (w *LiveBalanceAdjustmentOutboxWorker) processBatch(ctx context.Context) error {
	events, err := w.repo.Claim(ctx, w.workerID, liveBalanceOutboxBatchSize, liveBalanceOutboxLease)
	if err != nil {
		return fmt.Errorf("claim live balance adjustments: %w", err)
	}

	semaphore := make(chan struct{}, liveBalanceOutboxConcurrency)
	var wg sync.WaitGroup
	for i := range events {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case semaphore <- struct{}{}:
		}
		wg.Add(1)
		go func(event LiveBalanceAdjustmentEvent) {
			defer wg.Done()
			defer func() { <-semaphore }()
			w.processEvent(ctx, event)
		}(events[i])
	}
	wg.Wait()
	return nil
}

func (w *LiveBalanceAdjustmentOutboxWorker) processEvent(parent context.Context, event LiveBalanceAdjustmentEvent) {
	ctx, cancel := context.WithTimeout(parent, liveBalanceOutboxRedisTimeout)
	err := w.applier.ApplyExternalBalanceOutboxAdjustment(ctx, event)
	cancel()
	if err != nil {
		w.retry(event, err)
		return
	}

	ackCtx, ackCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = w.repo.MarkDelivered(ackCtx, event.ID, w.workerID)
	ackCancel()
	if err != nil {
		// The lease will expire and Redis will idempotently accept the same event.
		w.recordFailure("ack", event.ID, err)
		return
	}
	w.processed.Add(1)
	w.lastError.Store("")
}

func (w *LiveBalanceAdjustmentOutboxWorker) retry(event LiveBalanceAdjustmentEvent, cause error) {
	w.recordFailure("apply", event.ID, cause)
	retryAt := time.Now().UTC().Add(liveBalanceOutboxRetryDelay(event.Attempts + 1))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := w.repo.RetryClaimed(ctx, event.ID, w.workerID, retryAt, boundedLiveBalanceOutboxError(cause))
	cancel()
	if err != nil {
		w.recordFailure("release", event.ID, err)
		return
	}
	w.retries.Add(1)
}

func (w *LiveBalanceAdjustmentOutboxWorker) cleanup(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	deleted, err := w.repo.DeleteDelivered(
		ctx,
		time.Now().UTC().Add(-liveBalanceOutboxRetention),
		liveBalanceOutboxCleanupBatch,
	)
	cancel()
	if err != nil {
		w.recordFailure("cleanup", 0, err)
		return
	}
	if deleted > 0 {
		w.cleaned.Add(uint64(deleted))
	}
}

func (w *LiveBalanceAdjustmentOutboxWorker) recordFailure(operation string, eventID int64, err error) {
	if err == nil {
		return
	}
	w.failures.Add(1)
	w.lastError.Store(boundedLiveBalanceOutboxError(err))
	logger.L().Warn("billing.live_balance_outbox_failed",
		zap.String("operation", operation),
		zap.Int64("event_id", eventID),
		zap.Error(err),
	)
}

func (w *LiveBalanceAdjustmentOutboxWorker) Health(ctx context.Context) LiveBalanceAdjustmentOutboxHealth {
	health := LiveBalanceAdjustmentOutboxHealth{}
	if w == nil {
		return health
	}
	health.Running = w.running.Load()
	health.Processed = w.processed.Load()
	health.Retries = w.retries.Load()
	health.Failures = w.failures.Load()
	health.Cleaned = w.cleaned.Load()
	if value := w.lastError.Load(); value != nil {
		health.LastError, _ = value.(string)
	}
	if w.repo == nil {
		return health
	}
	stats, err := w.repo.Stats(ctx)
	if err != nil {
		health.StatsError = boundedLiveBalanceOutboxError(err)
		return health
	}
	health.Pending = stats.Pending
	health.Delivered = stats.Delivered
	health.MaxAttempts = stats.MaxAttempts
	if health.LastError == "" {
		health.LastError = stats.LastError
	}
	if stats.OldestCreatedAt != nil {
		health.OldestLag = time.Since(*stats.OldestCreatedAt)
		if health.OldestLag < 0 {
			health.OldestLag = 0
		}
	}
	return health
}
