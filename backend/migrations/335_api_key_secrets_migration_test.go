package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 335 加的是一张只存浏览器端密文的新表：服务端不存明文、不收密码，
// 拖库也解不出 Key。迁移必须是纯增量且幂等的（生产以 validate 模式启动，
// 迁移由发版前的 -migrate-only 执行）。
func TestMigration335APIKeySecrets(t *testing.T) {
	content, err := FS.ReadFile("335_api_key_secrets.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS api_key_secrets (")
	require.Contains(t, sql, "id BIGSERIAL PRIMARY KEY")
	require.Contains(t, sql, "api_key_id BIGINT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE")
	require.Contains(t, sql, "user_id BIGINT NOT NULL")
	require.Contains(t, sql, "version INT NOT NULL DEFAULT 1")
	require.Contains(t, sql, "kdf JSONB NOT NULL")
	require.Contains(t, sql, "sealed TEXT NOT NULL")
	require.Contains(t, sql, "created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")
	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_api_key_secrets_user ON api_key_secrets(user_id)")

	// 这张表只能放密文：不允许出现明文 key / hash 列，也不允许搬动 api_keys。
	lower := strings.ToLower(sql)
	for _, forbidden := range []string{"drop table", "truncate", "delete from", "update api_keys", "alter table api_keys", " key text", "key_hash"} {
		require.NotContains(t, lower, forbidden)
	}
}
