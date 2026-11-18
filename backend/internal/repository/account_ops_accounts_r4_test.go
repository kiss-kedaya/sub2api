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

func TestR4AccountOpsProjectionIncludesRefreshEligibility(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectQuery(`SELECT .*"accounts"\."status", "accounts"\."schedulable".*FROM "accounts"`).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "platform", "type", "credentials", "extra", "session_window_end", "status", "schedulable"}).AddRow(int64(9101), "synthetic-active", service.PlatformOpenAI, service.AccountTypeAPIKey, []byte(`{"api_key":"synthetic-r4-key"}`), []byte(`{}`), nil, service.StatusActive, true))
	repo := newAccountRepositoryWithSQL(client, db, nil)
	accounts, err := repo.ListAccountOpsSnapshots(context.Background(), []int64{9101})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.True(t, accounts[0].IsActive())
	require.True(t, accounts[0].Schedulable)
	require.NoError(t, mock.ExpectationsWereMet())
}
