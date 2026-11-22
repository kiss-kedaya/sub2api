//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 332 迁移建立「密钥轮换风险确认」表：一次确认有有效期、绑定当时的 gate cutoff，
// 供门禁在放行充值/兑换时快速校验。
func TestMigration332CreatesKeyRotationRiskAcks(t *testing.T) {
	content, err := FS.ReadFile("332_key_rotation_risk_acks.sql")
	require.NoError(t, err)
	sql := string(content)

	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS key_rotation_risk_acks")
	for _, column := range []string{
		"user_id BIGINT NOT NULL",
		"acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"expires_at TIMESTAMPTZ NOT NULL",
		"client_ip TEXT NOT NULL DEFAULT ''",
		"user_agent TEXT NOT NULL DEFAULT ''",
		"cutoff_snapshot TIMESTAMPTZ NOT NULL",
		"created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
	} {
		require.Contains(t, sql, column)
	}

	require.Contains(t, sql, "CREATE INDEX IF NOT EXISTS idx_key_rotation_risk_acks_user")
	require.Contains(t, sql, "ON key_rotation_risk_acks (user_id, expires_at DESC)")

	// append-only：不得回填/改写已有确认记录。
	code := strings.ToUpper(stripSQLLineComments(sql))
	require.NotContains(t, code, "UPDATE KEY_ROTATION_RISK_ACKS")
	require.NotContains(t, code, "DELETE FROM KEY_ROTATION_RISK_ACKS")
}
