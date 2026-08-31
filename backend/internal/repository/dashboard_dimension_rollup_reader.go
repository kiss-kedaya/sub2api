package repository

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// The reader is intentionally optional. Services type-assert these methods so
// existing repository test doubles and alternate storage implementations keep
// compiling without adopting the rollup schema.
type dashboardRollupMetric struct {
	BucketStart     time.Time
	DimensionKey    string
	UserID          int64
	GroupID         int64
	EndpointType    string
	Requests        int64
	InputTokens     int64
	OutputTokens    int64
	CacheCreation   int64
	CacheRead       int64
	Cost            float64
	ActualCost      float64
	AccountCost     float64
	DurationCount   int64
	TotalDurationMs int64
}

func (r *usageLogRepository) SetDashboardRollupReadEnabled(enabled bool) {
	if r != nil {
		r.rollupReadEnabled.Store(enabled)
	}
}

func (r *usageLogRepository) SetDashboardRollupStaleness(value time.Duration) {
	if r == nil || value <= 0 {
		return
	}
	r.rollupStalenessNanos.Store(int64(value))
}

func (r *usageLogRepository) rollupBounds(ctx context.Context, start, end time.Time) (time.Time, time.Time, bool) {
	if r == nil || r.sql == nil || !r.rollupReadEnabled.Load() || !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	now := time.Now().UTC()
	effectiveEnd := end.UTC()
	if effectiveEnd.After(now) {
		effectiveEnd = now
	}
	startLocal := start.In(timezone.Location())
	if startLocal.Minute() != 0 || startLocal.Second() != 0 || startLocal.Nanosecond() != 0 {
		return time.Time{}, time.Time{}, false
	}
	rollupEnd := effectiveEnd.In(timezone.Location()).Truncate(time.Hour).UTC()
	if !rollupEnd.After(start.UTC()) {
		return time.Time{}, time.Time{}, false
	}
	var watermark time.Time
	if err := scanSingleRow(ctx, r.sql, "SELECT last_aggregated_at FROM usage_dashboard_aggregation_watermark WHERE id = 1", nil, &watermark); err != nil {
		return time.Time{}, time.Time{}, false
	}
	watermark = watermark.UTC()
	staleness := time.Duration(r.rollupStalenessNanos.Load())
	if staleness <= 0 {
		staleness = time.Minute
	}
	if now.Sub(watermark) > staleness {
		return time.Time{}, time.Time{}, false
	}
	if !watermark.After(start.UTC()) {
		return time.Time{}, time.Time{}, false
	}
	var coverageStart time.Time
	if err := scanSingleRow(ctx, r.sql, "SELECT COALESCE(MIN(bucket_start), TIMESTAMPTZ 'epoch') FROM usage_dashboard_hourly", nil, &coverageStart); err != nil || coverageStart.Equal(time.Unix(0, 0).UTC()) || start.UTC().Before(coverageStart.UTC()) {
		return time.Time{}, time.Time{}, false
	}
	cutoff := watermark
	if cutoff.After(rollupEnd) {
		cutoff = rollupEnd
	}
	if !cutoff.After(start.UTC()) {
		return time.Time{}, time.Time{}, false
	}
	return cutoff, effectiveEnd, true
}

