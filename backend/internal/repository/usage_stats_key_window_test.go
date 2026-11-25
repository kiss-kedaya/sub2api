package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// The three aggregates one API key is asked for over a window of days: the by-day trend and the by-model report
// of /v1/usage, and the key summary of the per-key usage panels.
const (
	keyWindowTrendSQL = `(?s)FROM usage_logs.*GROUP BY date`
	keyWindowModelSQL = `(?s)FROM usage_logs.*GROUP BY COALESCE`
	// keyWindowAnySQL matches any of the three, for tests that only care that the database was asked.
	keyWindowAnySQL = `(?s)FROM usage_logs`
)

var (
	keyWindowTrendCols   = []string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}
	keyWindowModelCols   = []string{"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}
	keyWindowSummaryCols = []string{"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_tokens", "total_cache_creation_tokens", "total_cache_read_tokens", "total_cost", "total_actual_cost", "avg_duration_ms"}
)

type keyWindowAggregate struct {
	name string
	// query is what the database is expected to be asked; rows is the answer, carrying requests as its count.
	query string
	rows  func(requests int64) *sqlmock.Rows
	// run asks the aggregate of apiKeyID (owned by userID) over [start, end) and returns the request count.
	run func(ctx context.Context, repo *usageLogRepository, userID, apiKeyID int64, start, end time.Time) (int64, error)
}

func (a keyWindowAggregate) expect(mock sqlmock.Sqlmock, requests int64) {
	mock.ExpectQuery(a.query).WillReturnRows(a.rows(requests))
}

func (a keyWindowAggregate) ask(repo *usageLogRepository, userID, apiKeyID int64, start, end time.Time) (int64, error) {
	return a.run(context.Background(), repo, userID, apiKeyID, start, end)
}

func keyWindowAggregates() []keyWindowAggregate {
	return []keyWindowAggregate{
		{
			name:  "daily trend",
			query: keyWindowTrendSQL,
			rows: func(requests int64) *sqlmock.Rows {
				return sqlmock.NewRows(keyWindowTrendCols).AddRow("2026-11-12", requests, 0, 0, 0, 0, 0, 0, 0)
			},
			run: func(ctx context.Context, repo *usageLogRepository, userID, apiKeyID int64, start, end time.Time) (int64, error) {
				points, err := repo.GetUsageTrendWithFilters(ctx, start, end, "day", userID, apiKeyID, 0, 0, "", nil, nil, nil)
				if err != nil || len(points) != 1 {
					return 0, err
				}
				return points[0].Requests, nil
			},
		},
		{
			name:  "model report",
			query: keyWindowModelSQL,
			rows: func(requests int64) *sqlmock.Rows {
				return sqlmock.NewRows(keyWindowModelCols).AddRow("claude", requests, 0, 0, 0, 0, 0, 0, 0, 0)
			},
			run: func(ctx context.Context, repo *usageLogRepository, _, apiKeyID int64, start, end time.Time) (int64, error) {
				stats, err := repo.GetModelStatsWithFilters(ctx, start, end, 0, apiKeyID, 0, 0, nil, nil, nil)
				if err != nil || len(stats) != 1 {
					return 0, err
				}
				return stats[0].Requests, nil
			},
		},
		{
			name:  "key summary",
			query: `COUNT\(\*\) as total_requests`,
			rows: func(requests int64) *sqlmock.Rows {
				return sqlmock.NewRows(keyWindowSummaryCols).AddRow(requests, 0, 0, 0, 0, 0, 0.0, 0.0, 0.0)
			},
			run: func(ctx context.Context, repo *usageLogRepository, _, apiKeyID int64, start, end time.Time) (int64, error) {
				stats, err := repo.GetAPIKeyStatsAggregated(ctx, apiKeyID, start, end)
				if err != nil {
					return 0, err
				}
				return stats.TotalRequests, nil
			},
		},
	}
}

func newKeyWindowRepo(t *testing.T, ttl time.Duration) (*usageLogRepository, sqlmock.Sqlmock, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: ttl}})
	return repo, mock, server
}

func TestKeyAggregateTTL(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		apiKeyID int64
		window   time.Duration
		want     time.Duration
	}{
		{"key, 30 days", 7, 30 * 24 * time.Hour, usageStatsKeyWindowTTL},
		{"key, 7 days", 7, 7 * 24 * time.Hour, usageStatsKeyWindowTTL},
		{"key, 2 days", 7, 48 * time.Hour, usageStatsKeyWindowTTL},
		{"key, just over the longest natural day", 7, 25*time.Hour + time.Nanosecond, usageStatsKeyWindowTTL},
		{"key, a 25 hour DST day is still today", 7, 25 * time.Hour, 0},
		{"key, one natural day", 7, 24 * time.Hour, 0},
		{"key, a 23 hour DST day", 7, 23 * time.Hour, 0},
		{"key, an hour", 7, time.Hour, 0},
		{"key, empty window", 7, 0, 0},
		{"key, inverted window", 7, -30 * 24 * time.Hour, 0},
		{"no key, 30 days", 0, 30 * 24 * time.Hour, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, keyAggregateTTL(tc.apiKeyID, start, start.Add(tc.window)))
		})
	}
}

