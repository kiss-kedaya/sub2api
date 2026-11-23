package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 337 给 api_key_secrets 增加 auth_factor：记录「创建时用哪个因子授权封存」，
// 二次查看时要求同一因子。它只是授权方式元数据，不是解密材料。
//
// 迁移要求纯增量、幂等：生产以 validate 模式启动，迁移由发版前 -migrate-only
// 单独执行，所以这里只断言 SQL 形状，不实际连库。
func TestMigration337APIKeySecretAuthFactor(t *testing.T) {
	content, err := FS.ReadFile("337_api_key_secret_auth_factor.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	lower := strings.ToLower(sql)

	require.Contains(t, sql, "ALTER TABLE api_key_secrets")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS auth_factor VARCHAR(16) DEFAULT NULL")
	require.Contains(t, sql, "COMMENT ON COLUMN api_key_secrets.auth_factor IS")

	// 只允许加一列元数据：不得动其它表、不得删/改已有列、不得碰密文本体。
	for _, forbidden := range []string{
		"drop table", "drop column", "truncate", "delete from",
		"alter table api_keys", "alter table users",
		"update api_key_secrets set sealed", "add column sealed", "add column kdf",
	} {
		require.NotContains(t, lower, forbidden)
	}
}