// rollupBoundsForDimension additionally proves that every processed hour in
// the requested range has a row for the selected dimension. This keeps partial
// backfills and failed bucket writes on the raw-query path instead of silently
// returning incomplete aggregates.
func (r *usageLogRepository) rollupBoundsForDimension(ctx context.Context, start, end time.Time, dimensionType string, userID int64) (time.Time, time.Time, bool) {
	cutoff, effectiveEnd, ok := r.rollupBounds(ctx, start, end)
	if !ok {
		return time.Time{}, time.Time{}, false
	}

	expected := `
		SELECT bucket_start
		FROM usage_dashboard_hourly
		WHERE bucket_start >= $1 AND bucket_start < $2 AND total_requests > 0
	`
	staleness := time.Duration(r.rollupStalenessNanos.Load())
	if staleness <= 0 {
		staleness = time.Minute
	}
	args := []any{start.UTC(), cutoff.UTC(), dimensionType, staleness.Seconds()}
	if userID > 0 {
		expected = `
			SELECT bucket_start
			FROM usage_dashboard_hourly_dimensions
			WHERE dimension_type = 'user' AND user_id = $5
			  AND bucket_start >= $1 AND bucket_start < $2
		`
		args = append(args, userID)
	}
	query := fmt.Sprintf(`
		SELECT (
			NOT EXISTS (
				SELECT 1
				FROM generate_series($1::timestamptz, ($2::timestamptz - interval '1 microsecond'), interval '1 hour') AS hours(bucket_start)
				WHERE NOT EXISTS (
					SELECT 1 FROM usage_dashboard_hourly_dimension_coverage c
					WHERE c.bucket_start = hours.bucket_start
					  AND c.computed_at >= NOW() - ($4::double precision * interval '1 second')
				)
			)
			AND NOT EXISTS (
				SELECT 1
				FROM (%s) expected
				WHERE NOT EXISTS (
					SELECT 1
					FROM usage_dashboard_hourly_dimensions d
					WHERE d.dimension_type = $3
					  AND d.bucket_start = expected.bucket_start
					%s
				)
			)
		)`, expected, func() string {
		if userID > 0 {
			return " AND d.user_id = $5"
		}
		return ""
	}())
	var complete bool
	if err := scanSingleRow(ctx, r.sql, query, args, &complete); err != nil || !complete {
		return time.Time{}, time.Time{}, false
	}
	return cutoff, effectiveEnd, true
}

func (r *usageLogRepository) queryRollupMetrics(ctx context.Context, start, end time.Time, dimensionType string, userID int64, endpointType string) ([]dashboardRollupMetric, error) {
	query := `
		SELECT bucket_start, dimension_key, user_id, group_id, endpoint_type,
		       total_requests, input_tokens, output_tokens, cache_creation_tokens,
		       cache_read_tokens, total_cost, actual_cost, account_cost, duration_count, total_duration_ms
		FROM usage_dashboard_hourly_dimensions
		WHERE dimension_type = $1 AND bucket_start >= $2 AND bucket_start < $3`
	args := []any{dimensionType, start, end}
	if userID > 0 {
		query += " AND user_id = $4"
		args = append(args, userID)
	}
	if endpointType != "" {
		query += fmt.Sprintf(" AND endpoint_type = $%d", len(args)+1)
		args = append(args, endpointType)
	}
	query += " ORDER BY bucket_start ASC, dimension_key ASC"
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metrics := make([]dashboardRollupMetric, 0)
	for rows.Next() {
		var m dashboardRollupMetric
		if err := rows.Scan(&m.BucketStart, &m.DimensionKey, &m.UserID, &m.GroupID, &m.EndpointType,
			&m.Requests, &m.InputTokens, &m.OutputTokens, &m.CacheCreation, &m.CacheRead,
			&m.Cost, &m.ActualCost, &m.AccountCost, &m.DurationCount, &m.TotalDurationMs); err != nil {
			return nil, err
		}
		metrics = append(metrics, m)
	}
	return metrics, rows.Err()
}

func formatRollupDate(t time.Time, granularity string) string {
	t = t.In(timezone.Location())
	if granularity == "hour" {
		return t.Format("2006-01-02 15:00")
	}
	return t.Format("2006-01-02")
}

func (r *usageLogRepository) GetUserUsageTrendWithRollups(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]UserUsageTrendPoint, bool, error) {
	if granularity != "hour" && granularity != "day" {
		return nil, false, nil
	}
	cutoff, effectiveEnd, ok := r.rollupBoundsForDimension(ctx, startTime, endTime, "user", 0)
	if !ok {
		return nil, false, nil
	}
	metrics, err := r.queryRollupMetrics(ctx, startTime, cutoff, "user", 0, "")
	if err != nil || len(metrics) == 0 {
		return nil, false, err
	}
	points, err := r.mergeUserTrendMetrics(ctx, metrics, cutoff, effectiveEnd, granularity)
	if err != nil {
		return nil, false, err
	}
	if limit <= 0 {
		limit = 12
	}
	return topUserTrendPoints(points, limit), true, nil
}

