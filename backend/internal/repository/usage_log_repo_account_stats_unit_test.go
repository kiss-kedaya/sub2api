//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// The admin account stats endpoint used to read usage_logs five times: the daily
// aggregate, a separate AVG(duration_ms), and one statement per breakdown. This
// asserts the shape that matters: one aggregate statement that already carries
// the duration sum, and one GROUPING SETS statement for all three breakdowns.
func TestGetAccountUsageStatsScansAggregateAndBreakdownOnceEach(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)

	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL statement_timeout = '70s'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)SELECT.*duration_sum.*duration_count.*FROM usage_logs.*GROUP BY created_at::date`).
		WithArgs(int64(7), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"date", "requests", "tokens", "cost", "actual_cost", "user_cost", "duration_sum", "duration_count",
		}).
			AddRow("2026-11-01", int64(2), int64(300), 0.5, 0.4, 0.3, int64(2500), int64(2)).
			AddRow("2026-11-02", int64(1), int64(0), 0.0, 0.0, 0.0, int64(0), int64(0)))
	mock.ExpectQuery(`(?s)GROUP BY GROUPING SETS`).
		WithArgs(int64(7), start, end).
		WillReturnRows(sqlmock.NewRows([]string{
			"dim", "dim_key", "requests", "input_tokens", "output_tokens",
			"cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost",
		}).
			AddRow(int64(1), "claude-sonnet", int64(2), int64(100), int64(200), int64(0), int64(0), int64(300), 0.5, 0.4).
			AddRow(int64(2), "/v1/messages", int64(3), int64(100), int64(200), int64(0), int64(0), int64(300), 0.5, 0.4).
			AddRow(int64(3), "/v1/responses", int64(3), int64(100), int64(200), int64(0), int64(0), int64(300), 0.5, 0.4))
	mock.ExpectRollback()

	resp, err := repo.GetAccountUsageStats(context.Background(), 7, start, end)
	require.NoError(t, err)
	require.Len(t, resp.History, 2)
	require.Equal(t, int64(3), resp.Summary.TotalRequests)
	// 2500ms over 2 non-NULL durations: the NULL duration stays out of the
	// denominator exactly like AVG(duration_ms) did before the merge.
	require.Equal(t, 1250.0, resp.Summary.AvgDurationMs)
	require.Len(t, resp.Models, 1)
	require.Equal(t, "/v1/messages", resp.Endpoints[0].Endpoint)
	require.Len(t, resp.UpstreamEndpoints, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}
