//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCompleteBalancePreauthorizationSettlement_DirectPGHoldTransitionsDirectlyToApplied(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*WHEN request_fingerprint LIKE 'pg-direct-hold:%' THEN \$5.*ELSE \$3.*applied_at = CASE WHEN status = \$6 AND request_fingerprint LIKE 'pg-direct-hold:%' THEN NOW\(\) ELSE applied_at END`).
		WithArgs(
			"pg-hold:batch-image:b1",
			int64(11),
			service.BalanceSettlementPending,
			service.BalanceSettlementPending,
			service.BalanceSettlementApplied,
			service.BalanceSettlementFinalizationPending,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := &usageBillingRepository{db: db}
	require.NoError(t, repo.CompleteBalancePreauthorizationSettlement(context.Background(), "pg-hold:batch-image:b1", 11))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveBatchImageBalance_WithPreauthSkipsOutboxAndAdvancesSettlementAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cmd := &service.BatchImageBalanceHoldCommand{
		RequestID:        "batch_image_hold:b100",
		APIKeyID:         7,
		UserID:           42,
		BatchID:          "b100",
		HoldAmount:       6.5,
		PreauthRequestID: "pg-hold:batch-image:b100",
	}
	cmd.Normalize()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup`).
		WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(cmd.RequestID, cmd.APIKeyID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`SET LOCAL sub2api\.skip_live_balance_outbox = 'on'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*status = \$7`).
		WithArgs(
			"pg-hold:batch-image:b100",
			int64(7),
			6.5,
			"pg-direct-hold:pg-hold:batch-image:b100",
			service.BalanceSettlementFinalizationPending,
			service.BalanceSettlementApplied,
			service.BalanceSettlementAuthorized,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1`).
		WithArgs(6.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(3.5, 6.5))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	res, err := repo.ReserveBatchImageBalance(context.Background(), cmd)
	require.NoError(t, err)
	require.True(t, res.Applied)
	require.NotNil(t, res.NewBalance)
	require.InDelta(t, 3.5, *res.NewBalance, 1e-9)
	require.NotNil(t, res.FrozenBalance)
	require.InDelta(t, 6.5, *res.FrozenBalance, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveBatchImageBalance_WithPreauthRollsBackOnSettlementConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cmd := &service.BatchImageBalanceHoldCommand{
		RequestID:        "batch_image_hold:b101",
		APIKeyID:         7,
		UserID:           42,
		BatchID:          "b101",
		HoldAmount:       6.5,
		PreauthRequestID: "pg-hold:batch-image:b101",
	}
	cmd.Normalize()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup`).
		WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(cmd.RequestID, cmd.APIKeyID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`SET LOCAL sub2api\.skip_live_balance_outbox = 'on'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements`).
		WithArgs(
			"pg-hold:batch-image:b101",
			int64(7),
			6.5,
			"pg-direct-hold:pg-hold:batch-image:b101",
			service.BalanceSettlementFinalizationPending,
			service.BalanceSettlementApplied,
			service.BalanceSettlementAuthorized,
		).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	repo := &usageBillingRepository{db: db}
	_, err = repo.ReserveBatchImageBalance(context.Background(), cmd)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveBatchImageBalance_WithPreauthRollsBackOnPGInsufficientBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cmd := &service.BatchImageBalanceHoldCommand{
		RequestID:        "batch_image_hold:b102",
		APIKeyID:         7,
		UserID:           42,
		BatchID:          "b102",
		HoldAmount:       12.0,
		PreauthRequestID: "pg-hold:batch-image:b102",
	}
	cmd.Normalize()

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO usage_billing_dedup`).
		WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT request_fingerprint\s+FROM usage_billing_dedup_archive`).
		WithArgs(cmd.RequestID, cmd.APIKeyID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`SET LOCAL sub2api\.skip_live_balance_outbox = 'on'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = balance - \$1`).
		WithArgs(12.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT 1\s+FROM users\s+WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
	mock.ExpectRollback()

	repo := &usageBillingRepository{db: db}
	_, err = repo.ReserveBatchImageBalance(context.Background(), cmd)
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMediaBillingJob_WithPreauthSkipsOutboxAndAdvancesSettlement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	input := &service.MediaBillingJob{
		ID:               "video_100",
		Kind:             service.MediaBillingKindGrokVideo,
		UserID:           42,
		APIKeyID:         9,
		AccountID:        3,
		GroupID:          5,
		Status:           service.MediaBillingCreating,
		ReservedAmount:   4.25,
		AvailableAt:      now,
		PreauthRequestID: "pg-hold:media-video:video_100",
	}

	cols := []string{
		"id", "kind", "user_id", "api_key_id", "account_id", "group_id", "subscription_id",
		"upstream_task_id", "status", "reserved_amount", "snapshot", "intent",
		"created_at", "updated_at", "available_at", "attempts",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO media_billing_jobs`).
		WillReturnRows(sqlmock.NewRows(cols).AddRow(
			input.ID, input.Kind, input.UserID, input.APIKeyID, input.AccountID, input.GroupID, nil,
			"", input.Status, input.ReservedAmount, nil, nil,
			now, now, now, 0,
		))
	mock.ExpectExec(`SET LOCAL sub2api\.skip_live_balance_outbox = 'on'`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements`).
		WithArgs(
			"pg-hold:media-video:video_100",
			int64(9),
			4.25,
			"pg-direct-hold:pg-hold:media-video:video_100",
			service.BalanceSettlementFinalizationPending,
			service.BalanceSettlementApplied,
			service.BalanceSettlementAuthorized,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1`).
		WithArgs(4.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(5.75, 4.25))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	created, err := repo.CreateMediaBillingJob(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "pg-hold:media-video:video_100", created.PreauthRequestID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMediaBillingJob_ExistingConflictDoesNotAdvancePreauth(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	input := &service.MediaBillingJob{
		ID:               "video_dup",
		Kind:             service.MediaBillingKindGrokVideo,
		UserID:           42,
		APIKeyID:         9,
		AccountID:        3,
		GroupID:          5,
		Status:           service.MediaBillingCreating,
		ReservedAmount:   4.25,
		AvailableAt:      now,
		PreauthRequestID: "pg-hold:media-video:video_dup",
	}
	cols := []string{
		"id", "kind", "user_id", "api_key_id", "account_id", "group_id", "subscription_id",
		"upstream_task_id", "status", "reserved_amount", "snapshot", "intent",
		"created_at", "updated_at", "available_at", "attempts",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)INSERT INTO media_billing_jobs`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`(?s)SELECT .* FROM media_billing_jobs WHERE id = \$1 FOR UPDATE`).
		WithArgs("video_dup").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(
			input.ID, input.Kind, input.UserID, input.APIKeyID, input.AccountID, input.GroupID, nil,
			"", input.Status, input.ReservedAmount, nil, nil,
			now, now, now, 0,
		))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	existing, err := repo.CreateMediaBillingJob(context.Background(), input)
	require.NoError(t, err)
	require.Empty(t, existing.PreauthRequestID, "idempotent existing job hit must leave PreauthRequestID empty so caller refunds unused Redis hold")
	require.NoError(t, mock.ExpectationsWereMet())
}
