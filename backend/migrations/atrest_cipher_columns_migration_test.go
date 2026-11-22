package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// at-rest 加密阶段一只加列：accounts.credentials_cipher / proxies.password_cipher。
// 数据迁移不在 SQL 里做（密钥只允许存在于部署 env），由新版二进制的
// -atrest-migrate 分批完成。加列必须向后兼容：旧二进制不读新列。
func TestAtRestCipherColumnsMigration(t *testing.T) {
	content, err := FS.ReadFile("334_add_atrest_cipher_columns.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE accounts ADD COLUMN IF NOT EXISTS credentials_cipher TEXT")
	require.Contains(t, sql, "ALTER TABLE proxies ADD COLUMN IF NOT EXISTS password_cipher TEXT")

	// 只加列、不加约束、不动数据；可重复执行。
	lower := strings.ToLower(string(content))
	require.NotContains(t, lower, "drop column")
	require.NotContains(t, lower, "not null")
	require.NotContains(t, lower, "update ")
	require.NotContains(t, lower, "delete ")
	require.NotContains(t, lower, "concurrent")
}
