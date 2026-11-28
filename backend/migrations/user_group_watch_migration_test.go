package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserGroupWatchSubscriptionsMigration(t *testing.T) {
	content, err := FS.ReadFile("350_user_group_watch_subscriptions.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.True(t, strings.Contains(sql, "SET LOCAL lock_timeout = '5s';"))
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_group_watch_subscriptions")
	require.Contains(t, sql, "PRIMARY KEY (user_id, group_id)")
	require.Contains(t, sql, "idx_user_group_watch_subs_group_id")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS group_monitor_watch_states")
	require.Contains(t, sql, "black_notified BOOLEAN NOT NULL DEFAULT FALSE")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_group_watch_notifications")
	require.Contains(t, sql, "delivered_channels JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "delivery_attempts SMALLINT NOT NULL DEFAULT 1")
	require.Contains(t, sql, "CONSTRAINT uq_user_group_watch_notifs_user_dedupe UNIQUE (user_id, dedupe_key)")
	require.Contains(t, sql, "idx_user_group_watch_notifs_user_unread")
}
