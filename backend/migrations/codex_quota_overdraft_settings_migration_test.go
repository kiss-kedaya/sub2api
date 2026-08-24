package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodexQuotaOverdraftSettingsMigrationIsAdditive(t *testing.T) {
	content, err := FS.ReadFile("231_codex_quota_overdraft_settings.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "INSERT INTO settings (key, value)")
	require.Contains(t, sql, "('codex_quota_overdraft_enabled', 'true')")
	require.Contains(t, sql, "('codex_quota_overdraft_business_injection_enabled', 'false')")
	require.Contains(t, strings.ToUpper(sql), "ON CONFLICT (KEY) DO NOTHING")
	// The migration must never reset a value that an administrator already set.
	require.NotContains(t, strings.ToUpper(sql), "DO UPDATE")
	require.NotContains(t, strings.ToUpper(sql), "UPDATE SETTINGS")
}
