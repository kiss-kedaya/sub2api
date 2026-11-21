//go:build unit

package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// DDL 与 330 迁移执行后的表结构一致：position 只要求正序，不再有 <= 10 上限。
const groupRoutesRelaxedDDL = `
CREATE TABLE api_key_group_routes (
	api_key_id INTEGER NOT NULL,
	group_id   INTEGER NOT NULL,
	position   INTEGER NOT NULL CHECK (position >= 1),
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (api_key_id, group_id),
	UNIQUE (api_key_id, position)
)`

// DDL 与 239 迁移一致，用于复现 issue #494 的失败现场。
const groupRoutesLegacyDDL = `
CREATE TABLE api_key_group_routes (
	api_key_id INTEGER NOT NULL,
	group_id   INTEGER NOT NULL,
	position   INTEGER NOT NULL CHECK (position >= 1 AND position <= 10),
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (api_key_id, group_id),
	UNIQUE (api_key_id, position)
)`

func newGroupRoutesTestDB(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	return db
}

func readGroupRoutes(t *testing.T, db *sql.DB, apiKeyID int64) ([]int64, []int) {
	t.Helper()
	rows, err := db.Query(`
		SELECT group_id, position
		FROM api_key_group_routes
		WHERE api_key_id = $1
		ORDER BY position ASC`, apiKeyID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	groupIDs := make([]int64, 0)
	positions := make([]int, 0)
	for rows.Next() {
		var groupID int64
		var position int
		require.NoError(t, rows.Scan(&groupID, &position))
		groupIDs = append(groupIDs, groupID)
		positions = append(positions, position)
	}
	require.NoError(t, rows.Err())
	return groupIDs, positions
}

// 分组数超过 10（issue 里是 36 个）时，每个分组都要按顺序落库：
// position 1..N，不允许截断，也不允许跳号。
func TestReplaceGroupRoutesStoresEveryAvailableGroup(t *testing.T) {
	db := newGroupRoutesTestDB(t, groupRoutesRelaxedDDL)
	repo := &apiKeyRepository{sql: db}

	const groupCount = 50
	groupIDs := make([]int64, groupCount)
	for i := range groupIDs {
		groupIDs[i] = int64(1000 + i)
	}

	require.NoError(t, repo.ReplaceGroupRoutes(context.Background(), 7, groupIDs))

	gotIDs, gotPositions := readGroupRoutes(t, db, 7)
	require.Equal(t, groupIDs, gotIDs)
	require.Len(t, gotPositions, groupCount)
	for i, position := range gotPositions {
		require.Equal(t, i+1, position)
	}
}

// 替换是「先删后插」：上一次的分组多于本次时不能留下多余的尾部路由。
func TestReplaceGroupRoutesDropsStaleRoutesWhenListShrinks(t *testing.T) {
	db := newGroupRoutesTestDB(t, groupRoutesRelaxedDDL)
	repo := &apiKeyRepository{sql: db}

	require.NoError(t, repo.ReplaceGroupRoutes(context.Background(), 7, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}))
	require.NoError(t, repo.ReplaceGroupRoutes(context.Background(), 7, []int64{1, 2}))

	gotIDs, gotPositions := readGroupRoutes(t, db, 7)
	require.Equal(t, []int64{1, 2}, gotIDs)
	require.Equal(t, []int{1, 2}, gotPositions)
}

// 复现 issue #494：旧的 239 迁移约束下，第 11 个分组插入失败，
// 报 api_key_group_routes_position_check；事务回滚后不留半截路由。
func TestReplaceGroupRoutesLegacyPositionCeilingRejectsEleventhGroup(t *testing.T) {
	db := newGroupRoutesTestDB(t, groupRoutesLegacyDDL)
	repo := &apiKeyRepository{sql: db}

	groupIDs := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	err := repo.ReplaceGroupRoutes(context.Background(), 7, groupIDs)
	require.Error(t, err, "the legacy 239 schema must reject position 11")
	require.Contains(t, strings.ToLower(err.Error()), "check constraint")

	count := 0
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM api_key_group_routes WHERE api_key_id = 7`).Scan(&count))
	require.Zero(t, count, "a rejected replace must not leave partial routes behind")
}

// 同一把密钥被两个请求并发替换时，第二个写入会撞 UNIQUE(api_key_id, position)。
// 仓库层在有界重试后应成功收敛到后写者的分组列表，而不是把 500 抛给用户。
func TestReplaceGroupRoutesRetriesConcurrentPositionConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &apiKeyRepository{sql: db}

	deleteSQL := regexp.QuoteMeta("DELETE FROM api_key_group_routes WHERE api_key_id = $1")
	insertSQL := regexp.QuoteMeta("INSERT INTO api_key_group_routes (api_key_id, group_id, position)")
	conflict := &pq.Error{
		Code:       "23505",
		Constraint: "api_key_group_routes_api_key_id_position_key",
	}

	// 第一次：读到旧快照，DELETE 删不到并发写入的新行，INSERT position=1 撞唯一约束。
	mock.ExpectBegin()
	mock.ExpectExec(deleteSQL).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insertSQL).WithArgs(int64(7), int64(11), 1).WillReturnError(conflict)
	mock.ExpectRollback()

	// 重试：新事务里 DELETE 能看到并清掉赢家的行，随后按顺序写入本次列表。
	mock.ExpectBegin()
	mock.ExpectExec(deleteSQL).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insertSQL).WithArgs(int64(7), int64(11), 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(insertSQL).WithArgs(int64(7), int64(12), 2).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.ReplaceGroupRoutes(context.Background(), 7, []int64{11, 12}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// 主键冲突（同一次请求里出现重复 group_id）是真实客户端错误，不能被重试吞掉。
func TestReplaceGroupRoutesDoesNotRetryDuplicateGroupConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &apiKeyRepository{sql: db}

	deleteSQL := regexp.QuoteMeta("DELETE FROM api_key_group_routes WHERE api_key_id = $1")
	insertSQL := regexp.QuoteMeta("INSERT INTO api_key_group_routes (api_key_id, group_id, position)")

	mock.ExpectBegin()
	mock.ExpectExec(deleteSQL).WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(insertSQL).WithArgs(int64(7), int64(5), 1).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "api_key_group_routes_pkey"})
	mock.ExpectRollback()

	err = repo.ReplaceGroupRoutes(context.Background(), 7, []int64{5, 5})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet(), "a primary-key conflict must surface after a single attempt")
}
