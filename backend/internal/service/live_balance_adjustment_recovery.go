package service

import (
	"context"
	"errors"
	"fmt"
)

type liveBalanceWatermarkInitializer interface {
	InitializeLiveBalanceAtWatermark(
		ctx context.Context,
		userID int64,
		balance float64,
		watermark int64,
	) (LiveBalanceResult, error)
}

// recoveringLiveBalanceAdjustmentApplier repairs a missing Redis wallet from
// one authoritative PostgreSQL snapshot before replaying the claimed event.
// The atomic initializer never replaces an existing wallet, so a concurrent
// request authorization or worker cannot lose a hold or a newer adjustment.
type recoveringLiveBalanceAdjustmentApplier struct {
	cache          *BillingCacheService
	snapshotReader balancePreauthorizationSnapshotReader
	initializer    liveBalanceWatermarkInitializer
}

func newRecoveringLiveBalanceAdjustmentApplier(
	cache *BillingCacheService,
	snapshotReader balancePreauthorizationSnapshotReader,
) *recoveringLiveBalanceAdjustmentApplier {
	applier := &recoveringLiveBalanceAdjustmentApplier{
		cache:          cache,
		snapshotReader: snapshotReader,
	}
	if cache != nil {
		applier.initializer, _ = cache.cache.(liveBalanceWatermarkInitializer)
	}
	return applier
}

func (a *recoveringLiveBalanceAdjustmentApplier) ApplyExternalBalanceOutboxAdjustment(
	ctx context.Context,
	event LiveBalanceAdjustmentEvent,
) error {
	if a == nil || a.cache == nil {
		return errors.New("live balance adjustment applier is unavailable")
	}
	err := a.cache.ApplyExternalBalanceOutboxAdjustment(ctx, event)
	if !errors.Is(err, errLiveBalanceWalletNotInitialized) {
		return err
	}
	if a.snapshotReader == nil || a.initializer == nil {
		return fmt.Errorf("recover missing live balance wallet: recovery dependencies are unavailable: %w", err)
	}

	snapshot, snapshotErr := a.snapshotReader.LoadLiveBalanceInitializationSnapshot(ctx, event.UserID)
	if snapshotErr != nil {
		return fmt.Errorf("load live balance recovery snapshot: %w", snapshotErr)
	}
	if snapshot.HasUnsettled {
		return fmt.Errorf("recover missing live balance wallet: user_id=%d has unsettled billing", event.UserID)
	}

	result, initializeErr := a.initializer.InitializeLiveBalanceAtWatermark(
		ctx,
		event.UserID,
		snapshot.Balance,
		snapshot.Watermark,
	)
	if initializeErr != nil {
		return fmt.Errorf("initialize live balance recovery snapshot: %w", initializeErr)
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent:
	case LiveBalanceOutcomeConflict:
		return fmt.Errorf("initialize live balance recovery snapshot conflict: user_id=%d", event.UserID)
	default:
		return fmt.Errorf("unexpected live balance recovery outcome: %d", result.Outcome)
	}

	if err := a.cache.ApplyExternalBalanceOutboxAdjustment(ctx, event); err != nil {
		return fmt.Errorf("replay live balance adjustment after recovery: %w", err)
	}
	return nil
}
