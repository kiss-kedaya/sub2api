package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBulkUpdateAppliesAccountOwnerScope(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ctx       context.Context
		wantOwner bool
		ownerID   int64
	}{
		{name: "restricted admin under a different owner", ctx: service.WithAccountOwnerScope(context.Background(), 7, 9), wantOwner: true, ownerID: 7},
		{name: "pool owner", ctx: service.WithAccountOwnerScope(context.Background(), 7, 7)},
		{name: "unscoped worker", ctx: context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			expectedQuery := "UPDATE accounts SET name = $1, updated_at = NOW() WHERE id = ANY($2) AND deleted_at IS NULL"
			name := "changed"
			args := []driver.Value{name, sqlmock.AnyArg()}
			if tc.wantOwner {
				expectedQuery += " AND created_by = $3"
				args = append(args, tc.ownerID)
			}
			mock.ExpectExec(regexp.QuoteMeta(expectedQuery)).
				WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 0))

			repo := newAccountRepositoryWithSQL(nil, db, nil)
			_, err = repo.BulkUpdate(tc.ctx, []int64{11, 12}, service.AccountBulkUpdate{Name: &name})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
