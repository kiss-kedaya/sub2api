package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// The usage part of the user dashboard (GetUserDashboardStats) is read in two parts once the local day is an hour
// old (userDashboardUsage): the days that are over, kept for 26 hours, and today, kept for the cache's own TTL.
// These tests pin what is asked of the database and when, with sqlmock and miniredis; whether the two parts add up
// to the answer of one plain pass over the window, against a real PostgreSQL, is in
// usage_user_dashboard_split_pg_test.go.

const (
	dashUser = int64(42)

	dashKeysTotalSQL  = `SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND deleted_at IS NULL`
	dashKeysActiveSQL = `SELECT COUNT\(\*\) FROM api_keys WHERE user_id = \$1 AND status = \$2 AND deleted_at IS NULL`

	// Each of the reads of a cut dashboard, told apart by its time range: the rollup before the cut, the rollup of
	// today's whole hours, usage_logs between two instants (the closed part's fallback), usage_logs from an instant
	// on (the open hour, or all of today), the request rate, and the per-platform breakdown (with or without an end).
	dashClosedRollupSQL    = `FROM usage_dashboard_hourly_users WHERE user_id = \$1 AND bucket_start < \$2$`
	dashTodayRollupSQL     = `FROM usage_dashboard_hourly_users WHERE user_id = \$1 AND bucket_start >= \$2 AND bucket_start < \$3$`
	dashWholeRollupSQL     = `FROM usage_dashboard_hourly_users\s+WHERE user_id = \$1\s+AND bucket_start < \$3`
	dashRawBetweenSQL      = `FROM usage_logs WHERE user_id = \$1 AND created_at >= \$2 AND created_at < \$3$`
	dashRawFromSQL         = `FROM usage_logs WHERE user_id = \$1 AND created_at >= \$2$`
	dashPerformanceSQL     = `FROM usage_logs\s+WHERE created_at >= \$1 AND user_id = \$2`
	dashPlatformClosedSQL  = `FROM usage_logs ul .* AND ul.created_at >= \$3 AND ul.created_at < \$4 AND ul.actual_cost > 0`
	dashPlatformFromSQL    = `FROM usage_logs ul .* AND ul.created_at >= \$3 AND ul.actual_cost > 0`
	dashWholeWindowRawSQL  = `FROM usage_logs\s+WHERE user_id = \$1 AND created_at >= \$2\s*$`
	dashWholePlatformQuery = `FROM usage_logs ul`
)

var (
	dashRollupCols   = []string{"hourly_rows", "total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost", "total_duration_ms", "duration_count"}
	dashRawCols      = []string{"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost", "duration_ms", "duration_count"}
	dashPlatformCols = []string{"platform", "total_requests", "total_tokens", "total_actual_cost", "today_requests", "today_tokens", "today_actual_cost"}
	dashPerfCols     = []string{"request_count", "token_count"}

	// The whole answer's reads (what runs when the day is not cut) carry today's figures next to the totals.
	dashWholeRollupCols = append(slices.Clone(dashRollupCols), "today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens", "today_cost", "today_actual_cost")
	dashWholeTailCols   = []string{
		"c", "in", "out", "cc", "cr", "cost", "actual", "dur", "dur_n",
		"today_c", "today_in", "today_out", "today_cc", "today_cr", "today_cost", "today_actual",
	}
	dashWholeRawCols = []string{
		"total_requests", "total_input_tokens", "total_output_tokens", "total_cache_creation_tokens", "total_cache_read_tokens",
		"total_cost", "total_actual_cost", "avg_duration_ms",
		"today_requests", "today_input_tokens", "today_output_tokens", "today_cache_creation_tokens", "today_cache_read_tokens",
		"today_cost", "today_actual_cost",
	}
)

// dashSums is one set of the usage figures a read answers with.
type dashSums struct {
	rows, requests, input, output, cacheCreation, cacheRead int64
	cost, actual                                            float64
	durationMs, durationCount                               int64
}

func (s dashSums) rollupRows() *sqlmock.Rows {
	return sqlmock.NewRows(dashRollupCols).AddRow(s.rows, s.requests, s.input, s.output, s.cacheCreation, s.cacheRead, s.cost, s.actual, s.durationMs, s.durationCount)
}

func (s dashSums) rawRows() *sqlmock.Rows {
	return sqlmock.NewRows(dashRawCols).AddRow(s.requests, s.input, s.output, s.cacheCreation, s.cacheRead, s.cost, s.actual, s.durationMs, s.durationCount)
}

func dashPlatformRows(rows ...[]driver.Value) *sqlmock.Rows {
	out := sqlmock.NewRows(dashPlatformCols)
	for _, r := range rows {
		out.AddRow(r...)
	}
	return out
}

