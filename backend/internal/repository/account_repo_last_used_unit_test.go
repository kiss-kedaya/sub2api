package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAccountRepository_BatchUpdateLastUsed_SortedUnnestAndFixedSQL(t *testing.T) {
	ctx := context.Background()
	t1 := time.Date(2026, 11, 28, 10, 0, 1, 0, time.UTC)
	t2 := time.Date(2026, 11, 28, 10, 0, 2, 0, time.UTC)
	t3 := time.Date(2026, 11, 28, 10, 0, 3, 0, time.UTC)

	t.Run("empty map is a noop", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		repo := &accountRepository{sql: db}
		require.NoError(t, repo.BatchUpdateLastUsed(ctx, nil))
		require.NoError(t, repo.BatchUpdateLastUsed(ctx, map[int64]time.Time{}))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("sorts ids ascending and uses fixed unnest SQL", func(t *testing.T) {
		updates := map[int64]time.Time{
			30: t3,
			5:  t1,
			18: t2,
		}
		wantIDs := pq.Array([]int64{5, 18, 30})
		wantTimes := pq.Array([]time.Time{t1, t2, t3})

		// 多次执行验证不受 Go map 随机遍历顺序影响，始终按 ID 升序绑定参数。
		for range 10 {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)

			mock.ExpectExec(`(?s)UPDATE accounts AS a\s+SET last_used_at = v\.last_used_at,\s+updated_at = NOW\(\)\s+FROM unnest\(\$1::bigint\[\], \$2::timestamptz\[\]\) AS v\(id, last_used_at\)\s+WHERE a\.id = v\.id AND a\.deleted_at IS NULL`).
				WithArgs(wantIDs, wantTimes).
				WillReturnResult(sqlmock.NewResult(0, 3))
			mock.ExpectExec(`(?s)INSERT INTO scheduler_outbox`).
				WithArgs(service.SchedulerOutboxEventAccountLastUsed, nil, nil, sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))

			repo := &accountRepository{sql: db}
			require.NoError(t, repo.BatchUpdateLastUsed(ctx, updates))
			require.NoError(t, mock.ExpectationsWereMet())
			_ = db.Close()
		}
	})

	t.Run("propagates update error without enqueuing outbox", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		boom := errors.New("deadlock detected")
		mock.ExpectExec(`(?s)UPDATE accounts AS a.*FROM unnest`).
			WithArgs(pq.Array([]int64{7}), pq.Array([]time.Time{t1})).
			WillReturnError(boom)

		repo := &accountRepository{sql: db}
		err = repo.BatchUpdateLastUsed(ctx, map[int64]time.Time{7: t1})
		require.ErrorIs(t, err, boom)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
