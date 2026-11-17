package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
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

func TestUsageStatsCacheDashboardTrendCachesFullFilters(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	ctx := context.Background()
	for _, apiKeyID := range []int64{7, 9} {
		filters := UsageLogFilters{UserID: 42, APIKeyID: apiKeyID}
		mock.ExpectQuery(`(?s)FROM usage_logs.*AND user_id = \$3 AND api_key_id = \$4`).
			WithArgs(start, end, int64(42), apiKeyID).
			WillReturnRows(sqlmock.NewRows([]string{
				"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost",
			}).AddRow("2026-11-12", apiKeyID, 0, 0, 0, 0, 0, 0, 0))
		for range 2 {
			stats, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "day", filters)
			require.NoError(t, err)
			require.Len(t, stats, 1)
			require.Equal(t, apiKeyID, stats[0].Requests)
		}
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageStatsCacheRateLimitModelTotalsStayLive(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, requests := range []int64{1, 2} {
		mock.ExpectQuery(`(?s)FROM usage_logs.*AND account_id = \$3`).
			WithArgs(start, start.Add(time.Hour), int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{
				"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost",
			}).AddRow("model", requests, 0, 0, 0, 0, 0, 0, 0, 0))
		stats, err := repo.GetModelStatsWithFilters(context.Background(), start, start.Add(time.Hour), 0, 0, 9, 0, nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		require.Equal(t, requests, stats[0].Requests)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageStatsCacheKeepsUserKeyStatusLive(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	key := usageStatsCacheKey{Query: "user_dashboard_usage", UserID: 42, Start: timezone.Today().UTC().Format(time.RFC3339Nano)}
	_, err := loadUsageStatsCached(cache, context.Background(), key, func(context.Context) (*UserDashboardStats, error) {
		return &UserDashboardStats{TotalRequests: 123}, nil
	})
	require.NoError(t, err)
	for _, active := range []int64{1, 0} {
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`).
			WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`).
			WithArgs(int64(42), service.StatusActive).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
		stats, err := repo.GetUserDashboardStats(context.Background(), 42)
		require.NoError(t, err)
		require.EqualValues(t, 123, stats.TotalRequests)
		require.Equal(t, active, stats.ActiveAPIKeys)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

type usageStatsWaitingContext struct {
	context.Context
	waiting chan<- struct{}
}

func (c usageStatsWaitingContext) Done() <-chan struct{} {
	c.waiting <- struct{}{}
	return c.Context.Done()
}

func TestUsageStatsCacheConcurrentResultsAreIndependent(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	cache.skipUntil.Store(time.Now().Add(time.Minute).UnixNano())
	waiting := make(chan struct{}, 2)
	ctx := usageStatsWaitingContext{Context: context.Background(), waiting: waiting}
	key := usageStatsCacheKey{Query: "independent"}
	started, release := make(chan struct{}), make(chan struct{})
	// Reuse a map-valued aggregate to exercise nested copies without a bespoke clone.
	load := func(ctx context.Context) (map[int64]*BatchAPIKeyUsageStats, error) {
		close(started)
		select {
		case <-release:
			return map[int64]*BatchAPIKeyUsageStats{7: {APIKeyID: 7, TotalActualCost: 3}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	results := make(chan map[int64]*BatchAPIKeyUsageStats, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			value, err := loadUsageStatsCached(cache, ctx, key, load)
			results <- value
			errs <- err
		}()
	}
	<-started
	<-waiting
	<-waiting
	close(release)
	first, second := <-results, <-results
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	first[7].TotalActualCost = 99
	require.Equal(t, 3.0, second[7].TotalActualCost)
}

func newUsageStatsTestCache(t *testing.T) (*usageStatsCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return newUsageStatsCache(client, &config.Config{
		UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute},
	}), server
}

func TestUsageStatsCacheCollapsesConcurrentRequests(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "concurrent", APIKeyID: 7}
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) (int64, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	const n = 32
	waiting := make(chan struct{}, n)
	ctx := usageStatsWaitingContext{Context: context.Background(), waiting: waiting}
	var wg sync.WaitGroup
	wg.Add(n)
	results := make(chan int64, n)
	errs := make(chan error, n)
	for range n {
		go func() {
			defer wg.Done()
			value, err := loadUsageStatsCached(cache, ctx, key, load)
			results <- value
			errs <- err
		}()
	}
	<-started
	for range n {
		<-waiting
	}
	close(release)
	wg.Wait()
	for range n {
		require.NoError(t, <-errs)
		require.EqualValues(t, 42, <-results)
	}
	require.EqualValues(t, 1, calls.Load())
}

func TestUsageStatsCacheCallerCancellationDoesNotCancelSharedLoad(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "cancel", APIKeyID: 7}
	type contextKey struct{}
	caller, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "trace"))
	defer cancel()
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	var calls atomic.Int64
	load := func(ctx context.Context) (int, error) {
		calls.Add(1)
		started <- ctx
		select {
		case <-release:
			return 42, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	first := make(chan error, 1)
	go func() {
		_, err := loadUsageStatsCached(cache, caller, key, load)
		first <- err
	}()
	loadCtx := <-started
	deadline, ok := loadCtx.Deadline()
	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(usageStatsCacheLoadTimeout), deadline, time.Second)
	require.Equal(t, "trace", loadCtx.Value(contextKey{}))
	cancel()
	select {
	case err := <-first:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not return while the shared load was still running")
	}
	require.NoError(t, loadCtx.Err())
	close(release)
	second, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 42, second)
	require.EqualValues(t, 1, calls.Load())
}

func TestUsageStatsCacheFailureBackoffExpires(t *testing.T) {
	cache, server := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "failure", APIKeyID: 7}
	failure := errors.New("database timeout")
	calls := 0
	load := func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, failure
		}
		return 42, nil
	}
	for range 2 {
		_, err := loadUsageStatsCached(cache, context.Background(), key, load)
		require.ErrorIs(t, err, failure)
	}
	require.Equal(t, 1, calls)
	require.Empty(t, server.Keys(), "errors must not masquerade as usage numbers")
	redisKey := usageStatsRedisKey(key)
	cache.failures.Set(redisKey, failure, time.Nanosecond)
	require.Eventually(t, func() bool {
		_, ok := cache.failures.Get(redisKey)
		return !ok
	}, time.Second, time.Millisecond)
	value, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 42, value)
	require.Equal(t, 2, calls)
}

