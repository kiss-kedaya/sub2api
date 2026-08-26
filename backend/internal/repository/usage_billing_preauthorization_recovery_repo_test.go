package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestListRecoverableBalancePreauthorizationsReturnsFinalizationData(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	authorizationCutoff := time.Now().UTC()
	finalizationCutoff := authorizationCutoff.Add(-30 * time.Second)
	expiresAt := authorizationCutoff.Add(-time.Minute)
	updatedAt := finalizationCutoff.Add(-time.Second)
	mock.ExpectQuery(`(?s)SELECT request_id,.*FROM billing_balance_settlements.*expires_at <= \$1.*updated_at <= \$2.*ORDER BY CASE.*FOR UPDATE SKIP LOCKED`).
		WithArgs(authorizationCutoff, finalizationCutoff, 500, int16(0), int16(1), int16(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_id", "api_key_id", "user_id", "request_fingerprint", "authorization_fingerprint",
			"hold_usd", "amount_usd", "status", "expires_at", "updated_at",
		}).AddRow("request", 7, 42, "actual", "authorization", "0.50", "0.25", 2, expiresAt, updatedAt))

	repo := &usageBillingRepository{db: db}
	records, err := repo.ListRecoverableBalancePreauthorizations(
		context.Background(), authorizationCutoff, finalizationCutoff, 0,
	)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "request", records[0].RequestID)
	require.Equal(t, "actual", records[0].RequestFingerprint)
	require.Equal(t, "authorization", records[0].AuthorizationFingerprint)
	require.Equal(t, service.BalanceSettlementFinalizationPending, records[0].Status)
	require.InDelta(t, 0.25, records[0].Amount, 1e-12)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListRecoverableBalancePreauthorizationsClampsBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cutoff := time.Now().UTC()
	mock.ExpectQuery(`(?s)FROM billing_balance_settlements.*LIMIT \$3`).
		WithArgs(cutoff, cutoff, 5000, int16(0), int16(1), int16(2)).
		WillReturnRows(sqlmock.NewRows([]string{
			"request_id", "api_key_id", "user_id", "request_fingerprint", "authorization_fingerprint",
			"hold_usd", "amount_usd", "status", "expires_at", "updated_at",
		}))

	repo := &usageBillingRepository{db: db}
	records, err := repo.ListRecoverableBalancePreauthorizations(context.Background(), cutoff, cutoff, 99999)
	require.NoError(t, err)
	require.Empty(t, records)
	require.NoError(t, mock.ExpectationsWereMet())
}
