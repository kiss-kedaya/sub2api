package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupOpenAITierMultipliersMigration(t *testing.T) {
	content, err := FS.ReadFile("340_group_openai_tier_multipliers.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS openai_fast_multiplier NUMERIC(10,4)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS openai_ultrafast_multiplier NUMERIC(10,4)")
	require.Contains(t, sql, "groups_openai_fast_multiplier_range")
	require.Contains(t, sql, "groups_openai_ultrafast_multiplier_range")
	// One hot table per migration transaction: locking groups and then usage_logs in the same
	// transaction deadlocks against dashboard queries that read usage_logs and then join groups.
	require.NotContains(t, sql, "ALTER TABLE usage_logs", "340 touches groups only; the usage_logs column lives in 342")
	require.NotContains(t, sql, "COMMENT ON COLUMN usage_logs", "340 touches groups only; the usage_logs column lives in 342")
	// Taking the ACCESS EXCLUSIVE locks must be bounded, and the bound must precede the first ALTER.
	lockAt := strings.Index(sql, "SET LOCAL lock_timeout = '5s'")
	require.NotEqual(t, -1, lockAt, "the migration must carry its own lock_timeout")
	require.Less(t, lockAt, strings.Index(sql, "ALTER TABLE groups"))
}

func TestUsageLogServiceTierMultiplierMigration(t *testing.T) {
	content, err := FS.ReadFile("342_usage_log_service_tier_multiplier.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS service_tier_multiplier NUMERIC(10,4)")
	// Nullable columns without a default stay a metadata-only change on a hot table.
	require.NotContains(t, sql, "service_tier_multiplier NUMERIC(10,4) NOT NULL")
	require.NotContains(t, sql, "service_tier_multiplier NUMERIC(10,4) DEFAULT")
	require.NotContains(t, sql, "ALTER TABLE groups", "342 touches usage_logs only; the groups columns live in 340")
	require.NotContains(t, sql, "COMMENT ON COLUMN groups", "342 touches usage_logs only; the groups columns live in 340")

	lockAt := strings.Index(sql, "SET LOCAL lock_timeout = '5s'")
	require.NotEqual(t, -1, lockAt, "the migration must carry its own lock_timeout")
	require.Less(t, lockAt, strings.Index(sql, "ALTER TABLE usage_logs"))
}
