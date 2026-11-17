//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupUsageRollupTriggerInvalidatesCascadedHistoricalDelete(t *testing.T) {
	for _, partitioned := range []bool{false, true} {
		name := "ordinary"
		if partitioned {
			name = "partitioned"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, partitioned)
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()

			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
				VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2020-01-02 08:00:00+08');
				UPDATE usage_group_rollup_state
				SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
				WHERE id = 1;
				DELETE FROM users WHERE id = 1;
			`)
			require.NoError(t, err)

			var closedBefore string
			err = tx.QueryRowContext(ctx, `
				SELECT closed_before::text
				FROM usage_group_rollup_state
				WHERE id = 1
			`).Scan(&closedBefore)
			require.NoError(t, err)
			require.Equal(t, "2020-01-02", closedBefore)
		})
	}
}

func TestGroupUsageRollupTriggerSerializesLateHistoricalInsertWithPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seedTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seedTx.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2020-01-02'
		WHERE id = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, seedTx.Commit())

	syncTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = syncTx.Rollback() }()
	var stateID int16
	require.NoError(t, syncTx.QueryRowContext(ctx, `
		SELECT id
		FROM usage_group_rollup_state
		WHERE id = 1
		FOR UPDATE
	`).Scan(&stateID))

	lateTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = lateTx.Rollback() }()
	var lateBackendPID int
	require.NoError(t, lateTx.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&lateBackendPID))

	insertResult := make(chan error, 1)
	go func() {
		_, insertErr := lateTx.ExecContext(ctx, `
			INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
			VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2020-01-02 09:00:00+08')
		`)
		insertResult <- insertErr
	}()

	blocked, err := waitForGroupUsageRollupStateLock(ctx, lateBackendPID, insertResult)
	if err != nil || !blocked {
		_ = syncTx.Rollback()
		_ = lateTx.Rollback()
		require.NoError(t, err)
		require.True(t, blocked, "迟到写入必须等待正在发布水位的事务")
	}

	_, err = syncTx.ExecContext(ctx, `
		UPDATE usage_group_rollup_state
		SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		WHERE id = 1
	`)
	require.NoError(t, err)
	require.NoError(t, syncTx.Commit())

	select {
	case err = <-insertResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("等待迟到写入完成超时")
	}
	require.NoError(t, lateTx.Commit())

	var closedBefore string
	err = integrationDB.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT closed_before::text FROM %s.usage_group_rollup_state WHERE id = 1",
		pq.QuoteIdentifier(schema),
	)).Scan(&closedBefore)
	require.NoError(t, err)
	require.Equal(t, "2020-01-02", closedBefore)
}

func TestGroupUsageRollupTriggerDoesNotBlockCurrentDayInsertDuringHistoricalPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seedTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	_, err := seedTx.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		WHERE id = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, seedTx.Commit())

	syncTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = syncTx.Rollback() }()
	var stateID int16
	require.NoError(t, syncTx.QueryRowContext(ctx, `
		SELECT id
		FROM usage_group_rollup_state
		WHERE id = 1
		FOR UPDATE
	`).Scan(&stateID))

	insertTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = insertTx.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, insertTx, "Asia/Shanghai"))

	insertResult := make(chan error, 1)
	go func() {
		_, insertErr := insertTx.ExecContext(ctx, `
			INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
				(1, 1, 10, 1.25, CURRENT_TIMESTAMP),
				(2, 1, 10, 1.25, CURRENT_TIMESTAMP + INTERVAL '1 day')
		`)
		insertResult <- insertErr
	}()

	select {
	case err = <-insertResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("current-day insert waited for the historical rollup watermark lock")
	}
	require.NoError(t, insertTx.Commit())
	require.NoError(t, syncTx.Rollback())

	var inserted int
	err = integrationDB.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT COUNT(*) FROM %s.usage_logs WHERE id = 1",
		pq.QuoteIdentifier(schema),
	)).Scan(&inserted)
	require.NoError(t, err)
	require.Equal(t, 1, inserted)
}

func TestGroupUsageRollupTriggerKeepsWatermarkForTodayInsert(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)

	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, tx, "Asia/Shanghai"))
	_, err := tx.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		WHERE id = 1;
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
		VALUES (1, 1, 10, 1.25, CURRENT_TIMESTAMP);
	`)
	require.NoError(t, err)

	var unchanged bool
	err = tx.QueryRowContext(ctx, `
		SELECT closed_before = (CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date
		FROM usage_group_rollup_state
		WHERE id = 1
	`).Scan(&unchanged)
	require.NoError(t, err)
	require.True(t, unchanged)
}