// The figures of the main scenario: a user with a long history, a morning today and a busy open hour.
var (
	dashClosedRollup = dashSums{rows: 40, requests: 1000, input: 10000, output: 20000, cacheCreation: 300, cacheRead: 400, cost: 10.5, actual: 8.25, durationMs: 500000, durationCount: 1000}
	dashTodayRollup  = dashSums{rows: 12, requests: 50, input: 500, output: 600, cacheCreation: 7, cacheRead: 8, cost: 0.5, actual: 0.4, durationMs: 5000, durationCount: 50}
	dashOpenHour     = dashSums{requests: 5, input: 50, output: 60, cacheCreation: 1, cacheRead: 2, cost: 0.05, actual: 0.04, durationMs: 100, durationCount: 4}

	// A closed part has no "today" columns; a today part has the same figures in both.
	dashClosedPlatforms = [][]driver.Value{{"anthropic", int64(700), int64(25000), 6.0, int64(0), int64(0), 0.0}, {"openai", int64(300), int64(5000), 2.25, int64(0), int64(0), 0.0}}
	dashTodayPlatforms  = [][]driver.Value{{"openai", int64(55), int64(2000), 0.44, int64(55), int64(2000), 0.44}, {"gemini", int64(1), int64(10), 0.01, int64(1), int64(10), 0.01}}
)

// requireDashboardStats checks the main scenario's answer: the closed part, today's rollup hours and the open hour
// added up, today's own figures, the average duration from sums and counts, and the platforms added by name.
func requireDashboardStats(t *testing.T, stats *UserDashboardStats) {
	t.Helper()
	require.EqualValues(t, 1055, stats.TotalRequests)
	require.EqualValues(t, 10550, stats.TotalInputTokens)
	require.EqualValues(t, 20660, stats.TotalOutputTokens)
	require.EqualValues(t, 308, stats.TotalCacheCreationTokens)
	require.EqualValues(t, 410, stats.TotalCacheReadTokens)
	require.EqualValues(t, 31928, stats.TotalTokens)
	require.InDelta(t, 11.05, stats.TotalCost, 1e-9)
	require.InDelta(t, 8.69, stats.TotalActualCost, 1e-9)
	require.EqualValues(t, 55, stats.TodayRequests)
	require.EqualValues(t, 550, stats.TodayInputTokens)
	require.EqualValues(t, 660, stats.TodayOutputTokens)
	require.EqualValues(t, 8, stats.TodayCacheCreationTokens)
	require.EqualValues(t, 10, stats.TodayCacheReadTokens)
	require.EqualValues(t, 1228, stats.TodayTokens)
	require.InDelta(t, 0.55, stats.TodayCost, 1e-9)
	require.InDelta(t, 0.44, stats.TodayActualCost, 1e-9)
	require.InDelta(t, 505100.0/1054.0, stats.AverageDurationMs, 1e-9, "the average is the summed duration over the summed count, not an average of averages")
	require.EqualValues(t, 2, stats.Rpm)
	require.EqualValues(t, 200, stats.Tpm)
	require.Equal(t, []PlatformDashboardStats{
		{Platform: "anthropic", TotalRequests: 700, TotalTokens: 25000, TotalActualCost: 6.0},
		{Platform: "openai", TotalRequests: 355, TotalTokens: 7000, TotalActualCost: 2.69, TodayRequests: 55, TodayTokens: 2000, TodayActualCost: 0.44},
		{Platform: "gemini", TotalRequests: 1, TotalTokens: 10, TotalActualCost: 0.01, TodayRequests: 1, TodayTokens: 10, TodayActualCost: 0.01},
	}, roundedPlatforms(stats.ByPlatform))
}

// roundedPlatforms rounds the costs of platform rows to 6 places, so rows can be compared with Equal.
func roundedPlatforms(in []PlatformDashboardStats) []PlatformDashboardStats {
	out := slices.Clone(in)
	for i := range out {
		out[i].TotalActualCost = float64(int64(out[i].TotalActualCost*1e6+0.5)) / 1e6
		out[i].TodayActualCost = float64(int64(out[i].TodayActualCost*1e6+0.5)) / 1e6
	}
	return out
}

type dashboardSplitEnv struct {
	t      *testing.T
	repo   *usageLogRepository
	mock   sqlmock.Sqlmock
	server *miniredis.Miniredis
	now    time.Time // what the repository's clock says; tests move it
}

// newDashboardSplitEnv is a repository with a cache, whose clock stands at the given local time of the zone the
// application is set to.
func newDashboardSplitEnv(t *testing.T, zone string, year int, month time.Month, day, hour, minute int) *dashboardSplitEnv {
	t.Helper()
	useRollupTestTimezone(t, zone)
	repo, mock, server := newKeyWindowRepo(t, time.Minute)
	e := &dashboardSplitEnv{t: t, repo: repo, mock: mock, server: server, now: time.Date(year, month, day, hour, minute, 0, 0, timezone.Location())}
	repo.clock = func() time.Time { return e.now }
	return e
}

// cut is the start of the local day the clock stands in; closedStart the start of the window of the part before it.
func (e *dashboardSplitEnv) cut() time.Time { return timezone.StartOfDay(e.now) }

func (e *dashboardSplitEnv) closedStart() time.Time {
	return e.cut().Add(-dashboardSubjectUsageLookback)
}

