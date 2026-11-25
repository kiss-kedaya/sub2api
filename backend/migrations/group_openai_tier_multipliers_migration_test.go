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
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS service_tier_multiplier NUMERIC(10,4)")
	// Taking the ACCESS EXCLUSIVE locks must be bounded, and the bound must precede the first ALTER.
	lockAt := strings.Index(sql, "SET LOCAL lock_timeout = '5s'")
	require.NotEqual(t, -1, lockAt, "the migration must carry its own lock_timeout")
	require.Less(t, lockAt, strings.Index(sql, "ALTER TABLE groups"))
	// Nullable columns without a default stay a metadata-only change on a hot table.
	require.NotContains(t, sql, "service_tier_multiplier NUMERIC(10,4) NOT NULL")
	require.NotContains(t, sql, "service_tier_multiplier NUMERIC(10,4) DEFAULT")
}
