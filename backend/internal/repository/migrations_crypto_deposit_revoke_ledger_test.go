package repository

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// The chain claw-back ledger migration must stay additive and idempotent:
// re-running it may not error, rewrite the table, backfill rows or introduce a
// NOT NULL / DEFAULT that older writers would have to satisfy.
func TestCryptoDepositRevokeLedgerMigrationIsAdditiveAndIdempotent(t *testing.T) {
	const filename = "336_crypto_deposit_revoke_ledger.sql"
	raw, err := migrations.FS.ReadFile(filename)
	require.NoError(t, err)
	statement := string(raw)

	// The structured audit pointer plus the ledger-type key it is scoped to.
	require.Contains(t, statement, "ADD COLUMN IF NOT EXISTS source_ref VARCHAR(160)")
	require.Contains(t, statement, "CREATE UNIQUE INDEX IF NOT EXISTS uq_redeem_codes_crypto_revoke_source")
	require.Contains(t, statement, "WHERE type = 'crypto_revoke' AND source_ref IS NOT NULL")
	require.Contains(t, statement, "CREATE INDEX IF NOT EXISTS idx_redeem_codes_crypto_revoke_user")

	// Additive only: no rewrite, no backfill, no constraint that existing rows
	// would violate. (The comments may mention the runtime claim UPDATE, so the
	// assertion is scoped to statements that would actually rewrite data.)
	require.NotContains(t, statement, "VARCHAR(160) NOT NULL")
	require.NotContains(t, statement, "DEFAULT")
	require.NotContains(t, statement, "ALTER COLUMN")
	require.NotContains(t, statement, "UPDATE redeem_codes")
	require.NotContains(t, statement, "DELETE FROM")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	// IF NOT EXISTS is what makes the DDL safe to apply twice; every statement in
	// the migration takes no parameters and must succeed on both executions.
	for range 2 {
		mock.ExpectExec(regexp.QuoteMeta(statement)).WillReturnResult(sqlmock.NewResult(0, 0))
	}
	for range 2 {
		_, err = db.Exec(statement)
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
