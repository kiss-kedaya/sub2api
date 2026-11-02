package repository

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpdateAccountModelMappingCASWritesOnlyWhenUnchanged(t *testing.T) {
	db, mock := newSQLMock(t)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &accountRepository{client: client, sql: db}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE accounts").
		WithArgs(`{"gpt-5":"gpt-5","gpt-5-mini":"gpt-5-mini"}`, int64(7), `{}`, `{"gpt-5":"gpt-5"}`, `{}`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	applied, err := repo.UpdateAccountModelMappingCAS(
		context.Background(),
		&service.Account{ID: 7, Credentials: map[string]any{}},
		map[string]string{"gpt-5": "gpt-5"},
		map[string]string{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
		map[string]any{},
	)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Zero rows means a concurrent edit changed model_mapping (or the account is
// gone). The runner must treat that as "skipped", never as a write.
func TestUpdateAccountModelMappingCASReportsConcurrentEdit(t *testing.T) {
	db, mock := newSQLMock(t)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &accountRepository{client: client, sql: db}

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE accounts").
		WithArgs(sqlmock.AnyArg(), int64(7), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	applied, err := repo.UpdateAccountModelMappingCAS(
		context.Background(),
		&service.Account{ID: 7, Credentials: map[string]any{}},
		map[string]string{"a": "a"},
		map[string]string{"b": "b"},
		map[string]any{},
	)
	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}
