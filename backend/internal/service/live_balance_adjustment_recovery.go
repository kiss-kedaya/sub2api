package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

type liveBalanceWatermarkInitializer interface {
	InitializeLiveBalanceAtWatermark(
		ctx context.Context,
		userID int64,
		balance float64,
		watermark int64,
	) (LiveBalanceResult, error)
}

// LiveBalanceAdjustmentRecoveryStats reports how often the outbox applier had to
// repair the Redis projection instead of applying a delta directly.
type LiveBalanceAdjustmentRecoveryStats struct {
	Healed       uint64
	HealFailures uint64
	Abandoned    uint64
}

// recoveringLiveBalanceAdjustmentApplier repairs a missing Redis wallet from one
// authoritative PostgreSQL snapshot before replaying the claimed event, and
// re-anchors a stale or broken outbox watermark instead of retrying forever.
//
// The atomic initializer never replaces an existing wallet, so a concurrent
// request authorization or worker cannot lose a hold or a newer adjustment. When
// the watermark itself conflicts (lost keys, a Redis restore from an older
// snapshot, a chain break) the healer rebuilds wallet and watermark from the same
// database snapshot, guarded by a Redis compare-and-swap so a concurrent writer
// aborts the repair rather than being overwritten.
type recoveringLiveBalanceAdjustmentApplier struct {
	cache          *BillingCacheService
	snapshotReader balancePreauthorizationSnapshotReader
	initializer    liveBalanceWatermarkInitializer
	healer         liveBalanceWatermarkHealer

	healed       atomic.Uint64
	healFailures atomic.Uint64
	abandoned    atomic.Uint64
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
		applier.healer, _ = cache.cache.(liveBalanceWatermarkHealer)
	}
	return applier
}

func (a *recoveringLiveBalanceAdjustmentApplier) RecoveryStats() LiveBalanceAdjustmentRecoveryStats {
	if a == nil {
		return LiveBalanceAdjustmentRecoveryStats{}
	}
	return LiveBalanceAdjustmentRecoveryStats{
		Healed:       a.healed.Load(),
		HealFailures: a.healFailures.Load(),
		Abandoned:    a.abandoned.Load(),
	}
}

func (a *recoveringLiveBalanceAdjustmentApplier) ApplyExternalBalanceOutboxAdjustment(
	ctx context.Context,
	event LiveBalanceAdjustmentEvent,
) error {
	if a == nil || a.cache == nil {
		return errors.New("live balance adjustment applier is unavailable")
	}
	err := a.cache.ApplyExternalBalanceOutboxAdjustment(ctx, event)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errLiveBalanceWalletNotInitialized):
		return a.recoverMissingWallet(ctx, event.UserID, err, func() error {
			return a.cache.ApplyExternalBalanceOutboxAdjustment(ctx, event)
		})
	case errors.Is(err, errLiveBalanceWatermarkConflict):
		return a.healWatermarkConflict(ctx, event.UserID, err, func() error {
			return a.cache.ApplyExternalBalanceOutboxAdjustment(ctx, event)
		})
	default:
		return err
	}
}

func (a *recoveringLiveBalanceAdjustmentApplier) ApplyExternalBalanceOutboxAdjustments(
	ctx context.Context, events []LiveBalanceAdjustmentEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	if a == nil || a.cache == nil {
		return errors.New("live balance adjustment applier is unavailable")
	}
	replay := func() error {
		return a.cache.ApplyExternalBalanceOutboxAdjustments(ctx, events)
	}
	err := replay()
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errLiveBalanceWalletNotInitialized):
		return a.recoverMissingWallet(ctx, events[0].UserID, err, replay)
	case errors.Is(err, errLiveBalanceWatermarkConflict):
		return a.healWatermarkConflict(ctx, events[0].UserID, err, replay)
	default:
		return err
	}
}

