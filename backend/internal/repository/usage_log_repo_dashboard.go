package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// dashboardSubjectUsageLookback bounds user/API-key dashboard totals when the
// hourly rollup has no rows for that subject. Unbounded COUNT/SUM on usage_logs
// is what produced the 83GB full-table aggregations on the admin host.
const dashboardSubjectUsageLookback = 30 * 24 * time.Hour

// getPerformanceStats 获取 RPM 和 TPM（近5分钟平均值，可选按用户过滤）
func (r *usageLogRepository) getPerformanceStats(ctx context.Context, userID int64) (rpm, tpm int64, err error) {
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	query := `
		SELECT
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens + output_tokens), 0) as token_count
		FROM usage_logs
		WHERE created_at >= $1`
	args := []any{fiveMinutesAgo}
	if userID > 0 {
		query += " AND user_id = $2"
		args = append(args, userID)
	}

	var requestCount int64
	var tokenCount int64
	if err := scanSingleRow(ctx, r.sql, query, args, &requestCount, &tokenCount); err != nil {
		return 0, 0, err
	}
	return requestCount / 5, tokenCount / 5, nil
}

// UserStats 用户使用统计
type UserStats struct {
	TotalRequests   int64   `json:"total_requests"`
	TotalTokens     int64   `json:"total_tokens"`
	TotalCost       float64 `json:"total_cost"`
	InputTokens     int64   `json:"input_tokens"`
	OutputTokens    int64   `json:"output_tokens"`
	CacheReadTokens int64   `json:"cache_read_tokens"`
}

func (r *usageLogRepository) GetUserStats(ctx context.Context, userID int64, startTime, endTime time.Time) (*UserStats, error) {
	query := `
		SELECT
			COUNT(*) as total_requests,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(actual_cost), 0) as total_cost,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
	`

	stats := &UserStats{}
	if err := scanSingleRow(
		ctx,
		r.sql,
		query,
		[]any{userID, startTime, endTime},
		&stats.TotalRequests,
		&stats.TotalTokens,
		&stats.TotalCost,
		&stats.InputTokens,
		&stats.OutputTokens,
		&stats.CacheReadTokens,
	); err != nil {
		return nil, err
	}
	return stats, nil
}

// DashboardStats 仪表盘统计
type DashboardStats = usagestats.DashboardStats

func (r *usageLogRepository) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	stats := &DashboardStats{}
	now := timezone.Now()
	todayStart := timezone.Today()

	if err := r.fillDashboardEntityStats(ctx, stats, todayStart, now); err != nil {
		return nil, err
	}
	if err := r.fillDashboardUsageStatsAggregated(ctx, stats, todayStart, now); err != nil {
		return nil, err
	}

	rpm, tpm, err := r.getPerformanceStats(ctx, 0)
	if err != nil {
		return nil, err
	}
	stats.Rpm = rpm
	stats.Tpm = tpm

	return stats, nil
}

func (r *usageLogRepository) GetDashboardStatsWithRange(ctx context.Context, start, end time.Time) (*DashboardStats, error) {
	startUTC := start.UTC()
	endUTC := end.UTC()
	if !endUTC.After(startUTC) {
		return nil, errors.New("统计时间范围无效")
	}

	stats := &DashboardStats{}
	now := timezone.Now()
	todayStart := timezone.Today()

	if err := r.fillDashboardEntityStats(ctx, stats, todayStart, now); err != nil {
		return nil, err
	}
	if err := r.fillDashboardUsageStatsFromUsageLogs(ctx, stats, startUTC, endUTC, todayStart, now); err != nil {
		return nil, err
	}

	rpm, tpm, err := r.getPerformanceStats(ctx, 0)
	if err != nil {
		return nil, err
	}
	stats.Rpm = rpm
	stats.Tpm = tpm

	return stats, nil
}