func (e *dashboardSplitEnv) expectKeyCounts() {
	e.mock.ExpectQuery(dashKeysTotalSQL).WithArgs(dashUser).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	e.mock.ExpectQuery(dashKeysActiveSQL).WithArgs(dashUser, service.StatusActive).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
}

// expectClosed registers the reads of the closed part where the rollup has the user's history.
func (e *dashboardSplitEnv) expectClosed() {
	cut := e.cut()
	e.mock.ExpectQuery(dashClosedRollupSQL).WithArgs(dashUser, instant(cut)).WillReturnRows(dashClosedRollup.rollupRows())
	e.mock.ExpectQuery(dashPlatformClosedSQL).WithArgs(dashUser, instant(cut), instant(e.closedStart()), instant(cut)).
		WillReturnRows(dashPlatformRows(dashClosedPlatforms...))
}

// expectToday registers the reads of the today part where the rollup has today's whole hours: those hours, the
// open hour, the request rate and today's platforms.
func (e *dashboardSplitEnv) expectToday() {
	cut := e.cut()
	hourStart := e.now.Truncate(time.Hour)
	e.mock.ExpectQuery(dashTodayRollupSQL).WithArgs(dashUser, instant(cut), instant(hourStart)).WillReturnRows(dashTodayRollup.rollupRows())
	e.mock.ExpectQuery(dashRawFromSQL).WithArgs(dashUser, instant(hourStart)).WillReturnRows(dashOpenHour.rawRows())
	e.mock.ExpectQuery(dashPerformanceSQL).WithArgs(sqlmock.AnyArg(), dashUser).WillReturnRows(sqlmock.NewRows(dashPerfCols).AddRow(int64(10), int64(1000)))
	e.mock.ExpectQuery(dashPlatformFromSQL).WithArgs(dashUser, instant(cut), instant(cut)).WillReturnRows(dashPlatformRows(dashTodayPlatforms...))
}

func (e *dashboardSplitEnv) get() (*UserDashboardStats, error) {
	return e.repo.GetUserDashboardStats(context.Background(), dashUser)
}

// ttls are the lifetimes of everything the cache holds, shortest first.
func (e *dashboardSplitEnv) ttls() []time.Duration {
	var out []time.Duration
	for _, key := range e.server.Keys() {
		out = append(out, e.server.TTL(key))
	}
	slices.Sort(out)
	return out
}

func TestUserDashboardReadsClosedDaysOnceAndTodayEveryRefresh(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)

	// Cold: key counts, the closed part, then today's.
	e.expectKeyCounts()
	e.expectClosed()
	e.expectToday()
	stats, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	requireDashboardStats(t, stats)
	require.EqualValues(t, 3, stats.TotalAPIKeys)
	require.EqualValues(t, 2, stats.ActiveAPIKeys)
	// The closed part is kept for 26 hours, today's for the cache's own TTL.
	require.Equal(t, []time.Duration{time.Minute, 26 * time.Hour}, e.ttls())

	// Warm: only the key counts, which stay live.
	e.expectKeyCounts()
	again, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Equal(t, stats, again)

	// A refresh later today's part has lapsed and is read again, the closed days are not.
	e.server.FastForward(61 * time.Second)
	e.now = e.now.Add(61 * time.Second)
	e.expectKeyCounts()
	e.expectToday()
	refreshed, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	requireDashboardStats(t, refreshed)

	// The next local day asks a new question (the cut moved): the closed part is read again although the old entry
	// is far from expired, and the old one is left to expire on its own.
	e.now = time.Date(2026, 11, 27, 9, 5, 0, 0, timezone.Location())
	e.expectKeyCounts()
	e.expectClosed()
	e.expectToday()
	next, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	requireDashboardStats(t, next)
	require.Len(t, e.server.Keys(), 4, "yesterday's two entries are left to expire on their own, today's two are new")
}

// Before the first hour of the local day is over the day before is answered together with today, whole, as it was
// before the cut; at the hour the cut begins.
func TestUserDashboardIsAnsweredWholeInTheFirstHourOfTheDay(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 0, 59)
	e.now = e.now.Add(59 * time.Second)

	e.expectKeyCounts()
	// Whole: one rollup read over all of it, no rows there, so usage_logs since the start of the window.
	e.mock.ExpectQuery(dashWholeRollupSQL).WithArgs(dashUser, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(dashWholeRollupCols).AddRow(int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0, int64(0), int64(0),
			int64(0), int64(0), int64(0), int64(0), int64(0), 0.0, 0.0))
	e.mock.ExpectQuery(dashWholeWindowRawSQL).WithArgs(dashUser, instant(e.now.Add(-dashboardSubjectUsageLookback)), instant(e.cut())).
		WillReturnRows(sqlmock.NewRows(dashWholeRawCols).AddRow(int64(8), int64(80), int64(90), int64(1), int64(2), 3.0, 2.5, 12.0,
			int64(3), int64(30), int64(40), int64(0), int64(1), 1.0, 0.8))
	e.mock.ExpectQuery(dashPerformanceSQL).WillReturnRows(sqlmock.NewRows(dashPerfCols).AddRow(int64(0), int64(0)))
	e.mock.ExpectQuery(dashWholePlatformQuery).WithArgs(dashUser, instant(e.cut()), instant(e.now.Add(-dashboardSubjectUsageLookback))).
		WillReturnRows(dashPlatformRows())
	stats, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.EqualValues(t, 8, stats.TotalRequests)
	require.EqualValues(t, 3, stats.TodayRequests)
	require.Equal(t, []time.Duration{time.Minute}, e.ttls(), "the whole answer is one entry of the cache's own TTL")

	// At one o'clock sharp the day is cut.
	e.server.FlushAll()
	e.now = e.cut().Add(time.Hour)
	e.expectKeyCounts()
	e.expectClosed()
	e.expectToday()
	stats, err = e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	requireDashboardStats(t, stats)
}