func (r *usageLogRepository) GetUserSpendingRankingWithRollups(ctx context.Context, startTime, endTime time.Time, limit int) (*UserSpendingRankingResponse, bool, error) {
	cutoff, effectiveEnd, ok := r.rollupBoundsForDimension(ctx, startTime, endTime, "user", 0)
	if !ok {
		return nil, false, nil
	}
	metrics, err := r.queryRollupMetrics(ctx, startTime, cutoff, "user", 0, "")
	if err != nil || len(metrics) == 0 {
		return nil, false, err
	}
	byUser := make(map[int64]UserSpendingRankingItem)
	for _, m := range metrics {
		item := byUser[m.UserID]
		item.UserID = m.UserID
		item.Requests += m.Requests
		item.Tokens += m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead
		item.ActualCost += m.ActualCost
		byUser[m.UserID] = item
	}
	if effectiveEnd.After(cutoff) {
		tail, err := r.getUserSpendingRankingRaw(ctx, cutoff, effectiveEnd, 1_000_000)
		if err != nil {
			return nil, false, err
		}
		for _, item := range tail.Ranking {
			merged := byUser[item.UserID]
			merged.UserID = item.UserID
			merged.Requests += item.Requests
			merged.Tokens += item.Tokens
			merged.ActualCost += item.ActualCost
			byUser[item.UserID] = merged
		}
	}
	items := make([]UserSpendingRankingItem, 0, len(byUser))
	for _, item := range byUser {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ActualCost != items[j].ActualCost {
			return items[i].ActualCost > items[j].ActualCost
		}
		if items[i].Tokens != items[j].Tokens {
			return items[i].Tokens > items[j].Tokens
		}
		return items[i].UserID < items[j].UserID
	})
	if limit <= 0 {
		limit = 12
	}
	if len(items) > limit {
		items = items[:limit]
	}
	if err := r.populateUserNames(ctx, items); err != nil {
		return nil, false, err
	}
	var totalActualCost float64
	var totalRequests, totalTokens int64
	for _, item := range byUser {
		totalActualCost += item.ActualCost
		totalRequests += item.Requests
		totalTokens += item.Tokens
	}
	return &UserSpendingRankingResponse{Ranking: items, TotalActualCost: totalActualCost, TotalRequests: totalRequests, TotalTokens: totalTokens}, true, nil
}

