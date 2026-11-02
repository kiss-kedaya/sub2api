package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUpdateAccountModelMappingCASWritesOnlyWhenUnchanged(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &accountRepository{sql: db}

	mock.ExpectExec("UPDATE accounts").
		WithArgs(`{"gpt-5":"gpt-5","gpt-5-mini":"gpt-5-mini"}`, int64(7), `{"gpt-5":"gpt-5"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	applied, err := repo.UpdateAccountModelMappingCAS(
		context.Background(),
		7,
		map[string]string{"gpt-5": "gpt-5"},
		map[string]string{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
	)
	require.NoError(t, err)
	require.True(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Zero rows means a concurrent edit changed model_mapping (or the account is
// gone). The runner must treat that as "skipped", never as a write.
func TestUpdateAccountModelMappingCASReportsConcurrentEdit(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &accountRepository{sql: db}

	mock.ExpectExec("UPDATE accounts").
		WithArgs(sqlmock.AnyArg(), int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	applied, err := repo.UpdateAccountModelMappingCAS(
		context.Background(),
		7,
		map[string]string{"a": "a"},
		map[string]string{"b": "b"},
	)
	require.NoError(t, err)
	require.False(t, applied)
	require.NoError(t, mock.ExpectationsWereMet())
}
