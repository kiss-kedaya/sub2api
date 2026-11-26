package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// TrendDataPoint represents a single point in trend data
type TrendDataPoint = usagestats.TrendDataPoint

// ModelStat represents usage statistics for a single model
type ModelStat = usagestats.ModelStat

// UserUsageTrendPoint represents user usage trend data point
type UserUsageTrendPoint = usagestats.UserUsageTrendPoint

// UserSpendingRankingItem represents a user spending ranking row.
type UserSpendingRankingItem = usagestats.UserSpendingRankingItem
type UserSpendingRankingResponse = usagestats.UserSpendingRankingResponse

func (r *usageLogRepository) GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]UserUsageTrendPoint, error) {
	if r.rollupReadEnabled.Load() {
		if trend, covered, err := r.GetUserUsageTrendWithRollups(ctx, startTime, endTime, granularity, limit); err == nil && covered {
			return trend, nil
		}
	}
	return r.getUserUsageTrendRaw(ctx, startTime, endTime, granularity, limit)
}

func (r *usageLogRepository) GetUserSpendingRanking(ctx context.Context, startTime, endTime time.Time, limit int) (*UserSpendingRankingResponse, error) {
	if r.rollupReadEnabled.Load() {
		if ranking, covered, err := r.GetUserSpendingRankingWithRollups(ctx, startTime, endTime, limit); err == nil && covered {
			return ranking, nil
		}
	}
	return r.getUserSpendingRankingRaw(ctx, startTime, endTime, limit)
}

// APIKeyUsageTrendPoint represents API key usage trend data point
type APIKeyUsageTrendPoint = usagestats.APIKeyUsageTrendPoint

// GetAPIKeyUsageTrend returns usage trend data grouped by API key and date
func (r *usageLogRepository) GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []APIKeyUsageTrendPoint, err error) {
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_keys AS (
			SELECT api_key_id
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY api_key_id
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(u.created_at, '%s') as date,
			u.api_key_id,
			COALESCE(k.name, '') as key_name,
			COUNT(*) as requests,
			COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens
		FROM usage_logs u
		LEFT JOIN api_keys k ON u.api_key_id = k.id
		WHERE u.api_key_id IN (SELECT api_key_id FROM top_keys)
		  AND u.created_at >= $4 AND u.created_at < $5
		GROUP BY date, u.api_key_id, k.name
		ORDER BY date ASC, tokens DESC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]APIKeyUsageTrendPoint, 0)
	for rows.Next() {
		var row APIKeyUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.APIKeyID, &row.KeyName, &row.Requests, &row.Tokens); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserUsageTrend returns usage trend data grouped by user and date
