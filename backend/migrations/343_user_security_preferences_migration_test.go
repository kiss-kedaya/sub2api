package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 343 新建 user_security_preferences：用户自己决定「创建 API Key 时是否要验证身份」。
// 约束（发版安全）：纯增量、不回填、不碰别的表、拿 users 的外键锁要有上限。
// 迁移必须可重复执行（生产以 validate 模式启动，迁移由发版前的 -migrate-only 执行）。
func TestMigration343UserSecurityPreferences(t *testing.T) {
	raw, err := FS.ReadFile("343_user_security_preferences.sql")
	require.NoError(t, err)

	// 注释里会出现 update / delete 之类的字样，断言只针对实际执行的语句。
	statements := strings.Join(strings.Fields(regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(raw), "")), " ")
	lower := strings.ToLower(statements)

	// 表结构：一个用户一行（主键 = user_id），随用户删除而级联清掉，取值只允许 on / off。
	require.Contains(t, statements, "CREATE TABLE IF NOT EXISTS user_security_preferences (")
	require.Contains(t, statements, "user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE")
	require.Contains(t, statements, "api_key_create_verification TEXT NOT NULL CHECK (api_key_create_verification IN ('on', 'off'))")
	require.Contains(t, statements, "updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()")

	// 「auto」= 没有这一行，所以约束里不能有 auto，也不能给任何默认值。
	require.NotContains(t, lower, "'auto'")
	require.NotContains(t, lower, "api_key_create_verification text not null default")

	// 纯增量：不回填、不改存量、不碰 users / api_keys 这些热表，整个文件只新建这一张表。
	for _, forbidden := range []string{"alter table", "drop ", "truncate", "insert into", "update ", "delete from", "create index", "create trigger"} {
		require.NotContains(t, lower, forbidden, "343 must stay a pure additive create-table migration")
	}
	require.Equal(t, 1, strings.Count(lower, "create table"), "one table per migration")
	// 注意 user_security_preferences 本身含有 "references" 子串，所以按单词边界数。
	require.Len(t, regexp.MustCompile(`\breferences\b`).FindAllString(lower, -1), 1, "the only foreign key is user_id -> users(id)")

	// 外键要对 users 拿一瞬间的 SHARE ROW EXCLUSIVE 锁：必须自带 5 秒上限，且设置在 CREATE TABLE 之前
	// （迁移在事务里跑，SET LOCAL 只在该事务内生效；拿不到锁就整个回滚，发版在换二进制之前中止）。
	lockAt := strings.Index(statements, "SET LOCAL lock_timeout = '5s'")
	require.NotEqual(t, -1, lockAt, "the migration must carry its own lock_timeout")
	require.Less(t, lockAt, strings.Index(statements, "CREATE TABLE"))

	// 可重复执行：重放不报错、不丢数据（IF NOT EXISTS，且没有 DROP）。
	require.NotContains(t, lower, "create table user_security_preferences")
}

// 343 排在已有最新迁移（342）之后，且编号没有被别的迁移占用：
// 同一个编号出现两个文件会让「谁先跑」取决于文件名字母序，发版审计要求编号唯一指向本表。
func TestMigration343IsOrderedAfter342AndOwnsItsNumber(t *testing.T) {
	entries, err := FS.ReadDir(".")
	require.NoError(t, err)

	var with343 []string
	index342, index343 := -1, -1
	for i, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "343_") {
			with343 = append(with343, name)
		}
		switch name {
		case "342_usage_log_service_tier_multiplier.sql":
			index342 = i
		case "343_user_security_preferences.sql":
			index343 = i
		}
	}
	require.Equal(t, []string{"343_user_security_preferences.sql"}, with343)
	require.NotEqual(t, -1, index342)
	require.NotEqual(t, -1, index343)
	require.Less(t, index342, index343)
}
