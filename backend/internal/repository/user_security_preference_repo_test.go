package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// user_security_preferences（迁移 343）：每个用户最多一行，没有行 = auto。
// 写入是带期望值的比较并交换，这里用 SQLite（与生产同一套 SQL 文本）覆盖语义；
// 真实 PostgreSQL 上的表结构 / 约束 / 并发抢占由 user_security_preference_repo_integration_test.go 覆盖。

func newUserSecurityPreferenceSQLite(t *testing.T) (*userSecurityPreferenceRepository, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:user_security_pref_%d?mode=memory&cache=shared", userSecurityPrefDBSeq.Add(1)))
	require.NoError(t, err)
	// 内存库 + 共享缓存下多个连接并发写会撞 SQLITE_LOCKED，单连接串行化；并发抢占的语义由 PostgreSQL 集成测试覆盖。
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE user_security_preferences (
		user_id INTEGER PRIMARY KEY,
		api_key_create_verification TEXT NOT NULL CHECK (api_key_create_verification IN ('on', 'off')),
		updated_at TIMESTAMP NOT NULL
	)`)
	require.NoError(t, err)
	return &userSecurityPreferenceRepository{db: db}, db
}

var userSecurityPrefDBSeq atomic.Int64

func TestUserSecurityPreferenceRepositoryRoundTrip(t *testing.T) {
	repo, db := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()
	const userID = int64(7)

	// 没有行：auto（空串）。
	mode, err := repo.GetAPIKeyCreateVerificationMode(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, mode)

	// 没有行 → on：插入。
	ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, "", service.APIKeyCreateVerificationOn)
	require.NoError(t, err)
	require.True(t, ok)
	mode, err = repo.GetAPIKeyCreateVerificationMode(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, "on", mode)

	// on → off：更新，仍然只有一行。
	ok, err = repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, "on", "off")
	require.NoError(t, err)
	require.True(t, ok)
	mode, err = repo.GetAPIKeyCreateVerificationMode(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, "off", mode)
	var rows int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_security_preferences`).Scan(&rows))
	require.Equal(t, 1, rows)

	// off → 恢复默认：删行。
	ok, err = repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, userID, "off", "")
	require.NoError(t, err)
	require.True(t, ok)
	mode, err = repo.GetAPIKeyCreateVerificationMode(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, mode)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM user_security_preferences`).Scan(&rows))
	require.Zero(t, rows)
}

// 期望值对不上就不写：这是「读到旧值的请求不能覆盖别人刚做的修改」的数据库一侧保证。
func TestUserSecurityPreferenceRepositoryCompareAndSetRefusesStaleExpectations(t *testing.T) {
	repo, _ := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()
	const userID = int64(7)

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

	// 以为没有行，其实已经有了（被别人抢先插入）：插入失败，别人的值保留。
	require.True(t, cas("", "on"))
	require.False(t, cas("", "off"))
	require.Equal(t, "on", current())

	// 以为是 off，其实是 on：更新失败、删除失败。
	require.False(t, cas("off", "on"))
	require.False(t, cas("off", ""))
	require.Equal(t, "on", current())

	// 以为是 on，其实已经被删掉了：更新失败（不会凭空造出一行）、删除失败。
	require.True(t, cas("on", ""))
	require.False(t, cas("on", "off"))
	require.False(t, cas("on", ""))
	require.Empty(t, current())

	// 「没有行 → 没有行」只在确实没有行时算成功。
	require.True(t, cas("", ""))
	require.True(t, cas("", "off"))
	require.False(t, cas("", ""))
	require.Equal(t, "off", current())
}

func TestUserSecurityPreferenceRepositoryIsPerUser(t *testing.T) {
	repo, _ := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()

	ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 7, "", "off")
	require.NoError(t, err)
	require.True(t, ok)

	other, err := repo.GetAPIKeyCreateVerificationMode(ctx, 8)
	require.NoError(t, err)
	require.Empty(t, other, "another user is untouched")

	// 别人的 on 不能被「我以为是 off」的请求改掉，也删不掉。
	ok, err = repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 8, "", "on")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 7, "on", "")
	require.NoError(t, err)
	require.False(t, ok)
	mode, err := repo.GetAPIKeyCreateVerificationMode(ctx, 8)
	require.NoError(t, err)
	require.Equal(t, "on", mode)
}

func TestUserSecurityPreferenceRepositoryRejectsInvalidArguments(t *testing.T) {
	repo, _ := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name           string
		userID         int64
		expected, next string
	}{
		{"zero user", 0, "", "on"},
		{"negative user", -1, "", "on"},
		{"auto is not a stored value", 7, "", "auto"},
		{"unknown next", 7, "", "maybe"},
		{"unknown expected", 7, "maybe", "on"},
		{"upper-case is not normalized here", 7, "", "ON"},
	} {
		ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, tc.userID, tc.expected, tc.next)
		require.Error(t, err, tc.name)
		require.False(t, ok, tc.name)
	}
	_, err := repo.GetAPIKeyCreateVerificationMode(ctx, 0)
	require.Error(t, err)

	var nilRepo *userSecurityPreferenceRepository
	_, err = nilRepo.GetAPIKeyCreateVerificationMode(ctx, 7)
	require.Error(t, err)
	ok, err := nilRepo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 7, "", "on")
	require.Error(t, err)
	require.False(t, ok)

	// 存储层的 CHECK 约束是第二道防线：绕过仓储直接写一个不合法的值会被数据库拒绝。
	_, db := newUserSecurityPreferenceSQLite(t)
	_, err = db.Exec(`INSERT INTO user_security_preferences (user_id, api_key_create_verification, updated_at) VALUES (1, 'auto', CURRENT_TIMESTAMP)`)
	require.Error(t, err)
}

// 数据库错误如实向上返回（调用方 fail-closed），不吞掉也不当成「没有行」。
func TestUserSecurityPreferenceRepositorySurfacesStorageErrors(t *testing.T) {
	repo, db := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()
	require.NoError(t, db.Close())

	_, err := repo.GetAPIKeyCreateVerificationMode(ctx, 7)
	require.Error(t, err)
	ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 7, "", "on")
	require.Error(t, err)
	require.False(t, ok)
}

// 串行化的并发抢占（单连接）：同一个期望值同时被多个写者使用，只能有一个赢。
func TestUserSecurityPreferenceRepositoryOnlyOneWriterWinsAnExpectation(t *testing.T) {
	repo, _ := newUserSecurityPreferenceSQLite(t)
	ctx := context.Background()

	race := func(expected, next string) int {
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := repo.CompareAndSetAPIKeyCreateVerificationMode(ctx, 7, expected, next)
				if err == nil && ok {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		return int(wins.Load())
	}
	require.Equal(t, 1, race("", "on"))
	require.Equal(t, 1, race("on", "off"))
	require.Equal(t, 1, race("off", ""))
}
