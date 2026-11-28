package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration352ConvertsUsageLogsDeleteTriggerToStatementLevel(t *testing.T) {
	content, err := FS.ReadFile("352_usage_logs_delete_trigger_statement.sql")
	require.NoError(t, err)
	sql := string(content)

	require.True(t, strings.HasPrefix(strings.TrimSpace(sql), "SET LOCAL lock_timeout = '5s';"),
		"migration must start with SET LOCAL lock_timeout = '5s';")
	require.NotContains(t, strings.ToUpper(sql), "CONCURRENTLY", "transactional migration must not contain CONCURRENTLY")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_delete()")
	require.Contains(t, sql, "configured_timezone TEXT := current_setting('TimeZone')")
	require.NotContains(t, sql, "'Asia/Shanghai'")
	require.Contains(t, sql, "FROM deleted_usage_logs")
	require.Contains(t, sql, "WHERE group_id IS NOT NULL")
	require.Contains(t, sql, "FOR UPDATE;")
	require.Contains(t, sql, "AND (created_at AT TIME ZONE configured_timezone)::date > retained_before;")
	require.Contains(t, sql, "DROP TRIGGER IF EXISTS usage_logs_group_rollup_invalidate_delete ON usage_logs;")
	require.Contains(t, sql, "AFTER DELETE ON usage_logs")
	require.Contains(t, sql, "REFERENCING OLD TABLE AS deleted_usage_logs")
	require.Contains(t, sql, "FOR EACH STATEMENT")
	require.Contains(t, sql, "EXECUTE FUNCTION invalidate_group_usage_rollup_state_after_delete();")
	require.NotContains(t, sql, "FOR EACH ROW")
	// Must keep the UPDATE row trigger untouched.
	require.NotContains(t, sql, "usage_logs_group_rollup_invalidate_update")
}

func TestMigration353DropsRedundantUserTrafficIndexesConcurrently(t *testing.T) {
	content, err := FS.ReadFile("353_drop_redundant_user_traffic_indexes_notx.sql")
	require.NoError(t, err)
	sql := string(content)
	upper := strings.ToUpper(sql)

	require.NotContains(t, upper, "BEGIN")
	require.NotContains(t, upper, "COMMIT")
	require.NotContains(t, upper, "ROLLBACK")
	require.NotContains(t, upper, "SET LOCK_TIMEOUT")

	var stmtLines []string
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		stmtLines = append(stmtLines, trimmed)
	}
	statements := strings.Join(stmtLines, "\n")

	for _, idx := range []string{
		"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_user_time_idx;",
		"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_key_time_idx;",
		"DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_time_idx;",
	} {
		require.Contains(t, statements, idx)
	}
	// Must never drop the 273 cursor indexes or the primary key.
	require.NotContains(t, statements, "user_traffic_events_user_cursor_idx")
	require.NotContains(t, statements, "user_traffic_events_key_cursor_idx")
	require.NotContains(t, statements, "user_traffic_events_cursor_idx")
	// Comments in *_notx.sql must not contain semicolons because splitSQLStatements splits on ';'.
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			require.NotContains(t, line, ";", "comments in *_notx.sql must not contain ';'")
		}
	}
}
