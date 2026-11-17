package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/migrations"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestValidateMigrationExecutionMode(t *testing.T) {
	t.Run("事务迁移包含CONCURRENTLY会被拒绝", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_add_idx.sql", "CREATE INDEX CONCURRENTLY idx_a ON t(a);")
		require.False(t, nonTx)
		require.Error(t, err)
	})

	t.Run("notx迁移要求CREATE使用IF NOT EXISTS", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_add_idx_notx.sql", "CREATE INDEX CONCURRENTLY idx_a ON t(a);")
		require.False(t, nonTx)
		require.Error(t, err)
	})

	t.Run("notx迁移要求DROP使用IF EXISTS", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_drop_idx_notx.sql", "DROP INDEX CONCURRENTLY idx_a;")
		require.False(t, nonTx)
		require.Error(t, err)
	})

	t.Run("notx迁移禁止事务控制语句", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_add_idx_notx.sql", "BEGIN; CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t(a); COMMIT;")
		require.False(t, nonTx)
		require.Error(t, err)
	})

	t.Run("notx迁移禁止混用非CONCURRENTLY语句", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_add_idx_notx.sql", "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t(a); UPDATE t SET a = 1;")
		require.False(t, nonTx)
		require.Error(t, err)
	})

	t.Run("notx迁移允许幂等并发索引语句", func(t *testing.T) {
		nonTx, err := validateMigrationExecutionMode("001_add_idx_notx.sql", `
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_a ON t(a);
DROP INDEX CONCURRENTLY IF EXISTS idx_b;
`)
		require.True(t, nonTx)
		require.NoError(t, err)
	})
}

