//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestGetByIDReusesCreditTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	mock.ExpectBegin()
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	mock.ExpectQuery(`SELECT .* FROM "users"`).WillReturnRows(sqlmock.NewRows([]string{"id", "email", "balance"}).AddRow(42, "fixture@example.invalid", 3.5))
	mock.ExpectQuery(`SELECT .* FROM "user_allowed_groups"`).WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}).AddRow(42, 7))
	user, err := NewUserRepository(client, db).GetByID(dbent.NewTxContext(ctx, tx), 42)
	require.NoError(t, err)
	require.EqualValues(t, 42, user.ID)
	require.Equal(t, 3.5, user.Balance)
	require.Equal(t, []int64{7}, user.AllowedGroups)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