func (r *usageLogRepository) getUserUsageTrendRaw(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) (results []UserUsageTrendPoint, err error) {
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		WITH top_users AS (
			SELECT user_id
			FROM usage_logs
			WHERE created_at >= $1 AND created_at < $2
			GROUP BY user_id
			ORDER BY SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) DESC
			LIMIT $3
		)
		SELECT
			TO_CHAR(u.created_at, '%s') as date,
			u.user_id,
			COALESCE(us.email, '') as email,
			COALESCE(us.username, '') as username,
			COUNT(*) as requests,
			COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens,
			COALESCE(SUM(u.total_cost), 0) as cost,
			COALESCE(SUM(u.actual_cost), 0) as actual_cost
		FROM usage_logs u
		LEFT JOIN users us ON u.user_id = us.id
		WHERE u.user_id IN (SELECT user_id FROM top_users)
		  AND u.created_at >= $4 AND u.created_at < $5
		GROUP BY date, u.user_id, us.email, us.username
		ORDER BY date ASC, tokens DESC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]UserUsageTrendPoint, 0)
	for rows.Next() {
		var row UserUsageTrendPoint
		if err = rows.Scan(&row.Date, &row.UserID, &row.Email, &row.Username, &row.Requests, &row.Tokens, &row.Cost, &row.ActualCost); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// GetUserSpendingRanking returns user spending ranking aggregated within the time range.
func (r *usageLogRepository) getUserSpendingRankingRaw(ctx context.Context, startTime, endTime time.Time, limit int) (result *UserSpendingRankingResponse, err error) {
	if limit <= 0 {
		limit = 12
	}

	query := `
		WITH user_spend AS (
			SELECT
				u.user_id,
				COALESCE(us.email, '') as email,
				COALESCE(us.username, '') as username,
				COALESCE(SUM(u.actual_cost), 0) as actual_cost,
				COUNT(*) as requests,
				COALESCE(SUM(u.input_tokens + u.output_tokens + u.cache_creation_tokens + u.cache_read_tokens), 0) as tokens
			FROM usage_logs u
			LEFT JOIN users us ON u.user_id = us.id
			WHERE u.created_at >= $1 AND u.created_at < $2
			GROUP BY u.user_id, us.email, us.username
		),
		ranked AS (
			SELECT
				user_id,
				email,
				username,
				actual_cost,
				requests,
				tokens,
				COALESCE(SUM(actual_cost) OVER (), 0) as total_actual_cost,
				COALESCE(SUM(requests) OVER (), 0) as total_requests,
				COALESCE(SUM(tokens) OVER (), 0) as total_tokens
			FROM user_spend
			ORDER BY actual_cost DESC, tokens DESC, user_id ASC
			LIMIT $3
		)
		SELECT
			user_id,
			email,
			username,
			actual_cost,
			requests,
			tokens,
			total_actual_cost,
			total_requests,
			total_tokens
		FROM ranked
		ORDER BY actual_cost DESC, tokens DESC, user_id ASC
	`

	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			result = nil
		}
	}()

	ranking := make([]UserSpendingRankingItem, 0)
	totalActualCost := 0.0
	totalRequests := int64(0)
	totalTokens := int64(0)
	for rows.Next() {
		var row UserSpendingRankingItem
		if err = rows.Scan(&row.UserID, &row.Email, &row.Username, &row.ActualCost, &row.Requests, &row.Tokens, &totalActualCost, &totalRequests, &totalTokens); err != nil {
			return nil, err
		}
		ranking = append(ranking, row)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return &UserSpendingRankingResponse{
		Ranking:         ranking,
		TotalActualCost: totalActualCost,
		TotalRequests:   totalRequests,
		TotalTokens:     totalTokens,
	}, nil
}

