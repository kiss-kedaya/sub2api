package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2CNPlatformsMigration(t *testing.T) {
	content, err := FS.ReadFile("240_channel_monitor_v2_cn_platforms.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, `"platform":"kimi"`)
	require.Contains(t, sql, `"platform":"zhipu"`)
	require.Contains(t, sql, `"platform":"deepseek"`)
	require.Contains(t, sql, "channel_monitor_v2_config")
	require.Contains(t, sql, `platforms @> '[{"platform":"kimi"}]'`)
}
