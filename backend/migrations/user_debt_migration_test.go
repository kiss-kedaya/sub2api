package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The negative-balance write-off moves the debt out of users.balance into a
// dedicated ledger; the migration must stay additive/idempotent because
// production already created negative_balance_writeoffs by hand.
func TestUserDebtMigration(t *testing.T) {
	content, err := FS.ReadFile("323_user_debt.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS negative_balance_writeoffs (")
	require.Contains(t, sql, "writeoff_amount NUMERIC(20,8) NOT NULL CHECK (writeoff_amount > 0)")
	require.Contains(t, sql, "balance_before NUMERIC(20,8) NOT NULL CHECK (balance_before < 0)")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_debt (")
	require.Contains(t, sql, "user_id BIGINT PRIMARY KEY")
	require.Contains(t, sql, "amount NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (amount >= 0)")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_debt_clawbacks (")
	require.Contains(t, sql, "source VARCHAR(64) NOT NULL DEFAULT ''")
	require.Contains(t, sql, "source_id VARCHAR(128) NOT NULL DEFAULT ''")

	// Seed: outstanding debt comes from the write-off ledger, once per user.
	require.Contains(t, sql, "INSERT INTO user_debt (user_id, amount, created_at, updated_at)")
	require.Contains(t, sql, "SELECT user_id, SUM(writeoff_amount), MIN(created_at), NOW() FROM negative_balance_writeoffs GROUP BY user_id")
	require.Contains(t, sql, "ON CONFLICT (user_id) DO NOTHING")

	// Additive only: never rewrite existing balances here.
	lower := strings.ToLower(string(content))
	for _, forbidden := range []string{"drop table", "truncate", "delete from", "update users", "concurrent"} {
		require.NotContains(t, lower, forbidden)
	}
}