func TestGroupUsageRollupTriggerRetainsHistoricalInsertInvalidation(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	for _, tt := range []struct {
		name   string
		values string
		want   string
	}{
		{"retained_history", "(1, 1, 10, 1.25, '2020-01-05 12:00:00+08')", "2020-01-05"},
		{"retention_boundary", "(1, 1, 10, 1.25, '2020-01-02 12:00:00+08')", "2020-01-10"},
		{"expired_history", "(1, 1, 10, 1.25, '2020-01-01 12:00:00+08')", "2020-01-10"},
		{"mixed_expired_and_retained", "(1, 1, 10, 1.25, '2020-01-01 12:00:00+08'), (2, 1, 10, 2.5, '2020-01-05 12:00:00+08')", "2020-01-05"},
		{"ungrouped_history", "(1, 1, NULL, 1.25, '2020-01-05 12:00:00+08')", "2020-01-10"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()
			require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, tx, "Asia/Shanghai"))
			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				UPDATE usage_group_rollup_state
				SET closed_before = DATE '2020-01-10',
					retained_from = TIMESTAMPTZ '2020-01-02 00:00:00+08'
				WHERE id = 1;
			`)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
				VALUES `+tt.values)
			require.NoError(t, err)
			var closedBefore string
			require.NoError(t, tx.QueryRowContext(ctx, `
				SELECT closed_before::text FROM usage_group_rollup_state WHERE id = 1
			`).Scan(&closedBefore))
			require.Equal(t, tt.want, closedBefore)
		})
	}
}

func TestGroupUsageRollupRowTriggerUsesSessionTimezoneAcrossDST(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	for _, operation := range []string{
		"DELETE FROM usage_logs WHERE id = 1",
		"UPDATE usage_logs SET actual_cost = 2.5 WHERE id = 1",
	} {
		t.Run(operation, func(t *testing.T) {
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()
			require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, tx, "America/New_York"))
			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
				VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2020-03-08 04:30:00+00');
				UPDATE usage_group_rollup_state
				SET closed_before = DATE '2020-03-09',
					retained_from = TIMESTAMPTZ '2020-03-01 00:00:00+00',
					timezone_name = 'America/New_York'
				WHERE id = 1;
			`)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, operation)
			require.NoError(t, err)
			var closedBefore string
			require.NoError(t, tx.QueryRowContext(ctx, `
				SELECT closed_before::text FROM usage_group_rollup_state WHERE id = 1
			`).Scan(&closedBefore))
			require.Equal(t, "2020-03-07", closedBefore)
		})
	}
}

func TestGroupUsageRollupTriggerUsesSessionTimezoneAcrossDST(t *testing.T) {
	ctx := context.Background()
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)

	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()
	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'America/New_York';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2026-03-09',
			timezone_name = 'America/New_York'
		WHERE id = 1;
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
		VALUES (1, 1, 10, 1.25, TIMESTAMPTZ '2026-03-08 04:30:00+00');
	`)
	require.NoError(t, err)

	var closedBefore string
	err = tx.QueryRowContext(ctx, `
		SELECT closed_before::text
		FROM usage_group_rollup_state
		WHERE id = 1
	`).Scan(&closedBefore)
	require.NoError(t, err)
	require.Equal(t, "2026-03-07", closedBefore)
}

