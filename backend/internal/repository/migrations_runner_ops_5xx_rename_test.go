package repository

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// 337 已经随 #564 发版并在现网执行过（文件名 337_ops_error_5xx_split.sql）。
// 它不处理「本来就叫 gateway_5xx」的标准名存量规则，会导致网关 5xx 告警静默，
// 修法放进 339。337 因此只能改名不能改内容，这里固定住这条路径：
//   - 老库（有旧文件名历史）→ 先验历史 checksum，再自动跑新的 339；
//   - 新库（没有任何历史）→ 337_v2 与 339 都按顺序执行；
//   - 历史被篡改 → 直接报错，绝不静默跳过。
func TestOpsError5xx337RenameKeepsHistoryAndRunsReconcileMigration(t *testing.T) {
	const (
		current   = "337_ops_error_5xx_split_v2.sql"
		previous  = "337_ops_error_5xx_split.sql"
		lookup    = "SELECT checksum FROM schema_migrations WHERE filename = $1"
		record    = "INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)"
		reconcile = "339_ops_error_5xx_rule_reconcile.sql"
	)

	rename, ok := renamedMigrations[current]
	require.True(t, ok, "337 改名后必须在 renamedMigrations 里登记")
	require.Equal(t, previous, rename.previousName)

	currentContent, err := migrations.FS.ReadFile(current)
	require.NoError(t, err)
	currentChecksum := migrationTestChecksum(strings.TrimSpace(string(currentContent)))

	reconcileContent, err := migrations.FS.ReadFile(reconcile)
	require.NoError(t, err)
	reconcileChecksum := migrationTestChecksum(strings.TrimSpace(string(reconcileContent)))

	// 登记的历史 checksum 必须和真实文件一致，否则老库升级会被判成篡改。
	require.Equal(t, currentChecksum, rename.checksum)

	fsys := fstest.MapFS{
		current:   {Data: currentContent},
		reconcile: {Data: reconcileContent},
	}

	t.Run("老库只跑 339 不重跑 337", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		prepareMigrationsBootstrapExpectations(mock)

		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(current).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(previous).
			WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow(rename.checksum))

		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(reconcile).WillReturnError(sql.ErrNoRows)
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(string(reconcileContent)))).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta(record)).WithArgs(reconcile, reconcileChecksum).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
			WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, applyMigrationsFS(t.Context(), db, fsys))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("新库按顺序执行 337_v2 与 339", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		prepareMigrationsBootstrapExpectations(mock)

		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(current).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(previous).WillReturnError(sql.ErrNoRows)
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(string(currentContent)))).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta(record)).WithArgs(current, currentChecksum).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(reconcile).WillReturnError(sql.ErrNoRows)
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(string(reconcileContent)))).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta(record)).WithArgs(reconcile, reconcileChecksum).WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
			WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

		require.NoError(t, applyMigrationsFS(t.Context(), db, fsys))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("历史被篡改必须报错而不是跳过", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		prepareMigrationsBootstrapExpectations(mock)

		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(current).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(regexp.QuoteMeta(lookup)).WithArgs(previous).
			WillReturnRows(sqlmock.NewRows([]string{"checksum"}).AddRow("tampered"))

		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
			WithArgs(migrationsAdvisoryLockID).WillReturnResult(sqlmock.NewResult(0, 1))

		err = applyMigrationsFS(t.Context(), db, fsys)
		require.ErrorContains(t, err, "checksum mismatch for previous name")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
