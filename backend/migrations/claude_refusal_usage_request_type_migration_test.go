package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Claude 违规拒答的用量行 request_type=6：约束必须放宽到 <= 6，且 NOT VALID（大表不做全表校验）。
func TestClaudeRefusalUsageRequestTypeMigration(t *testing.T) {
	content, err := FS.ReadFile("341_allow_claude_refusal_usage_request_type.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS usage_logs_request_type_check")
	require.Contains(t, sql, "ADD CONSTRAINT usage_logs_request_type_check")
	require.Contains(t, sql, "CHECK (request_type >= 0 AND request_type <= 6) NOT VALID")
	require.NotContains(t, sql, "VALIDATE CONSTRAINT", "不在迁移里做全表校验")

	// 拿 ACCESS EXCLUSIVE 锁必须有上限，且设置要在第一条 ALTER 之前（SET LOCAL 只在迁移事务内生效）。
	lockAt := strings.Index(sql, "SET LOCAL lock_timeout = '5s'")
	require.NotEqual(t, -1, lockAt, "迁移必须自带 lock_timeout")
	require.Less(t, lockAt, strings.Index(sql, "ALTER TABLE usage_logs"))

	// 新迁移排在 188（上一次放宽到 <= 5）之后，重放时最终约束是 <= 6。
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)
	previousIndex, currentIndex := -1, -1
	for i, entry := range entries {
		switch entry.Name() {
		case "188_allow_live_usage_request_type.sql":
			previousIndex = i
		case "341_allow_claude_refusal_usage_request_type.sql":
			currentIndex = i
		}
	}
	require.NotEqual(t, -1, previousIndex)
	require.NotEqual(t, -1, currentIndex)
	require.Less(t, previousIndex, currentIndex)
}