func TestGroupUsageSummaryIncludesYesterdayAcrossWatermark(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	todayStart := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		closedBefore     string
		includeYesterday bool
	}{
		{name: "closed_rollup", closedBefore: "2026-08-14", includeYesterday: true},
		{name: "raw_tail", closedBefore: "2026-08-13", includeYesterday: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()

			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
					(1, 1, 10, 2, TIMESTAMPTZ '2026-08-12 12:00:00+08'),
					(2, 1, 10, 3, TIMESTAMPTZ '2026-08-13 12:00:00+08'),
					(3, 1, 10, 4, TIMESTAMPTZ '2026-08-14 12:00:00+08');
				INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
				VALUES (DATE '2026-08-12', 10, 2, NOW());
			`)
			require.NoError(t, err)
			if tt.includeYesterday {
				_, err = tx.ExecContext(ctx, `
					INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
					VALUES (DATE '2026-08-13', 10, 3, NOW())
				`)
				require.NoError(t, err)
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE usage_group_rollup_state
				SET closed_before = $1::date,
					retained_from = TIMESTAMPTZ '2026-08-12 00:00:00+08'
				WHERE id = 1
			`, tt.closedBefore)
			require.NoError(t, err)

			repo := newUsageLogRepositoryWithSQL(nil, tx)
			result, err := repo.GetAllGroupUsageSummary(ctx, todayStart)
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.InDelta(t, 9, result[0].TotalCost, 0.0000001)
			require.InDelta(t, 4, result[0].TodayCost, 0.0000001)
			require.InDelta(t, 3, result[0].YesterdayCost, 0.0000001)
		})
	}
}