// GetUserUsageTrendByUserID 获取指定用户的使用趋势
func (r *usageLogRepository) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	if r.rollupReadEnabled.Load() {
		if trend, covered, rollupErr := r.GetUsageTrendWithRollups(ctx, startTime, endTime, granularity, UsageLogFilters{UserID: userID}); rollupErr == nil && covered {
			return trend, nil
		}
	}
	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		SELECT
			TO_CHAR(created_at, '%s') as date,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			COALESCE(SUM(actual_cost), 0) as actual_cost
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		GROUP BY date
		ORDER BY date ASC
	`, dateFormat)

	rows, err := r.sql.QueryContext(ctx, query, userID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetUserModelStats 获取指定用户的模型统计
func (r *usageLogRepository) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) (results []ModelStat, err error) {
	return r.GetModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, 0, 0, 0, nil, nil, nil, usagestats.ModelSourceRequested)
}

// GetUsageTrendWithFilters returns usage trend data with optional filters.
//
// One API key's by-day trend over several days is answered in two parts when the window still holds today
// (keyDailyTrendSplit): the days that are over, which no longer change and are kept for a whole day, and today,
// which is read again every cache TTL. The closed days are scanned once a day instead of every quarter hour, and
// what is scanned again and again is today's rows only, a fraction of the window's, so no single statement is the
// scan of a whole month any more and today's bar is at most one cache TTL old instead of a quarter hour.
func (r *usageLogRepository) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) (results []TrendDataPoint, err error) {
	trend := func(start, end time.Time, ttl time.Duration) ([]TrendDataPoint, error) {
		return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
			Query: "usage_trend_with_filters", UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID,
			// The day labels are cut in the database session's time zone, so it is part of what was asked.
			Model: model, Timezone: resolveUsageStatsTimezone(),
			Start: start.UTC().Format(time.RFC3339Nano), End: end.UTC().Format(time.RFC3339Nano),
			Extra:   usageGranularityKey(granularity),
			Filters: &usageFiltersFingerprint{RequestType: requestType, Stream: stream, BillingType: billingType},
			TTL:     ttl,
		}, func(ctx context.Context) ([]TrendDataPoint, error) {
			return r.getUsageTrendWithFiltersResolved(ctx, start, end, granularity, userID, apiKeyID, accountID, groupID, model, requestType, stream, billingType)
		})
	}
	splitAt, split := r.keyDailyTrendSplit(apiKeyID, startTime, endTime, granularity)
	if !split {
		return trend(startTime, endTime, keyAggregateTTL(apiKeyID, startTime, endTime))
	}
	// Either part failing fails the call: half a trend would be taken for the whole of it.
	closed, err := trend(startTime, splitAt, usageStatsKeyClosedDaysTTL)
	if err != nil {
		return nil, err
	}
	// ttl 0 is the cache's own TTL, scan budget and memory of a timeout: today holds a fraction of the window's rows.
	today, err := trend(splitAt, endTime, 0)
	if err != nil {
		return nil, err
	}
	return mergeTrendPointsByDate(closed, today), nil
}

// keyDailyTrendSplit says where to cut one API key's by-day window [start, end) into the days that are over and
// the day still being written: the start of today in the window's own time zone (end's location), and whether to
// cut at all. It is the local midnight, not end-24h: the day of a DST change has 23 or 25 hours.
//
// Only the class that keyAggregateTTL gives the long TTL is cut (one key, window over 25 hours), and only by day;
// a window with no closed day (today starts at or before start) or without today (today starts at or after end)
// is answered whole, as before, and so is every window when there is no cache: two reads would only cost more.
//
// Where the cut falls changes nothing in the answer: the parts cover [start, end) exactly once, and the day
// labels are cut by the database session's zone, which need not be the window's, so one label can be in both
// parts and mergeTrendPointsByDate adds them. The cut only decides what can be kept.
func (r *usageLogRepository) keyDailyTrendSplit(apiKeyID int64, start, end time.Time, granularity string) (time.Time, bool) {
	if r.statsCache == nil || usageGranularityKey(granularity) != "day" || keyAggregateTTL(apiKeyID, start, end) == 0 {
		return time.Time{}, false
	}
	now := time.Now()
	if r.clock != nil {
		now = r.clock()
	}
	loc := end.Location()
	today := now.In(loc)
	splitAt := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	if !splitAt.After(start) || !splitAt.Before(end) {
		return time.Time{}, false
	}
	return splitAt, true
}

// mergeTrendPointsByDate adds the parts of one trend up by day label, in label order like the query's ORDER BY.
// Every figure of a TrendDataPoint is a sum, so a label found in both parts is the sum of the two rows, and a
// label found in one is that row unchanged. The result is never nil, as the query's is not.
func mergeTrendPointsByDate(parts ...[]TrendDataPoint) []TrendDataPoint {
	byDate := make(map[string]TrendDataPoint)
	for _, part := range parts {
		for _, p := range part {
			row := byDate[p.Date]
			row.Date = p.Date
			row.Requests += p.Requests
			row.InputTokens += p.InputTokens
			row.OutputTokens += p.OutputTokens
			row.CacheCreationTokens += p.CacheCreationTokens
			row.CacheReadTokens += p.CacheReadTokens
			row.TotalTokens += p.TotalTokens
			row.Cost += p.Cost
			row.ActualCost += p.ActualCost
			byDate[p.Date] = row
		}
	}
	merged := make([]TrendDataPoint, 0, len(byDate))
	for _, p := range byDate {
		merged = append(merged, p)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Date < merged[j].Date })
	return merged
}

func (r *usageLogRepository) getUsageTrendWithFiltersResolved(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) (results []TrendDataPoint, err error) {
	if r.rollupReadEnabled.Load() {
		filters := UsageLogFilters{UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, Model: model, RequestType: requestType, Stream: stream, BillingType: billingType}
		if trend, covered, rollupErr := r.GetUsageTrendWithRollups(ctx, startTime, endTime, granularity, filters); rollupErr == nil && covered {
			return trend, nil
		}
	}
	return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, userID, apiKeyID, accountID, groupID, model, "", requestType, stream, billingType, "", nil, nil)
}

func (r *usageLogRepository) GetUsageTrendWithUsageFilters(ctx context.Context, startTime, endTime time.Time, granularity string, filters UsageLogFilters) (results []TrendDataPoint, err error) {
	return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
		Query: "usage_trend_usage_filters", Timezone: resolveUsageStatsTimezone(),
		Start: startTime.UTC().Format(time.RFC3339Nano), End: endTime.UTC().Format(time.RFC3339Nano),
		Extra: usageGranularityKey(granularity), Filters: usageFiltersFingerprintOf(filters),
	}, func(ctx context.Context) ([]TrendDataPoint, error) {
		if r.rollupReadEnabled.Load() {
			if trend, covered, rollupErr := r.GetUsageTrendWithRollups(ctx, startTime, endTime, granularity, filters); rollupErr == nil && covered {
				return trend, nil
			}
		}
		return r.getUsageTrendWithFilters(ctx, startTime, endTime, granularity, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.ModelFilterSource, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.UpstreamModelMismatch, filters.NativeCompactionV2)
	})
}

func (r *usageLogRepository) getUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, accountID, groupID int64, model string, modelSource string, requestType *int16, stream *bool, billingType *int8, billingMode string, upstreamModelMismatch *bool, nativeCompactionV2 *bool) (results []TrendDataPoint, err error) {
	// When dimension rollup reads are enabled, a coverage miss must fall back to
	// the raw query. The legacy hourly aggregate has no watermark/tail contract
	// and could otherwise return stale data for unsupported filters.
	if !r.rollupReadEnabled.Load() && wholeUsageTrendBuckets(startTime, endTime, granularity) && shouldUsePreaggregatedTrend(granularity, userID, apiKeyID, accountID, groupID, model, requestType, stream, billingType, billingMode, upstreamModelMismatch, nativeCompactionV2) {
		aggregated, aggregatedErr := r.getUsageTrendFromAggregates(ctx, startTime, endTime, granularity)
		if aggregatedErr == nil && len(aggregated) > 0 {
			return aggregated, nil
		}
	}

	dateFormat := safeDateFormat(granularity)

	query := fmt.Sprintf(`
		SELECT
			TO_CHAR(created_at, '%s') as date,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			COALESCE(SUM(actual_cost), 0) as actual_cost
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
	`, dateFormat)

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND user_id = $%d", len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	query, args = appendUsageLogModelQueryFilter(query, args, model, modelSource)
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "")
	if billingType != nil {
		query += fmt.Sprintf(" AND billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "")
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition("upstream_model_mismatch", *upstreamModelMismatch)
	}
	query += " GROUP BY date ORDER BY date ASC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func shouldUsePreaggregatedTrend(granularity string, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, upstreamModelMismatch *bool, nativeCompactionV2 *bool) bool {
	if granularity != "day" && granularity != "hour" {
		return false
	}
	return userID == 0 &&
		apiKeyID == 0 &&
		accountID == 0 &&
		groupID == 0 &&
		model == "" &&
		requestType == nil &&
		stream == nil &&
		billingType == nil &&
		billingMode == "" &&
		upstreamModelMismatch == nil &&
		nativeCompactionV2 == nil
}

// Legacy aggregates cannot represent partial hours/days. Keep precise ranges on
// the existing time-bounded raw query rather than silently including whole buckets.
func wholeUsageTrendBuckets(start, end time.Time, granularity string) bool {
	for _, value := range []time.Time{start.In(timezone.Location()), end.In(timezone.Location())} {
		if value.Minute() != 0 || value.Second() != 0 || value.Nanosecond() != 0 {
			return false
		}
		if granularity != "hour" && value.Hour() != 0 {
			return false
		}
	}
	return end.After(start)
}

func (r *usageLogRepository) getUsageTrendFromAggregates(ctx context.Context, startTime, endTime time.Time, granularity string) (results []TrendDataPoint, err error) {
	dateFormat := safeDateFormat(granularity)
	query := ""
	args := []any{startTime, endTime}

	switch granularity {
	case "hour":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_start, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_hourly
			WHERE bucket_start >= $1 AND bucket_start < $2
			ORDER BY bucket_start ASC
		`, dateFormat)
	case "day":
		query = fmt.Sprintf(`
			SELECT
				TO_CHAR(bucket_date::timestamp, '%s') as date,
				total_requests as requests,
				input_tokens,
				output_tokens,
				cache_creation_tokens,
				cache_read_tokens,
				(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) as total_tokens,
				total_cost as cost,
				actual_cost
			FROM usage_dashboard_daily
			WHERE bucket_date >= $1::date AND bucket_date < $2::date
			ORDER BY bucket_date ASC
		`, dateFormat)
	default:
		return nil, nil
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanTrendRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetModelStatsWithFilters returns model statistics with optional filters
func (r *usageLogRepository) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []ModelStat, err error) {
	// Account-only model totals also drive rate-limit admission: keep them live.
	if accountID > 0 && userID == 0 && apiKeyID == 0 {
		return r.getModelStatsWithFiltersBySourceResolved(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, requestType, stream, billingType, usagestats.ModelSourceRequested)
	}
	return r.GetModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, requestType, stream, billingType, usagestats.ModelSourceRequested)
}

