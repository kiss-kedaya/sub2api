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

func TestLiveBalanceAdjustmentOutboxClaimReturnsContiguousPerUserPrefix(t *testing.T) {
	ctx := context.Background()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:   fmt.Sprintf("live-balance-outbox-batch-%d@example.com", time.Now().UnixNano()),
		Balance: 10, Concurrency: 1,
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
	for i := 1; i <= 4; i++ {
		_, err := integrationDB.ExecContext(ctx, "UPDATE users SET balance = balance + 0.01 WHERE id = $1", user.ID)
		require.NoError(t, err)
	}
	_, err := integrationDB.ExecContext(ctx, "UPDATE live_balance_adjustment_outbox SET available_at = TIMESTAMPTZ '2000-01-01' WHERE user_id = $1", user.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "UPDATE live_balance_adjustment_outbox SET claimed_at = NULL, claimed_by = NULL WHERE claimed_by = 'batch-test'")
		require.NoError(t, err)
	})
	claimed, err := NewLiveBalanceAdjustmentOutboxRepository(integrationDB).Claim(ctx, "batch-test", service.LiveBalanceOutboxMaxBatchSize, 30*time.Second)
	require.NoError(t, err)
	events := make([]service.LiveBalanceAdjustmentEvent, 0)
	for _, event := range claimed {
		if event.UserID == user.ID {
			events = append(events, event)
		}
	}
	require.Len(t, events, 4)
	for i, event := range events {
		require.Equal(t, user.ID, event.UserID)
		if i > 0 {
			require.Equal(t, events[i-1].ID, event.PredecessorID, "claimed page must be a gap-free per-user prefix")
		}
	}
	// A second worker must not steal any part of the leased prefix.
	other, err := NewLiveBalanceAdjustmentOutboxRepository(integrationDB).Claim(ctx, "other-batch-test", service.LiveBalanceOutboxMaxBatchSize, 30*time.Second)
	require.NoError(t, err)
	for _, event := range other {
		require.NotEqual(t, user.ID, event.UserID)
	}
	_, err = integrationDB.ExecContext(ctx, "UPDATE live_balance_adjustment_outbox SET claimed_at = NULL, claimed_by = NULL WHERE claimed_by IN ('batch-test','other-batch-test')")
	require.NoError(t, err)
	// A delayed middle event must stop the prefix, including all successors.
	_, err = integrationDB.ExecContext(ctx, "UPDATE live_balance_adjustment_outbox SET available_at = NOW() + INTERVAL '1 hour' WHERE id = $1", events[1].ID)
	require.NoError(t, err)
	claimed, err = NewLiveBalanceAdjustmentOutboxRepository(integrationDB).Claim(ctx, "batch-test", service.LiveBalanceOutboxMaxBatchSize, 30*time.Second)
	require.NoError(t, err)
	gotIDs := make([]int64, 0)
	for _, event := range claimed {
		if event.UserID == user.ID {
			gotIDs = append(gotIDs, event.ID)
		}
	}
	require.Equal(t, []int64{events[0].ID}, gotIDs)
	_, err = integrationDB.ExecContext(ctx, "UPDATE live_balance_adjustment_outbox SET available_at = TIMESTAMPTZ '2000-01-01', claimed_at = NULL, claimed_by = NULL WHERE user_id = $1", user.ID)
	require.NoError(t, err)
	cache := &billingCache{rdb: integrationRedis}
	t.Cleanup(func() {
		require.NoError(t, integrationRedis.Del(ctx, liveBalanceWalletKey(user.ID), liveBalanceWatermarkKey(user.ID)).Err())
	})
	_, err = cache.InitializeLiveBalanceAtWatermark(ctx, user.ID, 10, 0)
	require.NoError(t, err)
	result, err := cache.AdjustLiveBalanceAtWatermarkBatch(ctx, user.ID, events)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, result.Outcome)
	require.Equal(t, 10.04, result.AvailableBalance)
	replay, err := cache.AdjustLiveBalanceAtWatermarkBatch(ctx, user.ID, events[:2])
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, result.AvailableBalance, replay.AvailableBalance)
}