func (r *usageLogRepository) fillDashboardEntityStats(ctx context.Context, stats *DashboardStats, todayUTC, now time.Time) error {
	userStatsQuery := `
		SELECT
			COUNT(*) as total_users,
			COUNT(CASE WHEN created_at >= $1 THEN 1 END) as today_new_users
		FROM users
		WHERE deleted_at IS NULL
	`
	if err := scanSingleRow(
		ctx,
		r.sql,
		userStatsQuery,
		[]any{todayUTC},
		&stats.TotalUsers,
		&stats.TodayNewUsers,
	); err != nil {
		return err
	}

	apiKeyStatsQuery := `
		SELECT
			COUNT(*) as total_api_keys,
			COUNT(CASE WHEN status = $1 THEN 1 END) as active_api_keys
		FROM api_keys
		WHERE deleted_at IS NULL
	`
	if err := scanSingleRow(
		ctx,
		r.sql,
		apiKeyStatsQuery,
		[]any{service.StatusActive},
		&stats.TotalAPIKeys,
		&stats.ActiveAPIKeys,
	); err != nil {
		return err
	}

	accountStatsQuery := `
		SELECT
			COUNT(*) as total_accounts,
			COUNT(CASE WHEN status = $1 AND schedulable = true THEN 1 END) as normal_accounts,
			COUNT(CASE WHEN status = $2 THEN 1 END) as error_accounts,
			COUNT(CASE WHEN rate_limited_at IS NOT NULL AND rate_limit_reset_at > $3 THEN 1 END) as ratelimit_accounts,
			COUNT(CASE WHEN overload_until IS NOT NULL AND overload_until > $4 THEN 1 END) as overload_accounts
		FROM accounts
		WHERE deleted_at IS NULL
	`
	if err := scanSingleRow(
		ctx,
		r.sql,
		accountStatsQuery,
		[]any{service.StatusActive, service.StatusError, now, now},
		&stats.TotalAccounts,
		&stats.NormalAccounts,
		&stats.ErrorAccounts,
		&stats.RateLimitAccounts,
		&stats.OverloadAccounts,
	); err != nil {
		return err
	}

	return nil
}

func (r *usageLogRepository) fillDashboardUsageStatsAggregated(ctx context.Context, stats *DashboardStats, todayUTC, now time.Time) error {
	totalStatsQuery := `
		SELECT
			COALESCE(SUM(total_requests), 0) as total_requests,
			COALESCE(SUM(input_tokens), 0) as total_input_tokens,
			COALESCE(SUM(output_tokens), 0) as total_output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as total_cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as total_cache_read_tokens,
			COALESCE(SUM(total_cost), 0) as total_cost,
			COALESCE(SUM(actual_cost), 0) as total_actual_cost,
			COALESCE(SUM(account_cost), 0) as total_account_cost,
			COALESCE(SUM(total_duration_ms), 0) as total_duration_ms
		FROM usage_dashboard_daily
	`
	var totalDurationMs int64
	if err := scanSingleRow(
		ctx,
		r.sql,
		totalStatsQuery,
		nil,
		&stats.TotalRequests,
		&stats.TotalInputTokens,
		&stats.TotalOutputTokens,
		&stats.TotalCacheCreationTokens,
		&stats.TotalCacheReadTokens,
		&stats.TotalCost,
		&stats.TotalActualCost,
		&stats.TotalAccountCost,
		&totalDurationMs,
	); err != nil {
		return err
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	if stats.TotalRequests > 0 {
		stats.AverageDurationMs = float64(totalDurationMs) / float64(stats.TotalRequests)
	}

	todayStatsQuery := `
		SELECT
			total_requests as today_requests,
			input_tokens as today_input_tokens,
			output_tokens as today_output_tokens,
			cache_creation_tokens as today_cache_creation_tokens,
			cache_read_tokens as today_cache_read_tokens,
			total_cost as today_cost,
			actual_cost as today_actual_cost,
			account_cost as today_account_cost,
			active_users as active_users
		FROM usage_dashboard_daily
		WHERE bucket_date = $1::date
	`
	if err := scanSingleRow(
		ctx,
		r.sql,
		todayStatsQuery,
		[]any{todayUTC},
		&stats.TodayRequests,
		&stats.TodayInputTokens,
		&stats.TodayOutputTokens,
		&stats.TodayCacheCreationTokens,
		&stats.TodayCacheReadTokens,
		&stats.TodayCost,
		&stats.TodayActualCost,
		&stats.TodayAccountCost,
		&stats.ActiveUsers,
	); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	}
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens

	hourlyActiveQuery := `
		SELECT active_users
		FROM usage_dashboard_hourly
		WHERE bucket_start = $1
	`
	hourStart := now.In(timezone.Location()).Truncate(time.Hour)
	if err := scanSingleRow(ctx, r.sql, hourlyActiveQuery, []any{hourStart}, &stats.HourlyActiveUsers); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
	}

	return nil
}

