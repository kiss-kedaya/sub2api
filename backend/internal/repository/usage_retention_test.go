//go:build unit

package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// 诊断类表的保留窗口统一 7 天。写死断言是刻意的：这两张表按 GB/天 增长，
// 谁把窗口改大都会立刻体现在磁盘上，测试先拦住。
func TestDiagnosticRetentionWindowsAreSevenDays(t *testing.T) {
	if connectionTraceRetentionDays != 7 {
		t.Fatalf("request_connection_traces retention = %d days, want 7", connectionTraceRetentionDays)
	}
	if userTrafficEventRetentionDays != 7 {
		t.Fatalf("user_traffic_events retention = %d days, want 7", userTrafficEventRetentionDays)
	}
	if connectionTracePruneBatch != 10000 || userTrafficEventPruneBatch != 10000 {
		t.Fatalf("diagnostic prune batches = (%d, %d), want (10000, 10000)", connectionTracePruneBatch, userTrafficEventPruneBatch)
	}
	if connectionTracePrunePasses <= 0 || userTrafficEventPrunePasses <= 0 {
		t.Fatal("prune passes must allow the hourly loop to outpace ingestion")
	}
	if connectionTracePruneBatch*connectionTracePrunePasses < 1000000 || userTrafficEventPruneBatch*userTrafficEventPrunePasses < 1000000 {
		t.Fatal("hourly retention budget must comfortably outpace peak ingestion")
	}

	traceSQL := connectionTracePruneSQL()
	require.Contains(t, traceSQL, "WHERE ctid IN (SELECT ctid FROM request_connection_traces WHERE started_at < NOW() - INTERVAL '7 days' LIMIT 10000)")
	require.NotContains(t, traceSQL, "WHERE trace_id IN")

	trafficSQL := userTrafficEventPruneSQL()
	require.Contains(t, trafficSQL, "WHERE ctid IN (SELECT ctid FROM user_traffic_events WHERE started_at < NOW() - INTERVAL '7 days' LIMIT 10000)")
}

func TestDiagnosticRetentionAcquiresAdvisoryLockAndPrunesBothTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	// First trace batch hits the full batch size -> loop runs a second pass.
	mock.ExpectExec(regexp.QuoteMeta(connectionTracePruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, int64(connectionTracePruneBatch)))
	mock.ExpectExec(regexp.QuoteMeta(connectionTracePruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, 42))
	// Traffic events finish on the first partial batch.
	mock.ExpectExec(regexp.QuoteMeta(userTrafficEventPruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.runDiagnosticRetentionCycle(context.Background())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiagnosticRetentionSkipsWhenAdvisoryLockHeldByPeer(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.runDiagnosticRetentionCycle(context.Background())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiagnosticRetentionTableFailureStillCleansSecondTableAndUnlocks(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(connectionTracePruneSQL())).
		WillReturnError(errors.New("statement timeout"))
	mock.ExpectExec(regexp.QuoteMeta(userTrafficEventPruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.runDiagnosticRetentionCycle(context.Background())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDiagnosticRetentionUnlockFailureDiscardsConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_lock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta(connectionTracePruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(userTrafficEventPruneSQL())).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_unlock($1)`)).
		WithArgs(diagnosticRetentionLockKey).
		WillReturnError(errors.New("unlock connection broken"))
	mock.ExpectClose()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.runDiagnosticRetentionCycle(context.Background())
	require.Zero(t, db.Stats().Idle)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListConnectionTracesReportsUnifiedRetentionDays(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`WITH source AS`).
		WillReturnRows(sqlmock.NewRows([]string{
			"trace_id", "started_at", "request_id", "client_request_id", "user_id", "api_key_id",
			"account_id", "group_id", "model", "path", "client_ip", "user_agent", "stream", "detail",
			"legacy", "state", "source", "host", "scheme", "method", "http_version", "tls_version",
			"upstream_status", "network_error", "email", "key_name", "account_name", "group_name", "username",
		}))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	now := time.Now().UTC()
	list, err := repo.ListConnectionTraces(context.Background(), service.ConnectionTraceFilter{
		Start: now.Add(-time.Hour), End: now, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.Equal(t, connectionTraceRetentionDays, list.RetentionDays)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMigrations352And353PassRunnerValidation(t *testing.T) {
	t.Run("352 transactional statement delete trigger", func(t *testing.T) {
		const name = "352_usage_logs_delete_trigger_statement.sql"
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		nonTx, err := validateMigrationExecutionMode(name, string(raw))
		require.NoError(t, err)
		require.False(t, nonTx)
	})

	t.Run("353 non-transactional concurrent index drops", func(t *testing.T) {
		const name = "353_drop_redundant_user_traffic_indexes_notx.sql"
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		nonTx, err := validateMigrationExecutionMode(name, string(raw))
		require.NoError(t, err)
		require.True(t, nonTx)

		stmts := splitSQLStatements(string(raw))
		var nonEmpty []string
		for _, s := range stmts {
			if cleaned := strings.TrimSpace(stripSQLLineComment(s)); cleaned != "" {
				nonEmpty = append(nonEmpty, cleaned)
			}
		}
		require.Equal(t, []string{
			"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_user_time_idx",
			"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_key_time_idx",
			"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_time_idx",
		}, nonEmpty)
	})
}
