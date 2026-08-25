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
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).
			AddRow(42, 9.65).
			AddRow(77, 4.5))
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
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).
			AddRow(42, 9.8).
			AddRow(77, 4.5))
	// Only Redis-finalized charges suppress the generic balance trigger.
	mock.ExpectExec(`SET LOCAL sub2api.skip_live_balance_outbox`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)WITH deltas\(user_id, amount_usd\).*UPDATE users`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow(42, 9.7))
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
		WillReturnRows(sqlmock.NewRows([]string{"id", "balance"}).AddRow(42, 9.9))
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
