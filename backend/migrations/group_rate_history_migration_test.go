package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRateHistoryMigration(t *testing.T) {
	content, err := FS.ReadFile("311_group_rate_history.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// Append-only history table with the exact precision of groups.rate_multiplier.
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS group_rate_history (")
	require.Contains(t, sql, "id BIGSERIAL PRIMARY KEY")
	require.Contains(t, sql, "group_id BIGINT NOT NULL")
	require.Contains(t, sql, "old_rate DECIMAL(10, 4) NOT NULL")
	require.Contains(t, sql, "new_rate DECIMAL(10, 4) NOT NULL")
	require.Contains(t, sql, "changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_group_rate_history_group_changed ON group_rate_history (group_id, changed_at DESC)")

	// The row trigger is what puts the history write in the same transaction as
	// every writer of groups.rate_multiplier, and only for real changes.
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION record_group_rate_history()")
	require.Contains(t, sql, "IF NEW.rate_multiplier IS DISTINCT FROM OLD.rate_multiplier THEN")
	require.Contains(t, sql, "INSERT INTO group_rate_history (group_id, old_rate, new_rate) VALUES (NEW.id, OLD.rate_multiplier, NEW.rate_multiplier)")
	require.Contains(t, sql, "DROP TRIGGER IF EXISTS trg_groups_rate_history ON groups")
	require.Contains(t, sql, "CREATE TRIGGER trg_groups_rate_history AFTER UPDATE OF rate_multiplier ON groups FOR EACH ROW WHEN (OLD.rate_multiplier IS DISTINCT FROM NEW.rate_multiplier) EXECUTE FUNCTION record_group_rate_history()")
}

// The runner refuses index builds that need a non-transactional migration, and
// production applies this file by hand: it must stay a plain, additive,
// transactional script that never touches existing data.
func TestGroupRateHistoryMigrationStaysAdditiveAndTransactional(t *testing.T) {
	content, err := FS.ReadFile("311_group_rate_history.sql")
	require.NoError(t, err)

	// The runner matches the forbidden substring anywhere, comments included.
	require.NotContains(t, strings.ToLower(string(content)), "concurrent")

	// Statements only: the header comment explains why there is no foreign key.
	var statements []string
	for _, line := range strings.Split(string(content), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			statements = append(statements, trimmed)
		}
	}
	lower := strings.ToLower(strings.Join(statements, " "))

	for _, forbidden := range []string{"drop table", "alter table", "truncate", "delete from", "update groups", "insert into groups"} {
		require.NotContains(t, lower, forbidden)
	}
	require.NotContains(t, lower, "foreign key")
	require.NotContains(t, lower, "references")

	// No backfill: rows exist only for changes made after the trigger exists.
	require.Equal(t, 1, strings.Count(lower, "insert into"), "only the trigger function inserts")
}
