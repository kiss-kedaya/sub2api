package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupDailyResetModeMigration(t *testing.T) {
	content, err := FS.ReadFile("351_group_daily_reset_mode.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "SET LOCAL lock_timeout = '5s';")
	require.Contains(t, sql, "ALTER TABLE groups ADD COLUMN IF NOT EXISTS daily_reset_mode VARCHAR(20) NOT NULL DEFAULT 'calendar';")
	require.Contains(t, sql, "enqueue_group_auth_cache_invalidation")
	require.Contains(t, sql, "OLD.daily_reset_mode IS NOT DISTINCT FROM NEW.daily_reset_mode")
}