func (r *usageLogRepository) fillDashboardUsageStatsFromUsageLogs(ctx context.Context, stats *DashboardStats, startUTC, endUTC, todayUTC, now time.Time) error {
	todayEnd := todayUTC.Add(24 * time.Hour)
	hourStart := now.In(timezone.Location()).Truncate(time.Hour)
	if hourStart.Before(startUTC) {
		hourStart = startUTC
	}
	usedRollup, err := r.fillDashboardUsageStatsFromHourly(ctx, stats, startUTC, endUTC, todayUTC, todayEnd, hourStart)
	if err != nil {
		return err
	}
	if usedRollup {
		return nil
	}
	return r.fillDashboardUsageStatsFromUsageLogsRaw(ctx, stats, startUTC, endUTC, todayUTC, todayEnd, now)
}

func (r *usageLogRepository) fillDashboardUsageStatsFromHourly(ctx context.Context, stats *DashboardStats, startUTC, endUTC, todayUTC, todayEnd, hourStart time.Time) (bool, error) {
	hourlyQuery := `
		SELECT
			COUNT(*) FILTER (WHERE bucket_start >= $1 AND bucket_start < LEAST($2::timestamptz, $5::timestamptz)) AS hourly_rows,
			COALESCE(SUM(total_requests) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(input_tokens) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(account_cost) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(total_duration_ms) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2), 0),
			COALESCE(SUM(total_requests) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(input_tokens) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0),
			COALESCE(SUM(account_cost) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4 AND bucket_start < $5), 0)
		FROM usage_dashboard_hourly
		WHERE bucket_start >= LEAST($1::timestamptz, $3::timestamptz)
			AND bucket_start < $5
	`
	var hourlyRows, totalDurationMs int64
	if err := scanSingleRow(
		ctx,
		r.sql,
		hourlyQuery,
		[]any{startUTC, endUTC, todayUTC, todayEnd, hourStart},
		&hourlyRows,
		&stats.TotalRequests,
		&stats.TotalInputTokens,
		&stats.TotalOutputTokens,
		&stats.TotalCacheCreationTokens,
		&stats.TotalCacheReadTokens,
		&stats.TotalCost,
		&stats.TotalActualCost,
		&stats.TotalAccountCost,
		&totalDurationMs,
		&stats.TodayRequests,
		&stats.TodayInputTokens,
		&stats.TodayOutputTokens,
		&stats.TodayCacheCreationTokens,
		&stats.TodayCacheReadTokens,
		&stats.TodayCost,
		&stats.TodayActualCost,
		&stats.TodayAccountCost,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		// Missing rollup table → fall back to the bounded raw scan.
		if isMissingRelationError(err) {
			return false, nil
		}
		return false, err
	}
	if hourlyRows == 0 {
		return false, nil
	}

	activeQuery := `
		SELECT
			COUNT(DISTINCT user_id) FILTER (WHERE bucket_start >= $1 AND bucket_start < $2 AND bucket_start < $3),
			COUNT(DISTINCT user_id) FILTER (WHERE bucket_start >= $3 AND bucket_start < $4)
		FROM usage_dashboard_hourly_users
		WHERE bucket_start >= LEAST($1::timestamptz, $3::timestamptz)
			AND bucket_start < GREATEST($2::timestamptz, $4::timestamptz)
	`
	hourEnd := hourStart.Add(time.Hour)
	if err := scanSingleRow(ctx, r.sql, activeQuery, []any{todayUTC, todayEnd, hourStart, hourEnd}, &stats.ActiveUsers, &stats.HourlyActiveUsers); err != nil && !errors.Is(err, sql.ErrNoRows) {
		if !isMissingRelationError(err) {
			return false, err
		}
	}

	if err := r.addDashboardUsageTailFromUsageLogs(ctx, stats, hourStart, endUTC, todayUTC, todayEnd, &totalDurationMs); err != nil {
		return false, err
	}

	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	if stats.TotalRequests > 0 {
		stats.AverageDurationMs = float64(totalDurationMs) / float64(stats.TotalRequests)
	}
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens
	return true, nil
}

