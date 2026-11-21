package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	// DefaultPaymentFulfillmentRecoveryThreshold is how long an order may stay in
	// PAID / RECHARGING / FAILED after a confirmed payment before the sweeper
	// retries fulfillment. It is intentionally larger than a healthy fulfillment
	// (milliseconds) and small enough that a stuck order is rescued long before a
	// user complains.
	DefaultPaymentFulfillmentRecoveryThreshold = 2 * time.Minute

	// DefaultPaymentFulfillmentRecoveryMaxAttempts caps automatic recovery
	// retries per order. After this many recorded failures the order is left for a
	// human (the admin retry endpoint still works) instead of being retried forever.
	DefaultPaymentFulfillmentRecoveryMaxAttempts = 5

	// paymentFulfillmentRecoveredAuditAction is written once when the sweeper
	// finishes a stuck order and carries the original status and timings.
	paymentFulfillmentRecoveredAuditAction = "ORDER_FULFILLMENT_RECOVERED"
	// paymentFulfillmentRecoveryFailureAuditAction stores the recovery attempt
	// counter in a single audit row (payment_audit_logs has a unique
	// (order_id, action) index, so the row is updated instead of appended).
	paymentFulfillmentRecoveryFailureAuditAction = "FULFILLMENT_RECOVERY_FAILED"

	// paymentFulfillmentRecoveryBatchLimit bounds one sweep: each order may run a
	// full fulfillment (balance credit, redeem code, subscription assignment).
	paymentFulfillmentRecoveryBatchLimit = 20
	// paymentFulfillmentRecoveryPassTimeout bounds the whole sweep pass so the
	// periodic runner's leader lock is never held far past its TTL.
	paymentFulfillmentRecoveryPassTimeout = 60 * time.Second
)

// PaymentFulfillmentRecoveryStats is the per-sweep observability record. It is
// logged by the sweeper and returned to callers/tests.
type PaymentFulfillmentRecoveryStats struct {
	// Scanned is the number of stuck orders considered in this pass.
	Scanned int
	// Recovered is the number of orders the sweep moved to COMPLETED.
	Recovered int
	// Failed is the number of orders whose retry returned an error.
	Failed int
	// SkippedLeased is the number of orders another worker still owns (lease) or
	// that completed concurrently.
	SkippedLeased int
	// SkippedMaxAttempts is the number of orders that hit the retry cap.
	SkippedMaxAttempts int
	// SkippedCrypto is the number of crypto deposits left to the on-chain ledger.
	SkippedCrypto int
}

