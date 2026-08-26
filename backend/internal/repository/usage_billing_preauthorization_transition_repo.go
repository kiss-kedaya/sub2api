package repository

import (
	"context"
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
		SET status = CASE WHEN status IN ($4, $5) THEN status ELSE $3 END,
			wallet_preapplied = CASE WHEN status = $6 THEN TRUE ELSE wallet_preapplied END,
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
