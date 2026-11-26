package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func expectBalanceSettlementTransaction(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(true))
	mock.ExpectExec(`SET LOCAL lock_timeout`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET LOCAL statement_timeout`).WillReturnResult(sqlmock.NewResult(0, 0))
}

// settlementReturningColumns mirrors the RETURNING list of the settlement UPDATE:
// the new balance plus the low-balance alert preferences of the same row.
var settlementReturningColumns = []string{
	"id", "balance", "balance_notify_enabled", "balance_notify_threshold",
	"balance_notify_threshold_type", "total_recharged",
}

// settledRow builds one RETURNING row for a user with default alert preferences.
func settledRow(rows *sqlmock.Rows, id int64, balance float64) *sqlmock.Rows {
	return rows.AddRow(id, balance, true, nil, "fixed", 0.0)
}

func TestFlushPendingBalanceSettlementsCoalescesUsers(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount", "wallet_preapplied"}).
			AddRow(1, 42, "0.10000000", true).
			AddRow(2, 42, "0.25000000", true).
			AddRow(3, 77, "0.50000000", true))
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(settledRow(settledRow(sqlmock.NewRows(settlementReturningColumns), 42, 9.65), 77, 4.5))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*applied_at = NOW`).
		WithArgs(sqlmock.AnyArg(), 4, 3).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, int64(42), results[0].UserID)
	require.Equal(t, 2, results[0].EventCount)
	require.InDelta(t, 0.35, results[0].Amount, 1e-12)
	require.InDelta(t, 9.65, results[0].NewBalance, 1e-12)
	require.Equal(t, int64(77), results[1].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushPendingBalanceSettlementsSkipsOutboxOnlyForRedisPreappliedRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text, wallet_preapplied.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount", "wallet_preapplied"}).
			AddRow(1, 42, "0.10000000", true).
			AddRow(2, 42, "0.20000000", false).
			AddRow(3, 77, "0.50000000", false))
	// Non-preapplied rows update first with the trigger enabled, producing the
	// durable live-wallet delta for legacy/uncovered billing paths.
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(settledRow(settledRow(sqlmock.NewRows(settlementReturningColumns), 42, 9.8), 77, 4.5))
	// Only Redis-finalized charges suppress the generic balance trigger.
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(settledRow(sqlmock.NewRows(settlementReturningColumns), 42, 9.7))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*applied_at = NOW`).
		WithArgs(sqlmock.AnyArg(), 4, 3).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, int64(42), results[0].UserID)
	require.InDelta(t, 0.3, results[0].Amount, 1e-12)
	require.InDelta(t, 9.7, results[0].NewBalance, 1e-12)
	require.Equal(t, int64(77), results[1].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushPendingBalanceSettlementsQuarantinesDeletedUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount", "wallet_preapplied"}).
			AddRow(10, 42, "0.10000000", true).
			AddRow(11, 99, "0.20000000", true))
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(settledRow(sqlmock.NewRows(settlementReturningColumns), 42, 9.9))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*applied_at = NOW`).
		WithArgs(sqlmock.AnyArg(), 4, 3).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*user is missing or deleted`).
		WithArgs(sqlmock.AnyArg(), 6, 3).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, int64(42), results[0].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushPendingBalanceSettlementsBacksOffSelectedRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount", "wallet_preapplied"}).AddRow(1, 42, "0.10000000", true))
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnError(errors.New("lock timeout"))
	mock.ExpectRollback()
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*available_at`).
		WithArgs(sqlmock.AnyArg(), "lock timeout", 3).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := &usageBalanceSettlementRepository{db: db}
	_, err = repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.EqualError(t, err, "lock timeout")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushPendingBalanceSettlementsSkipsWhenAnotherInstanceLeads(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT pg_try_advisory_xact_lock`).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(false))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFlushPendingBalanceSettlementsCommitsEmptyBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text.*ORDER BY available_at, id.*FOR UPDATE SKIP LOCKED`).
		WithArgs(1000, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount"}))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 0)
	require.NoError(t, err)
	require.Empty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAppliedBalanceSettlementsUsesIndexedSkipLockedBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	before := time.Now().UTC().Add(-15 * time.Minute)
	mock.ExpectExec(`(?s)WITH doomed AS .*ORDER BY applied_at, id.*LIMIT \$2.*FOR UPDATE SKIP LOCKED.*DELETE FROM billing_balance_settlements`).
		WithArgs(before, 5000, 4, 5).
		WillReturnResult(sqlmock.NewResult(0, 321))

	repo := &usageBalanceSettlementRepository{db: db}
	deleted, err := repo.DeleteAppliedBalanceSettlements(context.Background(), before, 0)
	require.NoError(t, err)
	require.Equal(t, int64(321), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 结算 UPDATE 的 RETURNING 带出的提醒偏好必须原样进入结果：低余额观察者靠它判断
// 阈值，不再为此查库。同一用户既有预扣行又有普通行时，以第二次 UPDATE（最终状态）为准。
func TestFlushPendingBalanceSettlementsReturnsAlertPreferences(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expectBalanceSettlementTransaction(mock)
	mock.ExpectQuery(`(?s)SELECT id, user_id, amount_usd::text.*FOR UPDATE SKIP LOCKED`).
		WithArgs(100, 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "amount", "wallet_preapplied"}).
			AddRow(1, 42, "0.10000000", false).
			AddRow(2, 42, "0.20000000", true).
			AddRow(3, 77, "0.50000000", false))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users.*RETURNING u\.id, u\.balance, u\.balance_notify_enabled, u\.balance_notify_threshold,\s+u\.balance_notify_threshold_type, u\.total_recharged`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(settlementReturningColumns).
			AddRow(42, 9.9, true, 12.5, "fixed", 100.0).
			AddRow(77, 4.5, false, nil, "percentage", 250.0))
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(settlementReturningColumns).
			AddRow(42, 9.7, true, 12.5, "fixed", 100.0))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*applied_at = NOW`).
		WithArgs(sqlmock.AnyArg(), 4, 3).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	repo := &usageBalanceSettlementRepository{db: db}
	results, err := repo.FlushPendingBalanceSettlements(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, results, 2)

	first := results[0]
	require.Equal(t, int64(42), first.UserID)
	require.InDelta(t, 9.7, first.NewBalance, 1e-12, "the later (pre-applied) UPDATE is the final state")
	require.InDelta(t, 0.3, first.Amount, 1e-12)
	require.True(t, first.NotifyEnabled)
	require.NotNil(t, first.NotifyThreshold)
	require.InDelta(t, 12.5, *first.NotifyThreshold, 1e-12)
	require.Equal(t, "fixed", first.NotifyThresholdType)
	require.InDelta(t, 100.0, first.TotalRecharged, 1e-12)

	second := results[1]
	require.Equal(t, int64(77), second.UserID)
	require.False(t, second.NotifyEnabled)
	require.Nil(t, second.NotifyThreshold, "a NULL per-user threshold means the global default applies")
	require.Equal(t, "percentage", second.NotifyThresholdType)
	require.InDelta(t, 250.0, second.TotalRecharged, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}