// RecoverStuckFulfillments retries fulfillment for orders that were paid but did
// not reach COMPLETED: they may be stuck in PAID (the PAID transition committed
// but fulfillment never ran), RECHARGING (a worker crashed / its transaction was
// interrupted) or FAILED (fulfillment failed and no callback is coming anymore).
//
// Safety:
//   - Re-running executeFulfillment is idempotent: the CAS lease
//     (acquirePaymentFulfillmentLease) admits a single worker, markCompleted is
//     CAS-guarded on the lease version and order 100%-completed orders are
//     excluded by the query.
//   - Balance credit reuses the order's deterministic recharge code, so an
//     already-issued / already-used code is never credited twice.
//   - Automatic retries are capped per order via a persisted attempt counter.
//
// A successful recovery writes an ORDER_FULFILLMENT_RECOVERED audit entry with
// the original status and timing, and the sweep logs per-order plus aggregate
// counters.
func (s *PaymentService) RecoverStuckFulfillments(ctx context.Context, threshold time.Duration, maxAttempts int) (PaymentFulfillmentRecoveryStats, error) {
	var stats PaymentFulfillmentRecoveryStats
	if s == nil || s.entClient == nil {
		return stats, errors.New("payment service is unavailable")
	}
	if threshold <= 0 {
		threshold = DefaultPaymentFulfillmentRecoveryThreshold
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultPaymentFulfillmentRecoveryMaxAttempts
	}

	// The sweep is driven by a background ticker; detach it from the caller while
	// keeping one bounded budget for the whole pass.
	sweepCtx, cancel := detachPaymentMoneyContext(ctx, paymentFulfillmentRecoveryPassTimeout)
	defer cancel()

	staleBefore := time.Now().Add(-threshold)
	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.PaidAtNotNil(),
			paymentorder.StatusIn(OrderStatusPaid, OrderStatusRecharging, OrderStatusFailed),
			paymentorder.UpdatedAtLTE(staleBefore),
		).
		Order(dbent.Asc(paymentorder.FieldUpdatedAt)).
		Limit(paymentFulfillmentRecoveryBatchLimit).
		All(sweepCtx)
	if err != nil {
		return stats, fmt.Errorf("query stuck fulfillment orders: %w", err)
	}

	for _, o := range orders {
		if sweepCtx.Err() != nil {
			// Budget exhausted: the rest is picked up by the next tick.
			break
		}
		stats.Scanned++

		// Crypto deposits are settled only by the protected chain scanner/ledger;
		// the generic fulfillment path must not credit them (same rule as the
		// admin RetryFulfillment endpoint).
		if isCryptoDepositOrder(o) {
			stats.SkippedCrypto++
			continue
		}

		attempts, attemptsErr := s.paymentFulfillmentRecoveryAttempts(sweepCtx, o.ID)
		if attemptsErr != nil {
			stats.Failed++
			slog.Error("[PaymentRecovery] read recovery attempts failed", "orderID", o.ID, "error", attemptsErr)
			continue
		}
		if attempts >= maxAttempts {
			stats.SkippedMaxAttempts++
			continue
		}

		originalStatus := o.Status
		stuckFor := time.Since(o.UpdatedAt)
		startedAt := time.Now()
		fulfillErr := s.executeFulfillment(sweepCtx, o.ID)
		if fulfillErr != nil {
			if infraerrors.IsConflict(fulfillErr) {
				// Another worker currently owns the fulfillment lease.
				stats.SkippedLeased++
				continue
			}
			stats.Failed++
			nextAttempt := attempts + 1
			s.recordPaymentFulfillmentRecoveryFailure(sweepCtx, o.ID, nextAttempt, fulfillErr)
			slog.Error("[PaymentRecovery] retry fulfillment failed",
				"orderID", o.ID,
				"attempt", nextAttempt,
				"maxAttempts", maxAttempts,
				"originalStatus", originalStatus,
				"error", psErrMsg(fulfillErr),
			)
			continue
		}

		recoveredOrder, reloadErr := s.entClient.PaymentOrder.Get(sweepCtx, o.ID)
		if reloadErr != nil {
			stats.Failed++
			slog.Error("[PaymentRecovery] reload recovered order failed", "orderID", o.ID, "error", reloadErr)
			continue
		}
		if recoveredOrder.Status != OrderStatusCompleted {
			// The lease moved to a peer (or the order was re-armed) mid-pass; that
			// worker owns the outcome, so do not claim a recovery.
			stats.SkippedLeased++
			continue
		}

		stats.Recovered++
		recoveryDuration := time.Since(startedAt)
		s.writeAuditLog(sweepCtx, o.ID, paymentFulfillmentRecoveredAuditAction, "system", map[string]any{
			"originalStatus":     originalStatus,
			"finalStatus":        recoveredOrder.Status,
			"stuckSeconds":       int64(stuckFor / time.Second),
			"recoveryDurationMs": recoveryDuration.Milliseconds(),
			"attempt":            attempts + 1,
			"maxAttempts":        maxAttempts,
			"thresholdSeconds":   int64(threshold / time.Second),
		})
		slog.Info("[PaymentRecovery] recovered stuck fulfillment",
			"orderID", o.ID,
			"originalStatus", originalStatus,
			"stuckSeconds", int64(stuckFor/time.Second),
			"recoveryDurationMs", recoveryDuration.Milliseconds(),
		)
	}

	slog.Info("[PaymentRecovery] fulfillment sweep finished",
		"scanned", stats.Scanned,
		"recovered", stats.Recovered,
		"failed", stats.Failed,
		"skippedLeased", stats.SkippedLeased,
		"skippedMaxAttempts", stats.SkippedMaxAttempts,
		"skippedCrypto", stats.SkippedCrypto,
	)
	if stats.SkippedMaxAttempts > 0 {
		slog.Warn("[PaymentRecovery] orders exhausted automatic recovery attempts; manual retry required",
			"count", stats.SkippedMaxAttempts,
			"maxAttempts", maxAttempts,
		)
	}

	return stats, nil
}

// paymentFulfillmentRecoveryAttempts returns the persisted number of failed
// automatic recovery attempts for an order.
func (s *PaymentService) paymentFulfillmentRecoveryAttempts(ctx context.Context, orderID int64) (int, error) {
	row, err := s.entClient.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionEQ(paymentFulfillmentRecoveryFailureAuditAction),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	var detail struct {
		Attempt int `json:"attempt"`
	}
	if err := json.Unmarshal([]byte(row.Detail), &detail); err != nil || detail.Attempt < 0 {
		return 0, nil
	}
	return detail.Attempt, nil
}

// recordPaymentFulfillmentRecoveryFailure persists (or updates) the single
// per-order recovery-failure row. The audit table has a unique
// (order_id, action) index, so the attempt counter lives in the row detail and
// the row is updated in place.
func (s *PaymentService) recordPaymentFulfillmentRecoveryFailure(ctx context.Context, orderID int64, attempt int, cause error) {
	detail, _ := json.Marshal(map[string]any{
		"attempt": attempt,
		"error":   psErrMsg(cause),
	})
	updated, err := s.entClient.PaymentAuditLog.Update().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)),
			paymentauditlog.ActionEQ(paymentFulfillmentRecoveryFailureAuditAction),
		).
		SetDetail(string(detail)).
		SetOperator("system").
		Save(ctx)
	if err != nil {
		slog.Error("[PaymentRecovery] update recovery attempt audit failed", "orderID", orderID, "error", err)
		return
	}
	if updated > 0 {
		return
	}
	_, err = s.entClient.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction(paymentFulfillmentRecoveryFailureAuditAction).
		SetDetail(string(detail)).
		SetOperator("system").
		Save(ctx)
	if err != nil && !dbent.IsConstraintError(err) {
		slog.Error("[PaymentRecovery] create recovery attempt audit failed", "orderID", orderID, "error", err)
	}
}