func (r *usageLogRepository) mergeUserTrendMetrics(ctx context.Context, metrics []dashboardRollupMetric, cutoff, end time.Time, granularity string) ([]UserUsageTrendPoint, error) {
	points := make(map[string]UserUsageTrendPoint)
	add := func(date string, m dashboardRollupMetric) {
		key := strconv.FormatInt(m.UserID, 10) + "\x00" + date
		p := points[key]
		p.Date, p.UserID = date, m.UserID
		p.Requests += m.Requests
		p.Tokens += m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead
		p.Cost += m.Cost
		p.ActualCost += m.ActualCost
		points[key] = p
	}
	for _, m := range metrics {
		add(formatRollupDate(m.BucketStart, granularity), m)
	}
	if end.After(cutoff) {
		tail, err := r.getUserUsageTrendRawAll(ctx, cutoff, end, granularity)
		if err != nil {
			return nil, err
		}
		for _, p := range tail {
			add(p.Date, dashboardRollupMetric{UserID: p.UserID, Requests: p.Requests, InputTokens: p.Tokens, Cost: p.Cost, ActualCost: p.ActualCost})
		}
	}
	result := make([]UserUsageTrendPoint, 0, len(points))
	for _, p := range points {
		result = append(result, p)
	}
	if err := r.populateUserNames(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func topUserTrendPoints(points []UserUsageTrendPoint, limit int) []UserUsageTrendPoint {
	totals := make(map[int64]int64)
	for _, p := range points {
		totals[p.UserID] += p.Tokens
	}
	ids := make([]int64, 0, len(totals))
	for id := range totals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if totals[ids[i]] != totals[ids[j]] {
			return totals[ids[i]] > totals[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	allowed := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		allowed[id] = struct{}{}
	}
	result := make([]UserUsageTrendPoint, 0)
	for _, p := range points {
		if _, ok := allowed[p.UserID]; ok {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Date != result[j].Date {
			return result[i].Date < result[j].Date
		}
		return result[i].Tokens > result[j].Tokens
	})
	return result
}

func unsupportedRollupModelSource(source string) bool {
	trimmed := strings.TrimSpace(source)
	return trimmed != "" && usagestats.NormalizeModelSource(trimmed) != usagestats.ModelSourceRequested
}

func (r *usageLogRepository) populateUserNames(ctx context.Context, values any) error {
	ids := make(map[int64]struct{})
	switch rows := values.(type) {
	case []UserUsageTrendPoint:
		for _, row := range rows {
			ids[row.UserID] = struct{}{}
		}
	case []UserSpendingRankingItem:
		for _, row := range rows {
			ids[row.UserID] = struct{}{}
		}
	default:
		return nil
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids))
	placeholders := make([]string, 0, len(ids))
	for id := range ids {
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := r.sql.QueryContext(ctx, "SELECT id, COALESCE(email,''), COALESCE(username,'') FROM users WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := make(map[int64][2]string)
	for rows.Next() {
		var id int64
		var email, username string
		if err := rows.Scan(&id, &email, &username); err != nil {
			return err
		}
		names[id] = [2]string{email, username}
	}
	switch rows := values.(type) {
	case []UserUsageTrendPoint:
		for i := range rows {
			if name, ok := names[rows[i].UserID]; ok {
				rows[i].Email, rows[i].Username = name[0], name[1]
			}
		}
	case []UserSpendingRankingItem:
		for i := range rows {
			if name, ok := names[rows[i].UserID]; ok {
				rows[i].Email, rows[i].Username = name[0], name[1]
			}
		}
	}
	return rows.Err()
}

func (r *usageLogRepository) getUserUsageTrendRawAll(ctx context.Context, startTime, endTime time.Time, granularity string) ([]UserUsageTrendPoint, error) {
	dateFormat := safeDateFormat(granularity)
	query := fmt.Sprintf(`SELECT TO_CHAR(created_at, '%s'), user_id, COUNT(*),
		COALESCE(SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens), 0),
		COALESCE(SUM(total_cost), 0), COALESCE(SUM(actual_cost), 0)
		FROM usage_logs WHERE created_at >= $1 AND created_at < $2 GROUP BY 1, 2 ORDER BY 1 ASC`, dateFormat)
	rows, err := r.sql.QueryContext(ctx, query, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]UserUsageTrendPoint, 0)
	for rows.Next() {
		var p UserUsageTrendPoint
		if err := rows.Scan(&p.Date, &p.UserID, &p.Requests, &p.Tokens, &p.Cost, &p.ActualCost); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func rollupDimensionType(source, userScoped string) string {
	switch usagestats.NormalizeModelSource(source) {
	case usagestats.ModelSourceUpstream:
		return userScoped + "_upstream"
	case usagestats.ModelSourceMapping:
		return userScoped + "_mapping"
	default:
		return userScoped
	}
}

func (r *usageLogRepository) GetUsageTrendWithRollups(ctx context.Context, startTime, endTime time.Time, granularity string, filters UsageLogFilters) ([]TrendDataPoint, bool, error) {
	if granularity != "hour" && granularity != "day" {
		return nil, false, nil
	}
	if filters.APIKeyID != 0 || filters.AccountID != 0 || filters.GroupID != 0 || filters.Model != "" || unsupportedRollupModelSource(filters.ModelFilterSource) || filters.RequestType != nil || filters.Stream != nil || filters.BillingType != nil || filters.BillingMode != "" || filters.UpstreamModelMismatch != nil {
		return nil, false, nil
	}
	var cutoff, effectiveEnd time.Time
	var ok bool
	if filters.UserID > 0 {
		cutoff, effectiveEnd, ok = r.rollupBoundsForDimension(ctx, startTime, endTime, "user", filters.UserID)
	} else {
		cutoff, effectiveEnd, ok = r.rollupBounds(ctx, startTime, endTime)
	}
	if !ok {
		return nil, false, nil
	}
	byDate := make(map[string]TrendDataPoint)
	merge := func(p TrendDataPoint) {
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
	if filters.UserID > 0 {
		metrics, err := r.queryRollupMetrics(ctx, startTime, cutoff, "user", filters.UserID, "")
		if err != nil || len(metrics) == 0 {
			return nil, false, err
		}
		for _, m := range metrics {
			merge(TrendDataPoint{Date: formatRollupDate(m.BucketStart, granularity), Requests: m.Requests, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CacheCreationTokens: m.CacheCreation, CacheReadTokens: m.CacheRead, TotalTokens: m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead, Cost: m.Cost, ActualCost: m.ActualCost})
		}
	} else {
		metrics, err := r.queryGlobalHourlyMetrics(ctx, startTime, cutoff)
		if err != nil || len(metrics) == 0 {
			return nil, false, err
		}
		for _, m := range metrics {
			merge(TrendDataPoint{Date: formatRollupDate(m.BucketStart, granularity), Requests: m.Requests, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CacheCreationTokens: m.CacheCreation, CacheReadTokens: m.CacheRead, TotalTokens: m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead, Cost: m.Cost, ActualCost: m.ActualCost})
		}
	}
	if effectiveEnd.After(cutoff) {
		tail, err := r.getUsageTrendWithFilters(ctx, cutoff, effectiveEnd, granularity, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.ModelFilterSource, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.UpstreamModelMismatch)
		if err != nil {
			return nil, false, err
		}
		for _, p := range tail {
			merge(p)
		}
	}
	result := make([]TrendDataPoint, 0, len(byDate))
	for _, p := range byDate {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Date < result[j].Date })
	return result, true, nil
}

func (r *usageLogRepository) GetModelStatsWithRollups(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters, source string) ([]ModelStat, bool, error) {
	if filters.APIKeyID != 0 || filters.AccountID != 0 || filters.GroupID != 0 || filters.Model != "" || unsupportedRollupModelSource(filters.ModelFilterSource) || filters.RequestType != nil || filters.Stream != nil || filters.BillingType != nil || filters.BillingMode != "" || filters.UpstreamModelMismatch != nil {
		return nil, false, nil
	}
	userScoped := "model"
	if filters.UserID > 0 {
		userScoped = "user_model"
	}
	dimensionType := rollupDimensionType(source, userScoped)
	cutoff, effectiveEnd, ok := r.rollupBoundsForDimension(ctx, startTime, endTime, dimensionType, filters.UserID)
	if !ok {
		return nil, false, nil
	}
	metrics, err := r.queryRollupMetrics(ctx, startTime, cutoff, dimensionType, filters.UserID, "")
	if err != nil || len(metrics) == 0 {
		return nil, false, err
	}
	byModel := make(map[string]ModelStat)
	merge := func(m ModelStat) {
		row := byModel[m.Model]
		row.Model = m.Model
		row.Requests += m.Requests
		row.InputTokens += m.InputTokens
		row.OutputTokens += m.OutputTokens
		row.CacheCreationTokens += m.CacheCreationTokens
		row.CacheReadTokens += m.CacheReadTokens
		row.TotalTokens += m.TotalTokens
		row.Cost += m.Cost
		row.ActualCost += m.ActualCost
		row.AccountCost += m.AccountCost
		byModel[m.Model] = row
	}
	for _, m := range metrics {
		merge(ModelStat{Model: m.DimensionKey, Requests: m.Requests, InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, CacheCreationTokens: m.CacheCreation, CacheReadTokens: m.CacheRead, TotalTokens: m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead, Cost: m.Cost, ActualCost: m.ActualCost, AccountCost: m.AccountCost})
	}
	if effectiveEnd.After(cutoff) {
		tail, err := r.getModelStatsWithFiltersBySource(ctx, cutoff, effectiveEnd, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, nil, nil, nil, source, filters.BillingMode, filters.UpstreamModelMismatch)
		if err != nil {
			return nil, false, err
		}
		for _, m := range tail {
			merge(m)
		}
	}
	result := make([]ModelStat, 0, len(byModel))
	for _, m := range byModel {
		result = append(result, m)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalTokens != result[j].TotalTokens {
			return result[i].TotalTokens > result[j].TotalTokens
		}
		return result[i].Model < result[j].Model
	})
	return result, true, nil
}

func (r *usageLogRepository) GetGroupStatsWithRollups(ctx context.Context, startTime, endTime time.Time, filters UsageLogFilters) ([]usagestats.GroupStat, bool, error) {
	if filters.APIKeyID != 0 || filters.AccountID != 0 || filters.GroupID != 0 || filters.Model != "" || unsupportedRollupModelSource(filters.ModelFilterSource) || filters.RequestType != nil || filters.Stream != nil || filters.BillingType != nil || filters.BillingMode != "" || filters.UpstreamModelMismatch != nil {
		return nil, false, nil
	}
	userID := filters.UserID
	dimensionType := "group"
	if userID > 0 {
		dimensionType = "user_group"
	}
	cutoff, effectiveEnd, ok := r.rollupBoundsForDimension(ctx, startTime, endTime, dimensionType, userID)
	if !ok {
		return nil, false, nil
	}
	metrics, err := r.queryRollupMetrics(ctx, startTime, cutoff, dimensionType, userID, "")
	if err != nil || len(metrics) == 0 {
		return nil, false, err
	}
	byGroup := make(map[int64]usagestats.GroupStat)
	merge := func(m usagestats.GroupStat) {
		row := byGroup[m.GroupID]
		row.GroupID = m.GroupID
		row.Requests += m.Requests
		row.TotalTokens += m.TotalTokens
		row.Cost += m.Cost
		row.ActualCost += m.ActualCost
		row.AccountCost += m.AccountCost
		byGroup[m.GroupID] = row
	}
	for _, m := range metrics {
		merge(usagestats.GroupStat{GroupID: m.GroupID, Requests: m.Requests, TotalTokens: m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead, Cost: m.Cost, ActualCost: m.ActualCost, AccountCost: m.AccountCost})
	}
	if effectiveEnd.After(cutoff) {
		tail, err := r.getGroupStatsWithFilters(ctx, cutoff, effectiveEnd, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID, filters.Model, filters.RequestType, filters.Stream, filters.BillingType, filters.BillingMode, filters.UpstreamModelMismatch)
		if err != nil {
			return nil, false, err
		}
		for _, m := range tail {
			merge(m)
		}
	}
	result := make([]usagestats.GroupStat, 0, len(byGroup))
	for _, m := range byGroup {
		result = append(result, m)
	}
	if err := r.populateGroupNames(ctx, result); err != nil {
		return nil, false, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].TotalTokens != result[j].TotalTokens {
			return result[i].TotalTokens > result[j].TotalTokens
		}
		return result[i].GroupID < result[j].GroupID
	})
	return result, true, nil
}

func (r *usageLogRepository) populateGroupNames(ctx context.Context, values []usagestats.GroupStat) error {
	if len(values) == 0 {
		return nil
	}
	args := make([]any, 0, len(values))
	placeholders := make([]string, 0, len(values))
	seen := make(map[int64]struct{})
	for _, value := range values {
		if _, ok := seen[value.GroupID]; ok {
			continue
		}
		seen[value.GroupID] = struct{}{}
		args = append(args, value.GroupID)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	rows, err := r.sql.QueryContext(ctx, "SELECT id, COALESCE(name,'') FROM groups WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := make(map[int64]string)
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	for i := range values {
		values[i].GroupName = names[values[i].GroupID]
	}
	return rows.Err()
}

func (r *usageLogRepository) GetStatsWithRollups(ctx context.Context, filters UsageLogFilters) (*UsageStats, bool, error) {
	if filters.APIKeyID != 0 || filters.AccountID != 0 || filters.GroupID != 0 || filters.Model != "" || unsupportedRollupModelSource(filters.ModelFilterSource) || filters.RequestType != nil || filters.Stream != nil || filters.BillingType != nil || filters.BillingMode != "" || filters.UpstreamModelMismatch != nil || filters.StartTime == nil || filters.EndTime == nil {
		return nil, false, nil
	}
	start, end := filters.StartTime.UTC(), filters.EndTime.UTC()
	userID := filters.UserID
	endpointType := "user_endpoint"
	if userID == 0 {
		endpointType = "endpoint"
	}
	cutoff, effectiveEnd, ok := r.rollupBoundsForDimension(ctx, start, end, endpointType, userID)
	if !ok {
		return nil, false, nil
	}
	stats := &UsageStats{}
	var totalDuration float64
	var durationCount int64
	if userID > 0 {
		metrics, err := r.queryRollupMetrics(ctx, start, cutoff, "user", userID, "")
		if err != nil || len(metrics) == 0 {
			return nil, false, err
		}
		for _, m := range metrics {
			addUsageMetric(stats, m)
			totalDuration += float64(m.TotalDurationMs)
			durationCount += m.DurationCount
		}
	} else {
		metrics, err := r.queryGlobalHourlyMetrics(ctx, start, cutoff)
		if err != nil || len(metrics) == 0 {
			return nil, false, err
		}
		for _, m := range metrics {
			addUsageMetric(stats, m)
			totalDuration += float64(m.TotalDurationMs)
			durationCount += m.DurationCount
		}
	}
	endpointMetrics, err := r.queryRollupMetrics(ctx, start, cutoff, endpointType, userID, "")
	if err != nil {
		return nil, false, err
	}
	for _, m := range endpointMetrics {
		endpoint := usagestats.EndpointStat{Endpoint: m.DimensionKey, Requests: m.Requests, TotalTokens: m.InputTokens + m.OutputTokens + m.CacheCreation + m.CacheRead, Cost: m.Cost, ActualCost: m.ActualCost}
		switch m.EndpointType {
		case "inbound":
			stats.Endpoints = append(stats.Endpoints, endpoint)
		case "upstream":
			stats.UpstreamEndpoints = append(stats.UpstreamEndpoints, endpoint)
		case "path":
			stats.EndpointPaths = append(stats.EndpointPaths, endpoint)
		}
	}
	if effectiveEnd.After(cutoff) {
		tailFilters := filters
		tailStart, tailEnd := cutoff, effectiveEnd
		tailFilters.StartTime, tailFilters.EndTime = &tailStart, &tailEnd
		tail, err := r.getStatsWithFiltersRaw(ctx, tailFilters)
		if err != nil {
			return nil, false, err
		}
		mergeUsageStats(stats, tail)
		tailDuration, tailCount, err := r.queryRawDurationSummary(ctx, tailStart, tailEnd, userID)
		if err != nil {
			return nil, false, err
		}
		totalDuration += tailDuration
		durationCount += tailCount
	}
	if durationCount > 0 {
		stats.AverageDurationMs = totalDuration / float64(durationCount)
	}
	sortEndpointStatsForRollup(stats.Endpoints)
	sortEndpointStatsForRollup(stats.UpstreamEndpoints)
	sortEndpointStatsForRollup(stats.EndpointPaths)
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheTokens
	if stats.TotalAccountCost == nil {
		stats.TotalAccountCost = ptrFloat64(0)
	}
	return stats, true, nil
}

func (r *usageLogRepository) queryRawDurationSummary(ctx context.Context, start, end time.Time, userID int64) (float64, int64, error) {
	query := "SELECT COALESCE(SUM(duration_ms), 0), COUNT(duration_ms) FROM usage_logs WHERE created_at >= $1 AND created_at < $2"
	args := []any{start, end}
	if userID > 0 {
		query += " AND user_id = $3"
		args = append(args, userID)
	}
	var total float64
	var count int64
	if err := scanSingleRow(ctx, r.sql, query, args, &total, &count); err != nil {
		return 0, 0, err
	}
	return total, count, nil
}

func addUsageMetric(stats *UsageStats, m dashboardRollupMetric) {
	stats.TotalRequests += m.Requests
	stats.TotalInputTokens += m.InputTokens
	stats.TotalOutputTokens += m.OutputTokens
	stats.TotalCacheCreationTokens += m.CacheCreation
	stats.TotalCacheReadTokens += m.CacheRead
	stats.TotalCacheTokens += m.CacheCreation + m.CacheRead
	stats.TotalCost += m.Cost
	stats.TotalActualCost += m.ActualCost
	value := m.AccountCost
	if stats.TotalAccountCost == nil {
		stats.TotalAccountCost = &value
	} else {
		*stats.TotalAccountCost += value
	}
}

func mergeUsageStats(dst, src *UsageStats) {
	if src == nil {
		return
	}
	baseDuration := int64(dst.AverageDurationMs * float64(dst.TotalRequests))
	addUsageMetric(dst, dashboardRollupMetric{Requests: src.TotalRequests, InputTokens: src.TotalInputTokens, OutputTokens: src.TotalOutputTokens, CacheCreation: src.TotalCacheCreationTokens, CacheRead: src.TotalCacheReadTokens, Cost: src.TotalCost, ActualCost: src.TotalActualCost, AccountCost: derefFloat64(src.TotalAccountCost)})
	dst.Endpoints = append(dst.Endpoints, src.Endpoints...)
	dst.UpstreamEndpoints = append(dst.UpstreamEndpoints, src.UpstreamEndpoints...)
	dst.EndpointPaths = append(dst.EndpointPaths, src.EndpointPaths...)
	dst.Endpoints = consolidateEndpointStats(dst.Endpoints)
	dst.UpstreamEndpoints = consolidateEndpointStats(dst.UpstreamEndpoints)
	dst.EndpointPaths = consolidateEndpointStats(dst.EndpointPaths)
	if src.TotalRequests > 0 {
		dst.AverageDurationMs = float64(baseDuration+int64(src.AverageDurationMs*float64(src.TotalRequests))) / float64(dst.TotalRequests)
	}
}

func derefFloat64(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
func ptrFloat64(value float64) *float64 { return &value }
func sortEndpointStatsForRollup(values []usagestats.EndpointStat) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Requests != values[j].Requests {
			return values[i].Requests > values[j].Requests
		}
		return values[i].Endpoint < values[j].Endpoint
	})
}

func consolidateEndpointStats(values []usagestats.EndpointStat) []usagestats.EndpointStat {
	if len(values) < 2 {
		return values
	}
	byEndpoint := make(map[string]usagestats.EndpointStat, len(values))
	for _, value := range values {
		row := byEndpoint[value.Endpoint]
		row.Endpoint = value.Endpoint
		row.Requests += value.Requests
		row.TotalTokens += value.TotalTokens
		row.Cost += value.Cost
		row.ActualCost += value.ActualCost
		byEndpoint[value.Endpoint] = row
	}
	result := make([]usagestats.EndpointStat, 0, len(byEndpoint))
	for _, value := range byEndpoint {
		result = append(result, value)
	}
	return result
}

func (r *usageLogRepository) queryGlobalHourlyMetrics(ctx context.Context, start, end time.Time) ([]dashboardRollupMetric, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT bucket_start, total_requests, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, account_cost, duration_count, total_duration_ms FROM usage_dashboard_hourly WHERE bucket_start >= $1 AND bucket_start < $2 ORDER BY bucket_start`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]dashboardRollupMetric, 0)
	for rows.Next() {
		var m dashboardRollupMetric
		if err := rows.Scan(&m.BucketStart, &m.Requests, &m.InputTokens, &m.OutputTokens, &m.CacheCreation, &m.CacheRead, &m.Cost, &m.ActualCost, &m.AccountCost, &m.DurationCount, &m.TotalDurationMs); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
