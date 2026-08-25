//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLiveBalanceAdjustmentTriggerEnqueuesExternalDeltaAndSkipsUsage(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:       fmt.Sprintf("live-balance-outbox-%d@example.com", time.Now().UnixNano()),
		Balance:     10,
		Concurrency: 1,
	})
	clear := func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM live_balance_adjustment_outbox WHERE user_id = $1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM live_balance_adjustment_heads WHERE user_id = $1", user.ID)
		require.NoError(t, err)
	}
	clear()
	t.Cleanup(func() {
		clear()
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", user.ID)
		require.NoError(t, err)
	})

	_, err := integrationDB.ExecContext(ctx, "UPDATE users SET balance = balance + 1.25 WHERE id = $1", user.ID)
	require.NoError(t, err)
	var count int
	var delta float64
	var eventID, predecessorID, headID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(delta), 0), MAX(id), MAX(predecessor_id)
		FROM live_balance_adjustment_outbox
		WHERE user_id = $1
	`, user.ID).Scan(&count, &delta, &eventID, &predecessorID))
	require.Equal(t, 1, count)
	require.InDelta(t, 1.25, delta, 1e-8)
	require.Zero(t, predecessorID)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT last_event_id FROM live_balance_adjustment_heads WHERE user_id = $1", user.ID).Scan(&headID))
	require.Equal(t, eventID, headID)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, skipLiveBalanceAdjustmentOutbox(ctx, tx))
	_, err = tx.ExecContext(ctx, "UPDATE users SET balance = balance - 0.50 WHERE id = $1", user.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM live_balance_adjustment_outbox WHERE user_id = $1", user.ID).Scan(&count))
	require.Equal(t, 1, count, "usage-owned balance updates must not enqueue a second Redis delta")
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT last_event_id FROM live_balance_adjustment_heads WHERE user_id = $1", user.ID).Scan(&headID))
	require.Equal(t, eventID, headID, "usage catch-up must not advance the external wallet watermark")
}
