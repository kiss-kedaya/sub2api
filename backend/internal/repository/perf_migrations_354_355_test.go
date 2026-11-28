package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestMigrations354And355NonTransactionalIndexDefinitions(t *testing.T) {
	cases := []struct {
		filename string
		expected string
	}{
		{
			filename: "354_billing_settlements_user_unsettled_index_notx.sql",
			expected: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_billing_balance_settlements_user_unsettled ON billing_balance_settlements (user_id) WHERE status IN (1, 2, 3)",
		},
		{
			filename: "355_cmv2_hist_rollup_global_bucket_index_notx.sql",
			expected: "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cmv2_hist_rollup_global_bucket ON channel_monitor_v2_latency_histograms_rollup (bucket_seconds, bucket_start, platform, group_id, model, metric, upper_bound_ms) INCLUDE (sample_count) WHERE user_id = 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			content, err := migrations.FS.ReadFile(tc.filename)
			require.NoError(t, err)

			nonTx, err := validateMigrationExecutionMode(tc.filename, string(content))
			require.NoError(t, err)
			require.True(t, nonTx)

			statements := splitSQLStatements(string(content))
			require.Len(t, statements, 1)
			normalized := strings.Join(strings.Fields(stripSQLLineComment(statements[0])), " ")
			require.Equal(t, tc.expected, normalized)

			// 端到端验证 applyMigrationsFS 能按非事务模式执行并记录校验和。
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(1)

			prepareMigrationsBootstrapExpectations(mock)
			mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs(tc.filename).
				WillReturnError(sql.ErrNoRows)
			expectNonTransactionalTimeoutsDisabled(mock)
			mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS").
				WillReturnResult(sqlmock.NewResult(0, 0))
			expectNonTransactionalTimeoutsReset(mock)
			mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
				WithArgs(tc.filename, sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
				WithArgs(migrationsAdvisoryLockID).
				WillReturnResult(sqlmock.NewResult(0, 1))

			fsys := fstest.MapFS{
				tc.filename: &fstest.MapFile{Data: content},
			}
			require.NoError(t, applyMigrationsFS(context.Background(), db, fsys))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
