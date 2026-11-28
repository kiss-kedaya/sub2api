//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	conditionalBalanceDeductSQL         = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance`
	overdraftBalanceDeductSQL           = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING balance`
	reserveBatchImageHoldSQL            = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL            = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL            = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL             = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
	apiKeyQuotaIncrementSQL             = `(?s)UPDATE api_keys\s+SET quota_used = quota_used \+ \$1,.*WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING`
	apiKeyRateLimitIncrementSQL         = `(?s)UPDATE api_keys SET\s+usage_5h = .*WHERE id = \$2 AND deleted_at IS NULL`
	apiKeyQuotaAndRateLimitIncrementSQL = `(?s)UPDATE api_keys\s+SET quota_used = quota_used \+ \$1,.*usage_5h = .*WHERE id = \$3 AND deleted_at IS NULL\s+RETURNING`
)

func TestDeductUsageBillingBalance_UsesSufficientBalanceGuard(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.5))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 2.5)
	require.NoError(t, err)
	require.True(t, sufficient)
	require.InDelta(t, 7.5, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_RecordsOverdraftWhenGuardMisses(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(-5.0))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 10)
	require.NoError(t, err)
	require.False(t, sufficient)
	require.InDelta(t, -5.0, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeductsBalanceImmediately(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(90.0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		RequestID:          "req-async-balance",
		APIKeyID:           7,
		RequestFingerprint: "fp-async-balance",
		UserID:             42,
		BalanceCost:        10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.False(t, result.BalanceOverdrafted)
	require.False(t, result.BalanceFinalizationPending)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAPIKeyStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(90.0))
	mock.ExpectQuery(apiKeyQuotaAndRateLimitIncrementSQL).
		WithArgs(10.0, 10.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		BalanceCost:         10,
		APIKeyQuotaCost:     10,
		APIKeyRateLimitCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.False(t, result.APIKeyQuotaExhausted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_APIKeyCounterErrorStillFails(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		APIKeyRateLimitCost: 10,
	}, &service.UsageBillingApplyResult{Applied: true})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(overdraftBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, _, err = deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApply_DuplicatePreauthorizedChecksFinalizationPendingStatus(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		rowStatus   *int16
		wantPending bool
	}{
		{
			name:        "finalization_pending_row_reports_pending_true",
			rowStatus:   int16Ptr(service.BalanceSettlementFinalizationPending),
			wantPending: true,
		},
		{
			name:        "already_pending_settlement_row_reports_pending_false",
			rowStatus:   int16Ptr(service.BalanceSettlementPending),
			wantPending: false,
		},
		{
			name:        "already_applied_row_reports_pending_false",
			rowStatus:   int16Ptr(service.BalanceSettlementApplied),
			wantPending: false,
		},
		{
			name:        "already_refunded_row_reports_pending_false",
			rowStatus:   int16Ptr(service.BalanceSettlementRefunded),
			wantPending: false,
		},
		{
			name:        "missing_row_reports_pending_false",
			rowStatus:   nil,
			wantPending: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			cmd := &service.UsageBillingCommand{
				RequestID:            "cached:retry-1",
				APIKeyID:             7,
				UserID:               42,
				BalanceCost:          1.25,
				BalancePreauthorized: true,
				RequestPayloadHash:   "hash-1",
			}
			cmd.Normalize()

			mock.ExpectBegin()
			mock.ExpectQuery(dedupClaimCTESQL).
				WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
				WillReturnRows(dedupClaimConflictRows())
			mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
				WithArgs(cmd.RequestID, cmd.APIKeyID).
				WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow(cmd.RequestFingerprint))
			q := mock.ExpectQuery(`(?s)SELECT status\s+FROM billing_balance_settlements\s+WHERE request_id = \$1 AND api_key_id = \$2`).
				WithArgs(cmd.RequestID, cmd.APIKeyID)
			if tc.rowStatus != nil {
				q.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(*tc.rowStatus))
			} else {
				q.WillReturnError(sql.ErrNoRows)
			}
			mock.ExpectRollback()

			repo := newUsageBillingRepository(db, nil)
			res, err := repo.Apply(ctx, cmd)
			require.NoError(t, err)
			require.NotNil(t, res)
			require.False(t, res.Applied)
			require.Equal(t, tc.wantPending, res.BalanceFinalizationPending)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func int16Ptr(v int16) *int16 { return &v }

func TestClaimUsageBillingRequest_SingleCTEAllPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("fresh_request_including_local_claims_in_single_cte", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("local:fresh-1", int64(7), "fp-1").
			WillReturnRows(dedupClaimClaimedRows(10))
		mock.ExpectCommit()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "local:fresh-1", 7, "fp-1")
		require.NoError(t, err)
		require.True(t, applied)
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("archived_local_request_same_fingerprint_returns_false_in_single_cte", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("local:archived-1", int64(7), "fp-arch").
			WillReturnRows(dedupClaimArchivedRows(" fp-arch "))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "local:archived-1", 7, "fp-arch")
		require.NoError(t, err)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("archived_local_request_mismatched_fingerprint_returns_conflict", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("local:archived-2", int64(7), "fp-new").
			WillReturnRows(dedupClaimArchivedRows("fp-old"))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "local:archived-2", 7, "fp-new")
		require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing_hot_dedup_same_fingerprint_returns_false_in_single_cte", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("req-hot-dup", int64(7), "fp-hot").
			WillReturnRows(dedupClaimExistingRows(" fp-hot "))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "req-hot-dup", 7, "fp-hot")
		require.NoError(t, err)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("existing_hot_dedup_mismatched_fingerprint_returns_conflict_in_single_cte", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("req-hot-conflict", int64(7), "fp-new").
			WillReturnRows(dedupClaimExistingRows("fp-old"))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "req-hot-conflict", 7, "fp-new")
		require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("concurrent_active_dedup_conflict_same_fingerprint_returns_false", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("req-dup", int64(7), "fp-dup").
			WillReturnRows(dedupClaimConflictRows())
		mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
			WithArgs("req-dup", int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow("fp-dup"))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "req-dup", 7, "fp-dup")
		require.NoError(t, err)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("active_dedup_conflict_different_fingerprint_returns_conflict", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("req-conflict", int64(7), "fp-new").
			WillReturnRows(dedupClaimConflictRows())
		mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
			WithArgs("req-conflict", int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow("fp-existing"))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "req-conflict", 7, "fp-new")
		require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("concurrent_archival_between_cte_conflict_and_fallback_select_checks_archive", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(dedupClaimCTESQL).
			WithArgs("req-race", int64(7), "fp-race").
			WillReturnRows(dedupClaimConflictRows())
		mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
			WithArgs("req-race", int64(7)).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
			WithArgs("req-race", int64(7)).
			WillReturnRows(sqlmock.NewRows([]string{"request_fingerprint"}).AddRow("fp-race"))
		mock.ExpectRollback()

		applied, err := (&usageBillingRepository{}).claimUsageBillingRequest(ctx, tx, "req-race", 7, "fp-race")
		require.NoError(t, err)
		require.False(t, applied)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestApplyUsageBillingEffects_MergesAPIKeyQuotaAndRateLimitIntoOneUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("merges_distinct_quota_and_rate_limit_costs_into_single_update", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(apiKeyQuotaAndRateLimitIncrementSQL).
			WithArgs(1.5, 2.25, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
			WillReturnRows(sqlmock.NewRows([]string{"exhausted"}).AddRow(true))
		mock.ExpectCommit()

		result := &service.UsageBillingApplyResult{Applied: true}
		err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
			UserID:              42,
			APIKeyID:            7,
			APIKeyQuotaCost:     1.5,
			APIKeyRateLimitCost: 2.25,
		}, result)
		require.NoError(t, err)
		require.True(t, result.APIKeyQuotaExhausted)
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("merged_update_db_error_propagates", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(apiKeyQuotaAndRateLimitIncrementSQL).
			WithArgs(1.5, 2.25, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
			WillReturnError(sql.ErrConnDone)
		mock.ExpectRollback()

		err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
			UserID:              42,
			APIKeyID:            7,
			APIKeyQuotaCost:     1.5,
			APIKeyRateLimitCost: 2.25,
		}, &service.UsageBillingApplyResult{Applied: true})
		require.ErrorIs(t, err, sql.ErrConnDone)
		require.NoError(t, tx.Rollback())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("quota_only_uses_quota_update", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()

		mock.ExpectBegin()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		mock.ExpectQuery(apiKeyQuotaIncrementSQL).
			WithArgs(3.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
			WillReturnRows(sqlmock.NewRows([]string{"exhausted"}).AddRow(false))
		mock.ExpectCommit()

		result := &service.UsageBillingApplyResult{Applied: true}
		err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
			UserID:          42,
			APIKeyID:        7,
			APIKeyQuotaCost: 3.0,
		}, result)
		require.NoError(t, err)
		require.False(t, result.APIKeyQuotaExhausted)
		require.NoError(t, tx.Commit())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
