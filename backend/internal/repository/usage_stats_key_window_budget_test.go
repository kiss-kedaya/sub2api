package repository

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// How long one key's multi-day aggregate may scan, and how long a timeout is remembered. The helpers
// (keyWindowAggregates, newKeyWindowRepo, keyWindowTTL) are in usage_stats_key_window_test.go.

// roleStatementTimeout is the statement_timeout of the production database role. The scan budget of a key's
// multi-day aggregate has to end the scan before the server does.
const roleStatementTimeout = 30 * time.Second

// keyWindowCacheKey is the identity of one key's aggregate over a window, with the TTL the repository gives it.
func keyWindowCacheKey(apiKeyID int64, window time.Duration) usageStatsCacheKey {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	return usageStatsCacheKey{Query: "budget", APIKeyID: apiKeyID, TTL: keyAggregateTTL(apiKeyID, start, start.Add(window))}
}

// A heavy key's month needs 16 to 28 seconds on a saturated disk, which the usual 15 second budget cut off every
// time. One key's multi-day aggregate gets 28 seconds, still short of the role's 30; every other aggregate keeps
// its 15. Observed from inside the load: the deadline of the context it is handed.
func TestUsageStatsLoadBudgetFollowsTheAggregateClass(t *testing.T) {
	require.Less(t, usageStatsKeyWindowLoadTimeout, roleStatementTimeout, "our own budget must end the scan before the server's statement_timeout does")
	cases := []struct {
		name string
		key  usageStatsCacheKey
		want time.Duration
	}{
		{"one key over 30 days", keyWindowCacheKey(7, 30*24*time.Hour), 28 * time.Second},
		{"one key over 2 days", keyWindowCacheKey(7, 48*time.Hour), 28 * time.Second},
		{"one key over one day", keyWindowCacheKey(7, 24*time.Hour), 15 * time.Second},
		{"a user page", usageStatsCacheKey{Query: "budget", UserID: 7}, 15 * time.Second},
		{"an admin report", usageStatsCacheKey{Query: "budget"}, 15 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache, _ := newUsageStatsTestCache(t)
			var (
				deadline    time.Time
				hasDeadline bool
			)
			_, err := loadUsageStatsCached(cache, context.Background(), tc.key, func(ctx context.Context) (int, error) {
				deadline, hasDeadline = ctx.Deadline()
				return 1, nil
			})
			require.NoError(t, err)
			require.True(t, hasDeadline)
			require.WithinDuration(t, time.Now().Add(tc.want), deadline, time.Second)
		})
	}
}

// A scan that takes 20 seconds fits the budget of a key's multi-day aggregate: it finishes, its answer is
// published and served without scanning again. The same scan is cut inside the usual 15 seconds, as it always
// was. The scan is simulated by what it needs from its context (that much time left), not by waiting 20 seconds.
func TestUsageStatsKeyWindowScanOfTwentySecondsFitsTheBudgetAndIsCached(t *testing.T) {
	const scan = 20 * time.Second
	var calls atomic.Int64
	load := func(ctx context.Context) (int, error) {
		calls.Add(1)
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < scan {
			return 0, context.DeadlineExceeded // cut, as the driver would report it
		}
		return 7, nil
	}
	cache, server := newUsageStatsTestCache(t)

	key := keyWindowCacheKey(7, 30*24*time.Hour)
	got, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err, "a 20 second scan fits the 28 second budget")
	require.Equal(t, 7, got)
	require.Len(t, server.Keys(), 1, "its answer is published")
	require.Equal(t, keyWindowTTL, server.TTL(server.Keys()[0]))
	got, err = loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 7, got)
	require.EqualValues(t, 1, calls.Load(), "the published answer is served without scanning again")

	_, err = loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "budget", UserID: 7}, load)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the same scan is cut inside the usual 15 seconds")
	require.Len(t, server.Keys(), 1, "and nothing is published for it")
}

