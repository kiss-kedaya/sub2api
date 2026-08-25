package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const defaultBalancePreauthorizationTTL = 6 * time.Hour

func (r *usageBillingRepository) PrepareBalancePreauthorization(
	ctx context.Context,
	cmd *service.BalancePreauthorizationCommand,
) (*service.BalancePreauthorizationRecord, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	if cmd == nil {
		return nil, errors.New("balance preauthorization command is nil")
	}
	requestID := strings.TrimSpace(cmd.RequestID)
	fingerprint := strings.TrimSpace(cmd.AuthorizationFingerprint)
	holdAmount := service.QuantizeUsageBillingAmount(cmd.HoldAmount)
	if requestID == "" || fingerprint == "" || cmd.APIKeyID <= 0 || cmd.UserID <= 0 ||
		holdAmount < 0 || math.IsNaN(holdAmount) || math.IsInf(holdAmount, 0) {
		return nil, service.ErrInvalidBillingPreauthorizationEstimate
	}
	expiresAt := cmd.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = time.Now().Add(defaultBalancePreauthorizationTTL)
	} else if !expiresAt.After(time.Now()) {
		return nil, service.ErrInvalidBillingPreauthorizationEstimate
	}

	record := &service.BalancePreauthorizationRecord{}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO billing_balance_settlements (
			request_id,
			api_key_id,
			request_fingerprint,
			authorization_fingerprint,
			user_id,
			amount_usd,
			hold_usd,
			status,
			expires_at
		)
		SELECT $1, $2, '', $3, $4, 0, $5, $6, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		)
		AND NOT EXISTS (
			SELECT 1 FROM usage_billing_dedup_archive
			WHERE request_id = $1 AND api_key_id = $2
		)
		ON CONFLICT (request_id, api_key_id) DO UPDATE
		SET request_id = billing_balance_settlements.request_id
		WHERE billing_balance_settlements.authorization_fingerprint = EXCLUDED.authorization_fingerprint
			AND billing_balance_settlements.user_id = EXCLUDED.user_id
			AND billing_balance_settlements.hold_usd = EXCLUDED.hold_usd
			AND (
				billing_balance_settlements.status NOT IN ($8, $9)
				OR billing_balance_settlements.expires_at > NOW()
			)
		RETURNING request_id,
			api_key_id,
			user_id,
			request_fingerprint,
			authorization_fingerprint,
			hold_usd,
			amount_usd,
			status,
			expires_at,
			updated_at
	`, requestID, cmd.APIKeyID, fingerprint, cmd.UserID, holdAmount,
		service.BalanceSettlementPrepared,
		expiresAt,
		service.BalanceSettlementPrepared,
		service.BalanceSettlementAuthorized,
	).Scan(
		&record.RequestID,
		&record.APIKeyID,
		&record.UserID,
		&record.RequestFingerprint,
		&record.AuthorizationFingerprint,
		&record.HoldAmount,
		&record.Amount,
		&record.Status,
		&record.ExpiresAt,
		&record.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrUsageBillingRequestConflict
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}
