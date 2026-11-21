package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	conflicts atomic.Uint64
	abandoned atomic.Uint64
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
	startBackgroundJob(worker.Start)
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
	if BackgroundJobsDisabled() {
		return
	}
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
	cleanupTicker := time.NewTicker(liveBalanceOutboxCleanupInterval)
	defer cleanupTicker.Stop()
	pollTimer := time.NewTimer(0)
	defer pollTimer.Stop()
	pollDelay := liveBalanceOutboxPollInterval

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-pollTimer.C:
			events, err := w.processBatchWithResult(w.ctx)
			if err != nil && w.ctx.Err() == nil {
				w.recordFailure("claim", 0, err)
			}
			if err != nil || events == 0 {
				pollDelay *= 2
				if pollDelay > liveBalanceOutboxPollMaxInterval {
					pollDelay = liveBalanceOutboxPollMaxInterval
				}
			} else {
				pollDelay = liveBalanceOutboxPollInterval
			}
			pollTimer.Reset(pollDelay)
		case <-cleanupTicker.C:
			w.cleanup(w.ctx)
		}
	}
}

func (w *LiveBalanceAdjustmentOutboxWorker) processBatch(ctx context.Context) error {
	_, err := w.processBatchWithResult(ctx)
	return err
}

func (w *LiveBalanceAdjustmentOutboxWorker) processBatchWithResult(ctx context.Context) (int, error) {
	return w.processBatchLimit(ctx, liveBalanceOutboxBatchSize)
}

func (w *LiveBalanceAdjustmentOutboxWorker) processBatchLimit(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = liveBalanceOutboxBatchSize
	}
	claimCtx, cancelClaim := context.WithTimeout(ctx, liveBalanceOutboxClaimTimeout)
	events, err := w.repo.Claim(claimCtx, w.workerID, batchSize, liveBalanceOutboxLease)
	cancelClaim()
	if err != nil {
		return 0, fmt.Errorf("claim live balance adjustments: %w", err)
	}

	groups := make(map[int64][]LiveBalanceAdjustmentEvent)
	for _, event := range events {
		groups[event.UserID] = append(groups[event.UserID], event)
	}
	userIDs := make([]int64, 0, len(groups))
	for userID := range groups {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	semaphore := make(chan struct{}, liveBalanceOutboxConcurrency)
	var wg sync.WaitGroup
	for _, userID := range userIDs {
		userEvents := groups[userID]
		sort.Slice(userEvents, func(i, j int) bool { return userEvents[i].ID < userEvents[j].ID })
		select {
		case <-ctx.Done():
			wg.Wait()
			return len(events), ctx.Err()
		case semaphore <- struct{}{}:
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()
			w.processEventBatch(ctx, userEvents)
		}()
	}
	wg.Wait()
	return len(events), nil
}