// A scan that ran out of time, whoever ended it, is remembered so that it is not started again at once: for 5
// minutes when it is one key's multi-day aggregate (28 seconds of scan that did not fit will not fit again half a
// minute later), 30 seconds for everything else. When the memory lapses the scan is tried again.
func TestUsageStatsTimeoutMemoryFollowsTheAggregateClass(t *testing.T) {
	timeouts := []struct {
		name string
		load func(ctx context.Context) (int, error)
	}{
		{"the scan ran past our own budget", func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 0, &pq.Error{Code: "57014", Message: "canceling statement due to user request"} // what lib/pq reports for a cancelled context
		}},
		{"the server statement_timeout", func(context.Context) (int, error) {
			return 0, &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}
		}},
	}
	classes := []struct {
		name string
		key  usageStatsCacheKey
		want time.Duration
	}{
		{"one key over 30 days", keyWindowCacheKey(7, 30*24*time.Hour), 5 * time.Minute},
		{"one key over one day", keyWindowCacheKey(7, 24*time.Hour), 30 * time.Second},
		{"a user page", usageStatsCacheKey{Query: "memory", UserID: 7}, 30 * time.Second},
	}
	for _, class := range classes {
		for _, timeout := range timeouts {
			t.Run(class.name+"/"+timeout.name, func(t *testing.T) {
				cache, server := newUsageStatsTestCache(t)
				var calls atomic.Int64
				load := func(ctx context.Context) (int, error) {
					calls.Add(1)
					return timeout.load(ctx)
				}
				// A 30 ms budget stands in for the real one: what is under test is what follows its running out.
				ask := func() error {
					_, err := loadUsageStatsCachedWithTimeout(cache, context.Background(), class.key, 30*time.Millisecond, load)
					return err
				}
				for range 3 {
					require.Error(t, ask())
				}
				require.EqualValues(t, 1, calls.Load(), "a scan that timed out is replayed from memory, not started again")
				require.Empty(t, server.Keys())

				redisKey := usageStatsRedisKey(class.key)
				item, remembered := cache.failures.Items()[redisKey]
				require.True(t, remembered)
				remaining := time.Until(time.Unix(0, item.Expiration))
				require.Greater(t, remaining, class.want-2*time.Second)
				require.LessOrEqual(t, remaining, class.want)

				cache.failures.Set(redisKey, item.Object, time.Nanosecond) // the memory lapses
				require.Eventually(t, func() bool {
					_, ok := cache.failures.Get(redisKey)
					return !ok
				}, time.Second, time.Millisecond)
				require.Error(t, ask())
				require.EqualValues(t, 2, calls.Load(), "once the memory has lapsed the scan is tried again")
			})
		}
	}
}

// deadlineExecutor records the deadline of the context every query runs under.
type deadlineExecutor struct {
	sqlExecutor
	mu        sync.Mutex
	deadlines []time.Time
}

func (d *deadlineExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if at, ok := ctx.Deadline(); ok {
		d.mu.Lock()
		d.deadlines = append(d.deadlines, at)
		d.mu.Unlock()
	}
	return d.sqlExecutor.QueryContext(ctx, query, args...)
}

func (d *deadlineExecutor) first() (time.Time, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.deadlines) == 0 {
		return time.Time{}, false
	}
	return d.deadlines[0], true
}

// The long budget reaches the real aggregates: the by-day trend, the by-model report and the key summary, asked
// through the repository, scan under 28 seconds when they are one key's multi-day answer and under 15 otherwise.
func TestUsageStatsKeyWindowAggregatesScanUnderTheLongBudget(t *testing.T) {
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		apiKeyID int64
		window   time.Duration
		want     time.Duration
	}{
		{"key, 30 days", 7, 30 * 24 * time.Hour, 28 * time.Second},
		{"key, 2 days", 7, 48 * time.Hour, 28 * time.Second},
		{"key, today", 7, 24 * time.Hour, 15 * time.Second},
		{"no key, 30 days", 0, 30 * 24 * time.Hour, 15 * time.Second},
	}
	for _, agg := range keyWindowAggregates() {
		for _, tc := range cases {
			t.Run(agg.name+"/"+tc.name, func(t *testing.T) {
				repo, mock, _ := newKeyWindowRepo(t, time.Minute)
				recorder := &deadlineExecutor{sqlExecutor: repo.sql}
				repo.sql = recorder
				agg.expect(mock, 5)
				_, err := agg.ask(repo, 42, tc.apiKeyID, start, start.Add(tc.window))
				require.NoError(t, err)
				deadline, ok := recorder.first()
				require.True(t, ok, "the query must run under a deadline")
				require.WithinDuration(t, time.Now().Add(tc.want), deadline, time.Second)
			})
		}
	}
}
