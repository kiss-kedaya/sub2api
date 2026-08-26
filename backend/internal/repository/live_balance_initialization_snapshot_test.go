package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLoadLiveBalanceInitializationSnapshotUsesOneMVCCStatement(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`(?s)SELECT u.balance::text,.*COALESCE\(h.last_event_id, 0\),.*EXISTS.*billing_balance_settlements.*LEFT JOIN live_balance_adjustment_heads.*WHERE u.id = \$1`).
		WithArgs(
			int64(42),
			service.BalanceSettlementAuthorized,
			service.BalanceSettlementFinalizationPending,
			service.BalanceSettlementPending,
		).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "watermark", "has_unsettled"}).AddRow("12.34567890", 17, true))

	snapshot, err := (&usageBillingRepository{db: db}).LoadLiveBalanceInitializationSnapshot(context.Background(), 42)
	require.NoError(t, err)
	require.InDelta(t, 12.3456789, snapshot.Balance, 1e-12)
	require.Equal(t, int64(17), snapshot.Watermark)
	require.True(t, snapshot.HasUnsettled)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoadLiveBalanceInitializationSnapshotReturnsUserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectQuery(`SELECT u.balance::text`).
		WithArgs(
			int64(404),
			service.BalanceSettlementAuthorized,
			service.BalanceSettlementFinalizationPending,
			service.BalanceSettlementPending,
		).
		WillReturnError(sql.ErrNoRows)
	_, err = (&usageBillingRepository{db: db}).LoadLiveBalanceInitializationSnapshot(context.Background(), 404)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
