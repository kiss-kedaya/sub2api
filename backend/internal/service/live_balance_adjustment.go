package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	liveBalanceAdjustmentTimeout = 2 * time.Second
	liveBalanceAdjustmentRetries = 3
)

var liveBalanceAdjustmentFallbackSequence atomic.Uint64

var (
	errLiveBalanceWalletNotInitialized = errors.New("live balance wallet is not initialized")
	// errLiveBalanceWatermarkConflict marks a Redis-side outbox watermark that no
	// longer lines up with the database event chain (lost/rolled-back/evicted
	// watermark or wallet, historical manual edits, a chain break). It is
	// recoverable when the authoritative database snapshot says no settlement
	// hold is outstanding: the wallet is only a projection of users.balance.
	errLiveBalanceWatermarkConflict = errors.New("live balance outbox watermark conflict")
	// errLiveBalanceAdjustmentUserGone marks outbox rows that can never be
	// projected because the user no longer exists. Retrying them forever only
	// burns claims, so the worker abandons them after recording the reason.
	errLiveBalanceAdjustmentUserGone = errors.New("live balance outbox user is gone")
)

// LiveBalanceWatermarkState is the raw Redis projection state observed before a
// conflict repair. Comparing these exact strings inside the rebuild script makes
// the repair a compare-and-swap: a concurrent authorization or outbox write
// aborts the rebuild instead of overwriting a newer wallet.
type LiveBalanceWatermarkState struct {
	WalletValue     string
	WalletExists    bool
	WatermarkValue  string
	WatermarkExists bool
}

type liveBalanceWatermarkHealer interface {
	LoadLiveBalanceWatermarkState(ctx context.Context, userID int64) (LiveBalanceWatermarkState, error)
	RebuildLiveBalanceAtWatermark(
		ctx context.Context,
		userID int64,
		expected LiveBalanceWatermarkState,
		balance float64,
		watermark int64,
	) (LiveBalanceResult, error)
}

// ApplyExternalBalanceAdjustment publishes one already-committed, non-usage
// balance delta to the persistent Redis wallet. The legacy short-lived balance
// cache is invalidated first so a later wallet initialization does not reuse a
// stale pre-adjustment value.
func (s *BillingCacheService) ApplyExternalBalanceAdjustment(ctx context.Context, userID int64, eventID string, delta float64) error {
	if delta == 0 {
		return nil
	}
	if s == nil || s.cache == nil {
		return errors.New("live balance cache is unavailable")
	}
	return applyExternalBalanceAdjustmentToCache(ctx, s.cache, userID, eventID, delta)
}

type liveBalanceWatermarkedAdjustmentCache interface {
	AdjustLiveBalanceAtWatermark(
		ctx context.Context,
		userID int64,
		eventID string,
		eventWatermark int64,
		predecessorWatermark int64,
		deltaUnits int64,
	) (LiveBalanceResult, error)
}

type liveBalanceWatermarkedAdjustmentBatchCache interface {
	AdjustLiveBalanceAtWatermarkBatch(
		ctx context.Context,
		userID int64,
		events []LiveBalanceAdjustmentEvent,
	) (LiveBalanceResult, error)
}

func (s *BillingCacheService) mergeSettlementEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Billing.BalanceMergeSettlementEnabled
}

// shouldInvalidateLegacyBalanceForOutbox reports whether an outbox delivery
// should delete the legacy short-lived balance cache (billing:balance:{uid}).
//
// While merge settlement is enabled, users.balance lags behind unsettled rows
// in billing_balance_settlements. Deleting billing:balance:{uid} on negative
// debits emitted by settlement flushes or synchronous fallback would force the
// next request to reload the higher database balance and resurrect unsettled
// charges. Positive deltas (top-ups, refunds) still invalidate immediately;
// external negative adjustments (admin/refund deductions) already invalidate
// the legacy cache synchronously via syncCommittedLiveBalanceAdjustment.
func (s *BillingCacheService) shouldInvalidateLegacyBalanceForOutbox(events ...LiveBalanceAdjustmentEvent) bool {
	if !s.mergeSettlementEnabled() {
		return true
	}
	for _, event := range events {
		if event.DeltaUnits > 0 {
			return true
		}
	}
	return false
}

func (s *BillingCacheService) ApplyExternalBalanceOutboxAdjustment(
	ctx context.Context,
	event LiveBalanceAdjustmentEvent,
) error {
	if s == nil || s.cache == nil {
		return errors.New("live balance cache is unavailable")
	}
	cache, ok := s.cache.(liveBalanceWatermarkedAdjustmentCache)
	if !ok {
		return errors.New("live balance cache does not support outbox watermarks")
	}
	if s.shouldInvalidateLegacyBalanceForOutbox(event) {
		if err := s.cache.InvalidateUserBalance(ctx, event.UserID); err != nil {
			return fmt.Errorf("invalidate legacy balance cache: %w", err)
		}
	}
	result, err := cache.AdjustLiveBalanceAtWatermark(
		ctx,
		event.UserID,
		event.RedisEventID(),
		event.ID,
		event.PredecessorID,
		event.DeltaUnits,
	)
	if err != nil {
		return err
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent:
		return nil
	case LiveBalanceOutcomeNotFound:
		return fmt.Errorf("%w: user_id=%d", errLiveBalanceWalletNotInitialized, event.UserID)
	case LiveBalanceOutcomeConflict:
		return fmt.Errorf(
			"%w: user_id=%d event_id=%d predecessor_id=%d",
			errLiveBalanceWatermarkConflict,
			event.UserID,
			event.ID,
			event.PredecessorID,
		)
	default:
		return fmt.Errorf("unexpected live balance outbox outcome: %d", result.Outcome)
	}
}

