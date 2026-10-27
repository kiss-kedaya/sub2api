package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/accounttimer"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Exercise the existing account mutation transaction rather than a second
// implementation of the immediate switch. Outbox failure must undo cancellation.
func TestSchedulableTimerManualOverrideUsesExistingAccountTransaction(t *testing.T) {
	for _, outboxFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "outbox rollback"}[outboxFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			repo := newAccountRepositoryWithSQL(client, db, nil)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id FROM accounts WHERE id=ANY").
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			mock.ExpectExec("WITH cancelled AS").WithArgs(int64(7), int64(1), sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectExec("UPDATE accounts SET").WillReturnResult(sqlmock.NewResult(0, 1))
			event := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox"))
			if outboxFails {
				event.WillReturnError(errors.New("outbox fixture failure"))
				mock.ExpectRollback()
			} else {
				event.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			}
			ctx := accounttimer.WithManualIntent(context.Background(), 1)
			err = repo.SetSchedulable(ctx, 7, false)
			if outboxFails {
				require.ErrorContains(t, err, "outbox fixture failure")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSchedulableTimerCancellationLockRespectsOwnerScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := newAccountRepositoryWithSQL(client, db, nil)
	ctx := service.WithAccountOwnerScope(accounttimer.WithManualIntent(context.Background(), 1), 7, 9)
	paused := false
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL AND created_by=$2 ORDER BY id FOR UPDATE")).
		WithArgs(sqlmock.AnyArg(), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	// No authorized accounts means no timer cancellation or outbox event.
	mock.ExpectExec("UPDATE accounts SET.*AND created_by").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	n, err := repo.BulkUpdate(ctx, []int64{19}, service.AccountBulkUpdate{Schedulable: &paused})
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, mock.ExpectationsWereMet())
}