func (w *LiveBalanceAdjustmentOutboxWorker) processEventBatch(parent context.Context, events []LiveBalanceAdjustmentEvent) {
	if len(events) == 0 {
		return
	}
	batchApplier, ok := w.applier.(interface {
		ApplyExternalBalanceOutboxAdjustments(context.Context, []LiveBalanceAdjustmentEvent) error
	})
	if !ok {
		for _, event := range events {
			w.processEvent(parent, event)
		}
		return
	}
	ids := make([]int64, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	ctx, cancel := context.WithTimeout(parent, liveBalanceOutboxRedisTimeout)
	err := batchApplier.ApplyExternalBalanceOutboxAdjustments(ctx, events)
	cancel()
	if err != nil {
		w.handleApplyFailure(events[len(events)-1], err, func(reason string) error {
			return w.abandonEventBatch(events, reason)
		}, func(cause error) { w.retryEventBatch(events, cause) })
		return
	}
	ackCtx, ackCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = w.repo.MarkDeliveredBatch(ackCtx, ids, w.workerID)
	ackCancel()
	if err != nil {
		w.recordFailure("ack_batch", events[len(events)-1].ID, err)
		return
	}
	w.processed.Add(uint64(len(events)))
	w.lastError.Store("")
}

// handleApplyFailure decides between retry, self-heal, and abandonment. A
// vanished user can never be projected; an event that already exhausted the
// attempt budget is a poison row. Both are abandoned with the recorded error so
// they stop blocking the rest of that user's chain forever.
func (w *LiveBalanceAdjustmentOutboxWorker) handleApplyFailure(
	last LiveBalanceAdjustmentEvent,
	cause error,
	abandon func(reason string) error,
	retry func(cause error),
) {
	if errors.Is(cause, errLiveBalanceWatermarkConflict) {
		w.conflicts.Add(1)
	}
	if errors.Is(cause, errLiveBalanceAdjustmentUserGone) {
		if err := abandon(boundedLiveBalanceOutboxError(cause)); err != nil {
			w.recordFailure("abandon", last.ID, err)
			return
		}
		w.recordFailure("abandon", last.ID, cause)
		return
	}
	if last.Attempts+1 >= liveBalanceOutboxMaxAttempts {
		reason := boundedLiveBalanceOutboxError(fmt.Errorf("abandoned after %d attempts: %w", last.Attempts+1, cause))
		if err := abandon(reason); err != nil {
			w.recordFailure("abandon", last.ID, err)
			return
		}
		w.recordFailure("abandon", last.ID, cause)
		return
	}
	retry(cause)
}

func (w *LiveBalanceAdjustmentOutboxWorker) retryEventBatch(events []LiveBalanceAdjustmentEvent, cause error) {
	if len(events) == 0 {
		return
	}
	last := events[len(events)-1]
	w.recordFailure("apply_batch", last.ID, cause)
	retryAt := time.Now().UTC().Add(liveBalanceOutboxRetryDelay(last.Attempts + 1))
	ids := make([]int64, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := w.repo.RetryClaimedBatch(ctx, ids, w.workerID, retryAt, boundedLiveBalanceOutboxError(cause))
	cancel()
	if err != nil {
		w.recordFailure("release_batch", last.ID, err)
		return
	}
	w.retries.Add(uint64(len(events)))
}

func (w *LiveBalanceAdjustmentOutboxWorker) abandonEventBatch(
	events []LiveBalanceAdjustmentEvent,
	reason string,
) error {
	ids := make([]int64, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := w.repo.AbandonClaimedBatch(ctx, ids, w.workerID, reason)
	cancel()
	if err != nil {
		return fmt.Errorf("abandon live balance adjustment batch: %w", err)
	}
	w.abandoned.Add(uint64(len(events)))
	return nil
}

func (w *LiveBalanceAdjustmentOutboxWorker) abandonEvent(
	event LiveBalanceAdjustmentEvent,
	reason string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := w.repo.AbandonClaimed(ctx, event.ID, w.workerID, reason)
	cancel()
	if err != nil {
		return fmt.Errorf("abandon live balance adjustment: %w", err)
	}
	w.abandoned.Add(1)
	return nil
}

func (w *LiveBalanceAdjustmentOutboxWorker) processEvent(parent context.Context, event LiveBalanceAdjustmentEvent) {
	ctx, cancel := context.WithTimeout(parent, liveBalanceOutboxRedisTimeout)
	err := w.applier.ApplyExternalBalanceOutboxAdjustment(ctx, event)
	cancel()
	if err != nil {
		w.handleApplyFailure(event, err, func(reason string) error {
			return w.abandonEvent(event, reason)
		}, func(cause error) { w.retry(event, cause) })
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
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	before := time.Now().UTC().Add(-liveBalanceOutboxRetention)
	for i := 0; i < liveBalanceOutboxCleanupMaxBatches; i++ {
		deleted, err := w.repo.DeleteDelivered(ctx, before, liveBalanceOutboxCleanupBatch)
		if err != nil {
			w.recordFailure("cleanup", 0, err)
			return
		}
		if deleted <= 0 {
			return
		}
		w.cleaned.Add(uint64(deleted))
		if deleted < int64(liveBalanceOutboxCleanupBatch) {
			return
		}
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
	health.Conflicts = w.conflicts.Load()
	health.Abandoned = w.abandoned.Load()
	health.Cleaned = w.cleaned.Load()
	if provider, ok := w.applier.(liveBalanceRecoveryStatsProvider); ok {
		recovery := provider.RecoveryStats()
		health.Healed = recovery.Healed
		health.HealFailures = recovery.HealFailures
	}
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
