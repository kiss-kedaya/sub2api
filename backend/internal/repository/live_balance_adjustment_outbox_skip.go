package repository

import (
	"context"
	"database/sql"
)

// skipLiveBalanceAdjustmentOutbox marks a usage-owned transaction. Usage holds
// and finalization already change the Redis live wallet before users.balance is
// caught up, so the generic balance trigger must not enqueue the same delta.
func skipLiveBalanceAdjustmentOutbox(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SET LOCAL sub2api.skip_live_balance_outbox = 'on'`)
	return err
}