func TestUsageStatsCacheBatchAPIKeysCanonicalDefaultsAndRangeIsolation(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	query := `SELECT\s+api_key_id,`
	expect := func(id int64, cost float64) {
		mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"api_key_id", "total_cost", "today_cost"}).AddRow(id, cost, cost))
	}
	ctx := context.Background()
	expect(7, 1)
	ids := []int64{9, 7, 7, 0, -1}
	first, err := repo.GetBatchAPIKeyUsageStats(ctx, ids, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, []int64{9, 7, 7, 0, -1}, ids, "normalization must not mutate caller input")
	require.Equal(t, 1.0, first[7].TotalActualCost)
	second, err := repo.GetBatchAPIKeyUsageStats(ctx, []int64{7, 9}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, first, second, "rolling defaults and ID order must not defeat the cache")
	expect(9, 2)
	other, err := repo.GetBatchAPIKeyUsageStats(ctx, []int64{9}, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 2.0, other[9].TotalActualCost)
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	expect(7, 3)
	ranged, err := repo.GetBatchAPIKeyUsageStats(ctx, []int64{7, 9}, start, start.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 3.0, ranged[7].TotalActualCost)
	expect(7, 4)
	_, err = repo.GetBatchAPIKeyUsageStats(ctx, []int64{7, 9}, start, start.Add(2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageStatsCacheAPIKeyDashboardSeparatesKeys(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	for _, id := range []int64{7, 9} {
		mock.ExpectQuery(`FROM usage_logs\s+WHERE api_key_id = \$1 AND created_at >= \$2`).
			WithArgs(id, sqlmock.AnyArg(), sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{
				"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
				"total_cost", "total_actual_cost", "avg_duration_ms",
				"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
				"today_cost", "today_actual_cost",
			}).AddRow(id, 10, 20, 0, 0, 0.5, 0.4, 8.0, 1, 4, 6, 0, 0, 0.2, 0.2))
		mock.ExpectQuery(`FROM usage_logs\s+WHERE created_at >= \$1 AND api_key_id = \$2`).
			WithArgs(sqlmock.AnyArg(), id).
			WillReturnRows(sqlmock.NewRows([]string{"request_count", "token_count"}).AddRow(10, 100))
		stats, err := repo.GetAPIKeyDashboardStats(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, id, stats.TotalRequests)
	}
	stats, err := repo.GetAPIKeyDashboardStats(context.Background(), 7)
	require.NoError(t, err)
	require.EqualValues(t, 7, stats.TotalRequests)
	require.NoError(t, mock.ExpectationsWereMet())
}
