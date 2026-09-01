package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestLiveBalanceAdjustmentOutboxClaimUsesLeasedOrderedSkipLockedBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Now().UTC()
	mock.ExpectQuery(`(?s)WITH claim_lock AS .*pg_try_advisory_xact_lock.*candidates AS .*NOT EXISTS \(.*predecessor.user_id = candidate.user_id.*predecessor.id < candidate.id.*LIMIT \$2.*FOR UPDATE SKIP LOCKED.*UPDATE live_balance_adjustment_outbox`).
		WithArgs("worker-1", 200, int64(30), liveBalanceOutboxClaimLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "predecessor_id", "delta", "attempts", "created_at"}).
			AddRow(17, 42, 11, "-1.25000000", 2, createdAt))

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

func TestLiveBalanceAdjustmentOutboxAckRetryAndCleanupAreClaimSafe(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &liveBalanceAdjustmentOutboxRepository{db: db}

	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*delivered_at = NOW.*claimed_by = \$2`).
		WithArgs(int64(7), "worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.MarkDelivered(context.Background(), 7, "worker-1"))

	retryAt := time.Now().UTC().Add(time.Minute)
	mock.ExpectExec(`(?s)UPDATE live_balance_adjustment_outbox.*attempts = attempts \+ 1.*available_at = \$3`).
		WithArgs(int64(8), "worker-1", retryAt, "redis down").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RetryClaimed(context.Background(), 8, "worker-1", retryAt, "redis down"))

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