// Where the rollup has nothing for the user (no rows, or rows with no usage in them), each part falls back to
// usage_logs on its own range: the closed part from the start of its window to the cut, today from the cut on,
// without the open-hour read the rollup needs.
func TestUserDashboardPartsFallBackToUsageLogsOnTheirOwnRanges(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	cut := e.cut()

	e.expectKeyCounts()
	// Closed: no rows at all, then usage_logs between the start of the window and the cut.
	e.mock.ExpectQuery(dashClosedRollupSQL).WithArgs(dashUser, instant(cut)).WillReturnRows(dashSums{}.rollupRows())
	e.mock.ExpectQuery(dashRawBetweenSQL).WithArgs(dashUser, instant(e.closedStart()), instant(cut)).
		WillReturnRows(dashSums{requests: 8, input: 80, output: 90, cacheCreation: 1, cacheRead: 2, cost: 3, actual: 2.5, durationMs: 120, durationCount: 8}.rawRows())
	e.mock.ExpectQuery(dashPlatformClosedSQL).WithArgs(dashUser, instant(cut), instant(e.closedStart()), instant(cut)).
		WillReturnRows(dashPlatformRows())
	// Today: rows without usage in them (an active-user row) are a miss too; usage_logs from the cut on.
	e.mock.ExpectQuery(dashTodayRollupSQL).WithArgs(dashUser, instant(cut), instant(e.now.Truncate(time.Hour))).
		WillReturnRows(dashSums{rows: 4}.rollupRows())
	e.mock.ExpectQuery(dashRawFromSQL).WithArgs(dashUser, instant(cut)).
		WillReturnRows(dashSums{requests: 3, input: 30, output: 40, cacheRead: 1, cost: 1, actual: 0.8, durationMs: 60, durationCount: 2}.rawRows())
	e.mock.ExpectQuery(dashPerformanceSQL).WillReturnRows(sqlmock.NewRows(dashPerfCols).AddRow(int64(0), int64(0)))
	e.mock.ExpectQuery(dashPlatformFromSQL).WithArgs(dashUser, instant(cut), instant(cut)).WillReturnRows(dashPlatformRows())

	stats, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.EqualValues(t, 11, stats.TotalRequests)
	require.EqualValues(t, 3, stats.TodayRequests)
	require.EqualValues(t, 110, stats.TotalInputTokens)
	require.EqualValues(t, 70, stats.TodayInputTokens+stats.TodayOutputTokens)
	require.InDelta(t, 3.3, stats.TotalActualCost, 1e-9)
	require.InDelta(t, 180.0/10.0, stats.AverageDurationMs, 1e-9, "(120+60)/(8+2), not the mean of 15 and 30")
	require.Nil(t, stats.ByPlatform)
}

// With no cache the answer is never cut: two reads would only cost more.
func TestUserDashboardIsNeverCutWithoutACache(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	e.repo.statsCache = nil

	hourStart := e.now.Truncate(time.Hour)
	e.expectKeyCounts()
	e.mock.ExpectQuery(dashWholeRollupSQL).WithArgs(dashUser, instant(e.cut()), instant(hourStart)).
		WillReturnRows(sqlmock.NewRows(dashWholeRollupCols).AddRow(int64(40), int64(1000), int64(10000), int64(20000), int64(300), int64(400), 10.5, 8.25, int64(500000), int64(1000),
			int64(50), int64(500), int64(600), int64(7), int64(8), 0.5, 0.4))
	e.mock.ExpectQuery(dashRawFromSQL).WithArgs(dashUser, instant(hourStart), instant(e.cut())).
		WillReturnRows(sqlmock.NewRows(dashWholeTailCols).AddRow(int64(5), int64(50), int64(60), int64(1), int64(2), 0.05, 0.04, int64(100), int64(4),
			int64(5), int64(50), int64(60), int64(1), int64(2), 0.05, 0.04))
	e.mock.ExpectQuery(dashPerformanceSQL).WillReturnRows(sqlmock.NewRows(dashPerfCols).AddRow(int64(0), int64(0)))
	e.mock.ExpectQuery(dashWholePlatformQuery).WithArgs(dashUser, instant(e.cut()), instant(e.now.Add(-dashboardSubjectUsageLookback))).
		WillReturnRows(dashPlatformRows(dashClosedPlatforms[:1]...))

	stats, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.EqualValues(t, 1005, stats.TotalRequests)
	require.EqualValues(t, 55, stats.TodayRequests)
	require.Len(t, stats.ByPlatform, 1)
}