// GetModelStatsWithFiltersBySource returns model statistics with optional filters and model source dimension.
// source: requested | upstream | mapping.
func (r *usageLogRepository) GetModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8, source string) (results []ModelStat, err error) {
	return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
		Query: "model_stats_with_filters", UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID,
		ModelSource: usagestats.NormalizeModelSource(source),
		Start:       startTime.UTC().Format(time.RFC3339Nano), End: endTime.UTC().Format(time.RFC3339Nano),
		Filters: &usageFiltersFingerprint{RequestType: requestType, Stream: stream, BillingType: billingType},
		TTL:     keyAggregateTTL(apiKeyID, startTime, endTime),
	}, func(ctx context.Context) ([]ModelStat, error) {
		return r.getModelStatsWithFiltersBySourceResolved(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, requestType, stream, billingType, source)
	})
}

func (r *usageLogRepository) getModelStatsWithFiltersBySourceResolved(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8, source string) (results []ModelStat, err error) {
	if r.rollupReadEnabled.Load() {
		filters := UsageLogFilters{UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, RequestType: requestType, Stream: stream, BillingType: billingType}
		if stats, covered, rollupErr := r.GetModelStatsWithRollups(ctx, startTime, endTime, filters, source); rollupErr == nil && covered {
			return stats, nil
		}
	}
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, "", requestType, stream, billingType, source, "", nil, nil)
}

