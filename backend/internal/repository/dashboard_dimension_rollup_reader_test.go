package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUserUsageTrendRollupReaderMergesHourlyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	now := time.Now().UTC()
	start := now.Truncate(time.Hour).Add(-time.Hour)
	end := now.Truncate(time.Hour)

	mock.ExpectQuery("SELECT last_aggregated_at FROM usage_dashboard_aggregation_watermark").
		WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(now))
	mock.ExpectQuery(`SELECT COALESCE\(MIN\(bucket_start\)`).
		WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(start))
	mock.ExpectQuery(`SELECT \(`).
		WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
	mock.ExpectQuery("SELECT bucket_start, dimension_key, user_id, group_id, endpoint_type").
		WillReturnRows(sqlmock.NewRows([]string{
			"bucket_start", "dimension_key", "user_id", "group_id", "endpoint_type",
			"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_cost", "actual_cost", "account_cost", "duration_count", "total_duration_ms",
		}).AddRow(start, "7", int64(7), int64(0), "", int64(3), int64(10), int64(20), int64(2), int64(1), 0.5, 0.4, 0.6, int64(2), int64(100)))
	mock.ExpectQuery(`SELECT id, COALESCE\(email,''\), COALESCE\(username,''\)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "username"}).AddRow(int64(7), "u@example.com", "user"))

	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.SetDashboardRollupReadEnabled(true)
	result, covered, err := repo.GetUserUsageTrendWithRollups(context.Background(), start, end, "hour", 12)
	require.NoError(t, err)
	require.True(t, covered)
	require.Len(t, result, 1)
	require.Equal(t, int64(7), result[0].UserID)
	require.Equal(t, int64(3), result[0].Requests)
	require.Equal(t, int64(33), result[0].Tokens)
	require.Equal(t, "u@example.com", result[0].Email)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardRollupReadDisabledFallsBackToRawPath(t *testing.T) {
	repo := newUsageLogRepositoryWithSQL(nil, nil)
	repo.SetDashboardRollupReadEnabled(false)
	_, _, ok := repo.rollupBounds(context.Background(), time.Now().Add(-time.Hour), time.Now())
	require.False(t, ok)
}

func TestMergeUsageStatsLeavesAverageDurationForExplicitSummary(t *testing.T) {
	dst := &UsageStats{
		TotalRequests:     10,
		AverageDurationMs: 100,
	}
	src := &UsageStats{
		TotalRequests:     10,
		AverageDurationMs: 200,
	}

	mergeUsageStats(dst, src)

	// Requests with NULL duration are included in TotalRequests but excluded
	// from the duration denominator. The caller must use duration sum/count,
	// rather than reverse-engineering an average from request totals.
	require.Equal(t, int64(20), dst.TotalRequests)
	require.Equal(t, 100.0, dst.AverageDurationMs)
}
