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

func TestAccountOwnerScopeListAndBatchPredicates(t *testing.T) {
	for _, fullPool := range []bool{false, true} {
		for _, batch := range []bool{false, true} {
			name := map[bool]string{false: "ordinary", true: "pool owner"}[fullPool] + map[bool]string{false: "/list", true: "/batch"}[batch]
			t.Run(name, func(t *testing.T) {
				var query string
				db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &query}))
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
				repo := newAccountRepositoryWithSQL(client, db, nil)
				ownerID := int64(0)
				if fullPool {
					ownerID = 7
				}
				ctx := service.WithAccountOwnerScope(context.Background(), 7, ownerID)
				mock.ExpectQuery("scoped accounts").WillReturnRows(sqlmock.NewRows([]string{"id"}))
				if batch {
					_, err = repo.GetByIDs(ctx, []int64{11, 22})
				} else {
					_, err = repo.ListAllWithFilters(ctx, "", "", "", "", 0, "")
				}
				require.NoError(t, err)
				if fullPool {
					require.NotContains(t, query, "created_by")
				} else {
					require.Contains(t, query, `"created_by" =`)
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestAccountOwnerScopeIndividualVisibilityMatchesList(t *testing.T) {
	for _, tc := range []struct {
		name              string
		owner             any
		fullPool, allowed bool
	}{
		{"own upload", int64(7), false, true}, {"other upload", int64(8), false, false},
		{"legacy null", nil, false, false}, {"legacy zero", int64(0), false, false},
		{"pool owner all uploads", int64(8), true, true}, {"pool owner legacy", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			ownerID := int64(0)
			if tc.fullPool {
				ownerID = 7
			} else {
				mock.ExpectQuery("SELECT created_by FROM accounts WHERE id = ").WithArgs(int64(22)).WillReturnRows(sqlmock.NewRows([]string{"created_by"}).AddRow(tc.owner))
			}
			err = repo.ensureAccountOwnerVisible(service.WithAccountOwnerScope(context.Background(), 7, ownerID), 22)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, service.ErrAccountNotFound)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
