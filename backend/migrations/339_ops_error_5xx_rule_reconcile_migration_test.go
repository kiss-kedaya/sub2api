package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 339 是 337 口径拆分的收尾迁移，发版审计的两个问题都在这里收口：
//
//	A) 337 只归一了别名，本来就叫 gateway_5xx 的标准名存量规则没被处理，
//	   阈值仍是旧「全部 5xx」量级 → 拆分后永远打不到 → 网关 5xx 告警静默；
//	B) 337 的 SET LOCAL 在事务外是 no-op（PostgreSQL 事务外 SET LOCAL 只作用于
//	   当前事务，隐式事务里等于什么都没做），锁等待超时完全不生效。
func TestOpsError5xxRuleReconcileMigration(t *testing.T) {
	content, err := FS.ReadFile("339_ops_error_5xx_rule_reconcile.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	lower := strings.ToLower(sql)
	// 注释里同样会出现 SET LOCAL 字样，断言只针对实际执行的语句。
	statements := strings.ToLower(stripSQLComments(string(content)))

	// 1) 会话级超时：必须是 SET（会话级），不能是 SET LOCAL（事务外 no-op）。
	require.Contains(t, statements, "set lock_timeout = '5s';")
	require.Contains(t, statements, "set statement_timeout = '10min';")
	require.NotContains(t, statements, "set local",
		"迁移在隐式事务里跑，SET LOCAL 等于 no-op，必须用会话级 SET")

	// 2) 标准名存量规则必须被处理：标记 + 禁用，且以「未打标记」为条件保证幂等。
	require.Contains(t, sql, "WHERE metric_type = 'gateway_5xx'")
	require.Contains(t, sql, "COALESCE(description, '') NOT LIKE '%口径已拆分%'")
	require.Contains(t, sql, "口径已拆分：本条仅统计网关自产 5xx，阈值需按新量级重设")
	// 被处置的规则必须落成 enabled=false（这是「不静默误判」的关键）。
	// 另一处 enabled = false 出现在 WHERE 里，用于兼容运维已手工禁用的规则，
	// 因此断言「追加标记 + 禁用」出现在同一条 UPDATE 里，而不是只数出现次数。
	require.Regexp(t, `(?s)update ops_alert_rules\s+set description = coalesce\(description, ''\) .*?口径已拆分.*?enabled = false,`,
		statements, "启用中的 gateway_5xx 规则必须被禁用并追加口径标记")

	// 3) 不改用户阈值：全程不得出现改阈值 / 改 severity / 改窗口的赋值。
	//    注意 threshold = 也会出现在改名用的 WHERE 匹配条件里（那是读不是写），
	//    所以判据是「UPDATE 的 SET 子句里出现这些列」。
	setClauses := regexp.MustCompile(`(?s)\bset\b(.*?)\bwhere\b`).FindAllString(statements, -1)
	require.NotEmpty(t, setClauses, "应当能定位到 UPDATE 的 SET 子句")
	for _, clause := range setClauses {
		for _, forbidden := range []string{
			"threshold", "operator", "severity", "window_minutes", "sustained_minutes",
			"notify_email", "cooldown_minutes", "filters",
		} {
			require.NotContains(t, clause, forbidden,
				"迁移不得在 SET 子句里改动运维设置的规则字段：%s", forbidden)
		}
	}

	// 4) 不删数据、不改表结构。
	for _, forbidden := range []string{"delete from", "drop table", "truncate", "drop column", "alter table"} {
		require.NotContains(t, lower, forbidden, "迁移不得删除数据或改表结构：%s", forbidden)
	}

	// 5) 种子规则改用独立名字，且仍是 ON CONFLICT (name) DO NOTHING 幂等。
	seeds := []string{
		"'网关自身 5xx 激增（默认种子）'",
		"'上游 5xx 透传突增（观察·默认种子）'",
		"'上游 5xx 硬顶（观察·默认种子）'",
	}
	for _, seed := range seeds {
		require.Contains(t, sql, seed, "种子规则必须使用独立名字：%s", seed)
	}
	require.Equal(t, 3, strings.Count(sql, ") ON CONFLICT (name) DO NOTHING;"),
		"三条种子规则都必须幂等")
	require.Equal(t, 3, strings.Count(sql, "NOT EXISTS ( SELECT 1 FROM ops_alert_rules AS taken"),
		"三条种子改名都必须先确认新名字未被占用")

	// 6) 改名只认种子默认值：用户改过阈值的同名规则不能被改名/覆盖。
	require.Contains(t, sql, "AND metric_type = 'gateway_5xx' AND threshold = 500.0")
	require.Contains(t, sql, "AND metric_type = 'upstream_5xx_spike_ratio' AND threshold = 3.0")
	require.Contains(t, sql, "AND metric_type = 'upstream_5xx' AND threshold = 30000.0")
}

// 337 已经随 #564 发版并在现网执行过，因此不允许原地改内容（checksum 校验会拦住
// 所有升级实例）。修法放在 339，337 只改名，由 renamedMigrations 认历史名字。
func TestOpsError5xxSplitMigrationRenamedNotEdited(t *testing.T) {
	renamed, err := FS.ReadFile("337_ops_error_5xx_split_v2.sql")
	require.NoError(t, err)
	_, err = FS.ReadFile("337_ops_error_5xx_split.sql")
	require.Error(t, err, "旧文件名 337_ops_error_5xx_split.sql 必须已改名")

	sql := strings.Join(strings.Fields(string(renamed)), " ")

	// 内容保持 #564 发版时的原样：仍只归一别名、仍带（已失效的）SET LOCAL，
	// 处置逻辑一律不在这里新增，避免「同一个 migration 两种行为」。
	require.Contains(t, sql, "SET LOCAL lock_timeout = '5s'")
	require.Contains(t, sql, "SET LOCAL statement_timeout = '10min'")
	require.Contains(t, sql, "SET metric_type = 'gateway_5xx'")
	require.Contains(t, sql, "SET metric_type = 'upstream_5xx'")
	require.NotContains(t, sql, "口径已拆分", "337 不得新增处置逻辑，改动必须放进 339")
	require.NotContains(t, sql, "默认种子")
}

// 339 必须排在 337/338 之后并被 embed 进二进制，且不得含 CONCURRENTLY
// （否则会被执行器判成 *_notx 专用语句而拒绝执行）。
func TestOpsError5xxRuleReconcileMigrationIsLastAndEmbedded(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}

	index339 := indexOf(names, "339_ops_error_5xx_rule_reconcile.sql")
	require.GreaterOrEqual(t, index339, 0, "339 必须被 embed 进二进制")
	require.Greater(t, index339, indexOf(names, "337_ops_error_5xx_split_v2.sql"), "339 必须排在 337 之后")
	require.Greater(t, index339, indexOf(names, "338_ops_error_logs_5xx_time_index_notx.sql"), "339 必须排在 338 之后")

	content, err := FS.ReadFile("339_ops_error_5xx_rule_reconcile.sql")
	require.NoError(t, err)
	require.NotContains(t, strings.ToUpper(stripSQLComments(string(content))), "CONCURRENTLY")
}

// stripSQLComments 去掉整行 -- 注释，只留会被执行器真正执行的 SQL 文本。
func stripSQLComments(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