func TestUserDashboardCutDecision(t *testing.T) {
	useRollupTestTimezone(t, "Asia/Shanghai")
	loc := timezone.Location()
	cached := &usageLogRepository{statsCache: &usageStatsCache{}}
	uncached := &usageLogRepository{}
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, loc)
	windowStart := today.Add(-dashboardSubjectUsageLookback)

	for _, tc := range []struct {
		name        string
		repo        *usageLogRepository
		now         time.Time
		windowStart time.Time
		wantCut     bool
	}{
		{"mid day", cached, today.Add(12 * time.Hour), windowStart, true},
		{"no cache", uncached, today.Add(12 * time.Hour), windowStart, false},
		{"midnight sharp", cached, today, windowStart, false},
		{"a second before the hour", cached, today.Add(time.Hour - time.Second), windowStart, false},
		{"on the hour", cached, today.Add(time.Hour), windowStart, true},
		{"last second of the day", cached, today.AddDate(0, 0, 1).Add(-time.Second), windowStart, true},
		{"window starts at today (no closed day)", cached, today.Add(12 * time.Hour), today, false},
		{"window starts after today", cached, today.Add(12 * time.Hour), today.Add(time.Hour), false},
		{"window starts one microsecond before today", cached, today.Add(12 * time.Hour), today.Add(-time.Microsecond), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cut, ok := tc.repo.userDashboardCut(tc.now, today, tc.windowStart)
			require.Equal(t, tc.wantCut, ok)
			if ok {
				require.True(t, today.Equal(cut), "the cut is the start of today, not now-24h: %v", cut)
			} else {
				require.True(t, cut.IsZero())
			}
		})
	}
}

// The cut is the start of the local day, which on the day of a DST change is 23 or 25 hours (and in Lord Howe 23.5
// after the start): never the moment 24 hours before now.
func TestUserDashboardCutIsTheLocalMidnightOnDSTDays(t *testing.T) {
	for _, tc := range []struct {
		zone       string
		y, m, d    int
		dayLength  time.Duration
		hourOfNow  int
		wantCutUTC string
	}{
		{"America/Los_Angeles", 2026, 3, 8, 23 * time.Hour, 15, "2026-03-08T08:00:00Z"},
		{"America/Los_Angeles", 2026, 11, 1, 25 * time.Hour, 15, "2026-11-01T07:00:00Z"},
		{"Europe/London", 2026, 3, 29, 23 * time.Hour, 15, "2026-03-29T00:00:00Z"},
		{"Australia/Lord_Howe", 2026, 4, 5, 24*time.Hour + 30*time.Minute, 15, "2026-04-04T13:00:00Z"},
		{"Australia/Lord_Howe", 2026, 10, 4, 23*time.Hour + 30*time.Minute, 15, "2026-10-03T13:30:00Z"},
		{"Asia/Kolkata", 2026, 11, 26, 24 * time.Hour, 15, "2026-11-25T18:30:00Z"},
	} {
		t.Run(fmt.Sprintf("%s %d-%02d-%02d", tc.zone, tc.y, tc.m, tc.d), func(t *testing.T) {
			e := newDashboardSplitEnv(t, tc.zone, tc.y, time.Month(tc.m), tc.d, tc.hourOfNow, 0)
			today := timezone.StartOfDay(e.now)
			require.Equal(t, tc.wantCutUTC, today.UTC().Format(time.RFC3339))
			require.Equal(t, tc.dayLength, today.AddDate(0, 0, 1).Sub(today), "the day is as long as the fixture says")

			cut, ok := e.repo.userDashboardCut(e.now, today, dashboardSubjectWindowStart(e.now, today))
			require.True(t, ok)
			require.True(t, cut.Equal(today))
			require.False(t, cut.Equal(e.now.Add(-24*time.Hour)))
			// The key names that instant (in UTC) and the zone.
			closed := userDashboardClosedKey(dashUser, dashboardSubjectWindowStart(cut, cut), cut)
			require.Equal(t, tc.wantCutUTC, closed.End)
			require.Equal(t, tc.zone, closed.Timezone)
		})
	}
}

