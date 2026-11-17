//go:build integration

package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDashboardAggregationRepositoryCleanupUsageBillingDedupMixedHead(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	// Session-local copies keep other integration fixtures out of the PK head.
	_, err = tx.ExecContext(ctx, `
		CREATE TEMP TABLE usage_billing_dedup (LIKE public.usage_billing_dedup INCLUDING ALL) ON COMMIT DROP;
		CREATE TEMP TABLE usage_billing_dedup_archive (LIKE public.usage_billing_dedup_archive INCLUDING ALL) ON COMMIT DROP;
	`)
	require.NoError(t, err)
	cutoff := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	oldAt, freshAt := cutoff.Add(-time.Second), cutoff.Add(time.Second)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (id, request_id, api_key_id, request_fingerprint, created_at)
		SELECT n, 'cleanup-' || n, 1, repeat('a', 64), $1::timestamptz
		FROM generate_series(1, $2::bigint) AS n
	`, oldAt, usageBillingDedupCleanupBatchSize)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (id, request_id, api_key_id, request_fingerprint, created_at) VALUES
			($1::bigint + 1, 'fresh-before-old', 1, repeat('b', 64), $2),
			($1::bigint + 2, 'late-old', 1, repeat('c', 64), $3),
			($1::bigint + 3, 'at-cutoff', 1, repeat('d', 64), $4),
			($1::bigint + 4, 'last-old', 1, repeat('e', 64), $3);
	`, usageBillingDedupCleanupBatchSize, freshAt, oldAt, cutoff)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup_archive (request_id, api_key_id, request_fingerprint, created_at)
		SELECT request_id, api_key_id, request_fingerprint, created_at FROM usage_billing_dedup WHERE id = 1
	`)
	require.NoError(t, err)

	repo := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, cutoff))
	var expired, archived, retained, mismatched int64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE created_at < $1", cutoff).Scan(&expired))
	require.Zero(t, expired)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup_archive").Scan(&archived))
	require.EqualValues(t, usageBillingDedupCleanupBatchSize+2, archived)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE created_at >= $1", cutoff).Scan(&retained))
	require.EqualValues(t, 2, retained)
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM usage_billing_dedup_archive
		WHERE api_key_id <> 1 OR created_at <> $1 OR request_fingerprint <> CASE request_id
			WHEN 'late-old' THEN repeat('c', 64)
			WHEN 'last-old' THEN repeat('e', 64)
			ELSE repeat('a', 64) END
	`, oldAt).Scan(&mismatched))
	require.Zero(t, mismatched)
	// Repeating with a fresh-only head must stop and leave both tables unchanged.
	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, cutoff))
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup_archive").Scan(&archived))
	require.EqualValues(t, usageBillingDedupCleanupBatchSize+2, archived)
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedupRevisitsOutOfOrderRows(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	_, err = tx.ExecContext(ctx, `
		CREATE TEMP TABLE usage_billing_dedup (LIKE public.usage_billing_dedup INCLUDING ALL) ON COMMIT DROP;
		CREATE TEMP TABLE usage_billing_dedup_archive (LIKE public.usage_billing_dedup_archive INCLUDING ALL) ON COMMIT DROP;
	`)
	require.NoError(t, err)
	cutoff := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (id, request_id, api_key_id, request_fingerprint, created_at)
		SELECT n, 'out-of-order-' || n, 1, repeat('a', 64),
			CASE WHEN n = 1 THEN $1::timestamptz ELSE $1::timestamptz - INTERVAL '1 second' END
		FROM generate_series(1, $2::bigint + 1) AS n
	`, cutoff, usageBillingDedupCleanupBatchSize)
	require.NoError(t, err)
	repo := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, cutoff))
	var remaining int
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup").Scan(&remaining))
	require.Equal(t, 2, remaining, "one fresh row stops this pass before the old row beyond the head")
	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, cutoff))
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup").Scan(&remaining))
	require.Equal(t, 1, remaining, "the next call restarts at zero and collects the out-of-order old row")
	var retainedID int64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT id FROM usage_billing_dedup").Scan(&retainedID))
	require.EqualValues(t, 1, retainedID)
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedupHeadUsesPrimaryKey(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, tx.Rollback()) }()
	_, err = tx.ExecContext(ctx, `
		CREATE TEMP TABLE usage_billing_dedup (LIKE public.usage_billing_dedup INCLUDING ALL) ON COMMIT DROP;
		INSERT INTO usage_billing_dedup (id, request_id, api_key_id, request_fingerprint)
		SELECT n, 'plan-' || n, 1, repeat('a', 64) FROM generate_series(1, 100000) AS n;
		ANALYZE usage_billing_dedup;
	`)
	require.NoError(t, err)
	var primaryKey string
	require.NoError(t, tx.QueryRowContext(ctx, `
		SELECT indexrelid::regclass::text FROM pg_index
		WHERE indrelid = 'pg_temp.usage_billing_dedup'::regclass AND indisprimary
	`).Scan(&primaryKey))
	for _, cursor := range []int64{0, 50000, 100000} {
		rows, queryErr := tx.QueryContext(ctx, "EXPLAIN (COSTS OFF) "+usageBillingDedupCleanupHeadSQL,
			cursor, usageBillingDedupCleanupBatchSize)
		require.NoError(t, queryErr)
		var lines []string
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			lines = append(lines, line)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		plan := strings.Join(lines, "\n")
		require.Contains(t, plan, "Index Scan using "+primaryKey)
		require.NotContains(t, plan, "Seq Scan")
	}
}
