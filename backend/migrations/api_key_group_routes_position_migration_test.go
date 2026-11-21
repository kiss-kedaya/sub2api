//go:build unit

package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// issue #494：智能路由密钥必须能绑定全部可用分组。
// 330 迁移只保留 position >= 1 的顺序约束，去掉 239 的 10 个上限。
func TestMigration330DropsTenGroupPositionCeiling(t *testing.T) {
	content, err := FS.ReadFile("330_api_key_group_routes_unbounded_position.sql")
	require.NoError(t, err)
	sql := string(content)

	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS api_key_group_routes_position_check")
	require.Contains(t, sql, "ADD CONSTRAINT api_key_group_routes_position_check")
	require.Contains(t, sql, "CHECK (position >= 1)")

	// 注释里可以记录历史原因，可执行语句里不能再出现 10 个位置的上限。
	code := stripSQLLineComments(sql)
	require.NotContains(t, code, "<= 10", "the 10-group ceiling must be gone from the executable DDL")
	require.NotContains(t, code, "position <= 10")
}

func stripSQLLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// 迁移必须幂等（可重复执行）并且能规范线上已经被手工放宽过的同名约束。
func TestMigration330IsIdempotent(t *testing.T) {
	content, err := FS.ReadFile("330_api_key_group_routes_unbounded_position.sql")
	require.NoError(t, err)
	sql := string(content)

	require.Contains(t, sql, "DO $$")
	require.Contains(t, sql, "FROM pg_constraint")
	require.Contains(t, sql, "conname = 'api_key_group_routes_position_check'")
	require.Contains(t, sql, "IF NOT EXISTS")
	// 不碰数据：没有回填/更新语句，已有 1..10 路由原样保留。
	require.False(t, strings.Contains(strings.ToUpper(sql), "UPDATE API_KEY_GROUP_ROUTES"))
	require.False(t, strings.Contains(strings.ToUpper(sql), "DELETE FROM API_KEY_GROUP_ROUTES"))
}