// ApplyExternalBalanceOutboxAdjustments applies one user's contiguous outbox
// chain in a single Redis script. Database acknowledgements remain separate;
// a crash between Redis and PostgreSQL is safe because the wallet watermark
// makes replay idempotent.
func (s *BillingCacheService) ApplyExternalBalanceOutboxAdjustments(
	ctx context.Context,
	events []LiveBalanceAdjustmentEvent,
) error {
	if len(events) == 0 {
		return nil
	}
	if s == nil || s.cache == nil {
		return errors.New("live balance cache is unavailable")
	}
	userID := events[0].UserID
	if userID <= 0 {
		return errors.New("invalid live balance outbox batch user")
	}
	for i, event := range events {
		if event.UserID != userID || event.ID <= 0 || event.PredecessorID < 0 || event.DeltaUnits == 0 {
			return fmt.Errorf("invalid live balance outbox batch at index %d", i)
		}
		if i > 0 && (event.ID <= events[i-1].ID || event.PredecessorID != events[i-1].ID) {
			return fmt.Errorf("non-contiguous live balance outbox batch at index %d", i)
		}
	}
	cache, ok := s.cache.(liveBalanceWatermarkedAdjustmentBatchCache)
	if !ok {
		return errors.New("live balance cache does not support batched outbox watermarks")
	}
	if s.shouldInvalidateLegacyBalanceForOutbox(events...) {
		if err := s.cache.InvalidateUserBalance(ctx, userID); err != nil {
			return fmt.Errorf("invalidate legacy balance cache: %w", err)
		}
	}
	result, err := cache.AdjustLiveBalanceAtWatermarkBatch(ctx, userID, events)
	if err != nil {
		return err
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent:
		return nil
	case LiveBalanceOutcomeNotFound:
		return fmt.Errorf("%w: user_id=%d", errLiveBalanceWalletNotInitialized, userID)
	case LiveBalanceOutcomeConflict:
		return fmt.Errorf(
			"%w: user_id=%d first_event_id=%d last_event_id=%d",
			errLiveBalanceWatermarkConflict,
			userID,
			events[0].ID,
			events[len(events)-1].ID,
		)
	default:
		return fmt.Errorf("unexpected live balance batch outcome: %d", result.Outcome)
	}
}

func applyExternalBalanceAdjustmentToCache(ctx context.Context, cache BillingCache, userID int64, eventID string, delta float64) error {
	if cache == nil || delta == 0 {
		return nil
	}
	if userID <= 0 || strings.TrimSpace(eventID) == "" || math.IsNaN(delta) || math.IsInf(delta, 0) {
		return fmt.Errorf("invalid live balance adjustment: user_id=%d event_id_present=%t delta=%v", userID, strings.TrimSpace(eventID) != "", delta)
	}

	if err := cache.InvalidateUserBalance(ctx, userID); err != nil {
		return fmt.Errorf("invalidate legacy balance cache: %w", err)
	}
	result, err := cache.AdjustLiveBalance(ctx, userID, eventID, delta)
	if err != nil {
		return err
	}
	switch result.Outcome {
	case LiveBalanceOutcomeApplied, LiveBalanceOutcomeIdempotent, LiveBalanceOutcomeNotFound:
		return nil
	case LiveBalanceOutcomeConflict:
		return fmt.Errorf("live balance adjustment event conflict: user_id=%d event_id=%q", userID, eventID)
	default:
		return fmt.Errorf("unexpected live balance adjustment outcome: %d", result.Outcome)
	}
}

// syncCommittedLiveBalanceAdjustment only clears the legacy short-lived cache.
// The database trigger and durable outbox worker are the sole delivery path for
// persistent live-wallet deltas, preventing a request-thread/outbox double apply.
func syncCommittedLiveBalanceAdjustment(cache *BillingCacheService, userID int64, eventID string, delta float64) {
	if cache == nil || delta == 0 {
		return
	}
	syncCommittedLiveBalanceAdjustmentWith(
		func(ctx context.Context) error {
			return cache.InvalidateUserBalance(ctx, userID)
		},
		userID,
		eventID,
		delta,
	)
}

func syncCommittedRawLiveBalanceAdjustment(cache BillingCache, userID int64, eventID string, delta float64) {
	if cache == nil || delta == 0 {
		return
	}
	syncCommittedLiveBalanceAdjustmentWith(
		func(ctx context.Context) error {
			return cache.InvalidateUserBalance(ctx, userID)
		},
		userID,
		eventID,
		delta,
	)
}

func syncCommittedLiveBalanceAdjustmentWith(apply func(context.Context) error, userID int64, eventID string, delta float64) {
	ctx, cancel := context.WithTimeout(context.Background(), liveBalanceAdjustmentTimeout)
	defer cancel()

	var err error
	for attempt := 0; attempt < liveBalanceAdjustmentRetries; attempt++ {
		err = apply(ctx)
		if err == nil {
			return
		}
		if attempt+1 < liveBalanceAdjustmentRetries {
			delay := time.Duration(attempt+1) * 25 * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				attempt = liveBalanceAdjustmentRetries
			}
		}
	}
	logger.LegacyPrintf("service.billing_cache", "Warning: legacy balance cache invalidation failed; durable live-wallet delivery remains queued user_id=%d event_id=%q delta=%.8f err=%v", userID, eventID, delta, err)
}

func newLiveBalanceAdjustmentEventID(scope string) string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err == nil {
		return strings.TrimSpace(scope) + ":" + hex.EncodeToString(entropy[:])
	}
	sequence := liveBalanceAdjustmentFallbackSequence.Add(1)
	return fmt.Sprintf("%s:%d:%d", strings.TrimSpace(scope), time.Now().UnixNano(), sequence)
}
