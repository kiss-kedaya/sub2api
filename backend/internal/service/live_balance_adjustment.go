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

var errLiveBalanceWalletNotInitialized = errors.New("live balance wallet is not initialized")

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
	if err := s.cache.InvalidateUserBalance(ctx, event.UserID); err != nil {
		return fmt.Errorf("invalidate legacy balance cache: %w", err)
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
			"live balance outbox watermark conflict: user_id=%d event_id=%d predecessor_id=%d",
			event.UserID,
			event.ID,
			event.PredecessorID,
		)
	default:
		return fmt.Errorf("unexpected live balance outbox outcome: %d", result.Outcome)
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