func (r *usageLogRepository) GetModelStatsWithUsageFiltersBySource(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) (results []ModelStat, err error) {
	return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
		Query: "model_stats_usage_filters", ModelSource: usagestats.NormalizeModelSource(source),
		Start: startTime.UTC().Format(time.RFC3339Nano), End: endTime.UTC().Format(time.RFC3339Nano),
		Filters: usageFiltersFingerprintOf(filters),
	}, func(ctx context.Context) ([]ModelStat, error) {
		return r.getModelStatsWithUsageFiltersBySourceResolved(ctx, startTime, endTime, filters, source)
	})
}

func (r *usageLogRepository) getModelStatsWithUsageFiltersBySourceResolved(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) (results []ModelStat, err error) {
	if r.rollupReadEnabled.Load() {
		if stats, covered, rollupErr := r.GetModelStatsWithRollups(ctx, startTime, endTime, filters, source); rollupErr == nil && covered {
			return stats, nil
		}
	}
	return r.getModelStatsWithFiltersBySource(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, source, filters.BillingMode, filters.UpstreamModelMismatch, filters.NativeCompactionV2)
}

func (r *usageLogRepository) getModelStatsWithFiltersBySource(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, source string, billingMode string, upstreamModelMismatch *bool, nativeCompactionV2 *bool) (results []ModelStat, err error) {
	actualCostExpr := "COALESCE(SUM(actual_cost), 0) as actual_cost"
	// 当仅按 account_id 聚合时，实际费用使用账号倍率（total_cost * account_rate_multiplier）。
	if accountID > 0 && userID == 0 && apiKeyID == 0 {
		actualCostExpr = "COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as actual_cost"
	}
	accountCostExpr := "COALESCE(SUM(COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1)), 0) as account_cost"
	modelExpr := resolveModelDimensionExpression(source)

	query := fmt.Sprintf(`
		SELECT
			%s as model,
			COUNT(*) as requests,
			COALESCE(SUM(input_tokens), 0) as input_tokens,
			COALESCE(SUM(output_tokens), 0) as output_tokens,
			COALESCE(SUM(cache_creation_tokens), 0) as cache_creation_tokens,
			COALESCE(SUM(cache_read_tokens), 0) as cache_read_tokens,
			COALESCE(SUM(input_tokens::bigint + output_tokens + cache_creation_tokens + cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(total_cost), 0) as cost,
			%s,
			%s
		FROM usage_logs
		WHERE created_at >= $1 AND created_at < $2
	`, modelExpr, actualCostExpr, accountCostExpr)

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND user_id = $%d", len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if strings.TrimSpace(model) != "" {
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "")
	if billingType != nil {
		query += fmt.Sprintf(" AND billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "")
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition("upstream_model_mismatch", *upstreamModelMismatch)
	}
	query += fmt.Sprintf(" GROUP BY %s ORDER BY total_tokens DESC", modelExpr)

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// 保持主错误优先；仅在无错误时回传 Close 失败。
		// 同时清空返回值，避免误用不完整结果。
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results, err = scanModelStatsRows(rows)
	if err != nil {
		return nil, err
	}
	return results, nil
}

