package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 管理密钥只落不可逆摘要；范围、过期、IP 白名单与撤销时间都在库内，
// 列表隐藏通过 deleted_at 完成，历史审计记录永不被物理删除。
func TestScopedAdminAPIKeysMigration(t *testing.T) {
	content, err := FS.ReadFile("322_scoped_admin_api_keys.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS scoped_admin_api_keys")
	require.Contains(t, sql, "id VARCHAR(24) PRIMARY KEY")
	require.Contains(t, sql, "key_prefix VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "key_hash VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "enabled BOOLEAN NOT NULL DEFAULT TRUE")
	require.Contains(t, sql, "expires_at TIMESTAMPTZ")
	require.Contains(t, sql, "allowed_ips JSONB NOT NULL DEFAULT '[]'")
	require.Contains(t, sql, "policy JSONB NOT NULL DEFAULT '{}'")
	require.Contains(t, sql, "created_by BIGINT NOT NULL REFERENCES users(id)")
	require.Contains(t, sql, "last_used_at TIMESTAMPTZ")
	require.Contains(t, sql, "last_used_ip VARCHAR(64) NOT NULL DEFAULT ''")
	require.Contains(t, sql, "revoked_at TIMESTAMPTZ")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ")
	// 明文密钥、密文或可逆令牌都不允许出现在库表定义里。
	require.NotContains(t, strings.ToLower(sql), "key_secret")
	require.NotContains(t, strings.ToLower(sql), "key_plaintext")
}
