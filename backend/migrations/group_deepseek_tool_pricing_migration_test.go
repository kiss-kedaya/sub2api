package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupDeepSeekToolPricingMigrationIsSafeAndBackfillsLegacyCustomGroups(t *testing.T) {
	content, err := FS.ReadFile("345_group_deepseek_tool_pricing.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "SET LOCAL lock_timeout = '5s';")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS fetch_price_per_1k DECIMAL(20,8)")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS tool_rate_independent BOOLEAN NOT NULL DEFAULT false")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS tool_rate_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0")
	require.Contains(t, sql, "UPDATE groups SET tool_rate_independent = true, tool_rate_multiplier = 1.0 WHERE platform = 'deepseek'")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.fetch_price_per_1k")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.tool_rate_independent")
	require.Contains(t, sql, "COMMENT ON COLUMN groups.tool_rate_multiplier")
}
