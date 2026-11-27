package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageBillingRepository) MarkBalancePreauthorizationAuthorized(ctx context.Context, requestID string, apiKeyID int64) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE WHEN status = $4 THEN status ELSE $3 END,
			updated_at = CASE WHEN status = $4 THEN updated_at ELSE NOW() END
		WHERE request_id = $1
			AND api_key_id = $2
			AND status IN ($5, $4)
			AND expires_at > NOW()
	`, requestID, apiKeyID,
		service.BalanceSettlementAuthorized,
		service.BalanceSettlementAuthorized,
		service.BalanceSettlementPrepared,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementAuthorized)
}

// AdvanceBalancePreauthorizationHold durably records the largest cumulative
// Redis reservation while the request is still authorized. GREATEST makes
// delayed/retried writes monotonic; the status predicate prevents a late
// top-up acknowledgement from crossing finalize or refund.
func (r *usageBillingRepository) AdvanceBalancePreauthorizationHold(
	ctx context.Context,
	requestID string,
	apiKeyID int64,
	holdAmount float64,
) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	holdAmount = service.QuantizeUsageBillingAmount(holdAmount)
	if holdAmount < 0 || math.IsNaN(holdAmount) || math.IsInf(holdAmount, 0) {
		return service.ErrInvalidBillingPreauthorizationEstimate
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET hold_usd = GREATEST(hold_usd, $3),
			updated_at = CASE WHEN hold_usd < $3 THEN NOW() ELSE updated_at END
		WHERE request_id = $1
			AND api_key_id = $2
			AND status = $4
	`, requestID, apiKeyID, holdAmount, service.BalanceSettlementAuthorized)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementAuthorized)
}

func (r *usageBillingRepository) BeginBalancePreauthorizationFinalization(
	ctx context.Context,
	requestID string,
	apiKeyID int64,
	amount float64,
	requestFingerprint string,
) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	amount = service.QuantizeUsageBillingAmount(amount)
	requestFingerprint = strings.TrimSpace(requestFingerprint)
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || requestFingerprint == "" {
		return service.ErrInvalidBillingPreauthorizationEstimate
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE WHEN status IN ($5, $7, $8, $9) THEN status ELSE $5 END,
			amount_usd = CASE WHEN status IN ($5, $7, $8, $9) THEN amount_usd ELSE $3 END,
			request_fingerprint = CASE WHEN status IN ($5, $7, $8, $9) THEN request_fingerprint ELSE $4 END,
			updated_at = CASE WHEN status IN ($5, $7, $8, $9) THEN updated_at ELSE NOW() END
		WHERE request_id = $1
			AND api_key_id = $2
			AND (
				status = $6
				OR (
					status IN ($5, $7, $8, $9)
					AND amount_usd = $3
					AND request_fingerprint = $4
				)
			)
	`, requestID, apiKeyID, amount, requestFingerprint,
		service.BalanceSettlementFinalizationPending,
		service.BalanceSettlementAuthorized,
		service.BalanceSettlementPending,
		service.BalanceSettlementApplied,
		service.BalanceSettlementRefunded,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementFinalizationPending)
}

func (r *usageBillingRepository) CompleteBalancePreauthorizationSettlement(ctx context.Context, requestID string, apiKeyID int64) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE
				WHEN status IN ($4, $5) THEN status
				WHEN request_fingerprint LIKE 'pg-direct-hold:%' THEN $5
				ELSE $3
			END,
			wallet_preapplied = CASE WHEN status = $6 THEN TRUE ELSE wallet_preapplied END,
			applied_at = CASE WHEN status = $6 AND request_fingerprint LIKE 'pg-direct-hold:%' THEN NOW() ELSE applied_at END,
			available_at = CASE WHEN status = $6 THEN NOW() ELSE available_at END,
			last_error = CASE WHEN status = $6 THEN NULL ELSE last_error END,
			updated_at = CASE WHEN status = $6 THEN NOW() ELSE updated_at END
		WHERE request_id = $1
			AND api_key_id = $2
			AND (
				(status = $6 AND amount_usd > 0 AND BTRIM(request_fingerprint) <> '')
				OR status IN ($4, $5)
			)
	`, requestID, apiKeyID,
		service.BalanceSettlementPending,
		service.BalanceSettlementPending,
		service.BalanceSettlementApplied,
		service.BalanceSettlementFinalizationPending,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementPending)
}

