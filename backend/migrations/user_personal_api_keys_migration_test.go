package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserPersonalAPIKeysMigration(t *testing.T) {
	content, err := FS.ReadFile("349_user_personal_api_keys.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "SET LOCAL lock_timeout = '5s';")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS user_personal_api_keys")
	require.Contains(t, sql, "user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE")
	require.Contains(t, sql, "key_prefix VARCHAR(32) NOT NULL")
	require.Contains(t, sql, "key_hash CHAR(64) NOT NULL UNIQUE")
	require.Contains(t, sql, "last_used_at TIMESTAMPTZ")
	require.Contains(t, sql, "last_used_ip VARCHAR(64) NOT NULL DEFAULT ''")
	// 严禁在表结构中存放明文或可逆密文列。
	lower := strings.ToLower(sql)
	require.NotContains(t, lower, "key_plaintext")
	require.NotContains(t, lower, "key_secret")
}
