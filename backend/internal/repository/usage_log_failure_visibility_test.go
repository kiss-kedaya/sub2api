package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageFailureHydrationUsesScopedIndexedBatchWithoutChangingBilling(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	ended := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	duration := 10000
	logs := []service.UsageLog{
		{ID: 1, UserID: 2, APIKeyID: 3, AccountID: 4, RequestID: "client:synthetic-request", CreatedAt: ended, DurationMs: &duration, ActualCost: .125, InputTokens: 10},
		{ID: 5, UserID: 6, APIKeyID: 7, AccountID: 8, RequestID: "local:local-request", CreatedAt: ended, DurationMs: &duration},
	}
	// Exact indexed lookups, terminal-only status and all identity/time guards
	// must remain in both sides of the union.
	pattern := `(?s)WITH wanted.*e.request_id IN \(wanted.request_id, wanted.client_request_id\).*e.user_id = wanted.user_id.*e.api_key_id = wanted.api_key_id.*e.account_id = wanted.account_id.*e.status_code BETWEEN 400 AND 599.*INTERVAL '1 second'.*wanted.duration_ms.*UNION ALL.*e.client_request_id = wanted.client_request_id.*e.user_id = wanted.user_id.*e.api_key_id = wanted.api_key_id.*e.account_id = wanted.account_id.*e.status_code BETWEEN 400 AND 599.*INTERVAL '1 second'.*wanted.duration_ms.*LIMIT 1`
	mock.ExpectQuery(pattern).
		WithArgs(int64(1), int64(2), int64(3), int64(4), "client:synthetic-request", "synthetic-request", ended, duration,
			int64(5), int64(6), int64(7), int64(8), "local:local-request", "local-request", ended, duration).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status_code"}).AddRow(1, 502))
	require.NoError(t, repo.HydrateUsageLogFailures(context.Background(), logs))
	require.NotNil(t, logs[0].FailureStatusCode)
	require.Equal(t, 502, *logs[0].FailureStatusCode)
	require.Nil(t, logs[1].FailureStatusCode, "unknown outcome is not a success assertion")
	require.Equal(t, .125, logs[0].ActualCost)
	require.Equal(t, 10, logs[0].InputTokens)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageFailureHydrationMissingIdentityDoesNotQueryOrKeepStaleStatus(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	status := 500
	logs := []service.UsageLog{{ID: 1, RequestID: "req", FailureStatusCode: &status}}
	require.NoError(t, repo.HydrateUsageLogFailures(context.Background(), logs))
	require.Nil(t, logs[0].FailureStatusCode)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageFailureHydrationQueryFailureIsNotHidden(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("WITH wanted").WillReturnError(fmt.Errorf("lookup unavailable"))
	duration := 10000
	logs := []service.UsageLog{{ID: 1, UserID: 2, APIKeyID: 3, AccountID: 4, RequestID: "req", CreatedAt: time.Now(), DurationMs: &duration}}
	require.ErrorContains(t, repo.HydrateUsageLogFailures(context.Background(), logs), "lookup unavailable")
	require.Nil(t, logs[0].FailureStatusCode)
	require.NoError(t, mock.ExpectationsWereMet())
}