func TestApplyMigrationsFS_NonTransactionalMigration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("001_add_idx_notx.sql").
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t\\(a\\)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs("001_add_idx_notx.sql", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"001_add_idx_notx.sql": &fstest.MapFile{
			Data: []byte("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t(a);"),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_MultiStatements(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("001_add_multi_idx_notx.sql").
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t\\(a\\)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_b ON t\\(b\\)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs("001_add_multi_idx_notx.sql", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"001_add_multi_idx_notx.sql": &fstest.MapFile{
			Data: []byte(`
-- first
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t(a);
-- second
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_b ON t(b);
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_LatestAPIKeyIPIndexDropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs(latestAPIKeyIPIndexMigration).
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs(latestAPIKeyIPIndex).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS idx_usage_logs_api_key_latest_ip").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_latest_ip").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs(latestAPIKeyIPIndexMigration, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		latestAPIKeyIPIndexMigration: &fstest.MapFile{
			Data: []byte(`
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_latest_ip
    ON usage_logs (api_key_id, created_at DESC, id DESC)
    INCLUDE (ip_address)
    WHERE ip_address IS NOT NULL AND ip_address <> '';
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_UsageModelMismatchIndexDropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs(usageLogsUpstreamModelMismatchIndexMigration).
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs(usageLogsUpstreamModelMismatchIndex).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS idx_usage_logs_upstream_model_mismatch_created_at").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_model_mismatch_created_at").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs(usageLogsUpstreamModelMismatchIndexMigration, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		usageLogsUpstreamModelMismatchIndexMigration: &fstest.MapFile{Data: []byte(`
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_model_mismatch_created_at
    ON usage_logs (created_at DESC, id DESC)
    WHERE upstream_model_mismatch IS TRUE;
`)},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_EffectiveModelIndexesDropInvalidIndexesBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs(usageLogsEffectiveModelIndexesMigration).
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	for _, indexName := range []string{usageLogsEffectiveRequestedModelIndex, usageLogsEffectiveUpstreamModelIndex} {
		mock.ExpectQuery("SELECT EXISTS \\(").
			WithArgs(indexName).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS " + indexName).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_effective_requested_model_created").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_effective_upstream_model_created").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs(usageLogsEffectiveModelIndexesMigration, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		usageLogsEffectiveModelIndexesMigration: &fstest.MapFile{Data: []byte(`
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_effective_requested_model_created
    ON usage_logs ((COALESCE(NULLIF(BTRIM(requested_model), ''), model)), created_at DESC, id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_effective_upstream_model_created
    ON usage_logs ((COALESCE(NULLIF(BTRIM(upstream_model), ''), model)), created_at DESC, id DESC);
`)},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_PaymentOrdersOutTradeNoUniqueMigration_FailsFastOnDuplicatePrecheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("120_enforce_payment_orders_out_trade_no_unique_notx.sql").
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectQuery("SELECT out_trade_no, COUNT\\(\\*\\) AS duplicate_count FROM payment_orders").
		WillReturnRows(sqlmock.NewRows([]string{"out_trade_no", "duplicate_count"}).AddRow("dup-out-trade-no", 2))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"120_enforce_payment_orders_out_trade_no_unique_notx.sql": &fstest.MapFile{
			Data: []byte(`
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS paymentorder_out_trade_no_unique
    ON payment_orders (out_trade_no)
    WHERE out_trade_no <> '';

DROP INDEX CONCURRENTLY IF EXISTS paymentorder_out_trade_no;
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate out_trade_no")
	require.Contains(t, err.Error(), "dup-out-trade-no")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_PaymentOrdersOutTradeNoUniqueMigration_DropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("120_enforce_payment_orders_out_trade_no_unique_notx.sql").
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectQuery("SELECT out_trade_no, COUNT\\(\\*\\) AS duplicate_count FROM payment_orders").
		WillReturnRows(sqlmock.NewRows([]string{"out_trade_no", "duplicate_count"}))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs("paymentorder_out_trade_no_unique").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS paymentorder_out_trade_no_unique").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS paymentorder_out_trade_no_unique").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS paymentorder_out_trade_no").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs("120_enforce_payment_orders_out_trade_no_unique_notx.sql", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"120_enforce_payment_orders_out_trade_no_unique_notx.sql": &fstest.MapFile{
			Data: []byte(`
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS paymentorder_out_trade_no_unique
    ON payment_orders (out_trade_no)
    WHERE out_trade_no <> '';

DROP INDEX CONCURRENTLY IF EXISTS paymentorder_out_trade_no;
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_SchedulerOutboxPendingDedupKeyMigration_DropsInvalidIndexBeforeRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("153_scheduler_outbox_pending_dedup_key_index_notx.sql").
		WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs("idx_scheduler_outbox_pending_dedup_key").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS idx_scheduler_outbox_pending_dedup_key").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduler_outbox_pending_dedup_key").
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs("153_scheduler_outbox_pending_dedup_key_index_notx.sql", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"153_scheduler_outbox_pending_dedup_key_index_notx.sql": &fstest.MapFile{
			Data: []byte(`
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduler_outbox_pending_dedup_key
    ON scheduler_outbox (dedup_key)
    WHERE dedup_key IS NOT NULL;
`),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_TransactionalMigration(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	// The advisory lock and all migration work must share one session. This also
	// proves startup cannot self-deadlock when deployments cap the pool at one.
	db.SetMaxOpenConns(1)

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("001_add_col.sql").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec("ALTER TABLE t ADD COLUMN name TEXT").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
		WithArgs("001_add_col.sql", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"001_add_col.sql": &fstest.MapFile{
			Data: []byte("ALTER TABLE t ADD COLUMN name TEXT;"),
		},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_FailuresResetTimeouts(t *testing.T) {
	queries := []string{
		"SET statement_timeout = 0",
		"SET lock_timeout = 0",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t(a)",
	}
	for failedAt, failedQuery := range queries {
		t.Run(failedQuery, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			failure := errors.New("migration statement failed")
			prepareMigrationsBootstrapExpectations(mock)
			mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs("001_add_idx_notx.sql").WillReturnError(sql.ErrNoRows)
			for i, query := range queries[:failedAt+1] {
				expectation := mock.ExpectExec(regexp.QuoteMeta(query))
				if i == failedAt {
					expectation.WillReturnError(failure)
				} else {
					expectation.WillReturnResult(sqlmock.NewResult(0, 0))
				}
			}
			expectNonTransactionalTimeoutsReset(mock)
			mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
				WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

			err = applyMigrationsFS(context.Background(), db, fstest.MapFS{
				"001_add_idx_notx.sql": &fstest.MapFile{Data: []byte(queries[2] + ";")},
			})
			require.ErrorIs(t, err, failure)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestApplyMigrationsFS_NonTransactionalMigration_CancelledContextStillResetsTimeouts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const statement = "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t(a)"
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if actual == statement {
			cancel()
		}
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("001_add_idx_notx.sql").WillReturnError(sql.ErrNoRows)
	expectNonTransactionalTimeoutsDisabled(mock)
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnError(context.Canceled)
	expectNonTransactionalTimeoutsReset(mock)
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

	err = applyMigrationsFS(ctx, db, fstest.MapFS{
		"001_add_idx_notx.sql": &fstest.MapFile{Data: []byte(statement + ";")},
	})
	// sqlmock's cancellation select may return its own cancellation error before
	// the configured context.Canceled result; both must still run RESET.
	require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, sqlmock.ErrCancelled), "unexpected error: %v", err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyMigrationsFS_NonTransactionalMigration_ResetFailureDiscardsConnection(t *testing.T) {
	for _, failedSetting := range []string{"statement_timeout", "lock_timeout"} {
		t.Run(failedSetting, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			failure := errors.New("statement failed")
			resetFailure := errors.New("reset failed")
			prepareMigrationsBootstrapExpectations(mock)
			mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs("001_add_idx_notx.sql").WillReturnError(sql.ErrNoRows)
			expectNonTransactionalTimeoutsDisabled(mock)
			mock.ExpectExec("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a").WillReturnError(failure)
			for _, setting := range []string{"statement_timeout", "lock_timeout"} {
				expectation := mock.ExpectExec("RESET " + setting)
				if setting == failedSetting {
					expectation.WillReturnError(resetFailure)
				} else {
					expectation.WillReturnResult(sqlmock.NewResult(0, 0))
				}
			}
			mock.ExpectClose()

			err = applyMigrationsFS(context.Background(), db, fstest.MapFS{
				"001_add_idx_notx.sql": &fstest.MapFile{Data: []byte("CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_t_a ON t(a);")},
			})
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, err, resetFailure)
			require.Zero(t, db.Stats().Idle)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestMigration317NonTransactionalCoveringIndexDefinitions(t *testing.T) {
	content, err := migrations.FS.ReadFile(usageLogsDashboardCoveringIndexesMigration)
	require.NoError(t, err)
	nonTx, err := validateMigrationExecutionMode(usageLogsDashboardCoveringIndexesMigration, string(content))
	require.NoError(t, err)
	require.True(t, nonTx)
	statements := splitSQLStatements(string(content))
	expected := []string{
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_user_created_dims
    ON usage_logs (user_id, created_at)
    INCLUDE (group_id, account_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_created_stats
    ON usage_logs (api_key_id, created_at)
    INCLUDE (user_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms)`,
	}
	require.Len(t, statements, len(expected))
	for i, statement := range statements {
		require.Equal(t, strings.Join(strings.Fields(expected[i]), " "),
			strings.Join(strings.Fields(stripSQLLineComment(statement)), " "))
	}
}

func TestApplyMigrationsFS_NonTransactionalMigration317InvalidIndexRepairAndNumberGap(t *testing.T) {
	content, err := migrations.FS.ReadFile(usageLogsDashboardCoveringIndexesMigration)
	require.NoError(t, err)
	for _, tc := range []struct {
		name       string
		invalid    [2]bool
		prepareErr bool
	}{
		{name: "healthy_indexes_are_not_dropped"},
		{name: "invalid_user_index", invalid: [2]bool{true, false}},
		{name: "invalid_api_key_index", invalid: [2]bool{false, true}},
		{name: "both_invalid", invalid: [2]bool{true, true}},
		{name: "failed_invalid_index_cleanup", invalid: [2]bool{true, false}, prepareErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(1)
			failure := errors.New("drop invalid index failed")
			prepareMigrationsBootstrapExpectations(mock)
			mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs("315_previous.sql").
				WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow(migrationChecksum("SELECT 1;")))
			mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
				WithArgs(usageLogsDashboardCoveringIndexesMigration).WillReturnError(sql.ErrNoRows)
			expectNonTransactionalTimeoutsDisabled(mock)
			for i, indexName := range []string{usageLogsUserCreatedDimsIndex, usageLogsAPIKeyCreatedStatsIndex} {
				mock.ExpectQuery("SELECT EXISTS \\(").WithArgs(indexName).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(tc.invalid[i]))
				if tc.invalid[i] {
					expectation := mock.ExpectExec("DROP INDEX CONCURRENTLY IF EXISTS " + indexName)
					if tc.prepareErr {
						expectation.WillReturnError(failure)
						break
					}
					expectation.WillReturnResult(sqlmock.NewResult(0, 0))
				}
			}
			if !tc.prepareErr {
				for _, statement := range splitSQLStatements(string(content)) {
					mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(statement))).
						WillReturnResult(sqlmock.NewResult(0, 0))
				}
			}
			expectNonTransactionalTimeoutsReset(mock)
			if !tc.prepareErr {
				mock.ExpectExec("INSERT INTO schema_migrations \\(filename, checksum\\) VALUES \\(\\$1, \\$2\\)").
					WithArgs(usageLogsDashboardCoveringIndexesMigration, migrationChecksum(string(content))).
					WillReturnResult(sqlmock.NewResult(1, 1))
			}
			mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
				WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

			err = applyMigrationsFS(context.Background(), db, fstest.MapFS{
				"315_previous.sql":                         &fstest.MapFile{Data: []byte("SELECT 1;")},
				usageLogsDashboardCoveringIndexesMigration: &fstest.MapFile{Data: content},
			})
			if tc.prepareErr {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func expectNonTransactionalTimeoutsDisabled(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SET statement_timeout = 0").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET lock_timeout = 0").WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectNonTransactionalTimeoutsReset(mock sqlmock.Sqlmock) {
	mock.ExpectExec("RESET statement_timeout").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("RESET lock_timeout").WillReturnResult(sqlmock.NewResult(0, 0))
}

func prepareMigrationsBootstrapExpectations(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT pg_try_advisory_lock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs("schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT EXISTS \\(").
		WithArgs("atlas_schema_revisions").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM atlas_schema_revisions").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
}