func (r *usageLogRepository) addDashboardUsageTailFromUsageLogs(ctx context.Context, stats *DashboardStats, hourStart, endUTC, todayUTC, todayEnd time.Time, totalDurationMs *int64) error {
	if !endUTC.After(hourStart) && !todayEnd.After(hourStart) {
		return nil
	}
	tailQuery := `
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz),
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COALESCE(SUM(COALESCE(duration_ms, 0)) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0),
			COUNT(*) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz),
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0),
			COUNT(DISTINCT user_id) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz),
			COUNT(DISTINCT user_id) FILTER (WHERE created_at >= $5::timestamptz AND created_at < $6::timestamptz)
		FROM usage_logs
		WHERE created_at >= $5::timestamptz
			AND created_at < GREATEST($2::timestamptz, $4::timestamptz)
	`
	hourEnd := hourStart.Add(time.Hour)
	var (
		tailRequests, tailInput, tailOutput, tailCacheC, tailCacheR, tailDuration            int64
		tailCost, tailActual, tailAccount                                                    float64
		tailTodayRequests, tailTodayInput, tailTodayOutput, tailTodayCacheC, tailTodayCacheR int64
		tailTodayCost, tailTodayActual, tailTodayAccount                                     float64
		tailActive, tailHourlyActive                                                         int64
	)
	if err := scanSingleRow(
		ctx,
		r.sql,
		tailQuery,
		[]any{hourStart, endUTC, todayUTC, todayEnd, hourStart, hourEnd},
		&tailRequests,
		&tailInput,
		&tailOutput,
		&tailCacheC,
		&tailCacheR,
		&tailCost,
		&tailActual,
		&tailAccount,
		&tailDuration,
		&tailTodayRequests,
		&tailTodayInput,
		&tailTodayOutput,
		&tailTodayCacheC,
		&tailTodayCacheR,
		&tailTodayCost,
		&tailTodayActual,
		&tailTodayAccount,
		&tailActive,
		&tailHourlyActive,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	stats.TotalRequests += tailRequests
	stats.TotalInputTokens += tailInput
	stats.TotalOutputTokens += tailOutput
	stats.TotalCacheCreationTokens += tailCacheC
	stats.TotalCacheReadTokens += tailCacheR
	stats.TotalCost += tailCost
	stats.TotalActualCost += tailActual
	stats.TotalAccountCost += tailAccount
	*totalDurationMs += tailDuration
	stats.TodayRequests += tailTodayRequests
	stats.TodayInputTokens += tailTodayInput
	stats.TodayOutputTokens += tailTodayOutput
	stats.TodayCacheCreationTokens += tailTodayCacheC
	stats.TodayCacheReadTokens += tailTodayCacheR
	stats.TodayCost += tailTodayCost
	stats.TodayActualCost += tailTodayActual
	stats.TodayAccountCost += tailTodayAccount
	if tailActive > stats.ActiveUsers {
		stats.ActiveUsers = tailActive
	}
	if tailHourlyActive > stats.HourlyActiveUsers {
		stats.HourlyActiveUsers = tailHourlyActive
	}
	return nil
}

func (r *usageLogRepository) fillDashboardUsageStatsFromUsageLogsRaw(ctx context.Context, stats *DashboardStats, startUTC, endUTC, todayUTC, todayEnd, now time.Time) error {
	combinedStatsQuery := `
		WITH scoped AS (
			SELECT
				created_at,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				total_cost,
				actual_cost,
				COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1) AS account_cost,
				COALESCE(duration_ms, 0) AS duration_ms
			FROM usage_logs
			WHERE created_at >= LEAST($1::timestamptz, $3::timestamptz)
				AND created_at < GREATEST($2::timestamptz, $4::timestamptz)
		)
		SELECT
			COUNT(*) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz) AS total_requests,
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_input_tokens,
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_output_tokens,
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_cache_read_tokens,
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_cost,
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_actual_cost,
			COALESCE(SUM(account_cost) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_account_cost,
			COALESCE(SUM(duration_ms) FILTER (WHERE created_at >= $1::timestamptz AND created_at < $2::timestamptz), 0) AS total_duration_ms,
			COUNT(*) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz) AS today_requests,
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_input_tokens,
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_output_tokens,
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_cache_read_tokens,
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_cost,
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_actual_cost,
			COALESCE(SUM(account_cost) FILTER (WHERE created_at >= $3::timestamptz AND created_at < $4::timestamptz), 0) AS today_account_cost
		FROM scoped
	`
	var totalDurationMs int64
	if err := scanSingleRow(
		ctx,
		r.sql,
		combinedStatsQuery,
		[]any{startUTC, endUTC, todayUTC, todayEnd},
		&stats.TotalRequests,
		&stats.TotalInputTokens,
		&stats.TotalOutputTokens,
		&stats.TotalCacheCreationTokens,
		&stats.TotalCacheReadTokens,
		&stats.TotalCost,
		&stats.TotalActualCost,
		&stats.TotalAccountCost,
		&totalDurationMs,
		&stats.TodayRequests,
		&stats.TodayInputTokens,
		&stats.TodayOutputTokens,
		&stats.TodayCacheCreationTokens,
		&stats.TodayCacheReadTokens,
		&stats.TodayCost,
		&stats.TodayActualCost,
		&stats.TodayAccountCost,
	); err != nil {
		return err
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	if stats.TotalRequests > 0 {
		stats.AverageDurationMs = float64(totalDurationMs) / float64(stats.TotalRequests)
	}

	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens

	hourStart := now.UTC().Truncate(time.Hour)
	hourEnd := hourStart.Add(time.Hour)
	activeUsersQuery := `
		WITH scoped AS (
			SELECT user_id, created_at
			FROM usage_logs
			WHERE created_at >= LEAST($1::timestamptz, $3::timestamptz)
				AND created_at < GREATEST($2::timestamptz, $4::timestamptz)
		)
		SELECT
			COUNT(DISTINCT CASE WHEN created_at >= $1::timestamptz AND created_at < $2::timestamptz THEN user_id END) AS active_users,
			COUNT(DISTINCT CASE WHEN created_at >= $3::timestamptz AND created_at < $4::timestamptz THEN user_id END) AS hourly_active_users
		FROM scoped
	`
	if err := scanSingleRow(ctx, r.sql, activeUsersQuery, []any{todayUTC, todayEnd, hourStart, hourEnd}, &stats.ActiveUsers, &stats.HourlyActiveUsers); err != nil {
		return err
	}

	return nil
}

// UserDashboardStats 用户仪表盘统计
type UserDashboardStats = usagestats.UserDashboardStats

// PlatformDashboardStats 单平台用量明细
type PlatformDashboardStats = usagestats.PlatformDashboardStats

// GetUserDashboardStats 获取用户专属的仪表盘统计
func (r *usageLogRepository) GetUserDashboardStats(ctx context.Context, userID int64) (*UserDashboardStats, error) {
	stats := &UserDashboardStats{}
	today := timezone.Today()

	// API Key 统计
	if err := scanSingleRow(
		ctx,
		r.sql,
		"SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND deleted_at IS NULL",
		[]any{userID},
		&stats.TotalAPIKeys,
	); err != nil {
		return nil, err
	}
	if err := scanSingleRow(
		ctx,
		r.sql,
		"SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND status = $2 AND deleted_at IS NULL",
		[]any{userID, service.StatusActive},
		&stats.ActiveAPIKeys,
	); err != nil {
		return nil, err
	}

	now := timezone.Now()
	hourStart := now.In(timezone.Location()).Truncate(time.Hour)
	windowStart := dashboardSubjectWindowStart(now, today)
	if err := r.fillUserDashboardUsage(ctx, stats, userID, windowStart, today, hourStart); err != nil {
		return nil, err
	}

	// 性能指标：RPM 和 TPM（最近1分钟，仅统计该用户的请求）
	rpm, tpm, err := r.getPerformanceStats(ctx, userID)
	if err != nil {
		return nil, err
	}
	stats.Rpm = rpm
	stats.Tpm = tpm

	// 按"有效平台"维度拆分（group.platform 优先，否则 account.platform）。
	// 与 ops 路径口径一致；HAVING 过滤掉无法确定平台的行（避免出现空字符串平台）。
	// 与上面 fillUserDashboardUsage 的总值可能略微差异，原因有二：
	//   1) 无平台归属的极少数行（group/account 都没 platform）会被 HAVING 排除；
	//   2) usageLogSuccessFilterUL 会把 actual_cost = 0 的失败 placeholder 行排除，
	//      而 totalStatsQuery/todayStatsQuery 没有这层过滤、会把这些行的 request 计数算进去。
	platformQuery := `
		SELECT
			` + usageLogEffectivePlatformExpr + ` as platform,
			COUNT(*) as total_requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.actual_cost), 0) as total_actual_cost,
			COUNT(*) FILTER (WHERE ul.created_at >= $2) as today_requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens) FILTER (WHERE ul.created_at >= $2), 0) as today_tokens,
			COALESCE(SUM(ul.actual_cost) FILTER (WHERE ul.created_at >= $2), 0) as today_actual_cost
		FROM usage_logs ul
		LEFT JOIN groups g ON g.id = ul.group_id
		LEFT JOIN accounts a ON a.id = ul.account_id
		WHERE ul.user_id = $1
		  AND ul.created_at >= $3
		  AND ` + usageLogSuccessFilterUL + `
		GROUP BY ` + usageLogEffectivePlatformExpr + `
		HAVING ` + usageLogEffectivePlatformExpr + ` IS NOT NULL AND ` + usageLogEffectivePlatformExpr + ` <> ''
		ORDER BY total_actual_cost DESC
	`
	rows, err := r.sql.QueryContext(ctx, platformQuery, userID, today, windowStart)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p PlatformDashboardStats
		if err := rows.Scan(
			&p.Platform,
			&p.TotalRequests,
			&p.TotalTokens,
			&p.TotalActualCost,
			&p.TodayRequests,
			&p.TodayTokens,
			&p.TodayActualCost,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		stats.ByPlatform = append(stats.ByPlatform, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return stats, nil
}

// getPerformanceStatsByAPIKey 获取指定 API Key 的 RPM 和 TPM（近5分钟平均值）
func (r *usageLogRepository) getPerformanceStatsByAPIKey(ctx context.Context, apiKeyID int64) (rpm, tpm int64, err error) {
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	query := `
		SELECT
			COUNT(*) as request_count,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as token_count
		FROM usage_logs
		WHERE created_at >= $1 AND api_key_id = $2`
	args := []any{fiveMinutesAgo, apiKeyID}

	var requestCount int64
	var tokenCount int64
	if err := scanSingleRow(ctx, r.sql, query, args, &requestCount, &tokenCount); err != nil {
		return 0, 0, err
	}
	return requestCount / 5, tokenCount / 5, nil
}

// GetAPIKeyDashboardStats 获取指定 API Key 的仪表盘统计（按 api_key_id 过滤）
func (r *usageLogRepository) GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*UserDashboardStats, error) {
	stats := &UserDashboardStats{}
	today := timezone.Today()

	// API Key 维度不需要统计 key 数量，设为 1
	stats.TotalAPIKeys = 1
	stats.ActiveAPIKeys = 1

	now := timezone.Now()
	windowStart := dashboardSubjectWindowStart(now, today)
	if err := r.fillSubjectDashboardUsageFromWindow(ctx, stats, "api_key_id", apiKeyID, windowStart, today); err != nil {
		return nil, err
	}

	// 性能指标：RPM 和 TPM（最近5分钟，按 API Key 过滤）
	rpm, tpm, err := r.getPerformanceStatsByAPIKey(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	stats.Rpm = rpm
	stats.Tpm = tpm

	return stats, nil
}

func dashboardSubjectWindowStart(now, today time.Time) time.Time {
	start := now.Add(-dashboardSubjectUsageLookback)
	if start.After(today) {
		return today
	}
	return start
}

func isMissingRelationError(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "42P01" {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "undefined table") || strings.Contains(msg, "no such table")
}

func (r *usageLogRepository) fillUserDashboardUsage(ctx context.Context, stats *UserDashboardStats, userID int64, windowStart, today, hourStart time.Time) error {
	usedRollup, err := r.fillUserDashboardUsageFromHourlyUsers(ctx, stats, userID, today, hourStart)
	if err != nil {
		return err
	}
	if usedRollup {
		return nil
	}
	return r.fillSubjectDashboardUsageFromWindow(ctx, stats, "user_id", userID, windowStart, today)
}

func (r *usageLogRepository) fillUserDashboardUsageFromHourlyUsers(ctx context.Context, stats *UserDashboardStats, userID int64, today, hourStart time.Time) (bool, error) {
	hourlyQuery := `
		SELECT
			COUNT(*) AS hourly_rows,
			COALESCE(SUM(total_requests), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(total_duration_ms), 0),
			COALESCE(SUM(duration_count), 0),
			COALESCE(SUM(total_requests) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(input_tokens) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE bucket_start >= $2 AND bucket_start < $3), 0)
		FROM usage_dashboard_hourly_users
		WHERE user_id = $1
			AND bucket_start < $3
	`
	var hourlyRows, totalDurationMs, durationCount int64
	if err := scanSingleRow(
		ctx,
		r.sql,
		hourlyQuery,
		[]any{userID, today, hourStart},
		&hourlyRows,
		&stats.TotalRequests,
		&stats.TotalInputTokens,
		&stats.TotalOutputTokens,
		&stats.TotalCacheCreationTokens,
		&stats.TotalCacheReadTokens,
		&stats.TotalCost,
		&stats.TotalActualCost,
		&totalDurationMs,
		&durationCount,
		&stats.TodayRequests,
		&stats.TodayInputTokens,
		&stats.TodayOutputTokens,
		&stats.TodayCacheCreationTokens,
		&stats.TodayCacheReadTokens,
		&stats.TodayCost,
		&stats.TodayActualCost,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingRelationError(err) {
			return false, nil
		}
		return false, err
	}
	if hourlyRows == 0 || stats.TotalRequests == 0 {
		// DAU-only hourly_users rows can exist with zero usage columns.
		// Treat those as a miss so we fall back to the 30-day usage_logs window.
		return false, nil
	}

	tailQuery := `
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(COALESCE(duration_ms, 0)), 0),
			COUNT(duration_ms),
			COUNT(*) FILTER (WHERE created_at >= $3),
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $3), 0),
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $3), 0),
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $3), 0),
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $3), 0),
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $3), 0),
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $3), 0)
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2
	`
	var (
		tailRequests, tailInput, tailOutput, tailCacheC, tailCacheR, tailDuration, tailDurationCount int64
		tailCost, tailActual                                                                         float64
		tailTodayRequests, tailTodayInput, tailTodayOutput, tailTodayCacheC, tailTodayCacheR         int64
		tailTodayCost, tailTodayActual                                                               float64
	)
	if err := scanSingleRow(
		ctx,
		r.sql,
		tailQuery,
		[]any{userID, hourStart, today},
		&tailRequests,
		&tailInput,
		&tailOutput,
		&tailCacheC,
		&tailCacheR,
		&tailCost,
		&tailActual,
		&tailDuration,
		&tailDurationCount,
		&tailTodayRequests,
		&tailTodayInput,
		&tailTodayOutput,
		&tailTodayCacheC,
		&tailTodayCacheR,
		&tailTodayCost,
		&tailTodayActual,
	); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	stats.TotalRequests += tailRequests
	stats.TotalInputTokens += tailInput
	stats.TotalOutputTokens += tailOutput
	stats.TotalCacheCreationTokens += tailCacheC
	stats.TotalCacheReadTokens += tailCacheR
	stats.TotalCost += tailCost
	stats.TotalActualCost += tailActual
	totalDurationMs += tailDuration
	durationCount += tailDurationCount
	stats.TodayRequests += tailTodayRequests
	stats.TodayInputTokens += tailTodayInput
	stats.TodayOutputTokens += tailTodayOutput
	stats.TodayCacheCreationTokens += tailTodayCacheC
	stats.TodayCacheReadTokens += tailTodayCacheR
	stats.TodayCost += tailTodayCost
	stats.TodayActualCost += tailTodayActual

	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens
	if durationCount > 0 {
		stats.AverageDurationMs = float64(totalDurationMs) / float64(durationCount)
	}
	return true, nil
}

func (r *usageLogRepository) fillSubjectDashboardUsageFromWindow(ctx context.Context, stats *UserDashboardStats, idColumn string, id int64, windowStart, today time.Time) error {
	if idColumn != "user_id" && idColumn != "api_key_id" {
		return errors.New("unsupported dashboard usage id column")
	}
	query := `
		SELECT
			COUNT(*) as total_requests,
			COALESCE(SUM(input_tokens), 0) as total_input_tokens,
			COALESCE(SUM(output_tokens), 0) as total_output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as total_cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as total_cache_read_tokens,
			COALESCE(SUM(total_cost), 0) as total_cost,
			COALESCE(SUM(actual_cost), 0) as total_actual_cost,
			COALESCE(AVG(duration_ms), 0) as avg_duration_ms,
			COUNT(*) FILTER (WHERE created_at >= $3) as today_requests,
			COALESCE(SUM(input_tokens) FILTER (WHERE created_at >= $3), 0) as today_input_tokens,
			COALESCE(SUM(output_tokens) FILTER (WHERE created_at >= $3), 0) as today_output_tokens,
			COALESCE(SUM(cache_creation_tokens) FILTER (WHERE created_at >= $3), 0) as today_cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens) FILTER (WHERE created_at >= $3), 0) as today_cache_read_tokens,
			COALESCE(SUM(total_cost) FILTER (WHERE created_at >= $3), 0) as today_cost,
			COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $3), 0) as today_actual_cost
		FROM usage_logs
		WHERE ` + idColumn + ` = $1 AND created_at >= $2
	`
	if err := scanSingleRow(
		ctx,
		r.sql,
		query,
		[]any{id, windowStart, today},
		&stats.TotalRequests,
		&stats.TotalInputTokens,
		&stats.TotalOutputTokens,
		&stats.TotalCacheCreationTokens,
		&stats.TotalCacheReadTokens,
		&stats.TotalCost,
		&stats.TotalActualCost,
		&stats.AverageDurationMs,
		&stats.TodayRequests,
		&stats.TodayInputTokens,
		&stats.TodayOutputTokens,
		&stats.TodayCacheCreationTokens,
		&stats.TodayCacheReadTokens,
		&stats.TodayCost,
		&stats.TodayActualCost,
	); err != nil {
		return err
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens
	return nil
}
