package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 账号错误率断路器把账号写成临时不可调度，复用的就是这个方法。两条契约必须钉住：
//   - until 只会变大（SQL 里 temp_unschedulable_until < $1 才更新）：已有的更长的手动/规则冷却不会被缩短；
//   - 只写 temp_unschedulable_*（和 updated_at），不碰 schedulable / status：手动停用不会被覆盖或「救活」。
func TestAccountRepository_SetTempUnschedulable_OnlyExtendsAndOnlyTouchesTempUnschedFields(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)

	err := repo.SetTempUnschedulable(context.Background(), 42, time.Now().Add(time.Minute), "circuit_breaker: 100.0% of 20 upstream attempts failed in 5m")
	require.NoError(t, err)
	require.NotEmpty(t, exec.execQueries)

	update := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, update, "UPDATE accounts SET temp_unschedulable_until = $1, temp_unschedulable_reason = $2, updated_at = NOW() WHERE id = $3")
	require.Contains(t, update, "AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until < $1)",
		"the write must be max(existing, new): a longer cooldown that is already in place must never be shortened")
	require.Contains(t, update, "deleted_at IS NULL")

	// SET 子句里只有这三列
	setClause := update[strings.Index(update, "SET ")+4 : strings.Index(update, " WHERE ")]
	require.Equal(t, "temp_unschedulable_until = $1, temp_unschedulable_reason = $2, updated_at = NOW()", setClause)

	// 同一次写入把变更事件写进 scheduler outbox，调度快照随之更新（选号/粘连清理/后台列表都认它）。
	require.Contains(t, strings.Join(exec.execQueries, "\n"), "scheduler_outbox")
}

func TestAccountRepository_SetTempUnschedulable_NoopWhenNotExtendingSkipsOutbox(t *testing.T) {
	// 已有的冷却更长 -> 0 行受影响：不写 outbox、不报错（幂等，多进程同时熔断同一账号结果一致）。
	exec := &recordingSQLExecutor{result: rowsAffectedResult(0)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	require.NoError(t, repo.SetTempUnschedulable(context.Background(), 42, time.Now().Add(time.Minute), "circuit_breaker: x"))
	require.Len(t, exec.execQueries, 1)
	require.NotContains(t, exec.execQueries[0], "scheduler_outbox")
}

// 断路器冷却到期时没有任何事件会让调度快照重建这个账号所在的桶（构建桶的 SQL 会排除
// temp_unschedulable_until 还没到期的账号）：RefreshSchedulerAccount 只往 outbox 写一条账号变更事件，不改 accounts 表。
func TestAccountRepository_RefreshSchedulerAccount_OnlyEnqueuesAnAccountChangedEvent(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)

	require.NoError(t, repo.RefreshSchedulerAccount(context.Background(), 42))
	require.Len(t, exec.execQueries, 1)
	query := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, query, "INSERT INTO scheduler_outbox")
	require.NotContains(t, strings.ToLower(query), "update accounts", "the refresh must not touch the accounts table")

	require.Equal(t, service.SchedulerOutboxEventAccountChanged, exec.execArgs[0][0])
	accountID, ok := exec.execArgs[0][1].(*int64)
	require.True(t, ok)
	require.Equal(t, int64(42), *accountID)
}

func TestAccountRepository_RefreshSchedulerAccount_ReportsOutboxFailures(t *testing.T) {
	boom := errors.New("outbox insert failed")
	repo := newAccountRepositoryWithSQL(nil, &recordingSQLExecutor{err: boom}, nil)
	require.ErrorIs(t, repo.RefreshSchedulerAccount(context.Background(), 42), boom, "the breaker retries on error, so it must not be swallowed")
}
