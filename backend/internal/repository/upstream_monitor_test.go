package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamMonitorRecordAlertsUsesExplicitParameterTypes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Date(2026, time.November, 17, 0, 0, 0, 0, time.UTC)
	alert := service.UpstreamAlert{
		AccountID: 7,
		Kind:      service.UpstreamAlertKindBalanceLow,
		Level:     "warning",
		Unit:      "USD",
		Message:   "Low balance",
		CreatedAt: now,
	}
	cooldownSince := now.Add(-6 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectExec(`
INSERT INTO upstream_monitor_alerts(account_id, kind, level, balance, threshold, unit, old_rate, new_rate, message, created_at)
SELECT $1::bigint, $2::varchar, $3, $4, $5, $6, $7, $8, $9, $10
WHERE NOT EXISTS (
  SELECT 1 FROM upstream_monitor_alerts
  WHERE account_id = $1::bigint AND kind = $2::varchar AND created_at >= $11::timestamptz
)`).WithArgs(alert.AccountID, alert.Kind, alert.Level, nil, nil, alert.Unit, nil, nil,
		alert.Message, alert.CreatedAt, cooldownSince).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	recorded, err := NewUpstreamMonitorRepository(db).RecordAlerts(context.Background(), []service.UpstreamAlert{alert}, cooldownSince)
	require.NoError(t, err)
	require.Equal(t, 1, recorded)
	require.NoError(t, mock.ExpectationsWereMet())
}
