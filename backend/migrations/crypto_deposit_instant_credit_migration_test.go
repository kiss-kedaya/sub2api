//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 333 migration: instant (provisional) deposit credits need four durable
// markers on the deposit ledger so a revoked credit can never be re-credited.
func TestMigration333AddsInstantCreditColumns(t *testing.T) {
	content, err := FS.ReadFile("333_crypto_deposit_instant_credit.sql")
	require.NoError(t, err)
	sql := string(content)

	for _, column := range []string{
		"provisional BOOLEAN NOT NULL DEFAULT FALSE",
		"credited_at TIMESTAMPTZ",
		"revoked_at TIMESTAMPTZ",
		"revoke_reason TEXT",
	} {
		require.Contains(t, sql, column)
	}
	// ADD COLUMN on PG 11+ is metadata-only; a table rewrite here would lock
	// the deposit ledger during a deploy.
	for _, statement := range []string{"ADD CONSTRAINT", "ALTER COLUMN", "DROP COLUMN"} {
		require.NotContains(t, sql, statement)
	}
	// The risk-window recheck is indexed; the index must be partial so it stays
	// tiny after every provisional row is finalized.
	require.Contains(t, sql, "idx_deposit_transactions_provisional")
	require.Contains(t, sql, "WHERE provisional")
}

func TestMigration333IsIdempotent(t *testing.T) {
	content, err := FS.ReadFile("333_crypto_deposit_instant_credit.sql")
	require.NoError(t, err)
	code := stripSQLLineComments(string(content))

	require.True(t, strings.Contains(code, "ADD COLUMN IF NOT EXISTS provisional"))
	require.True(t, strings.Contains(code, "CREATE INDEX IF NOT EXISTS idx_deposit_transactions_provisional"))
	// The credit-time backfill must not overwrite an existing value on rerun.
	require.Contains(t, code, "credited_at IS NULL")
}
