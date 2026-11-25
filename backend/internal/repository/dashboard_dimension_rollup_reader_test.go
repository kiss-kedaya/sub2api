package repository

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestUserUsageTrendRollupReaderMergesHourlyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

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

func TestDashboardRollupBoundsUseOnlyClosedHours(t *testing.T) {
	for _, tc := range []struct {
		name            string
		clockOffset     time.Duration
		watermarkOffset time.Duration
		cutoffOffset    time.Duration
		checkCoverage   bool
		covered         bool
	}{
		{name: "lag within current hour", clockOffset: 30 * time.Minute, watermarkOffset: 25 * time.Minute, checkCoverage: true, covered: true},
		{name: "fresh watermark before rollover", clockOffset: 20 * time.Second, watermarkOffset: -20 * time.Second, cutoffOffset: -time.Hour, checkCoverage: true, covered: true},
		{name: "stale watermark from earlier hour", clockOffset: 30 * time.Minute, watermarkOffset: -5 * time.Minute},
		{name: "missing dimension coverage", clockOffset: 30 * time.Minute, watermarkOffset: 25 * time.Minute, checkCoverage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hour := time.Now().UTC().Truncate(time.Hour)
				time.Sleep(tc.clockOffset)
				start := hour.Add(-48 * time.Hour)
				end := hour.Add(24 * time.Hour)
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer func() { _ = db.Close() }()
				mock.ExpectQuery("SELECT last_aggregated_at FROM usage_dashboard_aggregation_watermark").
					WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(hour.Add(tc.watermarkOffset)))
				expectedCutoff := hour.Add(tc.cutoffOffset)
				if tc.checkCoverage {
					mock.ExpectQuery("SELECT COALESCE[(]MIN[(]bucket_start[)]").
						WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(start))
					mock.ExpectQuery("SELECT [(]").WithArgs(start, expectedCutoff, "user").
						WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(tc.covered))
				}
				// The reader only needs SQL. Do not start cache janitors inside the test clock.
				repo := &usageLogRepository{sql: db}
				repo.SetDashboardRollupReadEnabled(true)
				repo.SetDashboardRollupStaleness(time.Minute)
				window, ok := repo.rollupBoundsForDimension(context.Background(), start, end, "user", 0)
				require.Equal(t, tc.covered, ok)
				if ok {
					require.Equal(t, start, window.from, "an hour-aligned start has no raw head")
					require.Equal(t, expectedCutoff, window.cutoff)
					require.Equal(t, time.Now().UTC(), window.to)
				}
				require.NoError(t, mock.ExpectationsWereMet())
			})
		})
	}
}

func TestDashboardRollupReadDisabledFallsBackToRawPath(t *testing.T) {
	repo := newUsageLogRepositoryWithSQL(nil, nil)
	repo.SetDashboardRollupReadEnabled(false)
	_, ok := repo.rollupBounds(context.Background(), time.Now().Add(-time.Hour), time.Now())
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
