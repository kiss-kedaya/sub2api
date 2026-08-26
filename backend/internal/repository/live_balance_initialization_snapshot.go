package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// LoadLiveBalanceInitializationSnapshot reads the authoritative database
// balance and the latest external adjustment head in one PostgreSQL statement.
// That single MVCC snapshot is the boundary between deltas already included in
// fallback balance and later deltas the Redis outbox worker must apply.
func (r *usageBillingRepository) LoadLiveBalanceInitializationSnapshot(
	ctx context.Context,
	userID int64,
) (service.LiveBalanceInitializationSnapshot, error) {
	var snapshot service.LiveBalanceInitializationSnapshot
	if r == nil || r.db == nil {
		return snapshot, errors.New("usage billing repository db is nil")
	}
	if userID <= 0 {
		return snapshot, service.ErrUserNotFound
	}

	var balanceText string
	err := r.db.QueryRowContext(ctx, `
		SELECT u.balance::text,
			COALESCE(h.last_event_id, 0),
			EXISTS (
				SELECT 1
				FROM billing_balance_settlements AS pending
				WHERE pending.user_id = u.id
					AND pending.status IN ($2, $3, $4)
			)
		FROM users AS u
		LEFT JOIN live_balance_adjustment_heads AS h ON h.user_id = u.id
		WHERE u.id = $1 AND u.deleted_at IS NULL
	`, userID,
		service.BalanceSettlementAuthorized,
		service.BalanceSettlementFinalizationPending,
		service.BalanceSettlementPending,
	).Scan(&balanceText, &snapshot.Watermark, &snapshot.HasUnsettled)
	if errors.Is(err, sql.ErrNoRows) {
		return snapshot, service.ErrUserNotFound
	}
	if err != nil {
		return snapshot, err
	}
	balance, err := decimal.NewFromString(balanceText)
	if err != nil {
		return snapshot, fmt.Errorf("parse live balance initialization snapshot: %w", err)
	}
	snapshot.Balance, _ = balance.Float64()
	return snapshot, nil
}