func TestUserDashboardPartsHaveTheirOwnCacheClass(t *testing.T) {
	cut := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	closed := userDashboardClosedKey(dashUser, cut.Add(-dashboardSubjectUsageLookback), cut)
	today := userDashboardTodayKey(dashUser, cut)
	// The closed days stay as long as the closed days of a by-day trend and get the multi-day budget; today keeps
	// the cache's own TTL and budget.
	require.Equal(t, 26*time.Hour, closed.TTL)
	require.Equal(t, usageStatsKeyWindowLoadTimeout, closed.loadTimeout())
	require.Equal(t, usageStatsKeyWindowTimeoutTTL, closed.timeoutMemory())
	require.Zero(t, today.TTL)
	require.Equal(t, usageStatsCacheLoadTimeout, today.loadTimeout())
	require.Equal(t, usageStatsCacheTimeoutTTL, today.timeoutMemory())
	// Both are the user's, so they queue in the concurrency gate as the user.
	require.Equal(t, "u42", closed.gateOwner())
	require.Equal(t, "u42", today.gateOwner())

	// What is asked is part of the key: another user, another cut, another zone are other entries.
	seen := map[string]string{}
	for name, key := range map[string]usageStatsCacheKey{
		"closed":           closed,
		"today":            today,
		"closed, user 43":  userDashboardClosedKey(43, cut.Add(-dashboardSubjectUsageLookback), cut),
		"closed, next day": userDashboardClosedKey(dashUser, cut.AddDate(0, 0, 1).Add(-dashboardSubjectUsageLookback), cut.AddDate(0, 0, 1)),
		"today, next day":  userDashboardTodayKey(dashUser, cut.AddDate(0, 0, 1)),
		"whole":            {Query: "user_dashboard_usage", UserID: dashUser, Start: cut.Format(time.RFC3339Nano)},
	} {
		redisKey := usageStatsRedisKey(key)
		require.NotContains(t, seen, redisKey, "%s and %s share an entry", name, seen[redisKey])
		seen[redisKey] = name
	}
	useRollupTestTimezone(t, "America/New_York")
	require.NotEqual(t, usageStatsRedisKey(closed), usageStatsRedisKey(userDashboardClosedKey(dashUser, cut.Add(-dashboardSubjectUsageLookback), cut)))
}

func TestMergeUserDashboardParts(t *testing.T) {
	platform := func(name string, requests int64, cost float64, todayRequests int64, todayCost float64) PlatformDashboardStats {
		return PlatformDashboardStats{Platform: name, TotalRequests: requests, TotalTokens: requests * 10, TotalActualCost: cost, TodayRequests: todayRequests, TodayTokens: todayRequests * 10, TodayActualCost: todayCost}
	}

	t.Run("two empty parts are an empty dashboard", func(t *testing.T) {
		stats := mergeUserDashboardParts(&userDashboardPart{}, &userDashboardPart{})
		require.Equal(t, &UserDashboardStats{}, stats)
		require.Nil(t, stats.ByPlatform, "nothing to show stays nil, as the statement's result is")
	})

	t.Run("totals are the two parts, today is the second one", func(t *testing.T) {
		stats := mergeUserDashboardParts(
			&userDashboardPart{Requests: 100, InputTokens: 10, OutputTokens: 20, CacheCreationTokens: 3, CacheReadTokens: 4, Cost: 1.5, ActualCost: 1.25, Rpm: 99, Tpm: 99},
			&userDashboardPart{Requests: 7, InputTokens: 1, OutputTokens: 2, CacheCreationTokens: 3, CacheReadTokens: 4, Cost: 0.5, ActualCost: 0.25, Rpm: 3, Tpm: 30})
		require.EqualValues(t, 107, stats.TotalRequests)
		require.EqualValues(t, 11, stats.TotalInputTokens)
		require.EqualValues(t, 22, stats.TotalOutputTokens)
		require.EqualValues(t, 6, stats.TotalCacheCreationTokens)
		require.EqualValues(t, 8, stats.TotalCacheReadTokens)
		require.EqualValues(t, 47, stats.TotalTokens)
		require.InDelta(t, 2.0, stats.TotalCost, 1e-12)
		require.InDelta(t, 1.5, stats.TotalActualCost, 1e-12)
		require.EqualValues(t, 7, stats.TodayRequests)
		require.EqualValues(t, 1, stats.TodayInputTokens)
		require.EqualValues(t, 2, stats.TodayOutputTokens)
		require.EqualValues(t, 3, stats.TodayCacheCreationTokens)
		require.EqualValues(t, 4, stats.TodayCacheReadTokens)
		require.EqualValues(t, 10, stats.TodayTokens)
		require.InDelta(t, 0.5, stats.TodayCost, 1e-12)
		require.InDelta(t, 0.25, stats.TodayActualCost, 1e-12)
		require.EqualValues(t, 3, stats.Rpm, "the rate is what is happening now: the today part's")
		require.EqualValues(t, 30, stats.Tpm)
	})

	t.Run("the average is rebuilt from sums and counts", func(t *testing.T) {
		// Averages of 100 ms (10 rows) and 50 ms (1 row): 75 as a mean of means, 95.45 as it really is.
		stats := mergeUserDashboardParts(&userDashboardPart{DurationMs: 1000, DurationCount: 10}, &userDashboardPart{DurationMs: 50, DurationCount: 1})
		require.InDelta(t, 1050.0/11.0, stats.AverageDurationMs, 1e-12)
		// Rows without a duration are not counted, in whichever part they are.
		stats = mergeUserDashboardParts(&userDashboardPart{Requests: 5}, &userDashboardPart{Requests: 5, DurationMs: 40, DurationCount: 2})
		require.InDelta(t, 20.0, stats.AverageDurationMs, 1e-12)
		require.Zero(t, mergeUserDashboardParts(&userDashboardPart{Requests: 5}, &userDashboardPart{Requests: 5}).AverageDurationMs)
	})

	t.Run("a platform found in one part only is that row unchanged", func(t *testing.T) {
		stats := mergeUserDashboardParts(
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("anthropic", 7, 3.5, 0, 0)}},
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("gemini", 2, 0.5, 2, 0.5)}})
		require.Equal(t, []PlatformDashboardStats{platform("anthropic", 7, 3.5, 0, 0), platform("gemini", 2, 0.5, 2, 0.5)}, stats.ByPlatform)
		stats = mergeUserDashboardParts(&userDashboardPart{}, &userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("gemini", 2, 0.5, 2, 0.5)}})
		require.Equal(t, []PlatformDashboardStats{platform("gemini", 2, 0.5, 2, 0.5)}, stats.ByPlatform)
		stats = mergeUserDashboardParts(&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("gemini", 2, 0.5, 0, 0)}}, &userDashboardPart{})
		require.Equal(t, []PlatformDashboardStats{platform("gemini", 2, 0.5, 0, 0)}, stats.ByPlatform)
	})

	t.Run("a platform found in both parts is added, today's columns being today's own", func(t *testing.T) {
		stats := mergeUserDashboardParts(
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("openai", 30, 3, 0, 0)}},
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("openai", 5, 0.5, 5, 0.5)}})
		require.Equal(t, []PlatformDashboardStats{platform("openai", 35, 3.5, 5, 0.5)}, stats.ByPlatform)
	})

	t.Run("platforms come most expensive first, equal costs by name", func(t *testing.T) {
		stats := mergeUserDashboardParts(
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("openai", 1, 2, 0, 0), platform("anthropic", 1, 1, 0, 0), platform("zhipu", 1, 5, 0, 0)}},
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("gemini", 1, 1, 1, 1), platform("anthropic", 1, 3, 1, 3)}})
		var names []string
		for _, p := range stats.ByPlatform {
			names = append(names, p.Platform)
		}
		// anthropic 1+3=4 beats openai 2 and gemini 1; zhipu 5 is first.
		require.Equal(t, []string{"zhipu", "anthropic", "openai", "gemini"}, names)

		tied := mergeUserDashboardParts(
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("openai", 1, 1, 0, 0), platform("anthropic", 1, 1, 0, 0)}},
			&userDashboardPart{ByPlatform: []PlatformDashboardStats{platform("gemini", 1, 1, 1, 1)}})
		names = nil
		for _, p := range tied.ByPlatform {
			names = append(names, p.Platform)
		}
		require.Equal(t, []string{"anthropic", "gemini", "openai"}, names)
	})
}

