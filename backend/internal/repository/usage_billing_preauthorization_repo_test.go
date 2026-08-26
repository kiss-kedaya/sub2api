package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const prepareBalancePreauthorizationSQL = `(?s)INSERT INTO billing_balance_settlements.*WHERE NOT EXISTS .*usage_billing_dedup.*usage_billing_dedup_archive.*ON CONFLICT .*DO UPDATE.*expires_at > NOW\(\).*RETURNING request_id`

func TestPrepareBalancePreauthorizationCreatesPreparedRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Now().UTC().Add(time.Hour)
	updatedAt := time.Now().UTC()
	mock.ExpectQuery(prepareBalancePreauthorizationSQL).
		WithArgs("request-1", int64(7), "auth-fingerprint", int64(42), 0.12345679, int16(0), expiresAt, int16(0), int16(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_id", "api_key_id", "user_id", "request_fingerprint", "authorization_fingerprint",
			"hold_usd", "amount_usd", "status", "expires_at", "updated_at",
		}).AddRow("request-1", 7, 42, "", "auth-fingerprint", "0.12345679", "0", 0, expiresAt, updatedAt))

	repo := &usageBillingRepository{db: db}
	record, err := repo.PrepareBalancePreauthorization(context.Background(), &service.BalancePreauthorizationCommand{
		RequestID:                " request-1 ",
		APIKeyID:                 7,
		UserID:                   42,
		AuthorizationFingerprint: " auth-fingerprint ",
		HoldAmount:               0.123456789,
		ExpiresAt:                expiresAt,
	})
	require.NoError(t, err)
	require.Equal(t, "request-1", record.RequestID)
	require.Equal(t, "auth-fingerprint", record.AuthorizationFingerprint)
	require.Equal(t, service.BalanceSettlementPrepared, record.Status)
	require.InDelta(t, 0.12345679, record.HoldAmount, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareBalancePreauthorizationReturnsExistingFinalState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Now().UTC().Add(time.Hour)
	updatedAt := time.Now().UTC()
	mock.ExpectQuery(prepareBalancePreauthorizationSQL).
		WithArgs("request-2", int64(8), "same", int64(43), 0.25, int16(0), expiresAt, int16(0), int16(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_id", "api_key_id", "user_id", "request_fingerprint", "authorization_fingerprint",
			"hold_usd", "amount_usd", "status", "expires_at", "updated_at",
		}).AddRow("request-2", 8, 43, "", "same", "0.25", "0", service.BalanceSettlementRefunded, expiresAt, updatedAt))

	repo := &usageBillingRepository{db: db}
	record, err := repo.PrepareBalancePreauthorization(context.Background(), &service.BalancePreauthorizationCommand{
		RequestID:                "request-2",
		APIKeyID:                 8,
		UserID:                   43,
		AuthorizationFingerprint: "same",
		HoldAmount:               0.25,
		ExpiresAt:                expiresAt,
	})
	require.NoError(t, err)
	require.Equal(t, service.BalanceSettlementRefunded, record.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareBalancePreauthorizationAllowsZeroPricedHold(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Now().UTC().Add(time.Hour)
	updatedAt := time.Now().UTC()
	mock.ExpectQuery(prepareBalancePreauthorizationSQL).
		WithArgs("request-free", int64(10), "free-fingerprint", int64(45), 0.0, int16(0), expiresAt, int16(0), int16(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_id", "api_key_id", "user_id", "request_fingerprint", "authorization_fingerprint",
			"hold_usd", "amount_usd", "status", "expires_at", "updated_at",
		}).AddRow("request-free", 10, 45, "", "free-fingerprint", "0", "0", 0, expiresAt, updatedAt))

	repo := &usageBillingRepository{db: db}
	record, err := repo.PrepareBalancePreauthorization(context.Background(), &service.BalancePreauthorizationCommand{
		RequestID:                "request-free",
		APIKeyID:                 10,
		UserID:                   45,
		AuthorizationFingerprint: "free-fingerprint",
		HoldAmount:               0.000000001,
		ExpiresAt:                expiresAt,
	})
	require.NoError(t, err)
	require.Zero(t, record.HoldAmount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareBalancePreauthorizationRejectsConflictOrAlreadyBilledRequest(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	expiresAt := time.Now().UTC().Add(time.Hour)
	mock.ExpectQuery(prepareBalancePreauthorizationSQL).
		WithArgs("request-3", int64(9), "different", int64(44), 0.5, int16(0), expiresAt, int16(0), int16(1)).
		WillReturnError(sql.ErrNoRows)

	repo := &usageBillingRepository{db: db}
	_, err = repo.PrepareBalancePreauthorization(context.Background(), &service.BalancePreauthorizationCommand{
		RequestID:                "request-3",
		APIKeyID:                 9,
		UserID:                   44,
		AuthorizationFingerprint: "different",
		HoldAmount:               0.5,
		ExpiresAt:                expiresAt,
	})
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareBalancePreauthorizationValidatesInputBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &usageBillingRepository{db: db}

	valid := service.BalancePreauthorizationCommand{
		RequestID:                "request",
		APIKeyID:                 1,
		UserID:                   2,
		AuthorizationFingerprint: "fingerprint",
		HoldAmount:               0.1,
		ExpiresAt:                time.Now().Add(time.Hour),
	}
	tests := []struct {
		name   string
		mutate func(*service.BalancePreauthorizationCommand)
	}{
		{name: "empty request", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.RequestID = " " }},
		{name: "empty fingerprint", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.AuthorizationFingerprint = " " }},
		{name: "invalid api key", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.APIKeyID = 0 }},
		{name: "invalid user", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.UserID = 0 }},
		{name: "nan", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.HoldAmount = math.NaN() }},
		{name: "infinity", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.HoldAmount = math.Inf(1) }},
		{name: "expired", mutate: func(cmd *service.BalancePreauthorizationCommand) { cmd.ExpiresAt = time.Now().Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := valid
			test.mutate(&cmd)
			_, err := repo.PrepareBalancePreauthorization(context.Background(), &cmd)
			require.ErrorIs(t, err, service.ErrInvalidBillingPreauthorizationEstimate)
		})
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalancePreauthorizationTransitionsAreIdempotentWithoutRegression(t *testing.T) {
	tests := []struct {
		name  string
		call  func(*usageBillingRepository) error
		query string
		args  []driver.Value
	}{
		{
			name: "authorize",
			call: func(repo *usageBillingRepository) error {
				return repo.MarkBalancePreauthorizationAuthorized(context.Background(), " request ", 7)
			},
			query: `(?s)UPDATE billing_balance_settlements.*SET status = CASE.*updated_at = CASE.*status IN.*expires_at > NOW\(\)`,
			args:  []driver.Value{"request", int64(7), int16(1), int16(1), int16(0)},
		},
		{
			name: "begin finalization keeps exact target or completed descendant",
			call: func(repo *usageBillingRepository) error {
				return repo.BeginBalancePreauthorizationFinalization(context.Background(), "request", 7, 0.250000001, " actual ")
			},
			query: `(?s)UPDATE billing_balance_settlements.*status IN \(\$5, \$7, \$8, \$9\).*amount_usd = \$3.*request_fingerprint = \$4`,
			args:  []driver.Value{"request", int64(7), 0.25, "actual", int16(2), int16(1), int16(3), int16(4), int16(5)},
		},
		{
			name: "complete settlement keeps queued or applied target",
			call: func(repo *usageBillingRepository) error {
				return repo.CompleteBalancePreauthorizationSettlement(context.Background(), "request", 7)
			},
			query: `(?s)UPDATE billing_balance_settlements.*wallet_preapplied = CASE WHEN status = \$6 THEN TRUE.*available_at = CASE.*amount_usd > 0.*status IN`,
			args:  []driver.Value{"request", int64(7), int16(3), int16(3), int16(4), int16(2)},
		},
		{
			name: "begin refund preserves already refunded",
			call: func(repo *usageBillingRepository) error {
				return repo.BeginBalancePreauthorizationRefund(context.Background(), "request", 7)
			},
			query: `(?s)UPDATE billing_balance_settlements.*amount_usd = CASE.*status = \$4 AND amount_usd = 0.*status = \$5`,
			args:  []driver.Value{"request", int64(7), int16(2), int16(2), int16(5), int16(0), int16(1)},
		},
		{
			name: "complete refund requires zero amount and preserves target",
			call: func(repo *usageBillingRepository) error {
				return repo.CompleteBalancePreauthorizationRefund(context.Background(), "request", 7)
			},
			query: `(?s)UPDATE billing_balance_settlements.*applied_at = CASE.*status = \$5 AND amount_usd = 0.*status = \$4`,
			args:  []driver.Value{"request", int64(7), int16(5), int16(5), int16(2)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectExec(test.query).
				WithArgs(test.args...).
				WillReturnResult(sqlmock.NewResult(0, 1))

			err = test.call(&usageBillingRepository{db: db})
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBalancePreauthorizationTransitionRejectsWrongState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*WHERE request_id`).
		WithArgs("request", int64(7), int16(1), int16(1), int16(0)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := &usageBillingRepository{db: db}
	err = repo.MarkBalancePreauthorizationAuthorized(context.Background(), "request", 7)
	require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBeginBalancePreauthorizationFinalizationValidatesAmountAndFingerprint(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &usageBillingRepository{db: db}

	tests := []struct {
		amount      float64
		fingerprint string
	}{
		{amount: -1, fingerprint: "fingerprint"},
		{amount: math.NaN(), fingerprint: "fingerprint"},
		{amount: math.Inf(1), fingerprint: "fingerprint"},
		{amount: 0.1, fingerprint: " "},
	}
	for _, test := range tests {
		err := repo.BeginBalancePreauthorizationFinalization(context.Background(), "request", 7, test.amount, test.fingerprint)
		require.ErrorIs(t, err, service.ErrInvalidBillingPreauthorizationEstimate)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBalancePreauthorizationRepositoryErrors(t *testing.T) {
	var nilRepo *usageBillingRepository
	_, err := nilRepo.PrepareBalancePreauthorization(context.Background(), &service.BalancePreauthorizationCommand{})
	require.EqualError(t, err, "usage billing repository db is nil")
	require.EqualError(t, nilRepo.MarkBalancePreauthorizationAuthorized(context.Background(), "request", 1), "usage billing repository db is nil")

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectExec(`(?s)UPDATE billing_balance_settlements.*WHERE request_id`).
		WillReturnError(errors.New("database unavailable"))
	repo := &usageBillingRepository{db: db}
	require.EqualError(t, repo.MarkBalancePreauthorizationAuthorized(context.Background(), "request", 1), "database unavailable")
	require.NoError(t, mock.ExpectationsWereMet())
}
