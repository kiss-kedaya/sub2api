package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The user usage page aggregates (stats, groups, list total) were measured on production data: a filtered view
// took 4-21 s because every row had to be fetched from the table, while the same answer from the covering index
// takes ~0.14 s. These tests pin the SQL shape that makes that possible, so a later "tidy-up" cannot quietly put
// the table lookups back, and the cache identities that keep user-shaped and admin-shaped answers apart.

// capturedSQL records every statement a sqlmock connection receives.
type capturedSQL struct {
	mu      sync.Mutex
	queries []string
}

func (c *capturedSQL) last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queries) == 0 {
		return ""
	}
	return c.queries[len(c.queries)-1]
}

// newCapturingSQLMock matches every expectation by order only and keeps the executed SQL for the test to inspect.
func newCapturingSQLMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock, *capturedSQL) {
	t.Helper()
	captured := &capturedSQL{}
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(_, actual string) error {
		captured.mu.Lock()
		captured.queries = append(captured.queries, actual)
		captured.mu.Unlock()
		return nil
	})))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock, captured
}

func usagePageWindow() (time.Time, time.Time) {
	start := time.Date(2026, 11, 23, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	return start, start.Add(48 * time.Hour)
}

func TestGroupStatsLeaveAccountCostOutForUsers(t *testing.T) {
	start, end := usagePageWindow()
	cols := []string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}

	t.Run("user-facing: no account columns, so the covering index can answer", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows(cols).AddRow(int64(97), "G", int64(3), int64(30), 1.5, 1.2, 0.0))

		got, err := repo.getGroupStatsWithFilters(context.Background(), start, end, 1, 0, 0, 97, "", nil, nil, nil, "", nil, nil, true)

		require.NoError(t, err)
		query := captured.last()
		require.NotContains(t, query, "account_stats_cost")
		require.NotContains(t, query, "account_rate_multiplier")
		require.Contains(t, query, "0::double precision as account_cost")
		require.Len(t, got, 1)
		require.Equal(t, int64(97), got[0].GroupID)
		require.InDelta(t, 1.2, got[0].ActualCost, 1e-9)
		require.InDelta(t, 0, got[0].AccountCost, 1e-9)
	})

	t.Run("admin: the account cost is still computed", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows(cols).AddRow(int64(97), "G", int64(3), int64(30), 1.5, 1.2, 0.9))

		got, err := repo.getGroupStatsWithFilters(context.Background(), start, end, 0, 0, 0, 97, "", nil, nil, nil, "", nil, nil, false)

		require.NoError(t, err)
		require.Contains(t, captured.last(), "account_stats_cost")
		require.Contains(t, captured.last(), "account_rate_multiplier")
		require.InDelta(t, 0.9, got[0].AccountCost, 1e-9)
	})
}

// The hourly rollups answer the closed hours; the open hour is read from usage_logs. That raw tail is the
// query a user's group chart still runs on every load, so it must keep the covering-index shape too.
func TestGroupStatsRollupTailFollowsOmitAccountCost(t *testing.T) {
	now := time.Now().UTC()
	start := now.Truncate(time.Hour).Add(-2 * time.Hour)
	end := now.Add(24 * time.Hour) // the window runs into the future, so the open hour is a raw tail
	metricCols := []string{
		"bucket_start", "dimension_key", "user_id", "group_id", "endpoint_type",
		"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"total_cost", "actual_cost", "account_cost", "duration_count", "total_duration_ms",
	}
	tailCols := []string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}

	for _, omit := range []bool{true, false} {
		db, mock, captured := newCapturingSQLMock(t)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(now))
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(start))
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows(metricCols).
			AddRow(start, "7:3", int64(7), int64(3), "", int64(4), int64(10), int64(20), int64(2), int64(1), 0.5, 0.4, 0.0, int64(4), int64(400)))
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows(tailCols).AddRow(int64(3), "G", int64(2), int64(8), 0.2, 0.1, 0.0))
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(3), "G"))
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.SetDashboardRollupReadEnabled(true)

		groups, covered, err := repo.GetGroupStatsWithRollups(context.Background(), start, end, usagestats.UsageLogFilters{UserID: 7, OmitAccountCost: omit})

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, groups, 1)
		require.EqualValues(t, 6, groups[0].Requests, "closed hours from the rollup plus the open hour from the table")
		tail := ""
		for _, query := range captured.queries {
			if strings.Contains(query, "FROM usage_logs ul") {
				tail = query
			}
		}
		require.NotEmpty(t, tail, "the open hour is read from usage_logs")
		if omit {
			require.NotContains(t, tail, "account_stats_cost")
			require.Contains(t, tail, "0::double precision as account_cost")
		} else {
			require.Contains(t, tail, "account_stats_cost")
		}
	}
}