// The part is what the cache keeps, as JSON: every figure survives the round trip.
func TestUserDashboardPartSurvivesTheCache(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	want := userDashboardPart{
		Requests: 1, InputTokens: 2, OutputTokens: 3, CacheCreationTokens: 4, CacheReadTokens: 5, Cost: 0.1 + 0.2, ActualCost: 1.0 / 3.0,
		DurationMs: 6, DurationCount: 7, Rpm: 8, Tpm: 9,
		ByPlatform: []PlatformDashboardStats{{Platform: "openai", TotalRequests: 1, TotalTokens: 2, TotalActualCost: 0.7, TodayRequests: 3, TodayTokens: 4, TodayActualCost: 1.0 / 7.0}},
	}
	key := userDashboardTodayKey(dashUser, e.cut())
	_, err := loadUsageStatsCached(e.repo.statsCache, context.Background(), key, func(context.Context) (*userDashboardPart, error) { return &want, nil })
	require.NoError(t, err)
	got, err := loadUsageStatsCached(e.repo.statsCache, context.Background(), key, func(context.Context) (*userDashboardPart, error) {
		return nil, errors.New("must be served from the cache")
	})
	require.NoError(t, err)
	require.Equal(t, want, *got)
}

// A slow database is not a broken one: when the budget of a part runs out, or a statement is cancelled, the
// dashboard answers with the busy answer of the concurrency gate (429, Retry-After) instead of a 500.
func TestUserDashboardOverBudgetAnswersBusyNotAnError(t *testing.T) {
	canceled := &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	timedOut := &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}

	for _, tc := range []struct {
		name string
		err  error
		busy bool
	}{
		{"our own load budget (the driver reports it as a user request)", canceled, true},
		{"the server's statement timeout", timedOut, true},
		{"a deadline", context.DeadlineExceeded, true},
		{"a deadline further down", fmt.Errorf("scan: %w", context.DeadlineExceeded), true},
		{"a broken connection", sql.ErrConnDone, false},
		{"another database error", &pq.Error{Code: "42703", Message: "undefined column"}, false},
	} {
		for _, part := range []string{"closed", "today"} {
			t.Run(tc.name+" in the "+part+" part", func(t *testing.T) {
				e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
				e.expectKeyCounts()
				if part == "closed" {
					e.mock.ExpectQuery(dashClosedRollupSQL).WillReturnError(tc.err)
				} else {
					e.expectClosed()
					e.mock.ExpectQuery(dashTodayRollupSQL).WillReturnError(tc.err)
				}
				stats, err := e.get()
				require.NoError(t, e.mock.ExpectationsWereMet())
				require.Nil(t, stats)
				if !tc.busy {
					require.NotErrorIs(t, err, ErrUsageStatsBusy)
					require.ErrorIs(t, err, tc.err, "other errors are returned as they are")
					return
				}
				require.ErrorIs(t, err, ErrUsageStatsBusy)
				// As the handler sees it, wrapped by the service: 429 with the wait, never a 5xx.
				status, body := infraerrors.ToHTTP(fmt.Errorf("get user dashboard stats: %w", err))
				require.Equal(t, http.StatusTooManyRequests, status)
				require.Equal(t, "USAGE_STATS_BUSY", body.Reason)
				require.Equal(t, "2", body.Metadata["retry_after"])
			})
		}
	}
}

