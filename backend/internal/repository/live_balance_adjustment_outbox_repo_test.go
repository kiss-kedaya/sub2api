package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestLiveBalanceAdjustmentOutboxClaimUsesLeasedOrderedSkipLockedBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock\(\$1::bigint\)`).
		WithArgs(liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectExec(`SET LOCAL enable_sort = off; SET LOCAL jit = off`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH RECURSIVE pending_users \(user_id\) AS .*o\.delivered_at IS NULL AND o\.user_id > 0.*o\.delivered_at IS NULL AND o\.user_id > p\.user_id.*active_users AS .*FROM pending_users AS p.*first_event\.predecessor_id = 0 OR COALESCE.*SELECT predecessor\.delivered_at IS NOT NULL.*WHERE predecessor\.id = first_event\.predecessor_id.*user_pages AS .*row_number\(\) OVER.*lag\(page\.id\).*page\.predecessor_id = page\.previous_id.*LIMIT \$2.*FOR UPDATE OF o SKIP LOCKED.*UPDATE live_balance_adjustment_outbox`).
		WithArgs("worker-1", 1024, int64(30), service.LiveBalanceOutboxPerUserMaxBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "predecessor_id", "delta", "attempts", "created_at"}).
			AddRow(17, 42, 11, "-1.25000000", 2, createdAt))
	mock.ExpectCommit()

	repo := &liveBalanceAdjustmentOutboxRepository{db: db}
	events, err := repo.Claim(context.Background(), "worker-1", 0, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(17), events[0].ID)
	require.Equal(t, int64(42), events[0].UserID)
	require.Equal(t, int64(11), events[0].PredecessorID)
	require.Equal(t, int64(-125000000), events[0].DeltaUnits)
	require.Equal(t, "live-balance-outbox:17", events[0].RedisEventID())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxClaimPlannerFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock\(\$1::bigint\)`).
		WithArgs(liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	plannerErr := errors.New("planner settings failed")
	mock.ExpectExec(`SET LOCAL enable_sort = off; SET LOCAL jit = off`).WillReturnError(plannerErr)
	mock.ExpectRollback()

	events, err := (&liveBalanceAdjustmentOutboxRepository{db: db}).Claim(context.Background(), "worker-1", 1, time.Second)
	require.ErrorIs(t, err, plannerErr)
	require.Nil(t, events)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxClaimSkipsBusyWorker(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock\(\$1::bigint\)`).
		WithArgs(liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(false))
	mock.ExpectRollback()

	events, err := (&liveBalanceAdjustmentOutboxRepository{db: db}).Claim(context.Background(), "worker-1", 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, events)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxAckRetryAndCleanupAreClaimSafe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &liveBalanceAdjustmentOutboxRepository{db: db}

	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*delivered_at = NOW.*claimed_by = \$2`).
		WithArgs(int64(7), "worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkDelivered(context.Background(), 7, "worker-1"))
	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*delivered_at = NOW.*id = ANY\(\$1::bigint\[\]\).*claimed_by = \$2`).
		WithArgs(pq.Array([]int64{7, 8}), "worker-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.MarkDeliveredBatch(context.Background(), []int64{7, 8}, "worker-1"))

	retryAt := time.Now().UTC().Add(time.Minute)
	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*attempts = attempts \+ 1.*available_at = \$3`).
		WithArgs(int64(8), "worker-1", retryAt, "redis down").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RetryClaimed(context.Background(), 8, "worker-1", retryAt, "redis down"))
	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*attempts = attempts \+ 1.*id = ANY\(\$1::bigint\[\]\).*claimed_by = \$2`).
		WithArgs(pq.Array([]int64{8, 9}), "worker-1", retryAt, "redis down").
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.RetryClaimedBatch(context.Background(), []int64{8, 9}, "worker-1", retryAt, "redis down"))

	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*delivered_at = NOW\(\).*last_error = \$3.*WHERE id = \$1`).
		WithArgs(int64(7), "worker-1", "abandoned after 64 attempts: conflict").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.AbandonClaimed(context.Background(), 7, "worker-1", "abandoned after 64 attempts: conflict"))
	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*last_error = \$3.*id = ANY\(\$1::bigint\[\]\)`).
		WithArgs(pq.Array([]int64{7, 8}), "worker-1", "abandoned after 64 attempts: conflict").
		WillReturnResult(sqlmock.NewResult(0, 2))
	require.NoError(t, repo.AbandonClaimedBatch(context.Background(), []int64{7, 8}, "worker-1", "abandoned after 64 attempts: conflict"))

	before := time.Now().UTC().Add(-24 * time.Hour)
	mock.ExpectExec(`(?s)WITH doomed AS .*delivered_at < \$1.*FOR UPDATE SKIP LOCKED.*DELETE FROM live_balance_adjustment_outbox`).
		WithArgs(before, 10000).
		WillReturnResult(sqlmock.NewResult(0, 12))
	deleted, err := repo.DeleteDelivered(context.Background(), before, 0)
	require.NoError(t, err)
	require.Equal(t, int64(12), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxRejectsLostClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`UPDATE live_balance_adjustment_outbox`).
		WithArgs(int64(7), "other-worker").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err = (&liveBalanceAdjustmentOutboxRepository{db: db}).MarkDelivered(context.Background(), 7, "other-worker")
	require.ErrorContains(t, err, "no longer claimed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxStatsExposeBacklog(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	oldest := time.Now().UTC().Add(-time.Minute)
	mock.ExpectQuery(`(?s)COUNT\(\*\) FILTER.*FROM live_balance_adjustment_outbox`).
		WillReturnRows(sqlmock.NewRows([]string{"pending", "delivered", "oldest", "max_attempts", "last_error"}).
			AddRow(4, 9, oldest, 3, "redis down"))
	stats, err := (&liveBalanceAdjustmentOutboxRepository{db: db}).Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(4), stats.Pending)
	require.Equal(t, int64(9), stats.Delivered)
	require.Equal(t, 3, stats.MaxAttempts)
	require.Equal(t, "redis down", stats.LastError)
	require.Equal(t, oldest, *stats.OldestCreatedAt)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxMigrationDefinesTransactionalSkip(t *testing.T) {
	content, err := migrations.FS.ReadFile("233_live_balance_adjustment_outbox.sql")
	require.NoError(t, err)
	sqlText := string(content)
	require.Contains(t, sqlText, "AFTER UPDATE OF balance ON users")
	require.Contains(t, sqlText, "current_setting('sub2api.skip_live_balance_outbox', TRUE)")
	require.Contains(t, sqlText, "NEW.balance - OLD.balance")
	require.Contains(t, sqlText, "idx_live_balance_adjustment_outbox_user_order")
	require.Contains(t, sqlText, "live_balance_adjustment_heads")
	require.Contains(t, sqlText, "predecessor_id")
	require.Contains(t, sqlText, "FOR EACH ROW EXECUTE FUNCTION enqueue_live_balance_adjustment()")
}

