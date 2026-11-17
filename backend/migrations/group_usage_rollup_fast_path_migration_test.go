package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration316RestoresCurrentDayFastPath(t *testing.T) {
	content, err := FS.ReadFile("316_group_usage_rollup_trigger_fast_path.sql")
	require.NoError(t, err)
	sql := string(content)
	require.NotContains(t, strings.ToUpper(sql), "CONCURRENTLY", "the runner scans comments too")
	_, insert, ok := strings.Cut(sql, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()")
	require.True(t, ok)

	_, slowPath, ok := strings.Cut(insert, "IF affected_date IS NULL OR affected_date >= current_date_in_timezone THEN\n        RETURN NULL;\n    END IF;")
	require.True(t, ok, "today, future and ungrouped inserts must return before reading the watermark")
	fastPath := strings.TrimSuffix(insert, slowPath)
	require.NotContains(t, fastPath, "FROM usage_group_rollup_state")
	require.Contains(t, fastPath, "configured_timezone TEXT := current_setting('TimeZone')")
	require.Contains(t, fastPath, "clock_timestamp() AT TIME ZONE configured_timezone")
	require.Contains(t, fastPath, "MIN((created_at AT TIME ZONE configured_timezone)::date)")

	_, invalidation, ok := strings.Cut(slowPath, "FOR KEY SHARE;")
	require.True(t, ok, "historical inserts must wait for a publisher before testing its watermark")
	require.Contains(t, slowPath, "SELECT closed_before, retained_from::date")
	require.Contains(t, invalidation, "WHERE group_id IS NOT NULL\n      AND (created_at AT TIME ZONE configured_timezone)::date > retained_before;")
	require.Contains(t, invalidation, "IF published_before > affected_date AND affected_date > retained_before THEN")
	require.Contains(t, invalidation, "SET closed_before = LEAST(closed_before, affected_date)")
	require.NotContains(t, strings.TrimSuffix(slowPath, invalidation), "IF published_before > affected_date")
}

func TestMigration316RestoresRowTriggerTimezoneAndKeepsTrimGuard(t *testing.T) {
	content, err := FS.ReadFile("316_group_usage_rollup_trigger_fast_path.sql")
	require.NoError(t, err)
	row, _, ok := strings.Cut(string(content), "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()")
	require.True(t, ok)

	require.Contains(t, row, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state()")
	require.Contains(t, row, "configured_timezone TEXT := current_setting('TimeZone')")
	require.NotContains(t, row, "'Asia/Shanghai'")
	require.Contains(t, row, "OLD.created_at AT TIME ZONE configured_timezone")
	require.Contains(t, row, "NEW.created_at AT TIME ZONE configured_timezone")
	_, invalidation, ok := strings.Cut(row, "FOR UPDATE;")
	require.True(t, ok)
	require.Contains(t, invalidation, "IF published_before > affected_date AND affected_date > retained_before THEN")
	require.Contains(t, invalidation, "SET closed_before = LEAST(closed_before, affected_date)")
}