func TestGroupUsageRollupSyncRebuildsAfterTimezoneChange(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	todayStart := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()

	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'UTC';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 3, TIMESTAMPTZ '2026-03-08 05:30:00+00'),
			(2, 1, 10, 5, TIMESTAMPTZ '2026-03-09 04:30:00+00');
		INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost, computed_at)
		VALUES (DATE '2026-03-08', 10, 99, NOW());
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2026-03-09',
			retained_from = TIMESTAMPTZ '2026-03-08 05:30:00+00',
			timezone_name = 'Asia/Shanghai'
		WHERE id = 1;
	`)
	require.NoError(t, err)

	repo := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, repo.SyncGroupUsageRollups(ctx, todayStart))

	var stateTimezone string
	var closedBefore string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT timezone_name, closed_before::text
		FROM usage_group_rollup_state
		WHERE id = 1
	`).Scan(&stateTimezone, &closedBefore))
	require.Equal(t, "America/New_York", stateTimezone)
	require.Equal(t, "2026-03-09", closedBefore)

	var rollupCost float64
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT actual_cost
		FROM usage_group_daily_rollups
		WHERE bucket_date = DATE '2026-03-08' AND group_id = 10
	`).Scan(&rollupCost))
	require.InDelta(t, 3, rollupCost, 0.0000001)

	usageRepo := newUsageLogRepositoryWithSQL(nil, tx)
	result, err := usageRepo.GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.InDelta(t, 8, result[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, result[0].TodayCost, 0.0000001)
	require.InDelta(t, 3, result[0].YesterdayCost, 0.0000001)
}

func TestGroupUsageSummaryUsesConfiguredDSTBoundaries(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	todayStart := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = tx.Rollback() }()

	_, err := tx.ExecContext(ctx, `
		SET LOCAL TIME ZONE 'UTC';
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 100, TIMESTAMPTZ '2026-03-08 04:30:00+00'),
			(2, 1, 10, 3, TIMESTAMPTZ '2026-03-08 05:30:00+00'),
			(3, 1, 10, 4, TIMESTAMPTZ '2026-03-09 03:30:00+00'),
			(4, 1, 10, 5, TIMESTAMPTZ '2026-03-09 04:30:00+00');
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '1970-01-01',
			retained_from = TIMESTAMPTZ '1970-01-01 00:00:00+00',
			timezone_name = 'America/New_York'
		WHERE id = 1;
	`)
	require.NoError(t, err)

	repo := newUsageLogRepositoryWithSQL(nil, tx)
	result, err := repo.GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.InDelta(t, 112, result[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, result[0].TodayCost, 0.0000001)
	require.InDelta(t, 7, result[0].YesterdayCost, 0.0000001)
}

func TestGroupUsageSummarySnapshotSurvivesConcurrentHistoryCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	useGroupUsageRepositoryTestTimezone(t, "UTC")
	todayStart := time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seedTx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = seedTx.Rollback() }()
	// Keep the deleted day inside the retained window; its first-day boundary
	// deliberately does not invalidate under the retention guard from 315.
	_, err := seedTx.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 2, TIMESTAMPTZ '2026-03-07 12:00:00+00'),
			(2, 1, 10, 3, TIMESTAMPTZ '2026-03-08 12:00:00+00'),
			(3, 1, 10, 5, TIMESTAMPTZ '2026-03-09 12:00:00+00'),
			(4, 1, 10, 0, TIMESTAMPTZ '2026-03-06 12:00:00+00');
		UPDATE usage_group_rollup_state SET closed_before = DATE '1970-01-01', timezone_name = 'UTC' WHERE id = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(seedTx).SyncGroupUsageRollups(ctx, todayStart))
	require.NoError(t, seedTx.Commit())

	reader, err := integrationDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer func() { _ = reader.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, reader, pq.QuoteIdentifier(schema)))
	repo := newUsageLogRepositoryWithSQL(nil, reader)
	state, err := repo.readGroupUsageRollupSnapshot(ctx, "UTC", "2026-03-09")
	require.NoError(t, err)
	require.True(t, state.valid)

	// Publish a different retention boundary after the reader has captured its
	// watermark. Its totals must still belong to the earlier snapshot.
	writer := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = writer.Rollback() }()
	_, err = writer.ExecContext(ctx, "DELETE FROM usage_logs WHERE id = 1")
	require.NoError(t, err)
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(writer).SyncGroupUsageRollups(ctx, todayStart))
	require.NoError(t, writer.Commit())

	before, err := repo.GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.InDelta(t, 10, before[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, before[0].TodayCost, 0.0000001)
	require.InDelta(t, 3, before[0].YesterdayCost, 0.0000001)
	require.NoError(t, reader.Commit())

	fresh := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = fresh.Rollback() }()
	after, err := newUsageLogRepositoryWithSQL(nil, fresh).GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.InDelta(t, 8, after[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, after[0].TodayCost, 0.0000001)
	require.InDelta(t, 3, after[0].YesterdayCost, 0.0000001)
}

func TestGroupUsageRollupRetentionCutoffCrossesDayWithoutRewind(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	todayStart := time.Date(2026, 11, 16, 16, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 11, 1, 16, 1, 0, 0, time.UTC)
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	seed := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
	defer func() { _ = seed.Rollback() }()
	_, err := seed.ExecContext(ctx, `
		INSERT INTO groups (id) VALUES (10);
		INSERT INTO users (id) VALUES (1);
		INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at) VALUES
			(1, 1, 10, 1, TIMESTAMPTZ '2026-11-01 23:59:50+08'),
			(2, 1, 10, 2, TIMESTAMPTZ '2026-11-02 00:00:30+08'),
			(3, 1, 10, 4, TIMESTAMPTZ '2026-11-02 12:00:00+08'),
			(4, 1, 10, 3, TIMESTAMPTZ '2026-11-16 12:00:00+08'),
			(5, 1, 10, 5, TIMESTAMPTZ '2026-11-17 12:00:00+08');
		INSERT INTO usage_group_daily_rollups (bucket_date, group_id, actual_cost) VALUES
			('2026-11-01', 10, 1), ('2026-11-02', 10, 6), ('2026-11-16', 10, 3);
		UPDATE usage_group_rollup_state
		SET closed_before = DATE '2026-11-17',
			retained_from = TIMESTAMPTZ '2026-11-01 23:59:50+08',
			timezone_name = 'Asia/Shanghai'
		WHERE id = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, seed.Commit())

	// Reuse the real cleanup transaction on an isolated schema/connection, never
	// changing the integration pool's session defaults or public usage tables.
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	defer func() {
		_, resetErr := conn.ExecContext(context.Background(), "RESET search_path; RESET TIME ZONE")
		require.NoError(t, resetErr)
	}()
	_, err = conn.ExecContext(ctx, "SET search_path TO "+pq.QuoteIdentifier(schema)+"; SET TIME ZONE 'Asia/Shanghai'")
	require.NoError(t, err)
	deleted, err := cleanupUsageLogsBatchWithRollupInvalidation(ctx, conn, cutoff)
	require.NoError(t, err)
	require.EqualValues(t, 2, deleted)
	// A peer with an older cutoff must not move the retained boundary backwards.
	deleted, err = cleanupUsageLogsBatchWithRollupInvalidation(ctx, conn, cutoff.Add(-time.Minute))
	require.NoError(t, err)
	require.Zero(t, deleted)
	var closedBefore string
	var retainedFrom time.Time
	require.NoError(t, conn.QueryRowContext(ctx, `
		SELECT closed_before::text, retained_from FROM usage_group_rollup_state WHERE id = 1
	`).Scan(&closedBefore, &retainedFrom))
	require.Equal(t, "2026-11-17", closedBefore)
	require.True(t, cutoff.Equal(retainedFrom))

	syncTx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = syncTx.Rollback() }()
	require.NoError(t, newDashboardAggregationRepositoryWithSQL(syncTx).SyncGroupUsageRollups(ctx, todayStart))
	require.NoError(t, syncTx.Commit())
	result, err := newUsageLogRepositoryWithSQL(nil, conn).GetAllGroupUsageSummary(ctx, todayStart)
	require.NoError(t, err)
	require.Len(t, result, 1)
	// Historical totals retain the whole first day, not a partial-day raw sum:
	// drop Nov 1, retain Nov 2's bucket once, and leave today/yesterday unchanged.
	require.InDelta(t, 14, result[0].TotalCost, 0.0000001)
	require.InDelta(t, 5, result[0].TodayCost, 0.0000001)
	require.InDelta(t, 3, result[0].YesterdayCost, 0.0000001)
}