// GetGroupStatsWithFilters returns group usage statistics with optional filters
func (r *usageLogRepository) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, requestType *int16, stream *bool, billingType *int8) (results []usagestats.GroupStat, err error) {
	if r.rollupReadEnabled.Load() {
		filters := UsageLogFilters{UserID: userID, APIKeyID: apiKeyID, AccountID: accountID, GroupID: groupID, RequestType: requestType, Stream: stream, BillingType: billingType}
		if stats, covered, rollupErr := r.GetGroupStatsWithRollups(ctx, startTime, endTime, filters); rollupErr == nil && covered {
			return stats, nil
		}
	}
	return r.getGroupStatsWithFilters(ctx, startTime, endTime, userID, apiKeyID, accountID, groupID, "", requestType, stream, billingType, "", nil, nil, false)
}

func (r *usageLogRepository) GetGroupStatsWithUsageFilters(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters) (results []usagestats.GroupStat, err error) {
	// Cached like the other usage-page aggregates (60 s, one shared load per key): switching a filter back and
	// forth, or two tabs, must not run the same aggregate again.
	return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
		Query: "group_stats_usage_filters",
		Start: startTime.UTC().Format(time.RFC3339Nano), End: endTime.UTC().Format(time.RFC3339Nano),
		Filters: usageFiltersFingerprintOf(filters),
	}, func(ctx context.Context) ([]usagestats.GroupStat, error) {
		if r.rollupReadEnabled.Load() {
			if stats, covered, rollupErr := r.GetGroupStatsWithRollups(ctx, startTime, endTime, filters); rollupErr == nil && covered {
				return stats, nil
			}
		}
		return r.getGroupStatsWithFilters(ctx, startTime, endTime, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.UpstreamModelMismatch, filters.NativeCompactionV2, filters.OmitAccountCost)
	})
}