func TestUsageStatsScopes(t *testing.T) {
	start, end := usagePageWindow()
	base := usagestats.UsageLogFilters{UserID: 1, StartTime: &start, EndTime: &end, OmitAccountCost: true}

	t.Run("summary: one aggregate row, no endpoint text and no account columns", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"c", "i", "o", "cc", "cr", "cost", "actual", "account", "avg"}).
			AddRow(int64(10), int64(100), int64(50), int64(20), int64(30), 2.5, 2.0, 0.0, 120.5))
		filters := base
		filters.StatsScope = usagestats.StatsScopeSummary

		stats, err := repo.getStatsWithFiltersRaw(context.Background(), filters)

		require.NoError(t, err)
		query := captured.last()
		require.NotContains(t, query, "GROUPING SETS")
		require.NotContains(t, query, "inbound_endpoint")
		require.NotContains(t, query, "upstream_endpoint")
		require.NotContains(t, query, "account_stats_cost")
		require.Contains(t, query, "user_id = $1")
		require.EqualValues(t, 10, stats.TotalRequests)
		require.EqualValues(t, 100, stats.TotalInputTokens)
		require.EqualValues(t, 50, stats.TotalOutputTokens)
		require.EqualValues(t, 50, stats.TotalCacheTokens)
		require.EqualValues(t, 20, stats.TotalCacheCreationTokens)
		require.EqualValues(t, 30, stats.TotalCacheReadTokens)
		require.EqualValues(t, 200, stats.TotalTokens, "input + output + cache")
		require.InDelta(t, 2.5, stats.TotalCost, 1e-9)
		require.InDelta(t, 2.0, stats.TotalActualCost, 1e-9)
		require.InDelta(t, 120.5, stats.AverageDurationMs, 1e-9)
		require.Empty(t, stats.Endpoints)
	})

	t.Run("summary for an admin keeps the account cost expression", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"c", "i", "o", "cc", "cr", "cost", "actual", "account", "avg"}).
			AddRow(int64(1), int64(1), int64(1), int64(0), int64(0), 1.0, 1.0, 0.7, 5.0))
		filters := base
		filters.OmitAccountCost = false
		filters.StatsScope = usagestats.StatsScopeSummary

		stats, err := repo.getStatsWithFiltersRaw(context.Background(), filters)

		require.NoError(t, err)
		require.Contains(t, captured.last(), "account_stats_cost")
		require.InDelta(t, 0.7, *stats.TotalAccountCost, 1e-9)
	})

	t.Run("endpoints: grouped by inbound endpoint only, busiest first, no totals", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnRows(sqlmock.NewRows([]string{"endpoint", "requests", "tokens", "cost", "actual"}).
			AddRow("/v1/chat/completions", int64(3), int64(30), 0.3, 0.2).
			AddRow("/v1/messages", int64(9), int64(90), 0.9, 0.8).
			AddRow("/v1/embeddings", int64(3), int64(5), 0.1, 0.1))
		filters := base
		filters.StatsScope = usagestats.StatsScopeEndpoints

		stats, err := repo.getStatsWithFiltersRaw(context.Background(), filters)

		require.NoError(t, err)
		query := captured.last()
		require.Contains(t, query, "inbound_endpoint")
		require.NotContains(t, query, "upstream_endpoint")
		require.NotContains(t, query, "GROUPING SETS")
		require.NotContains(t, query, "duration_ms")
		require.Contains(t, query, "GROUP BY 1")
		require.Len(t, stats.Endpoints, 3)
		require.Equal(t, "/v1/messages", stats.Endpoints[0].Endpoint, "most requests first")
		require.Equal(t, "/v1/chat/completions", stats.Endpoints[1].Endpoint, "ties broken by name")
		require.Equal(t, "/v1/embeddings", stats.Endpoints[2].Endpoint)
		require.EqualValues(t, 0, stats.TotalRequests)
		require.Empty(t, stats.UpstreamEndpoints)
		require.Empty(t, stats.EndpointPaths)
	})

	t.Run("no scope: the original single-pass grouping sets query is untouched", func(t *testing.T) {
		db, mock, captured := newCapturingSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		mock.ExpectQuery("x").WillReturnError(errors.New("stop here"))

		_, err := repo.getStatsWithFiltersRaw(context.Background(), usagestats.UsageLogFilters{UserID: 1, StartTime: &start, EndTime: &end})

		require.Error(t, err)
		require.Contains(t, captured.last(), "GROUPING SETS")
		require.Contains(t, captured.last(), "account_stats_cost")
	})
}

