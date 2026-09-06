package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGetUserDashboardStatsUsesHourlyUsersNotFullTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	userID := int64(42)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`).
		WithArgs(userID, service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(`FROM usage_dashboard_hourly_users`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"hourly_rows", "total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_cost", "actual_cost", "total_duration_ms", "duration_count",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(3), int64(10), int64(100), int64(200), int64(1), int64(2), 1.5, 1.2, int64(300), int64(10),
			int64(4), int64(40), int64(80), int64(0), int64(1), 0.4, 0.3))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE user_id = \$1 AND created_at >= \$2`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"c", "in", "out", "cc", "cr", "cost", "actual", "dur", "dur_n",
			"today_c", "today_in", "today_out", "today_cc", "today_cr", "today_cost", "today_actual",
		}).AddRow(int64(1), int64(5), int64(6), int64(0), int64(0), 0.1, 0.1, int64(50), int64(1),
			int64(1), int64(5), int64(6), int64(0), int64(0), 0.1, 0.1))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE created_at >= \$1`).
		WithArgs(sqlmock.AnyArg(), userID).
		WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(int64(5), int64(50)))
	mock.ExpectQuery(`FROM usage_logs ul`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "total_requests", "total_tokens", "total_actual_cost", "today_requests", "today_tokens", "today_actual_cost",
		}))

	stats, err := repo.GetUserDashboardStats(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, int64(11), stats.TotalRequests)
	require.Equal(t, int64(105), stats.TotalInputTokens)
	require.Equal(t, int64(5), stats.TodayRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserDashboardStatsFallsBackToWindowedUsageLogs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	userID := int64(7)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`).
		WithArgs(userID, service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(`FROM usage_dashboard_hourly_users`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"hourly_rows", "total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_cost", "actual_cost", "total_duration_ms", "duration_count",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0, int64(0), int64(0),
			int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE user_id = \$1 AND created_at >= \$2`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
			"total_cost", "total_actual_cost", "avg_duration_ms",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(8), int64(80), int64(90), int64(1), int64(2), 3.0, 2.5, 12.0,
			int64(3), int64(30), int64(40), int64(0), int64(1), 1.0, 0.8))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE created_at >= \$1`).
		WithArgs(sqlmock.AnyArg(), userID).
		WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(int64(0), int64(0)))
	mock.ExpectQuery(`FROM usage_logs ul`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "total_requests", "total_tokens", "total_actual_cost", "today_requests", "today_tokens", "today_actual_cost",
		}))

	stats, err := repo.GetUserDashboardStats(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, int64(8), stats.TotalRequests)
	require.Equal(t, int64(3), stats.TodayRequests)
	require.Equal(t, int64(173), stats.TotalTokens)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUserDashboardStatsIgnoresZeroUsageHourlyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	userID := int64(11)

	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`).
		WithArgs(userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`).
		WithArgs(userID, service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery(`FROM usage_dashboard_hourly_users`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"hourly_rows", "total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_cost", "actual_cost", "total_duration_ms", "duration_count",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(4), int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0, int64(0), int64(0),
			int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE user_id = \$1 AND created_at >= \$2`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
			"total_cost", "total_actual_cost", "avg_duration_ms",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(12), int64(120), int64(80), int64(0), int64(0), 4.0, 3.5, 10.0,
			int64(2), int64(20), int64(10), int64(0), int64(0), 0.5, 0.4))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE created_at >= \$1`).
		WithArgs(sqlmock.AnyArg(), userID).
		WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(int64(0), int64(0)))
	mock.ExpectQuery(`FROM usage_logs ul`).
		WithArgs(userID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "total_requests", "total_tokens", "total_actual_cost", "today_requests", "today_tokens", "today_actual_cost",
		}))

	stats, err := repo.GetUserDashboardStats(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, int64(12), stats.TotalRequests)
	require.Equal(t, int64(2), stats.TodayRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAPIKeyDashboardStatsUsesCreatedAtWindow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	apiKeyID := int64(9)
	mock.ExpectQuery(`FROM usage_logs\s+WHERE api_key_id = \$1 AND created_at >= \$2`).
		WithArgs(apiKeyID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
			"total_cost", "total_actual_cost", "avg_duration_ms",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost",
		}).AddRow(int64(2), int64(10), int64(20), int64(0), int64(0), 0.5, 0.4, 8.0,
			int64(1), int64(4), int64(6), int64(0), int64(0), 0.2, 0.2))
	mock.ExpectQuery(`FROM usage_logs\s+WHERE created_at >= \$1 AND api_key_id = \$2`).
		WithArgs(sqlmock.AnyArg(), apiKeyID).
		WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(int64(10), int64(100)))

	stats, err := repo.GetAPIKeyDashboardStats(context.Background(), apiKeyID)
	require.NoError(t, err)
	require.Equal(t, int64(2), stats.TotalRequests)
	require.Equal(t, int64(1), stats.TodayRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFillDashboardUsageStatsFromHourlyUsesRollupThenTail(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := newUsageLogRepositoryWithSQL(nil, db)
	now := time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)
	today := timezone.StartOfDayInUserLocation(now, "UTC")
	hourStart := now.Truncate(time.Hour)
	start := today.Add(-24 * time.Hour)
	end := now
	todayEnd := today.Add(24 * time.Hour)
	stats := &DashboardStats{}

	mock.ExpectQuery(`FROM usage_dashboard_hourly\s+WHERE`).
		WillReturnRows(sqlmock.NewRows([]string{
			"hourly_rows", "total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
			"total_cost", "actual_cost", "account_cost", "total_duration_ms",
			"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
			"today_cost", "today_actual_cost", "today_account_cost",
		}).AddRow(int64(5), int64(20), int64(200), int64(300), int64(2), int64(3), 4.0, 3.5, 4.0, int64(400),
			int64(6), int64(60), int64(70), int64(1), int64(1), 1.0, 0.9, 1.0))
	mock.ExpectQuery(`FROM usage_dashboard_hourly_users`).
		WillReturnRows(sqlmock.NewRows([]string{"active_users", "hourly_active_users"}).AddRow(int64(4), int64(1)))
	mock.ExpectQuery(`FROM usage_logs`).
		WillReturnRows(sqlmock.NewRows([]string{
			"tr", "ti", "to", "tcc", "tcr", "tc", "ta", "tac", "td",
			"tr2", "ti2", "to2", "tcc2", "tcr2", "tc2", "ta2", "tac2", "act", "hact",
		}).AddRow(int64(2), int64(10), int64(20), int64(0), int64(0), 0.2, 0.2, 0.2, int64(30),
			int64(2), int64(10), int64(20), int64(0), int64(0), 0.2, 0.2, 0.2, int64(2), int64(2)))

	ok, err := repo.fillDashboardUsageStatsFromHourly(context.Background(), stats, start, end, today, todayEnd, hourStart)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, int64(22), stats.TotalRequests)
	require.Equal(t, int64(8), stats.TodayRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDashboardSubjectWindowStart(t *testing.T) {
	today := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	now := today.Add(12 * time.Hour)
	got := dashboardSubjectWindowStart(now, today)
	require.Equal(t, now.Add(-dashboardSubjectUsageLookback), got)

	early := today.Add(time.Hour)
	got = dashboardSubjectWindowStart(early, today)
	require.False(t, got.After(today))
}

func TestIsMissingRelationError(t *testing.T) {
	require.True(t, isMissingRelationError(&pq.Error{Code: "42P01"}))
	require.False(t, isMissingRelationError(&pq.Error{Code: "23505"}))
}