func TestGroupUsageRollupRetainedHistoryStillInvalidates(t *testing.T) {
	ctx := context.Background()
	useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	schema := createGroupUsageRollupTriggerTestSchema(t, ctx, false)
	for _, operation := range []string{"update", "delete", "go_invalidation"} {
		t.Run(operation, func(t *testing.T) {
			tx := beginGroupUsageRollupTriggerTestTx(t, ctx, schema)
			defer func() { _ = tx.Rollback() }()
			require.NoError(t, setGroupUsageRollupTriggerTimeZone(ctx, tx, "Asia/Shanghai"))
			_, err := tx.ExecContext(ctx, `
				INSERT INTO groups (id) VALUES (10);
				INSERT INTO users (id) VALUES (1);
				INSERT INTO usage_logs (id, user_id, group_id, actual_cost, created_at)
				VALUES (1, 1, 10, 3, TIMESTAMPTZ '2026-11-16 12:00:00+08');
				UPDATE usage_group_rollup_state
				SET closed_before = DATE '2026-11-17',
					retained_from = TIMESTAMPTZ '2026-11-02 00:01:00+08'
				WHERE id = 1;
			`)
			require.NoError(t, err)
			switch operation {
			case "update":
				_, err = tx.ExecContext(ctx, "UPDATE usage_logs SET actual_cost = 5 WHERE id = 1")
			case "delete":
				_, err = tx.ExecContext(ctx, "DELETE FROM usage_logs WHERE id = 1")
			case "go_invalidation":
				err = invalidateGroupUsageRollupsAt(ctx, tx, time.Date(2026, 11, 16, 4, 0, 0, 0, time.UTC))
			}
			require.NoError(t, err)
			var closedBefore string
			require.NoError(t, tx.QueryRowContext(ctx, `
				SELECT closed_before::text FROM usage_group_rollup_state WHERE id = 1
			`).Scan(&closedBefore))
			require.Equal(t, "2026-11-16", closedBefore)
		})
	}
}