func TestLiveBalanceAdjustmentOutboxClaimSQLAvoidsFullHeadsScanAndPreservesOrdering(t *testing.T) {
	// Must drive from the partial pending index (delivered_at IS NULL), never from
	// live_balance_adjustment_heads (which grows forever and causes thousands of
	// empty LATERAL probes per poll).
	require.NotContains(t, liveBalanceOutboxClaimSQL, "FROM live_balance_adjustment_heads")
	require.Contains(t, liveBalanceOutboxClaimSQL, "WITH RECURSIVE pending_users (user_id) AS")
	require.Contains(t, liveBalanceOutboxClaimSQL, "WHERE o.delivered_at IS NULL AND o.user_id > 0")
	require.Contains(t, liveBalanceOutboxClaimSQL, "WHERE o.delivered_at IS NULL AND o.user_id > p.user_id")
	require.Contains(t, liveBalanceOutboxClaimSQL, "ORDER BY o.user_id, o.id")
	require.Contains(t, liveBalanceOutboxClaimSQL, "WHERE p.user_id IS NOT NULL")
	// Must keep single-user contiguous chain ordering and SKIP LOCKED semantics.
	require.Contains(t, liveBalanceOutboxClaimSQL, "page.predecessor_id = page.previous_id")
	require.Contains(t, liveBalanceOutboxClaimSQL, "FOR UPDATE OF o SKIP LOCKED")
}

func TestLiveBalanceAdjustmentOutboxClaimClampsLimitAndLeaseAndSortsByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock\(\$1::bigint\)`).
		WithArgs(liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectExec(`SET LOCAL enable_sort = off; SET LOCAL jit = off`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Oversized limit clamps to LiveBalanceOutboxMaxBatchSize; sub-second lease defaults to 30s.
	mock.ExpectQuery(`(?s)WITH RECURSIVE pending_users`).
		WithArgs("worker-2", service.LiveBalanceOutboxMaxBatchSize, int64(30), service.LiveBalanceOutboxPerUserMaxBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "predecessor_id", "delta", "attempts", "created_at"}).
			AddRow(25, 9, 20, "0.50000000", 0, createdAt).
			AddRow(12, 3, 0, "-2.00000000", 1, createdAt))
	mock.ExpectCommit()

	events, err := (&liveBalanceAdjustmentOutboxRepository{db: db}).Claim(
		context.Background(), "worker-2", service.LiveBalanceOutboxMaxBatchSize+50, 500*time.Millisecond,
	)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, int64(12), events[0].ID)
	require.Equal(t, int64(25), events[1].ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLiveBalanceAdjustmentOutboxClaimEmptyReturnsEmptySlice(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock\(\$1::bigint\)`).
		WithArgs(liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"acquired"}).AddRow(true))
	mock.ExpectExec(`SET LOCAL enable_sort = off; SET LOCAL jit = off`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH RECURSIVE pending_users`).
		WithArgs("worker-empty", 16, int64(45), service.LiveBalanceOutboxPerUserMaxBatchSize).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "predecessor_id", "delta", "attempts", "created_at"}))
	mock.ExpectCommit()

	events, err := (&liveBalanceAdjustmentOutboxRepository{db: db}).Claim(context.Background(), "worker-empty", 16, 45*time.Second)
	require.NoError(t, err)
	require.Empty(t, events)
	require.NoError(t, mock.ExpectationsWereMet())
}
