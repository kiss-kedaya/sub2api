//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// 迁移 343（user_security_preferences）与它的仓储在真实 PostgreSQL 上的行为：
// 表结构与约束、迁移可重复执行、带期望值的比较并交换在真正的并发下只有一个赢家。

// mustCreateCommittedPreferenceUser 创建一个已提交的用户（并发写者各自用独立连接，事务内的用户它们看不到）。
// 测试结束后硬删除：外键 ON DELETE CASCADE 会一并带走偏好行。
func mustCreateCommittedPreferenceUser(t *testing.T) int64 {
	t.Helper()
	u := mustCreateUser(t, integrationEntClient, &service.User{
		Email: fmt.Sprintf("user-security-pref-%d@example.com", time.Now().UnixNano()),
	})
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, u.ID)
	})
	return u.ID
}

func requireUserSecurityPreferenceSQLState(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var pqErr *pq.Error
	require.True(t, errors.As(err, &pqErr), "expected a postgres error, got %T: %v", err, err)
	require.Equal(t, code, string(pqErr.Code), "unexpected SQLSTATE: %v", err)
}

func TestUserSecurityPreferencesSchema(t *testing.T) {
	tx := testTx(t)

	requireColumn(t, tx, "user_security_preferences", "user_id", "bigint", 0, false)
	requireColumn(t, tx, "user_security_preferences", "api_key_create_verification", "text", 0, false)
	requireColumn(t, tx, "user_security_preferences", "updated_at", "timestamp with time zone", 0, false)
	requireColumnDefaultContains(t, tx, "user_security_preferences", "updated_at", "now()")
	// 主键 = user_id：一个用户最多一行，按用户查询走主键索引。
	requireIndex(t, tx, "user_security_preferences", "user_security_preferences_pkey")
	requireForeignKeyOnDelete(t, tx, "user_security_preferences", "user_id", "users", "CASCADE")

	rows, err := tx.QueryContext(context.Background(), `
SELECT pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = 'user_security_preferences'
  AND c.contype = 'c'`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var checks []string
	for rows.Next() {
		var def string
		require.NoError(t, rows.Scan(&def))
		checks = append(checks, def)
	}
	require.NoError(t, rows.Err())
	require.Len(t, checks, 1, "exactly one CHECK constraint")
	require.Contains(t, checks[0], "'on'")
	require.Contains(t, checks[0], "'off'")
	require.NotContains(t, checks[0], "auto", "auto = no row, never a stored value")
}

func TestUserSecurityPreferencesConstraintsOnPostgres(t *testing.T) {
	ctx := context.Background()
	userID := mustCreateCommittedPreferenceUser(t)
	insert := func(id int64, mode string) error {
		_, err := integrationDB.ExecContext(ctx,
			`INSERT INTO user_security_preferences (user_id, api_key_create_verification) VALUES ($1, $2)`, id, mode)
		return err
	}

	// 取值只允许 on / off：auto（没有行才是 auto）、空串、拼错、大小写不对都被 CHECK 拦下（23514）。
	for _, bad := range []string{"auto", "", "maybe", "ON", "On"} {
		requireUserSecurityPreferenceSQLState(t, insert(userID, bad), "23514")
	}
	require.NoError(t, insert(userID, "on"))
	// 一个用户只有一行（23505）。
	requireUserSecurityPreferenceSQLState(t, insert(userID, "off"), "23505")
	// 用户必须存在（23503）。
	requireUserSecurityPreferenceSQLState(t, insert(userID+1_000_000_000, "on"), "23503")

	var updatedAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT updated_at FROM user_security_preferences WHERE user_id = $1`, userID).Scan(&updatedAt))
	require.WithinDuration(t, time.Now(), updatedAt, time.Minute, "updated_at defaults to NOW()")

	// 用户被删除时偏好行级联清掉，不留孤儿行。
	_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	require.NoError(t, err)
	var remaining int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_security_preferences WHERE user_id = $1`, userID).Scan(&remaining))
	require.Zero(t, remaining)
}

