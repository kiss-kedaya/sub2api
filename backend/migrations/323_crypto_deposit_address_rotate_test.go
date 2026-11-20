package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 轮换保留历史行：同一 (user_id, chain) 只允许一条 active，旧地址退成
// disabled 历史继续被扫描器监控，所以唯一约束要变成 active 部分唯一索引。
func TestCryptoDepositAddressRotateMigration(t *testing.T) {
	content, err := FS.ReadFile("323_crypto_deposit_address_rotate.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE deposit_addresses DROP CONSTRAINT IF EXISTS uq_deposit_addr_user_chain")
	require.Contains(t, sql, "DROP INDEX IF EXISTS uq_deposit_addr_user_chain")
	require.Contains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_addr_user_chain_active")
	require.Contains(t, sql, "ON deposit_addresses (user_id, chain) WHERE status = 'active'")
	// 旧的全量唯一约束不能再以任何形式复活（否则第二条历史行插不进去）。
	require.NotContains(t, sql, "CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_addr_user_chain ON")
}