// recoverMissingWallet initializes both Redis keys from one database snapshot
// when neither exists yet. It is the cold-wallet path and never overwrites state.
func (a *recoveringLiveBalanceAdjustmentApplier) recoverMissingWallet(
	ctx context.Context,
	userID int64,
	cause error,
	replay func() error,
) error {
	if a.snapshotReader == nil || a.initializer == nil {
		return fmt.Errorf("recover missing live balance wallet: recovery dependencies are unavailable: %w", cause)
	}
	snapshot, err := a.snapshotReader.LoadLiveBalanceInitializationSnapshot(ctx, userID)
	if err != nil {
		return a.wrapSnapshotError("load live balance recovery snapshot", userID, err)
	}
	if snapshot.HasUnsettled {
		return fmt.Errorf("recover missing live balance wallet: user_id=%d has unsettled billing: %w", userID, cause)
	}

	result, err := a.initializer.InitializeLiveBalanceAtWatermark(
		ctx,
		userID,
		snapshot.Balance,
		snapshot.Watermark,
	)
	if err != nil {
		return fmt.Errorf("initialize live balance recovery snapshot: %w", err)
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent:
	case LiveBalanceOutcomeConflict:
		a.healFailures.Add(1)
		return fmt.Errorf("initialize live balance recovery snapshot conflict: user_id=%d", userID)
	default:
		return fmt.Errorf("unexpected live balance recovery outcome: %d", result.Outcome)
	}

	if err := replay(); err != nil {
		return fmt.Errorf("replay live balance adjustment after recovery: %w", err)
	}
	return nil
}

// healWatermarkConflict re-anchors the Redis projection at the database
// snapshot head and replays the claimed chain. Every event at or below that head
// is already included in users.balance, so the replay of those rows is an
// idempotent no-op and cannot double charge or credit. A settlement hold that is
// not yet reflected in users.balance can never be freed this way: such repairs
// wait until the user has no unsettled billing.
func (a *recoveringLiveBalanceAdjustmentApplier) healWatermarkConflict(
	ctx context.Context,
	userID int64,
	cause error,
	replay func() error,
) error {
	if a.snapshotReader == nil || a.healer == nil {
		return fmt.Errorf("heal live balance watermark conflict: recovery dependencies are unavailable: %w", cause)
	}
	state, err := a.healer.LoadLiveBalanceWatermarkState(ctx, userID)
	if err != nil {
		a.healFailures.Add(1)
		return fmt.Errorf("load live balance watermark state: %w", err)
	}
	snapshot, err := a.snapshotReader.LoadLiveBalanceInitializationSnapshot(ctx, userID)
	if err != nil {
		return a.wrapSnapshotError("load live balance watermark snapshot", userID, err)
	}
	if snapshot.HasUnsettled {
		a.healFailures.Add(1)
		return fmt.Errorf("heal live balance watermark conflict: user_id=%d has unsettled billing: %w", userID, cause)
	}

	result, err := a.healer.RebuildLiveBalanceAtWatermark(
		ctx,
		userID,
		state,
		snapshot.Balance,
		snapshot.Watermark,
	)
	if err != nil {
		a.healFailures.Add(1)
		return fmt.Errorf("rebuild live balance watermark: %w", err)
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent:
	default:
		// The compare-and-swap observed a concurrent wallet change. Re-reading the
		// state on the next attempt lets the other writer finish first.
		a.healFailures.Add(1)
		return fmt.Errorf("rebuild live balance watermark conflict: user_id=%d: %w", userID, cause)
	}
	a.healed.Add(1)

	if err := replay(); err != nil {
		return fmt.Errorf("replay live balance adjustment after watermark rebuild: %w", err)
	}
	return nil
}

// wrapSnapshotError turns a vanished user into a terminal skip signal: the row
// can never be projected and retrying it forever would jam the user's chain.
func (a *recoveringLiveBalanceAdjustmentApplier) wrapSnapshotError(
	operation string,
	userID int64,
	err error,
) error {
	if errors.Is(err, ErrUserNotFound) {
		a.abandoned.Add(1)
		return fmt.Errorf("%w: user_id=%d", errLiveBalanceAdjustmentUserGone, userID)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