func createGroupUsageRollupTriggerTestSchema(t *testing.T, ctx context.Context, partitioned bool) string {
	t.Helper()

	schema := fmt.Sprintf("group_usage_rollup_trigger_%d", time.Now().UnixNano())
	quotedSchema := pq.QuoteIdentifier(schema)
	_, err := integrationDB.ExecContext(ctx, "CREATE SCHEMA "+quotedSchema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE")
	})

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, tx, quotedSchema))

	usageLogsDDL := `
		CREATE TABLE usage_logs (
			id BIGINT PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
			actual_cost NUMERIC(20, 10) NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
	`
	if partitioned {
		usageLogsDDL = `
			CREATE TABLE usage_logs (
				id BIGINT NOT NULL,
				user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
				group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
				actual_cost NUMERIC(20, 10) NOT NULL,
				created_at TIMESTAMPTZ NOT NULL
			) PARTITION BY RANGE (created_at);
			CREATE TABLE usage_logs_default PARTITION OF usage_logs DEFAULT;
		`
	}

	_, err = tx.ExecContext(ctx, `
		CREATE TABLE users (id BIGINT PRIMARY KEY);
		CREATE TABLE groups (id BIGINT PRIMARY KEY);
	`+usageLogsDDL)
	require.NoError(t, err)

	for _, migrationName := range []string{
		"222_group_usage_daily_rollups.sql",
		"223_group_usage_rollup_timezone.sql",
		"234_group_usage_rollup_current_day_fast_path.sql",
		"315_group_usage_rollup_trim_guard.sql",
		"316_group_usage_rollup_trigger_fast_path.sql",
	} {
		migrationSQL, readErr := migrations.FS.ReadFile(migrationName)
		require.NoError(t, readErr)
		for range 2 {
			_, err = tx.ExecContext(ctx, string(migrationSQL))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tx.Commit())

	return schema
}

func beginGroupUsageRollupTriggerTestTx(t *testing.T, ctx context.Context, schema string) *sql.Tx {
	t.Helper()

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, setGroupUsageRollupTriggerSearchPath(ctx, tx, pq.QuoteIdentifier(schema)))
	return tx
}

func setGroupUsageRollupTriggerSearchPath(ctx context.Context, tx *sql.Tx, quotedSchema string) error {
	_, err := tx.ExecContext(ctx, "SET LOCAL search_path TO "+quotedSchema)
	return err
}

func setGroupUsageRollupTriggerTimeZone(ctx context.Context, tx *sql.Tx, name string) error {
	_, err := tx.ExecContext(ctx, "SET LOCAL TIME ZONE "+pq.QuoteLiteral(name))
	return err
}

func waitForGroupUsageRollupStateLock(
	ctx context.Context,
	backendPID int,
	insertResult <-chan error,
) (bool, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-insertResult:
			if err != nil {
				return false, err
			}
			return false, nil
		case <-ticker.C:
			var waitEventType sql.NullString
			err := integrationDB.QueryRowContext(ctx, `
				SELECT wait_event_type
				FROM pg_stat_activity
				WHERE pid = $1
			`, backendPID).Scan(&waitEventType)
			if err != nil {
				return false, err
			}
			if waitEventType.Valid && waitEventType.String == "Lock" {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}