// 积压读取必须是有界查询：深度计数带 LIMIT、最老一笔只看索引序的队首若干行，
// 两处都过滤 status、没有 FOR UPDATE（不和结算 worker 争锁）。
func TestBalanceSettlementBacklogQueryIsBoundedAndIndexShaped(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)SELECT 1 FROM billing_balance_settlements WHERE status = \$1 LIMIT \$2.*` +
		`WHERE status = \$1\s+ORDER BY available_at, id\s+LIMIT \$3`).
		WithArgs(3, int64(40000), 256).
		WillReturnRows(sqlmock.NewRows([]string{"depth", "oldest_ms"}).AddRow(int64(1234), int64(2500)))

	repo := &usageBalanceSettlementRepository{db: db}
	backlog, err := repo.BalanceSettlementBacklog(context.Background(), 40000)
	require.NoError(t, err)
	require.Equal(t, int64(1234), backlog.Pending)
	require.False(t, backlog.PendingCapped)
	require.Equal(t, 2500*time.Millisecond, backlog.OldestAge)
	require.NoError(t, mock.ExpectationsWereMet())

	require.NotContains(t, balanceSettlementBacklogSQL, "FOR UPDATE")
}

func TestBalanceSettlementBacklogReportsCappedDepthAndDefaultsTheCap(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)FROM billing_balance_settlements`).
		WithArgs(3, int64(20000), 256).
		WillReturnRows(sqlmock.NewRows([]string{"depth", "oldest_ms"}).AddRow(int64(20000), int64(0)))

	repo := &usageBalanceSettlementRepository{db: db}
	backlog, err := repo.BalanceSettlementBacklog(context.Background(), 0)
	require.NoError(t, err)
	require.True(t, backlog.PendingCapped, "reaching the cap means \"at least this many\"")
	require.Equal(t, int64(20000), backlog.Pending)
	require.Zero(t, backlog.OldestAge)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalanceSettlementBacklogPropagatesQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)FROM billing_balance_settlements`).WillReturnError(errors.New("db down"))

	repo := &usageBalanceSettlementRepository{db: db}
	_, err = repo.BalanceSettlementBacklog(context.Background(), 100)
	require.EqualError(t, err, "db down")

	var nilRepo *usageBalanceSettlementRepository
	_, err = nilRepo.BalanceSettlementBacklog(context.Background(), 100)
	require.Error(t, err)
}