func TestStatsScopeIsPartOfTheCacheIdentity(t *testing.T) {
	base := usagestats.UsageLogFilters{UserID: 1}
	key := func(f usagestats.UsageLogFilters) string {
		return usageStatsRedisKey(usageStatsCacheKey{Query: "stats_with_filters", Filters: usageFiltersFingerprintOf(f)})
	}
	omit, summary, endpoints := base, base, base
	omit.OmitAccountCost = true
	summary.StatsScope = usagestats.StatsScopeSummary
	endpoints.StatsScope = usagestats.StatsScopeEndpoints

	keys := map[string]string{"base": key(base), "omit": key(omit), "summary": key(summary), "endpoints": key(endpoints)}
	seen := map[string]string{}
	for name, k := range keys {
		require.NotEmpty(t, k)
		other, dup := seen[k]
		require.False(t, dup, "%s and %s must not share a cache entry", name, other)
		seen[k] = name
	}

	// Existing (admin) entries keep their keys: the new fields are omitted when unset.
	raw, err := json.Marshal(usageFiltersFingerprintOf(base))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "omit_account_cost")
	require.NotContains(t, string(raw), "stats_scope")
}

func TestGroupStatsAreCachedPerFilterSet(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start, end := usagePageWindow()
	cols := []string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}
	expectGroups := func(requests int64) {
		mock.ExpectQuery(`FROM usage_logs ul`).WillReturnRows(sqlmock.NewRows(cols).AddRow(int64(3), "G", requests, int64(30), 1.0, 0.9, 0.0))
	}
	userFilters := usagestats.UsageLogFilters{UserID: 1, StartTime: &start, EndTime: &end, OmitAccountCost: true}

	expectGroups(5)
	first, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, userFilters)
	require.NoError(t, err)
	require.EqualValues(t, 5, first[0].Requests)

	// No second expectation: asking again for the same filters must not reach the database.
	second, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, userFilters)
	require.NoError(t, err)
	require.EqualValues(t, 5, second[0].Requests)

	// A different filter set (or the admin shape of the same one) is a different answer.
	expectGroups(7)
	adminShape := userFilters
	adminShape.OmitAccountCost = false
	other, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, adminShape)
	require.NoError(t, err)
	require.EqualValues(t, 7, other[0].Requests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserListTotalIsCountedOncePerFilterSet(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start, _ := usagePageWindow()
	where := "WHERE user_id = $1 AND created_at >= $2"
	args := []any{int64(1), start}
	pageOf := func(n int) pagination.PaginationParams { return pagination.PaginationParams{Page: n, PageSize: 20} }
	expectCount := func(total int64) {
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM usage_logs`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(total))
	}
	expectPage := func() {
		mock.ExpectQuery(`FROM usage_logs .*LIMIT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}

	expectCount(45)
	expectPage()
	_, page1, err := repo.listUsageLogsWithPagination(ctx, where, args, pageOf(1), true)
	require.NoError(t, err)
	require.EqualValues(t, 45, page1.Total)

	// Turning the page: the total is reused, only the page itself is read.
	expectPage()
	_, page2, err := repo.listUsageLogsWithPagination(ctx, where, args, pageOf(2), true)
	require.NoError(t, err)
	require.EqualValues(t, 45, page2.Total)
	require.Equal(t, 3, page2.Pages)

	// Other filters have their own total.
	expectCount(8)
	expectPage()
	_, filtered, err := repo.listUsageLogsWithPagination(ctx, where+" AND group_id = $3", append(append([]any{}, args...), int64(97)), pageOf(1), true)
	require.NoError(t, err)
	require.EqualValues(t, 8, filtered.Total)

	// Callers that ask for a live count (admin, ExactTotal, the legacy ListBy* helpers) still get one every time.
	expectCount(46)
	expectPage()
	_, live, err := repo.listUsageLogsWithPagination(ctx, where, args, pageOf(1), false)
	require.NoError(t, err)
	require.EqualValues(t, 46, live.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Only a user's own list reuses its total. Admin-style scopes (an API key or account without a user) and callers
// that ask for the exact total keep counting live, so the admin screens never show a minute-old number.
func TestListWithFiltersReusesTheTotalOnlyForAUsersOwnList(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start, end := usagePageWindow()
	pageOf := func(n int) pagination.PaginationParams { return pagination.PaginationParams{Page: n, PageSize: 20} }
	expectCount := func(total int64) {
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM usage_logs`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(total))
	}
	expectPage := func() {
		mock.ExpectQuery(`FROM usage_logs .*LIMIT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	}
	totalOf := func(filters usagestats.UsageLogFilters, page int) int64 {
		_, result, err := repo.ListWithFilters(ctx, pageOf(page), filters)
		require.NoError(t, err)
		return result.Total
	}

	own := usagestats.UsageLogFilters{UserID: 1, StartTime: &start, EndTime: &end}
	expectCount(45)
	expectPage()
	require.EqualValues(t, 45, totalOf(own, 1))
	expectPage() // page 2 of the same filters: no second COUNT
	require.EqualValues(t, 45, totalOf(own, 2))

	exact := own
	exact.ExactTotal = true
	expectCount(46)
	expectPage()
	require.EqualValues(t, 46, totalOf(exact, 1), "ExactTotal always counts")

	byKey := usagestats.UsageLogFilters{APIKeyID: 9, StartTime: &start, EndTime: &end}
	expectCount(3)
	expectPage()
	require.EqualValues(t, 3, totalOf(byKey, 1))
	expectCount(4)
	expectPage()
	require.EqualValues(t, 4, totalOf(byKey, 2), "no user: every page counts live")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageListTotalCacheExtraIsStable(t *testing.T) {
	instant := time.Date(2026, 11, 23, 0, 0, 0, 0, time.FixedZone("CST", 8*3600))
	inUTC := instant.UTC()
	where := "WHERE user_id = $1 AND created_at >= $2"

	require.Equal(t,
		usageListTotalCacheExtra(where, []any{int64(1), instant}),
		usageListTotalCacheExtra(where, []any{int64(1), inUTC}),
		"the same instant in another zone is the same window")
	require.NotEqual(t,
		usageListTotalCacheExtra(where, []any{int64(1), instant}),
		usageListTotalCacheExtra(where, []any{int64(2), instant}))
	require.NotEqual(t,
		usageListTotalCacheExtra(where, []any{int64(1), instant}),
		usageListTotalCacheExtra(where+" AND group_id = $3", []any{int64(1), instant, int64(97)}))
	// Two argument lists whose text runs together must not collide.
	require.NotEqual(t,
		usageListTotalCacheExtra("W", []any{"ab", "c"}),
		usageListTotalCacheExtra("W", []any{"a", "bc"}))
	require.False(t, strings.Contains(usageListTotalCacheExtra(where, nil), "m=+"), "no monotonic clock text")
}

func TestStatsFromRollupsHonoursTheScope(t *testing.T) {
	now := time.Now().UTC()
	start := now.Truncate(time.Hour).Add(-time.Hour)
	end := now.Truncate(time.Hour)
	metricCols := []string{
		"bucket_start", "dimension_key", "user_id", "group_id", "endpoint_type",
		"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"total_cost", "actual_cost", "account_cost", "duration_count", "total_duration_ms",
	}
	expectCoverage := func(mock sqlmock.Sqlmock) {
		mock.ExpectQuery("SELECT last_aggregated_at FROM usage_dashboard_aggregation_watermark").
			WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(now))
		mock.ExpectQuery(`SELECT COALESCE\(MIN\(bucket_start\)`).
			WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(start))
		mock.ExpectQuery(`SELECT \(`).WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
	}
	filters := func(scope string) usagestats.UsageLogFilters {
		return usagestats.UsageLogFilters{UserID: 7, StartTime: &start, EndTime: &end, OmitAccountCost: true, StatsScope: scope}
	}

	t.Run("summary: reads the user's totals and never asks for endpoint rows", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		expectCoverage(mock)
		mock.ExpectQuery("SELECT bucket_start, dimension_key, user_id, group_id, endpoint_type").
			WillReturnRows(sqlmock.NewRows(metricCols).AddRow(start, "7", int64(7), int64(0), "", int64(4), int64(10), int64(20), int64(2), int64(1), 0.5, 0.4, 0.0, int64(4), int64(400)))
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.SetDashboardRollupReadEnabled(true)

		stats, covered, err := repo.GetStatsWithRollups(context.Background(), filters(usagestats.StatsScopeSummary))

		require.NoError(t, err)
		require.True(t, covered)
		require.EqualValues(t, 4, stats.TotalRequests)
		require.InDelta(t, 100.0, stats.AverageDurationMs, 1e-9)
		require.Empty(t, stats.Endpoints)
		require.NoError(t, mock.ExpectationsWereMet(), "exactly one rollup read: the totals")
	})

	t.Run("endpoints: reads the user's endpoint rows and never asks for the totals", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		expectCoverage(mock)
		mock.ExpectQuery("SELECT bucket_start, dimension_key, user_id, group_id, endpoint_type").
			WillReturnRows(sqlmock.NewRows(metricCols).
				AddRow(start, "/v1/messages", int64(7), int64(0), "inbound", int64(9), int64(10), int64(20), int64(2), int64(1), 0.5, 0.4, 0.0, int64(9), int64(900)).
				AddRow(start, "/v1/embeddings", int64(7), int64(0), "inbound", int64(2), int64(1), int64(1), int64(0), int64(0), 0.1, 0.1, 0.0, int64(2), int64(20)))
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.SetDashboardRollupReadEnabled(true)

		stats, covered, err := repo.GetStatsWithRollups(context.Background(), filters(usagestats.StatsScopeEndpoints))

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, stats.Endpoints, 2)
		require.Equal(t, "/v1/messages", stats.Endpoints[0].Endpoint)
		require.EqualValues(t, 0, stats.TotalRequests)
		require.NoError(t, mock.ExpectationsWereMet(), "exactly one rollup read: the endpoints")
	})

	t.Run("endpoints with no rollup rows is 'not covered', so the raw path answers", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		expectCoverage(mock)
		mock.ExpectQuery("SELECT bucket_start, dimension_key, user_id, group_id, endpoint_type").
			WillReturnRows(sqlmock.NewRows(metricCols))
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.SetDashboardRollupReadEnabled(true)

		_, covered, err := repo.GetStatsWithRollups(context.Background(), filters(usagestats.StatsScopeEndpoints))

		require.NoError(t, err)
		require.False(t, covered)
	})
}
