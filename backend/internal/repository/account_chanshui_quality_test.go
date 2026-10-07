package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChanshuiAccountStateSQLGuardsAndAtomicOutbox(t *testing.T) {
	for _, paused := range []bool{true, false} {
		t.Run(map[bool]string{true: "pause", false: "recover"}[paused], func(t *testing.T) {
			exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
			repo := newAccountRepositoryWithSQL(nil, exec, nil)
			now := time.Now().UTC()
			changed, err := repo.ApplyChanshuiQualityState(context.Background(), 2, 3, now, paused)
			require.NoError(t, err)
			require.True(t, changed)
			require.Len(t, exec.execQueries, 1, "state and outbox must be one atomic statement")
			q := normalizeSQLWhitespace(exec.execQueries[0])
			require.Contains(t, q, "WITH changed AS ( UPDATE accounts")
			require.Contains(t, q, "INSERT INTO scheduler_outbox")
			require.Contains(t, q, "deleted_at IS NULL AND status='active'")
			require.Contains(t, q, "p.account_id=$1")
			require.Contains(t, q, "p.enabled=TRUE AND p.quality_check_enabled=TRUE")
			require.Contains(t, q, "p.quality_provider='chanshui' AND p.updated_at=$4")
			require.Contains(t, q, "jsonb_typeof(extra->'chanshui_quality_pauses')='object'")
			require.Equal(t, []any{int64(2), int64(3), service.SchedulerOutboxEventAccountChanged, now}, exec.execArgs[0])
			if paused {
				require.Contains(t, q, "schedulable IS TRUE OR")
				require.Contains(t, q, "AND NOT ((")
				require.Contains(t, q, "|| jsonb_build_object($2::bigint::text,true)")
			} else {
				require.Contains(t, q, "? $2::bigint::text")
				require.Contains(t, q, "- $2::bigint::text")
				require.Contains(t, q, "='{}'::jsonb")
				require.Contains(t, q, "THEN TRUE ELSE schedulable END")
				require.Contains(t, q, "auto_pause_on_expired IS NOT TRUE OR expires_at IS NULL OR expires_at>NOW()")
				require.Contains(t, q, "LIKE 'scheduled_quality_check:%' AND temp_unschedulable_until IS NULL")
			}
		})
	}
}

func TestChanshuiAccountStateNoopAndFailure(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &accountRepository{sql: db}
	now := time.Now().UTC()
	mock.ExpectExec("WITH changed AS").WithArgs(int64(2), int64(3), service.SchedulerOutboxEventAccountChanged, now).WillReturnResult(sqlmock.NewResult(0, 0))
	changed, err := repo.ApplyChanshuiQualityState(context.Background(), 2, 3, now, false)
	require.NoError(t, err)
	require.False(t, changed)
	mock.ExpectExec("WITH changed AS").WillReturnError(errors.New("outbox unavailable"))
	changed, err = repo.ApplyChanshuiQualityState(context.Background(), 2, 3, now, true)
	require.Error(t, err)
	require.False(t, changed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestChanshuiBulkUpdateManualOverrideAndBackgroundRecovery(t *testing.T) {
	for _, manual := range []bool{true, false} {
		for _, enabled := range []bool{true, false} {
			exec := &recordingSQLExecutor{result: rowsAffectedResult(0)}
			repo := newAccountRepositoryWithSQL(nil, exec, nil)
			ctx := context.Background()
			if manual {
				ctx = service.WithAccountOwnerScope(ctx, 7, 0)
			}
			require.NoError(t, repo.SetSchedulable(ctx, 2, enabled))
			require.Len(t, exec.execQueries, 1)
			q := normalizeSQLWhitespace(exec.execQueries[0])
			if manual {
				require.Contains(t, q, "- 'chanshui_quality_pauses'")
			} else {
				require.NotContains(t, q, "- 'chanshui_quality_pauses'")
				if enabled {
					require.Contains(t, q, "schedulable = CASE WHEN jsonb_typeof(extra->'chanshui_quality_pauses')='object'")
					require.Contains(t, q, "THEN FALSE ELSE $1 END")
				}
			}
		}
	}
}

func TestChanshuiClearErrorPreservesOwnedPause(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	require.NoError(t, repo.ClearError(context.Background(), 2))
	require.Len(t, exec.execQueries, 1)
	q := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, q, "status='active',error_message=''")
	require.Contains(t, q, "extra->'chanshui_quality_pauses'<>'{}'::jsonb THEN FALSE ELSE TRUE END")
	require.Contains(t, q, "status='error' AND deleted_at IS NULL")
	require.Contains(t, q, "INSERT INTO scheduler_outbox")
}