// The TTL says how long to reuse an answer, not what was asked: it must never reach the hash.
func TestUsageStatsKeyWindowTTLIsNotPartOfTheIdentity(t *testing.T) {
	key := usageStatsCacheKey{Query: "api_key_stats_aggregated", APIKeyID: 7, Start: "a", End: "b"}
	longer := key
	longer.TTL = usageStatsKeyWindowTTL
	require.Equal(t, usageStatsRedisKey(key), usageStatsRedisKey(longer))
	raw, err := json.Marshal(longer)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "TTL")
	require.NotContains(t, string(raw), "ttl")
}

// Every entry point that answers one key over several days keeps the answer five minutes; everything else stays
// on the cache's own TTL (one minute here).
func TestUsageStatsKeyWindowAggregatesAreKeptFiveMinutes(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		apiKeyID int64
		window   time.Duration
		want     time.Duration
	}{
		{"key, 30 days", 7, 30 * 24 * time.Hour, usageStatsKeyWindowTTL},
		{"key, 2 days", 7, 48 * time.Hour, usageStatsKeyWindowTTL},
		{"key, today", 7, 24 * time.Hour, time.Minute},
		{"key, an hour", 7, time.Hour, time.Minute},
		{"no key, 30 days", 0, 30 * 24 * time.Hour, time.Minute},
	}
	for _, agg := range keyWindowAggregates() {
		for _, tc := range cases {
			t.Run(agg.name+"/"+tc.name, func(t *testing.T) {
				repo, mock, server := newKeyWindowRepo(t, time.Minute)
				agg.expect(mock, 5)
				got, err := agg.ask(repo, 42, tc.apiKeyID, start, start.Add(tc.window))
				require.NoError(t, err)
				require.EqualValues(t, 5, got)
				require.NoError(t, mock.ExpectationsWereMet())
				keys := server.Keys()
				require.Len(t, keys, 1)
				require.Equal(t, tc.want, server.TTL(keys[0]))
			})
		}
	}
}