// advanceDirectPGHoldToFinalizationPending atomically binds a Redis live_wallet
// direct-PG hold into the caller's PostgreSQL balance-freeze transaction:
//  1. Sets sub2api.skip_live_balance_outbox = 'on' locally so the users.balance
//     decrement in the same transaction does not emit a duplicate negative delta
//     to live_balance_adjustment_outbox.
//  2. Moves the billing_balance_settlements row from Authorized ->
//     FinalizationPending with request_fingerprint = 'pg-direct-hold:' || requestID.
//     When CompleteBalancePreauthorizationSettlement subsequently runs (in-band
//     or via crash recovery), matching rows move directly to Applied instead of
//     Pending so FlushPendingBalanceSettlements never deducts PG twice.
func advanceDirectPGHoldToFinalizationPending(
	ctx context.Context,
	tx *sql.Tx,
	requestID string,
	apiKeyID int64,
	holdAmount float64,
) error {
	if tx == nil {
		return errors.New("usage billing direct PG hold transaction is nil")
	}
	requestID = strings.TrimSpace(requestID)
	holdAmount = service.QuantizeUsageBillingAmount(holdAmount)
	if requestID == "" || apiKeyID <= 0 {
		return service.ErrUsageBillingRequestIDRequired
	}
	if holdAmount <= 0 || math.IsNaN(holdAmount) || math.IsInf(holdAmount, 0) {
		return service.ErrInvalidBillingPreauthorizationEstimate
	}
	if err := skipLiveBalanceAdjustmentOutbox(ctx, tx); err != nil {
		return err
	}
	fingerprint := service.BalancePreauthorizationDirectPGHoldFingerprintPrefix + requestID
	result, err := tx.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE WHEN status IN ($5, $6) THEN status ELSE $5 END,
			amount_usd = CASE WHEN status IN ($5, $6) THEN amount_usd ELSE $3 END,
			request_fingerprint = CASE WHEN status IN ($5, $6) THEN request_fingerprint ELSE $4 END,
			updated_at = CASE WHEN status IN ($5, $6) THEN updated_at ELSE NOW() END
		WHERE request_id = $1
			AND api_key_id = $2
			AND (
				status = $7
				OR (
					status IN ($5, $6)
					AND amount_usd = $3
					AND request_fingerprint = $4
				)
			)
	`, requestID, apiKeyID, holdAmount, fingerprint,
		service.BalanceSettlementFinalizationPending,
		service.BalanceSettlementApplied,
		service.BalanceSettlementAuthorized,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementFinalizationPending)
}

func (r *usageBillingRepository) BeginBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE WHEN status IN ($4, $5) THEN status ELSE $3 END,
			amount_usd = CASE WHEN status IN ($4, $5) THEN amount_usd ELSE 0 END,
			updated_at = CASE WHEN status IN ($4, $5) THEN updated_at ELSE NOW() END
		WHERE request_id = $1
			AND api_key_id = $2
			AND (
				status IN ($6, $7)
				OR (status = $4 AND amount_usd = 0)
				OR status = $5
			)
	`, requestID, apiKeyID,
		service.BalanceSettlementFinalizationPending,
		service.BalanceSettlementFinalizationPending,
		service.BalanceSettlementRefunded,
		service.BalanceSettlementPrepared,
		service.BalanceSettlementAuthorized,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementFinalizationPending)
}

func (r *usageBillingRepository) CompleteBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error {
	requestID, err := r.validateBalancePreauthorizationIdentity(requestID, apiKeyID)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET status = CASE WHEN status = $4 THEN status ELSE $3 END,
			applied_at = CASE WHEN status = $4 THEN applied_at ELSE NOW() END,
			updated_at = CASE WHEN status = $4 THEN updated_at ELSE NOW() END
		WHERE request_id = $1
			AND api_key_id = $2
			AND ((status = $5 AND amount_usd = 0) OR status = $4)
	`, requestID, apiKeyID,
		service.BalanceSettlementRefunded,
		service.BalanceSettlementRefunded,
		service.BalanceSettlementFinalizationPending,
	)
	return balancePreauthorizationTransitionResult(result, err, service.BalanceSettlementRefunded)
}

func (r *usageBillingRepository) validateBalancePreauthorizationIdentity(requestID string, apiKeyID int64) (string, error) {
	if r == nil || r.db == nil {
		return "", errors.New("usage billing repository db is nil")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || apiKeyID <= 0 {
		return "", service.ErrUsageBillingRequestIDRequired
	}
	return requestID, nil
}

type balanceTransitionRowsAffected interface {
	RowsAffected() (int64, error)
}

func balancePreauthorizationTransitionResult(result balanceTransitionRowsAffected, err error, target int16) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("balance preauthorization transition %d: %w", target, service.ErrUsageBillingRequestConflict)
	}
	return nil
}
