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

func TestCRSAndDuplicateLookupsApplyAccountOwnerScope(t *testing.T) {
	for _, poolOwner := range []bool{false, true} {
		for _, kind := range []string{"CRS lookup", "CRS map", "duplicate recovery"} {
			t.Run(kind+map[bool]string{false: "/ordinary", true: "/pool owner"}[poolOwner], func(t *testing.T) {
				var query string
				db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &query}))
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
				repo := newAccountRepositoryWithSQL(client, db, nil)
				poolOwnerID := int64(0)
				if poolOwner {
					poolOwnerID = 7
				}
				ctx := service.WithAccountOwnerScope(context.Background(), 7, poolOwnerID)
				mock.ExpectQuery("scoped lookup").WillReturnRows(sqlmock.NewRows([]string{"id"}))
				switch kind {
				case "CRS lookup":
					account, err := repo.GetByCRSAccountID(ctx, "remote-account")
					require.NoError(t, err)
					require.Nil(t, account)
				case "CRS map":
					mapping, err := repo.ListCRSAccountIDs(ctx)
					require.NoError(t, err)
					require.Empty(t, mapping)
				case "duplicate recovery":
					accounts, err := repo.FindByExtraField(ctx, "duplicate_operation_id", "same-operation")
					require.NoError(t, err)
					require.Empty(t, accounts)
				}
				if poolOwner {
					require.NotContains(t, query, "created_by")
				} else {
					require.Contains(t, query, "created_by")
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestAccountUpdateRejectsOtherAndLegacyBeforeMutation(t *testing.T) {
	for _, createdBy := range []any{int64(8), nil} {
		t.Run("inaccessible", func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("SELECT created_by FROM accounts WHERE id = ").
				WithArgs(int64(12)).WillReturnRows(sqlmock.NewRows([]string{"created_by"}).AddRow(createdBy))
			// No Ent client: reaching the write transaction would panic.
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			err = repo.Update(service.WithAccountOwnerScope(context.Background(), 7, 9), &service.Account{ID: 12})
			require.ErrorIs(t, err, service.ErrAccountNotFound)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