// What the closed part cannot do in its budget is not asked again at once: the cache remembers a timeout (for five
// minutes, in the class of a month of one user's rows), and every refresh in that time is answered busy without a
// read. A statement somebody cancelled says nothing about the next attempt and is asked again.
func TestUserDashboardRemembersAClosedPartThatTimedOut(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	e.expectKeyCounts()
	e.mock.ExpectQuery(dashClosedRollupSQL).WillReturnError(context.DeadlineExceeded)
	_, err := e.get()
	require.ErrorIs(t, err, ErrUsageStatsBusy)

	// No read is registered: the only statements are the two live key counts.
	e.expectKeyCounts()
	_, err = e.get()
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.NoError(t, e.mock.ExpectationsWereMet())

	// Cancelled by somebody: not remembered, so the next refresh asks again, and is answered.
	e = newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	e.expectKeyCounts()
	e.mock.ExpectQuery(dashClosedRollupSQL).WillReturnError(&pq.Error{Code: "57014", Message: "canceling statement due to user request"})
	_, err = e.get()
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	e.expectKeyCounts()
	e.expectClosed()
	e.expectToday()
	stats, err := e.get()
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	requireDashboardStats(t, stats)
}

// The answer does not depend on whether the cache has the closed days: asked cold or warm it is the same.
func TestUserDashboardSplitSurvivesAFlushedCache(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Shanghai", 2026, time.November, 26, 12, 30)
	var answers []*UserDashboardStats
	for range 3 {
		e.server.FlushAll()
		e.expectKeyCounts()
		e.expectClosed()
		e.expectToday()
		stats, err := e.get()
		require.NoError(t, err)
		answers = append(answers, stats)
	}
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.Equal(t, answers[0], answers[1])
	require.Equal(t, answers[1], answers[2])
}

// The open hour that starts before the cut cannot happen an hour into the local day (the cut is not made before), but
// a clock that is off must not make the rollup and the open hour overlap: nothing is read from the rollup then, and
// today is read from usage_logs whole. In a half-hour zone the open hour, cut on absolute hours, does start before
// the zone's midnight in its first half hour.
func TestUserDashboardTodayPartNeverOverlapsTheRollupWithTheOpenHour(t *testing.T) {
	e := newDashboardSplitEnv(t, "Asia/Kolkata", 2026, time.November, 26, 0, 10)
	cut := e.cut()
	require.True(t, e.now.Truncate(time.Hour).Before(cut), "the open hour starts before the zone's midnight")

	e.mock.ExpectQuery(dashRawFromSQL).WithArgs(dashUser, instant(cut)).WillReturnRows(dashOpenHour.rawRows())
	e.mock.ExpectQuery(dashPerformanceSQL).WillReturnRows(sqlmock.NewRows(dashPerfCols).AddRow(int64(0), int64(0)))
	e.mock.ExpectQuery(dashPlatformFromSQL).WithArgs(dashUser, instant(cut), instant(cut)).WillReturnRows(dashPlatformRows())
	part, err := e.repo.userDashboardTodayPart(context.Background(), dashUser, cut)
	require.NoError(t, err)
	require.NoError(t, e.mock.ExpectationsWereMet())
	require.EqualValues(t, 5, part.Requests)
}

// usageRangeSQL numbers the placeholders after the ones already in use, and leaves out a bound that is not set.
func TestUsageRangeSQL(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, tc := range []struct {
		name     string
		from, to time.Time
		args     []any
		wantSQL  string
		wantArgs []any
	}{
		{"no bounds", time.Time{}, time.Time{}, []any{int64(1)}, "", []any{int64(1)}},
		{"a start only", from, time.Time{}, []any{int64(1)}, " AND created_at >= $2", []any{int64(1), from}},
		{"an end only", time.Time{}, to, []any{int64(1)}, " AND created_at < $2", []any{int64(1), to}},
		{"both", from, to, []any{int64(1)}, " AND created_at >= $2 AND created_at < $3", []any{int64(1), from, to}},
		{"after more arguments", from, to, []any{int64(1), "x"}, " AND created_at >= $3 AND created_at < $4", []any{int64(1), "x", from, to}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotArgs := usageRangeSQL("created_at", tc.from, tc.to, tc.args)
			require.Equal(t, tc.wantSQL, gotSQL)
			require.Equal(t, tc.wantArgs, gotArgs)
		})
	}
}
