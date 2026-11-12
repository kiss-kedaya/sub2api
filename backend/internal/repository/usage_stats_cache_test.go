//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newUsageStatsCacheRepo(t *testing.T, rdb *redis.Client, enabled bool) (*usageLogRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newSQLMock(t)
	cfg := &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: enabled, TTL: time.Minute}}
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = newUsageStatsCache(rdb, cfg)
	return repo, mock
}

func usageStatsAggregateRow(mock sqlmock.Sqlmock, requests, inputTokens int64) {
	mock.ExpectQuery(`COUNT\(\*\) as total_requests`).WillReturnRows(
		sqlmock.NewRows([]string{
			"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_tokens",
			"total_cache_creation_tokens", "total_cache_read_tokens", "total_cost",
			"total_actual_cost", "avg_duration_ms",
		}).AddRow(requests, inputTokens, 0, 0, 0, 0, 0.0, 0.0, 0.0))
}

func TestUsageStatsCacheServesRepeatedQueryFromRedis(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	usageStatsAggregateRow(mock, 7, 70)
	first, err := repo.GetUserStatsAggregated(ctx, 42, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 7, first.TotalRequests)

	// No second expectation: a database call here would fail the test.
	second, err := repo.GetUserStatsAggregated(ctx, 42, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 7, second.TotalRequests)
	require.EqualValues(t, 70, second.TotalInputTokens)
	require.NoError(t, mock.ExpectationsWereMet())

	keys := server.Keys()
	require.Len(t, keys, 1)
	require.Contains(t, keys[0], usageStatsCachePrefix)
}

func TestUsageStatsCacheExpiresAfterTTL(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	usageStatsAggregateRow(mock, 1, 10)
	_, err := repo.GetAPIKeyStatsAggregated(ctx, 9, start, end)
	require.NoError(t, err)

	server.FastForward(61 * time.Second)
	usageStatsAggregateRow(mock, 2, 20)
	stats, err := repo.GetAPIKeyStatsAggregated(ctx, 9, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.TotalRequests, "an expired entry must be recomputed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageStatsCacheFallsBackWhenRedisIsUnavailable(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	server.Close()

	usageStatsAggregateRow(mock, 3, 30)
	stats, err := repo.GetAccountStatsAggregated(ctx, 5, start, end)
	require.NoError(t, err, "a dead cache must never fail the request")
	require.EqualValues(t, 3, stats.TotalRequests)

	// The failure pauses cache IO, so the next request goes straight to the
	// database instead of retrying a dead connection.
	usageStatsAggregateRow(mock, 4, 40)
	stats, err = repo.GetAccountStatsAggregated(ctx, 5, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 4, stats.TotalRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageStatsCacheIsolatesDifferentQueries(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, true)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	// Same query text, different user / range / time zone must not share a key.
	usageStatsAggregateRow(mock, 1, 10)
	_, err := repo.GetUserStatsAggregated(ctx, 1, start, end)
	require.NoError(t, err)
	usageStatsAggregateRow(mock, 2, 20)
	_, err = repo.GetUserStatsAggregated(ctx, 2, start, end)
	require.NoError(t, err)
	usageStatsAggregateRow(mock, 3, 30)
	_, err = repo.GetUserStatsAggregated(ctx, 1, start.Add(time.Hour), end)
	require.NoError(t, err)

	// The repeated (user, range) pair must be served from Redis: no fourth
	// expectation is registered, so a database call would fail this test.
	stats, err := repo.GetUserStatsAggregated(ctx, 1, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.TotalRequests, "the first user's original entry must still be intact")
	require.NoError(t, mock.ExpectationsWereMet())
	require.Len(t, server.Keys(), 3, "each distinct parameter set gets its own key")
}

func TestUsageStatsCacheDisabledQueriesDatabaseEveryTime(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repo, mock := newUsageStatsCacheRepo(t, client, false)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	usageStatsAggregateRow(mock, 1, 10)
	_, err := repo.GetModelStatsAggregated(ctx, "claude", start, end)
	require.NoError(t, err)
	usageStatsAggregateRow(mock, 2, 20)
	stats, err := repo.GetModelStatsAggregated(ctx, "claude", start, end)
	require.NoError(t, err)
	require.EqualValues(t, 2, stats.TotalRequests)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Empty(t, server.Keys(), "a disabled cache must not write to Redis")
}

func TestUsageStatsCacheKeyIncludesTimezoneAndFilters(t *testing.T) {
	base := usageStatsCacheKey{Query: "daily_stats_aggregated", UserID: 1, Timezone: "UTC"}
	other := base
	other.Timezone = "Asia/Shanghai"
	require.NotEqual(t, usageStatsRedisKey(base), usageStatsRedisKey(other),
		"the grouping time zone changes the result, so it must change the key")

	trend := usageStatsCacheKey{Query: "usage_trend_with_filters", Start: "a"}
	stream := true
	withStream := trend
	withStream.Filters = &usageFiltersFingerprint{Stream: &stream}
	require.NotEqual(t, usageStatsRedisKey(trend), usageStatsRedisKey(withStream),
		"optional filters must be part of the key")
	require.Contains(t, usageStatsRedisKey(trend), usageStatsCachePrefix)
}
