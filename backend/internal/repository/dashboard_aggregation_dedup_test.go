package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

const usageBillingDedupCleanupQueryPattern = `(?s)WITH batch AS MATERIALIZED .*WHERE id > \$1\s+ORDER BY id\s+LIMIT \$2.*WHERE created_at < \$3.*ON CONFLICT \(request_id, api_key_id\) DO NOTHING.*DELETE FROM usage_billing_dedup.*WHERE id IN \(SELECT id FROM victims\).*RETURNING id.*SELECT COUNT\(\*\), COALESCE\(MAX\(id\), \$1\) FROM deleted`

func TestDashboardAggregationRepositoryCleanupUsageBillingDedup(t *testing.T) {
	type batch struct {
		affected   int64
		nextCursor int64
	}
	for _, tc := range []struct {
		name    string
		batches []batch
	}{
		{"multiple_batches_advance_cursor", []batch{{10000, 10003}, {10000, 20007}, {3, 20012}}},
		{"partial_batch_stops", []batch{{3, 12}}},
		{"empty_head_stops", []batch{{0, 0}}},
		{"head_without_expired_rows_stops", []batch{{0, 0}}},
		{"full_batch_then_empty_head_stops", []batch{{10000, 10003}, {0, 10003}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := newDashboardAggregationRepositoryWithSQL(db)
			cutoff := time.Date(2026, 8, 19, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
			var cursor int64
			for _, result := range tc.batches {
				mock.ExpectQuery(usageBillingDedupCleanupQueryPattern).
					WithArgs(cursor, usageBillingDedupCleanupBatchSize, cutoff.UTC()).
					WillReturnRows(sqlmock.NewRows([]string{"count", "next_cursor"}).AddRow(result.affected, result.nextCursor)).
					RowsWillBeClosed()
				cursor = result.nextCursor
			}

			require.NoError(t, repo.CleanupUsageBillingDedup(context.Background(), cutoff))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedupRestartsCursor(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newDashboardAggregationRepositoryWithSQL(db)
	cutoff := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	for range 2 {
		mock.ExpectQuery(usageBillingDedupCleanupQueryPattern).
			WithArgs(int64(0), usageBillingDedupCleanupBatchSize, cutoff).
			WillReturnRows(sqlmock.NewRows([]string{"count", "next_cursor"}).AddRow(1, 42))
		require.NoError(t, repo.CleanupUsageBillingDedup(context.Background(), cutoff))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardAggregationRepositoryCleanupUsageBillingDedupReturnsError(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows *sqlmock.Rows
	}{
		{name: "query_error"},
		{name: "row_error", rows: sqlmock.NewRows([]string{"count", "next_cursor"}).AddRow(0, 0).RowError(0, sql.ErrConnDone)},
		{name: "close_error", rows: sqlmock.NewRows([]string{"count", "next_cursor"}).AddRow(0, 0).CloseError(sql.ErrConnDone)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := newDashboardAggregationRepositoryWithSQL(db)
			cutoff := time.Now().UTC()
			query := mock.ExpectQuery(usageBillingDedupCleanupQueryPattern).
				WithArgs(int64(0), usageBillingDedupCleanupBatchSize, cutoff)
			if tc.rows == nil {
				query.WillReturnError(sql.ErrConnDone)
			} else {
				query.WillReturnRows(tc.rows)
			}

			require.ErrorIs(t, repo.CleanupUsageBillingDedup(context.Background(), cutoff), sql.ErrConnDone)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
