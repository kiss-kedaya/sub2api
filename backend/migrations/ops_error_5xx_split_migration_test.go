package migrations

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 5xx 告警口径拆分迁移必须幂等、只做增量：
//   - 存量规则只归一 metric_type / 补空描述，不覆盖运维设置的阈值与开关；
//   - 种子规则用 ON CONFLICT (name) DO NOTHING，重跑不重复插入；
//   - 不删除、不 truncate 任何规则。
func TestOpsError5xxSplitMigration(t *testing.T) {
	content, err := FS.ReadFile("337_ops_error_5xx_split_v2.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")

	// 1) metric_type 归一：历史别名归到规范指标名。
	require.Contains(t, sql, "SET metric_type = 'gateway_5xx'")
	require.Contains(t, sql, "SET metric_type = 'upstream_5xx'")
	for _, alias := range []string{
		"'gateway_5xx_count'", "'gateway_5xx_total'", "'gateway_5xx_spike'",
		"'error_5xx_count'", "'total_5xx_count'", "'gateway5xx'",
		"'upstream_5xx_count'", "'upstream_5xx_total'", "'upstream5xx'",
	} {
		require.Contains(t, sql, alias, "历史别名 %s 必须被归一，避免存量规则永远不触发", alias)
	}

	// 2) 存量规则补描述只在空描述时生效，不能覆盖运维文案。
	require.Contains(t, sql, "WHERE metric_type = 'gateway_5xx' AND COALESCE(TRIM(description), '') = ''")
	require.Contains(t, sql, "WHERE metric_type = 'upstream_5xx' AND COALESCE(TRIM(description), '') = ''")

	// 3) 种子规则：网关自身 P1 发邮件，上游观察类降级且默认不发邮件。
	require.Contains(t, sql, "'网关自身 5xx 激增'")
	require.Contains(t, sql, "'上游 5xx 透传突增（观察）'")
	require.Contains(t, sql, "'上游 5xx 硬顶（观察）'")
	require.Contains(t, sql, "true, 'gateway_5xx', '>', 500.0, 5, 5, 'P1', true, 20")
	require.Contains(t, sql, "true, 'upstream_5xx_spike_ratio', '>', 3.0, 5, 10, 'P3', false, 60")
	require.Contains(t, sql, "true, 'upstream_5xx', '>', 30000.0, 5, 5, 'P2', false, 30")
	require.Equal(t, 3, strings.Count(sql, ") ON CONFLICT (name) DO NOTHING;"),
		"三条种子规则都必须幂等")

	// 4) 文案区分：网关自身 vs 上游透传，并提示 error_source 口径。
	require.Contains(t, sql, "【网关自身 5xx】")
	require.Contains(t, sql, "【上游 503 透传】")
	require.Contains(t, sql, "error_source<>upstream_http")
	require.Contains(t, sql, "error_source=upstream_http")

	// 5) 迁移只读规则表，不删数据、不改阈值/开关。
	lower := strings.ToLower(string(content))
	for _, forbidden := range []string{"delete from", "drop table", "truncate", "alter table ops_alert_rules", "set threshold", "set enabled"} {
		require.NotContains(t, lower, forbidden)
	}
}

// 338 是并发索引迁移：必须独立 _notx 文件、带 IF NOT EXISTS，且谓词与查询一致。
func TestOpsErrorLogs5xxTimeIndexMigration(t *testing.T) {
	content, err := FS.ReadFile("338_ops_error_logs_5xx_time_index_notx.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_error_logs_5xx_time_source")
	require.Contains(t, sql, "ON ops_error_logs (created_at, error_source)")
	require.Contains(t, sql, "WHERE COALESCE(status_code, 0) >= 500")
	require.Contains(t, sql, "COALESCE(is_count_tokens, false) = false")

	upper := strings.ToUpper(string(content))
	require.NotContains(t, upper, "BEGIN")
	require.NotContains(t, upper, "COMMIT")
	require.NotContains(t, upper, "ROLLBACK")
}

// 迁移文件必须按编号顺序排在 336 之后，且 337/338 都被嵌入。
func TestOpsError5xxSplitMigrationsAreEmbeddedInOrder(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	index337 := indexOf(names, "337_ops_error_5xx_split_v2.sql")
	require.GreaterOrEqual(t, index337, 0, "337 必须被 embed 进二进制（#564 后改名 v2，内容不变）")
	index338 := indexOf(names, "338_ops_error_logs_5xx_time_index_notx.sql")
	require.Greater(t, index338, index337, "338 必须排在 337 之后")

	previous := indexOf(names, "336_crypto_deposit_revoke_ledger.sql")
	require.GreaterOrEqual(t, previous, 0)
	require.Less(t, previous, index337, "337 必须排在 336 之后")
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