// Over time: a poll every minute finds the multi-day answer until five minutes are up, and only then scans again.
func TestUsageStatsKeyWindowAnswerExpiresAfterFiveMinutes(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	for _, agg := range keyWindowAggregates() {
		t.Run(agg.name, func(t *testing.T) {
			repo, mock, server := newKeyWindowRepo(t, time.Minute)
			agg.expect(mock, 1)
			got, err := agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 1, got)

			// No further expectation is registered: a query during the next four minutes would fail the test.
			for range 4 {
				server.FastForward(time.Minute)
				got, err = agg.ask(repo, 42, 7, start, end)
				require.NoError(t, err)
				require.EqualValues(t, 1, got, "still inside the five minutes")
			}
			server.FastForward(59 * time.Second)
			got, err = agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 1, got, "4 min 59 s old")

			server.FastForward(2 * time.Second)
			agg.expect(mock, 2)
			got, err = agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 2, got, "an expired answer is recomputed")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// A single natural day keeps the old minute, whatever the key.
func TestUsageStatsSingleDayKeyAggregateStillExpiresAfterOneMinute(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	for _, agg := range keyWindowAggregates() {
		t.Run(agg.name, func(t *testing.T) {
			repo, mock, server := newKeyWindowRepo(t, time.Minute)
			agg.expect(mock, 1)
			_, err := agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			server.FastForward(61 * time.Second)
			agg.expect(mock, 2)
			got, err := agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 2, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// An operator who configured a longer TTL than five minutes keeps it: the constant only ever extends.
func TestUsageStatsKeyWindowNeverShortensTheConfiguredTTL(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	repo, mock, server := newKeyWindowRepo(t, 10*time.Minute)
	usageStatsAggregateRow(mock, 1, 0)
	_, err := repo.GetAPIKeyStatsAggregated(context.Background(), 7, start, start.Add(30*24*time.Hour))
	require.NoError(t, err)
	keys := server.Keys()
	require.Len(t, keys, 1)
	require.Equal(t, 10*time.Minute, server.TTL(keys[0]))
}

// The identity of a multi-day answer still carries everything that changes it: a longer TTL must not let one
// key or window read another's entry.
func TestUsageStatsKeyWindowAggregatesNeverShareAnEntry(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	for _, agg := range keyWindowAggregates() {
		t.Run(agg.name, func(t *testing.T) {
			repo, mock, server := newKeyWindowRepo(t, time.Minute)
			asks := []struct {
				name       string
				apiKeyID   int64
				start, end time.Time
				requests   int64
			}{
				{"first key", 7, start, end, 1},
				{"another key, same window", 9, start, end, 2},
				{"same key, next 5 minute bucket", 7, start.Add(5 * time.Minute), end.Add(5 * time.Minute), 3},
				{"same key, another window length", 7, start, end.Add(24 * time.Hour), 4},
			}
			for _, ask := range asks {
				agg.expect(mock, ask.requests)
				got, err := agg.ask(repo, 42, ask.apiKeyID, ask.start, ask.end)
				require.NoError(t, err, ask.name)
				require.Equal(t, ask.requests, got, ask.name)
			}
			require.Len(t, server.Keys(), len(asks))
			// Asking again is answered by the entry each question owns, not by a neighbour's, with no query.
			for _, ask := range asks {
				got, err := agg.ask(repo, 42, ask.apiKeyID, ask.start, ask.end)
				require.NoError(t, err, ask.name)
				require.Equal(t, ask.requests, got, ask.name)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// The by-day trend is owned by (user, key): the same key asked as another user is another entry, and the time
// zone the day labels are cut in is part of the identity too.
func TestUsageStatsKeyWindowTrendIdentityIncludesUserAndTimezone(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	trend := keyWindowAggregates()[0]
	repo, mock, server := newKeyWindowRepo(t, time.Minute)

	trend.expect(mock, 1)
	got, err := trend.ask(repo, 42, 7, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 1, got)

	trend.expect(mock, 2)
	got, err = trend.ask(repo, 43, 7, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 2, got, "the same key asked as another user is another question")

	useGroupUsageRepositoryTestTimezone(t, "America/New_York")
	trend.expect(mock, 3)
	got, err = trend.ask(repo, 42, 7, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 3, got, "day labels cut in another time zone are another answer")
	require.Len(t, server.Keys(), 3)
	require.NoError(t, mock.ExpectationsWereMet())
}

// gatedExecutor holds every query until release is closed, so a test can look at Redis while a scan is in flight.
type gatedExecutor struct {
	sqlExecutor
	started chan struct{} // buffered: one signal per query that reached the gate
	release chan struct{}
}

func (g *gatedExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.sqlExecutor.QueryContext(ctx, query, args...)
}

// A poll that gave up (the /v1/usage budget) leaves nothing behind in Redis while the scan is still running, and
// what the finished scan publishes afterwards is the real answer: the next poll reads it, not an empty report.
func TestUsageStatsKeyWindowBudgetTimeoutNeverCachesAnEmptyAnswer(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	for _, agg := range keyWindowAggregates() {
		t.Run(agg.name, func(t *testing.T) {
			repo, mock, server := newKeyWindowRepo(t, time.Minute)
			gate := &gatedExecutor{sqlExecutor: repo.sql, started: make(chan struct{}, 1), release: make(chan struct{})}
			repo.sql = gate
			agg.expect(mock, 9)

			// The handler's budget, shortened: the caller gives up while the scan is still held at the gate.
			budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, err := agg.run(budget, repo, 42, 7, start, end)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			select {
			case <-gate.started:
			case <-time.After(5 * time.Second):
				t.Fatal("the scan never started: the caller's budget must not cancel the shared load")
			}
			require.Empty(t, server.Keys(), "nothing may be published for a caller that gave up, while the scan is in flight")

			// The scan was not cancelled with the caller: it finishes and publishes what the database said.
			close(gate.release)
			require.Eventually(t, func() bool { return len(server.Keys()) == 1 }, 5*time.Second, time.Millisecond)
			require.Equal(t, usageStatsKeyWindowTTL, server.TTL(server.Keys()[0]))
			got, err := agg.ask(repo, 42, 7, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 9, got, "the next poll must read the real answer, never an empty one")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// A scan that fails publishes nothing to Redis: the failure is remembered in the process only, and only a
// statement timeout for long (#656). A scan somebody stopped is asked again at once.
func TestUsageStatsKeyWindowFailuresNeverReachRedis(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour)
	failures := []struct {
		name       string
		err        error
		remembered bool
	}{
		{"statement timeout", &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}, true},
		{"cancelled by an operator", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}, false},
		{"cancelled context", context.Canceled, false},
	}
	for _, agg := range keyWindowAggregates() {
		for _, failure := range failures {
			t.Run(agg.name+"/"+failure.name, func(t *testing.T) {
				repo, mock, server := newKeyWindowRepo(t, time.Minute)
				mock.ExpectQuery(keyWindowAnySQL).WillReturnError(failure.err)
				_, err := agg.ask(repo, 42, 7, start, end)
				require.ErrorIs(t, err, failure.err)
				require.Empty(t, server.Keys(), "an error must never masquerade as an answer")

				// A second answer is registered either way; whether it gets used is the point.
				agg.expect(mock, 4)
				got, err := agg.ask(repo, 42, 7, start, end)
				if failure.remembered {
					require.ErrorIs(t, err, failure.err, "a scan that ran out of time is replayed, not run again")
					require.Error(t, mock.ExpectationsWereMet(), "the database must not have been asked again")
					require.Empty(t, server.Keys())
					return
				}
				require.NoError(t, err)
				require.EqualValues(t, 4, got, "a scan somebody stopped is asked again")
				require.Len(t, server.Keys(), 1)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