// getGroupStatsWithFilters aggregates usage per group. omitAccountCost leaves the account-cost expression out
// (it is selected as a constant 0): that is the only thing besides the group, token and cost columns this query
// needs from the row, so for user/group/time scopes PostgreSQL can answer it from idx_usage_logs_user_created_dims
// without a heap fetch per row. Measured on production data: 4.3 s with the expression, 0.14 s without.
func (r *usageLogRepository) getGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, accountID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8, billingMode string, upstreamModelMismatch *bool, nativeCompactionV2 *bool, omitAccountCost bool) (results []usagestats.GroupStat, err error) {
	accountCostExpr := "COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0) as account_cost"
	if omitAccountCost {
		accountCostExpr = "0::double precision as account_cost"
	}
	query := `
		SELECT
			COALESCE(ul.group_id, 0) as group_id,
			COALESCE(g.name, '') as group_name,
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.total_cost), 0) as cost,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			` + accountCostExpr + `
		FROM usage_logs ul
		LEFT JOIN groups g ON g.id = ul.group_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`

	args := []any{startTime, endTime}
	if userID > 0 {
		query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
		args = append(args, userID)
	}
	if apiKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, apiKeyID)
	}
	if accountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, accountID)
	}
	if groupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, groupID)
	}
	if strings.TrimSpace(model) != "" {
		modelExpr := resolveModelDimensionExpressionWithAlias(usagestats.ModelSourceRequested, "ul")
		query += fmt.Sprintf(" AND %s = $%d", modelExpr, len(args)+1)
		args = append(args, model)
	}
	query, args = appendRequestTypeOrStreamQueryFilter(query, args, requestType, stream)
	query, args = appendNativeCompactionV2QueryFilter(query, args, nativeCompactionV2, "ul")
	if billingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, int16(*billingType))
	}
	query, args = appendUsageLogBillingModeQueryFilter(query, args, billingMode, "ul")
	if upstreamModelMismatch != nil {
		query += " AND " + upstreamModelMismatchCondition("ul.upstream_model_mismatch", *upstreamModelMismatch)
	}
	query += " GROUP BY ul.group_id, g.name ORDER BY total_tokens DESC"

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.GroupStat, 0)
	for rows.Next() {
		var row usagestats.GroupStat
		if err := rows.Scan(
			&row.GroupID,
			&row.GroupName,
			&row.Requests,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetUserBreakdownStats returns per-user usage breakdown within a specific dimension.
func (r *usageLogRepository) GetUserBreakdownStats(ctx context.Context, startTime, endTime time.Time, dim usagestats.UserBreakdownDimension, limit int) (results []usagestats.UserBreakdownItem, err error) {
	query := `
		SELECT
			COALESCE(ul.user_id, 0) as user_id,
			COALESCE(u.email, '') as email,
			COUNT(*) as requests,
			COALESCE(SUM(ul.input_tokens), 0) as input_tokens,
			COALESCE(SUM(ul.output_tokens), 0) as output_tokens,
			COALESCE(SUM(ul.cache_creation_tokens + ul.cache_read_tokens), 0) as cache_tokens,
			COALESCE(SUM(ul.input_tokens + ul.output_tokens + ul.cache_creation_tokens + ul.cache_read_tokens), 0) as total_tokens,
			COALESCE(SUM(ul.total_cost), 0) as cost,
			COALESCE(SUM(ul.actual_cost), 0) as actual_cost,
			COALESCE(SUM(COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1)), 0) as account_cost
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		WHERE ul.created_at >= $1 AND ul.created_at < $2
	`
	args := []any{startTime, endTime}

	if dim.GroupID > 0 {
		query += fmt.Sprintf(" AND ul.group_id = $%d", len(args)+1)
		args = append(args, dim.GroupID)
	}
	if dim.Model != "" {
		query += fmt.Sprintf(" AND %s = $%d", resolveModelDimensionExpression(dim.ModelType), len(args)+1)
		args = append(args, dim.Model)
	}
	if dim.Endpoint != "" {
		col := resolveEndpointColumn(dim.EndpointType)
		query += fmt.Sprintf(" AND %s = $%d", col, len(args)+1)
		args = append(args, dim.Endpoint)
	}
	if dim.UserID > 0 {
		query += fmt.Sprintf(" AND ul.user_id = $%d", len(args)+1)
		args = append(args, dim.UserID)
	}
	if dim.APIKeyID > 0 {
		query += fmt.Sprintf(" AND ul.api_key_id = $%d", len(args)+1)
		args = append(args, dim.APIKeyID)
	}
	if dim.AccountID > 0 {
		query += fmt.Sprintf(" AND ul.account_id = $%d", len(args)+1)
		args = append(args, dim.AccountID)
	}
	if dim.RequestType != nil {
		condition, conditionArgs := buildRequestTypeFilterConditionWithAlias(len(args)+1, *dim.RequestType, "ul")
		query += " AND " + condition
		args = append(args, conditionArgs...)
	}
	if dim.Stream != nil {
		query += fmt.Sprintf(" AND ul.stream = $%d", len(args)+1)
		args = append(args, *dim.Stream)
	}
	query, args = appendNativeCompactionV2QueryFilter(query, args, dim.NativeCompactionV2, "ul")
	if dim.BillingType != nil {
		query += fmt.Sprintf(" AND ul.billing_type = $%d", len(args)+1)
		args = append(args, *dim.BillingType)
	}

	// ORDER BY 列来自固定 allowlist(非用户原样字符串),避免 SQL 注入。
	orderBy := "actual_cost"
	switch dim.SortBy {
	case "total_tokens", "input_tokens", "output_tokens", "cache_tokens", "requests", "cost", "actual_cost":
		orderBy = dim.SortBy
	}
	query += " GROUP BY ul.user_id, u.email ORDER BY " + orderBy + " DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			results = nil
		}
	}()

	results = make([]usagestats.UserBreakdownItem, 0)
	for rows.Next() {
		var row usagestats.UserBreakdownItem
		if err := rows.Scan(
			&row.UserID,
			&row.Email,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// GetAllGroupUsageSummary 返回所有分组在服务端配置时区内的今日、昨日与当前保留记录累计金额。
func (r *usageLogRepository) GetAllGroupUsageSummary(ctx context.Context, todayStart time.Time) ([]usagestats.GroupUsageSummary, error) {
	return r.getAllGroupUsageSummaryFromRollups(ctx, todayStart)
}

// resolveModelDimensionExpression maps model source type to a safe SQL expression.
func resolveModelDimensionExpression(modelType string) string {
	return resolveModelDimensionExpressionWithAlias(modelType, "")
}

func resolveModelDimensionExpressionWithAlias(modelType, alias string) string {
	column := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	requestedExpr := fmt.Sprintf("COALESCE(NULLIF(TRIM(%s), ''), %s)", column("requested_model"), column("model"))
	upstreamExpr := fmt.Sprintf("COALESCE(NULLIF(TRIM(%s), ''), %s)", column("upstream_model"), column("model"))
	switch usagestats.NormalizeModelSource(modelType) {
	case usagestats.ModelSourceUpstream:
		return upstreamExpr
	case usagestats.ModelSourceMapping:
		return fmt.Sprintf("(%s || ' -> ' || %s)", requestedExpr, upstreamExpr)
	default:
		return requestedExpr
	}
}

func scanTrendRows(rows *sql.Rows) ([]TrendDataPoint, error) {
	results := make([]TrendDataPoint, 0)
	for rows.Next() {
		var row TrendDataPoint
		if err := rows.Scan(
			&row.Date,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func scanModelStatsRows(rows *sql.Rows) ([]ModelStat, error) {
	results := make([]ModelStat, 0)
	for rows.Next() {
		var row ModelStat
		if err := rows.Scan(
			&row.Model,
			&row.Requests,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CacheCreationTokens,
			&row.CacheReadTokens,
			&row.TotalTokens,
			&row.Cost,
			&row.ActualCost,
			&row.AccountCost,
		); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