// 发版会在旧二进制还在线时先跑迁移，之后任何一次重放（-migrate-only 重跑、回滚后再发）都不能报错、不能动数据。
func TestUserSecurityPreferencesMigrationCanBeReplayed(t *testing.T) {
	ctx := context.Background()
	userID := mustCreateCommittedPreferenceUser(t)
	_, err := integrationDB.ExecContext(ctx,
		`INSERT INTO user_security_preferences (user_id, api_key_create_verification) VALUES ($1, 'off')`, userID)
	require.NoError(t, err)

	content, err := migrations.FS.ReadFile("343_user_security_preferences.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, string(content))
		require.NoError(t, err, "replay %d", i+1)
		require.NoError(t, tx.Commit())
	}
	require.NoError(t, ApplyMigrations(ctx, integrationDB), "the whole runner stays idempotent")

	var mode string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT api_key_create_verification FROM user_security_preferences WHERE user_id = $1`, userID).Scan(&mode))
	require.Equal(t, "off", mode, "a replay must not touch existing rows")
}

func TestUserSecurityPreferenceRepositoryOnPostgres(t *testing.T) {
	ctx := context.Background()
	repo := NewUserSecurityPreferenceRepository(integrationDB)
	userID := mustCreateCommittedPreferenceUser(t)

	cas := func(expected, next string) bool {
		t.Helper()
		ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, expected, next)
		require.NoError(t, err)
		return ok
	}
	current := func() string {
		t.Helper()
		mode, err := repo.GetAPIKeyCreateVerificationMode(ctx, userID)
		require.NoError(t, err)
		return mode
	}
	updatedAt := func() time.Time {
		t.Helper()
		var at time.Time
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			`SELECT updated_at FROM user_security_preferences WHERE user_id = $1`, userID).Scan(&at))
		return at
	}

	require.Empty(t, current(), "no row = auto")
	require.True(t, cas("", "on"))
	require.Equal(t, "on", current())
	// 以为没有行，其实已经有了：失败，原值保留。
	require.False(t, cas("", "off"))
	require.Equal(t, "on", current())

	first := updatedAt()
	time.Sleep(5 * time.Millisecond)
	require.True(t, cas("on", "off"))
	require.Equal(t, "off", current())
	require.True(t, updatedAt().After(first), "updated_at moves on every write")

	// 读到旧值的写者：以为是 on（其实已经是 off）→ 改不动、也删不掉。
	require.False(t, cas("on", "off"))
	require.False(t, cas("on", ""))
	require.Equal(t, "off", current())

	require.True(t, cas("off", ""))
	require.Empty(t, current())
	// 以为是 off（其实已被删掉）→ 不会凭空造出一行。
	require.False(t, cas("off", "on"))
	require.Empty(t, current())
	require.True(t, cas("", ""))
	require.True(t, cas("", "on"))
	require.False(t, cas("", ""))

	var rows int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_security_preferences WHERE user_id = $1`, userID).Scan(&rows))
	require.Equal(t, 1, rows)
}

// 真实并发（每个写者各用连接池里的一条连接）：同一个期望值同时被很多写者使用，只有一个赢家；
// 输家看到的是 (false, nil)，由服务层重新读取再判断。这是「读到旧值的请求不能把别人刚收紧的设置改回宽松」
// 在数据库一侧的保证。
func TestUserSecurityPreferenceRepositoryOnlyOneConcurrentWriterWinsOnPostgres(t *testing.T) {
	ctx := context.Background()
	repo := NewUserSecurityPreferenceRepository(integrationDB)
	userID := mustCreateCommittedPreferenceUser(t)

	race := func(expected string, targets ...string) (wins int32, final string) {
		const writers = 32
		var (
			won   atomic.Int32
			wg    sync.WaitGroup
			start = make(chan struct{})
		)
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(next string) {
				defer wg.Done()
				<-start
				ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, expected, next)
				if err == nil && ok {
					won.Add(1)
				}
			}(targets[i%len(targets)])
		}
		close(start)
		wg.Wait()
		mode, err := repo.GetAPIKeyCreateVerificationMode(ctx, userID)
		require.NoError(t, err)
		return won.Load(), mode
	}

	wins, final := race("", "on")
	require.EqualValues(t, 1, wins, "insert race")
	require.Equal(t, "on", final)

	// 不同写者想把同一个旧值改成不同的新值：总共仍然只有一个赢。
	wins, final = race("on", "off", "")
	require.EqualValues(t, 1, wins, "update/delete race from the same expectation")
	require.Contains(t, []string{"off", ""}, final)

	// 没有并发写者了：把状态收敛到确定的 off，再比一轮删除。
	if final != "off" {
		ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, final, "off")
		require.NoError(t, err)
		require.True(t, ok)
	}
	wins, final = race("off", "")
	require.EqualValues(t, 1, wins, "delete race")
	require.Empty(t, final)
}
