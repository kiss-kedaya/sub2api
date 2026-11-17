//go:build integration

package repository

import (
	"context"
	"encoding/json"
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

func TestLiveBalanceAdjustmentOutboxClaimPlanSurvivesEmptyPendingStats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	// A pinned session keeps the fixture isolated from the migrated public tables.
	_, err = conn.ExecContext(ctx, `
		CREATE TEMP TABLE live_balance_adjustment_heads
			(LIKE public.live_balance_adjustment_heads INCLUDING ALL);
		CREATE TEMP TABLE live_balance_adjustment_outbox
			(LIKE public.live_balance_adjustment_outbox INCLUDING DEFAULTS INCLUDING CONSTRAINTS);
		ALTER TABLE pg_temp.live_balance_adjustment_outbox ADD PRIMARY KEY (id);
		CREATE INDEX idx_live_balance_adjustment_outbox_pending
			ON pg_temp.live_balance_adjustment_outbox (available_at, id) WHERE delivered_at IS NULL;
		CREATE INDEX idx_live_balance_adjustment_outbox_user_order
			ON pg_temp.live_balance_adjustment_outbox (user_id, id) WHERE delivered_at IS NULL;
		INSERT INTO pg_temp.live_balance_adjustment_heads (user_id, last_event_id)
			SELECT u, u * 50 FROM generate_series(1, 4000) AS u;
		INSERT INTO pg_temp.live_balance_adjustment_outbox (id, user_id, predecessor_id, delta, delivered_at)
			SELECT i, (i - 1) / 50 + 1, CASE WHEN (i - 1) % 50 = 0 THEN 0 ELSE i - 1 END, 0.01, NOW()
			FROM generate_series(1, 200000) AS i;
		ANALYZE pg_temp.live_balance_adjustment_heads;
		ANALYZE pg_temp.live_balance_adjustment_outbox;
	`)
	require.NoError(t, err)
	defer func() {
		_, err := conn.ExecContext(context.Background(),
			"DROP TABLE pg_temp.live_balance_adjustment_outbox, pg_temp.live_balance_adjustment_heads")
		require.NoError(t, err)
	}()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO pg_temp.live_balance_adjustment_outbox
			(id, user_id, predecessor_id, delta, available_at)
			SELECT 200000 + (u - 1) * 256 + n, u,
				CASE WHEN n = 1 THEN u * 50 ELSE 200000 + (u - 1) * 256 + n - 1 END,
				0.01, TIMESTAMPTZ '2000-01-01'
			FROM generate_series(1, 256) AS u CROSS JOIN generate_series(1, 256) AS n;
		UPDATE pg_temp.live_balance_adjustment_heads SET last_event_id = 200000 + user_id * 256
			WHERE user_id <= 256;
		-- Block a pending predecessor, a delayed head, and a live lease.
		UPDATE pg_temp.live_balance_adjustment_outbox SET predecessor_id = 200257 WHERE id = 200001;
		UPDATE pg_temp.live_balance_adjustment_outbox SET available_at = NOW() + INTERVAL '1 hour' WHERE id = 200257;
		UPDATE pg_temp.live_balance_adjustment_outbox SET claimed_at = NOW(), claimed_by = 'leased' WHERE id = 200513;
		-- An expired lease, a pruned predecessor, and a root must remain eligible.
		UPDATE pg_temp.live_balance_adjustment_outbox SET claimed_at = NOW() - INTERVAL '1 minute' WHERE id = 200769;
		UPDATE pg_temp.live_balance_adjustment_outbox SET predecessor_id = 999999 WHERE id = 201025;
		UPDATE pg_temp.live_balance_adjustment_outbox SET predecessor_id = 0 WHERE id = 201281;
	`)
	require.NoError(t, err)
	var nullFraction float64
	require.NoError(t, conn.QueryRowContext(ctx, `
		SELECT null_frac FROM pg_stats
		WHERE schemaname = (SELECT nspname FROM pg_namespace WHERE oid = pg_my_temp_schema())
			AND tablename = 'live_balance_adjustment_outbox' AND attname = 'delivered_at'
	`).Scan(&nullFraction))
	require.Zero(t, nullFraction, "pending rows must remain invisible to the sampled statistics")
	var sortBefore, jitBefore string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT current_setting('enable_sort'), current_setting('jit')").
		Scan(&sortBefore, &jitBefore))

	for _, name := range []string{"empty_pending_stats", "fresh_pending_stats"} {
		t.Run(name, func(t *testing.T) {
			if name == "fresh_pending_stats" {
				_, err := conn.ExecContext(ctx, "ANALYZE pg_temp.live_balance_adjustment_outbox")
				require.NoError(t, err)
			}
			claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			tx, err := conn.BeginTx(claimCtx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			_, err = tx.ExecContext(claimCtx, liveBalanceOutboxClaimPlannerSQL)
			require.NoError(t, err)
			var encoded []byte
			started := time.Now()
			require.NoError(t, tx.QueryRowContext(claimCtx,
				"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+liveBalanceOutboxClaimSQL,
				"plan-test", service.LiveBalanceOutboxMaxBatchSize, int64(30), service.LiveBalanceOutboxPerUserMaxBatchSize).
				Scan(&encoded))
			require.Less(t, time.Since(started), 5*time.Second)
			var plans []map[string]any
			require.NoError(t, json.Unmarshal(encoded, &plans))
			require.Len(t, plans, 1)
			t.Logf("execution_ms=%v planning_ms=%v", plans[0]["Execution Time"], plans[0]["Planning Time"])
			require.NotContains(t, plans[0], "JIT", "disabled sorts must not trigger JIT compilation")
			userProbes, predecessorProbes := 0, 0
			var visit func(map[string]any)
			visit = func(node map[string]any) {
				require.NotEqual(t, "idx_live_balance_adjustment_outbox_pending", node["Index Name"], string(encoded))
				if node["Index Name"] == "idx_live_balance_adjustment_outbox_user_order" {
					userProbes++
					require.Contains(t, fmt.Sprint(node["Index Cond"]), "user_id", string(encoded))
				}
				if node["Alias"] == "predecessor" {
					predecessorProbes++
					require.Equal(t, "live_balance_adjustment_outbox_pkey", node["Index Name"], string(encoded))
				}
				if children, ok := node["Plans"].([]any); ok {
					for _, child := range children {
						visit(child.(map[string]any))
					}
				}
			}
			plan := plans[0]["Plan"].(map[string]any)
			visit(plan)
			require.Equal(t, 2, userProbes, "both LATERAL lookups must be keyed by user")
			require.Equal(t, 1, predecessorProbes)
			require.Equal(t, float64(service.LiveBalanceOutboxMaxBatchSize), plan["Actual Rows"])
			rows, err := tx.QueryContext(claimCtx, `
				SELECT user_id, MIN(id), MAX(id), COUNT(*)
				FROM pg_temp.live_balance_adjustment_outbox WHERE claimed_by = 'plan-test'
				GROUP BY user_id ORDER BY user_id
			`)
			require.NoError(t, err)
			users := 0
			for rows.Next() {
				var userID, firstID, lastID, count int64
				require.NoError(t, rows.Scan(&userID, &firstID, &lastID, &count))
				require.Equal(t, int64(4+users), userID, "blocked users must not be claimed")
				require.Equal(t, int64(128), count, "round-robin batch must retain eight contiguous prefixes")
				require.Equal(t, 200000+(userID-1)*256+1, firstID)
				require.Equal(t, firstID+count-1, lastID)
				users++
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			require.Equal(t, 8, users)
			if name == "fresh_pending_stats" {
				require.NoError(t, tx.Commit())
			} else {
				require.NoError(t, tx.Rollback())
			}
			var sortAfter, jitAfter string
			require.NoError(t, conn.QueryRowContext(ctx, "SELECT current_setting('enable_sort'), current_setting('jit')").
				Scan(&sortAfter, &jitAfter))
			require.Equal(t, sortBefore, sortAfter, "planner settings must not escape the transaction")
			require.Equal(t, jitBefore, jitAfter)
		})
	}
}
