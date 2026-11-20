//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	dedupClaimInsertSQL   = `(?s)INSERT INTO usage_billing_dedup \(request_id, api_key_id, request_fingerprint\)\s+VALUES \(\$1, \$2, \$3\)\s+ON CONFLICT \(request_id, api_key_id\) DO NOTHING\s+RETURNING id`
	dedupArchiveSelectSQL = `(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`
	balanceQueueInsertSQL = `(?s)INSERT INTO billing_balance_settlements \(\s*request_id,\s*api_key_id,\s*request_fingerprint,\s*user_id,\s*amount_usd,\s*status\s*\)\s+VALUES \(\$1, \$2, \$3, \$4, \$5, \$6\)\s+ON CONFLICT \(request_id, api_key_id\) DO NOTHING\s+RETURNING status`
)

type noopSettlementRepo struct{}

func (noopSettlementRepo) FlushPendingBalanceSettlements(context.Context, int) ([]service.UsageBalanceSettlementResult, error) {
	return nil, nil
}

func (noopSettlementRepo) DeleteAppliedBalanceSettlements(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

// startSettlementConsumerForTest makes the process-wide consumer flag true so
// the repository takes the merge path. The no-op repo backend keeps the worker
// away from the sqlmock database.
func startSettlementConsumerForTest(t *testing.T) {
	t.Helper()
	worker := service.NewUsageBalanceSettlementWorker(noopSettlementRepo{})
	require.True(t, service.UsageBalanceSettlementConsumerRunning())
	t.Cleanup(worker.Stop)
}

func TestUsageBillingApply_MergeEnabledQueuesBalanceInsteadOfUpdatingIt(t *testing.T) {
	startSettlementConsumerForTest(t)
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: true}

	cmd := &service.UsageBillingCommand{
		RequestID:   "req-merge-1",
		APIKeyID:    7,
		UserID:      42,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 2.5,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(dedupClaimInsertSQL).
		WithArgs("req-merge-1", int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(dedupArchiveSelectSQL).
		WithArgs("req-merge-1", int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(balanceQueueInsertSQL).
		WithArgs("req-merge-1", int64(7), sqlmock.AnyArg(), int64(42), 2.5, service.BalanceSettlementPending).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.BalanceSettlementPending))
	mock.ExpectCommit()

	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.True(t, result.BalanceSettlementDeferred)
	// The durable queue owns the deduction now: no synchronous balance is returned
	// and the users.balance UPDATE must not appear in this transaction.
	require.Nil(t, result.NewBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApply_MergeDisabledKeepsSynchronousDeduction(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: false}

	cmd := &service.UsageBillingCommand{
		RequestID:   "req-legacy-1",
		APIKeyID:    7,
		UserID:      42,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 2.5,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(dedupClaimInsertSQL).
		WithArgs("req-legacy-1", int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(dedupArchiveSelectSQL).
		WithArgs("req-legacy-1", int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(97.5))
	mock.ExpectCommit()

	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 97.5, *result.NewBalance, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApply_MergeEnabledButNoConsumerFallsBackToSynchronousDeduction(t *testing.T) {
	// Make sure this process has no settlement consumer: stopping any worker clears
	// the process-wide flag. Starting and immediately stopping one establishes the
	// known-clean state even if another test left a worker running.
	stopWorker := service.NewUsageBalanceSettlementWorker(noopSettlementRepo{})
	stopWorker.Stop()
	require.False(t, service.UsageBalanceSettlementConsumerRunning())

	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: true}

	cmd := &service.UsageBillingCommand{
		RequestID:   "req-fallback-1",
		APIKeyID:    7,
		UserID:      42,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 1.5,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(dedupClaimInsertSQL).
		WithArgs("req-fallback-1", int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(dedupArchiveSelectSQL).
		WithArgs("req-fallback-1", int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(1.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(8.5))
	mock.ExpectCommit()

	result, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 8.5, *result.NewBalance, 0.000001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageBillingApply_MergeQueueInsertMapsMissingUserToErrUserNotFound(t *testing.T) {
	startSettlementConsumerForTest(t)
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: true}

	cmd := &service.UsageBillingCommand{
		RequestID:   "req-missing-user",
		APIKeyID:    7,
		UserID:      4242,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 1,
	}

	mock.ExpectBegin()
	mock.ExpectQuery(dedupClaimInsertSQL).
		WithArgs("req-missing-user", int64(7), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1)))
	mock.ExpectQuery(dedupArchiveSelectSQL).
		WithArgs("req-missing-user", int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(balanceQueueInsertSQL).
		WithArgs("req-missing-user", int64(7), sqlmock.AnyArg(), int64(4242), 1.0, service.BalanceSettlementPending).
		WillReturnError(&pq.Error{Code: "23503", Message: "insert or update on table \"billing_balance_settlements\" violates foreign key constraint"})
	mock.ExpectRollback()

	result, err := repo.Apply(ctx, cmd)
	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, errors.Is(err, service.ErrUserNotFound), "want ErrUserNotFound, got %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}
