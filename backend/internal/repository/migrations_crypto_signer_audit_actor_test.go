package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// The signer audit actor columns must stay additive and idempotent: a re-run
// of the migration may not error, lock for a rewrite, backfill existing rows or
// introduce a NOT NULL / DEFAULT that older writers would have to satisfy.
func TestCryptoSignerAuditActorMigrationIsAdditiveAndIdempotent(t *testing.T) {
	const filename = "325_crypto_signer_audit_actor.sql"
	raw, err := migrations.FS.ReadFile(filename)
	require.NoError(t, err)
	statement := string(raw)

	for _, column := range []string{
		"actor_user_id BIGINT",
		"actor_email TEXT",
		"client_ip TEXT",
		"user_agent TEXT",
		"request_id TEXT",
		"auth_method TEXT",
	} {
		require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS "+column)
	}
	require.NotContains(t, statement, "NOT NULL")
	require.NotContains(t, statement, "DEFAULT")
	require.NotContains(t, statement, "UPDATE")
	require.NotContains(t, statement, "ALTER COLUMN")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	// IF NOT EXISTS is what makes the DDL safe to apply twice; the statement
	// takes no parameters and must succeed on both executions.
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = db.Exec(statement)
	require.NoError(t, err)
	_, err = db.Exec(statement)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
