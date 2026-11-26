package repository

import (
	"context"
	"database/sql/driver"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// One API key's by-day trend over a window that still holds today is read in two parts (GetUsageTrendWithFilters,
// keyDailyTrendSplit): the days that are over, kept for 26 hours, and today, kept for the cache's own TTL. These
// tests pin what is asked of the database and when, with sqlmock and miniredis; the answer itself, against a real
// PostgreSQL, is in usage_key_daily_split_pg_test.go. The helpers newKeyWindowRepo, keyWindowTrendSQL and
// deadlineExecutor are in usage_stats_key_window_test.go and usage_stats_key_window_budget_test.go, instant (a time
// argument matched by the moment it denotes) in dashboard_dimension_rollup_unaligned_test.go.

const (
	splitTrendUser = int64(42)
	splitTrendKey  = int64(7)
)

// pt is a trend row whose figures all follow from its request count, so the sum of two rows of one day is the row of
// the summed count.
func pt(date string, requests int64) TrendDataPoint {
	return TrendDataPoint{
		Date: date, Requests: requests, InputTokens: 10 * requests, OutputTokens: 20 * requests,
		CacheCreationTokens: requests, CacheReadTokens: 2 * requests, TotalTokens: 33 * requests,
		Cost: 0.5 * float64(requests), ActualCost: 0.25 * float64(requests),
	}
}

func trendRows(points ...TrendDataPoint) *sqlmock.Rows {
	rows := sqlmock.NewRows(keyWindowTrendCols)
	for _, p := range points {
		rows.AddRow(p.Date, p.Requests, p.InputTokens, p.OutputTokens, p.CacheCreationTokens, p.CacheReadTokens, p.TotalTokens, p.Cost, p.ActualCost)
	}
	return rows
}

func loadZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}

// dailyWindow is the window of the last `days` days up to the end of today, cut in zone the way the usage handlers
// cut it (handler.apiKeyDailyUsageRange).
func dailyWindow(t *testing.T, now time.Time, days int, zone string) (start, end time.Time) {
	t.Helper()
	local := now.In(loadZone(t, zone))
	return timezone.StartOfDayInUserLocation(local.AddDate(0, 0, -(days-1)), zone), timezone.StartOfDayInUserLocation(local.AddDate(0, 0, 1), zone)
}

type splitTrendEnv struct {
	t      *testing.T
	repo   *usageLogRepository
	mock   sqlmock.Sqlmock
	server *miniredis.Miniredis
	now    time.Time // what the repository's clock says; tests move it
}

func newSplitTrendEnv(t *testing.T, now time.Time, cacheTTL time.Duration) *splitTrendEnv {
	t.Helper()
	repo, mock, server := newKeyWindowRepo(t, cacheTTL)
	e := &splitTrendEnv{t: t, repo: repo, mock: mock, server: server, now: now}
	repo.clock = func() time.Time { return e.now }
	return e
}

// expect registers one read of the key's trend over [start, end), answered with the given rows.
func (e *splitTrendEnv) expect(start, end time.Time, rows ...TrendDataPoint) {
	e.mock.ExpectQuery(keyWindowTrendSQL).
		WithArgs(instant(start), instant(end), splitTrendUser, splitTrendKey).
		WillReturnRows(trendRows(rows...))
}

func (e *splitTrendEnv) expectError(start, end time.Time, err error) {
	e.mock.ExpectQuery(keyWindowTrendSQL).
		WithArgs(instant(start), instant(end), splitTrendUser, splitTrendKey).
		WillReturnError(err)
}

func (e *splitTrendEnv) ask(start, end time.Time) ([]TrendDataPoint, error) {
	return e.repo.GetUsageTrendWithFilters(context.Background(), start, end, "day", splitTrendUser, splitTrendKey, 0, 0, "", nil, nil, nil)
}

// ttls are the lifetimes of everything the cache holds, shortest first.
func (e *splitTrendEnv) ttls() []time.Duration {
	var out []time.Duration
	for _, key := range e.server.Keys() {
		out = append(out, e.server.TTL(key))
	}
	slices.Sort(out)
	return out
}

func (e *splitTrendEnv) requireAsked(start, end time.Time, want []TrendDataPoint, msg string) {
	e.t.Helper()
	got, err := e.ask(start, end)
	require.NoError(e.t, err, msg)
	require.Equal(e.t, want, got, msg)
}

func concatPoints(parts ...[]TrendDataPoint) []TrendDataPoint {
	var out []TrendDataPoint
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// The point of the split, over time: the closed days are read once for the whole day, however often the key is
// polled, and today is read again every cache TTL (a minute here) with the newest count. Before, the whole window
// was read again every quarter hour.
func TestKeyDailyTrendSplitReadsClosedDaysOncePerDayAndTodayEveryMinute(t *testing.T) {
	env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
	start, end := dailyWindow(t, env.now, 15, "UTC") // 11-12 .. 11-27
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	closed := []TrendDataPoint{pt("2026-11-24", 10), pt("2026-11-25", 20)}
	today := func(requests int64) []TrendDataPoint { return []TrendDataPoint{pt("2026-11-26", requests)} }

	env.expect(start, splitAt, closed...)
	env.expect(splitAt, end, today(3)...)
	env.requireAsked(start, end, concatPoints(closed, today(3)), "first call reads both parts")
	require.Equal(t, []time.Duration{time.Minute, 26 * time.Hour}, env.ttls(), "today keeps the cache's own TTL, the closed days a whole day")
	require.NoError(t, env.mock.ExpectationsWereMet())

	// Inside the minute nothing is asked (no expectation is registered: a query would fail the call).
	env.server.FastForward(59 * time.Second)
	env.requireAsked(start, end, concatPoints(closed, today(3)), "59 s later")

	// A poll every 61 seconds for half an hour, twice the quarter hour the whole window used to be kept: the closed
	// days are never asked again, today is asked every time and the newest count shows.
	for i := int64(1); i <= 30; i++ {
		env.server.FastForward(61 * time.Second)
		env.expect(splitAt, end, today(3+i)...)
		env.requireAsked(start, end, concatPoints(closed, today(3+i)), "a poll after today's entry expired")
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// After local midnight the closed days are another question ([start, splitAt) moved a day): they are read once
// more, then kept again; today starts over. The window moves with the day, as the handlers cut it.
func TestKeyDailyTrendSplitAsksTheClosedDaysAgainAfterLocalMidnight(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 23, 59, 0, 0, shanghai), time.Minute)

	start, end := dailyWindow(t, env.now, 15, "Asia/Shanghai")
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env.expect(start, splitAt, pt("2026-11-25", 20))
	env.expect(splitAt, end, pt("2026-11-26", 40))
	env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-25", 20), pt("2026-11-26", 40)}, "the evening before")

	// 00:00:30 the next day: the caller's window has moved a day. Today's entry has expired (the minute is over), the
	// closed days' entry has not, but it is for the old window and nobody asks for it any more.
	env.now = time.Date(2026, 11, 27, 0, 0, 30, 0, shanghai)
	env.server.FastForward(90 * time.Second)
	start2, end2 := dailyWindow(t, env.now, 15, "Asia/Shanghai")
	splitAt2 := time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	require.True(t, end2.Equal(splitAt2.Add(24*time.Hour)))
	env.expect(start2, splitAt2, pt("2026-11-25", 20), pt("2026-11-26", 41))
	env.expect(splitAt2, end2, pt("2026-11-27", 1))
	env.requireAsked(start2, end2, []TrendDataPoint{pt("2026-11-25", 20), pt("2026-11-26", 41), pt("2026-11-27", 1)}, "after midnight the closed days are read once more")

	// And then again only today, for the rest of the day.
	for i := int64(2); i <= 5; i++ {
		env.now = env.now.Add(61 * time.Second)
		env.server.FastForward(61 * time.Second)
		env.expect(splitAt2, end2, pt("2026-11-27", i))
		env.requireAsked(start2, end2, []TrendDataPoint{pt("2026-11-25", 20), pt("2026-11-26", 41), pt("2026-11-27", i)}, "the rest of the day")
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Today keeps whatever TTL the operator configured for the cache (a minute by default), never the quarter hour of the
// whole window and never the day of the closed days; the closed days keep their 26 hours whatever it is.
func TestKeyDailyTrendSplitTodayKeepsTheCacheTTL(t *testing.T) {
	for _, configured := range []time.Duration{time.Minute, 90 * time.Second, 30 * time.Minute} {
		t.Run(configured.String(), func(t *testing.T) {
			env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), configured)
			start, end := dailyWindow(t, env.now, 15, "UTC")
			splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
			env.expect(start, splitAt, pt("2026-11-25", 1))
			env.expect(splitAt, end, pt("2026-11-26", 1))
			env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-25", 1), pt("2026-11-26", 1)}, "")
			require.Equal(t, []time.Duration{configured, 26 * time.Hour}, env.ttls())
		})
	}
}

// The closed days are a key's multi-day aggregate and scan under 28 seconds; today is a small read and keeps the
// usual 15.
func TestKeyDailyTrendSplitScansUnderTheBudgetOfItsPart(t *testing.T) {
	env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
	recorder := &deadlineExecutor{sqlExecutor: env.repo.sql}
	env.repo.sql = recorder
	start, end := dailyWindow(t, env.now, 15, "UTC")
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	env.expect(start, splitAt, pt("2026-11-25", 1))
	env.expect(splitAt, end, pt("2026-11-26", 1))
	env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-25", 1), pt("2026-11-26", 1)}, "")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Len(t, recorder.deadlines, 2, "one scan per part, the closed days first")
	require.WithinDuration(t, time.Now().Add(usageStatsKeyWindowLoadTimeout), recorder.deadlines[0], time.Second)
	require.WithinDuration(t, time.Now().Add(usageStatsCacheLoadTimeout), recorder.deadlines[1], time.Second)
	require.Equal(t, 28*time.Second, usageStatsKeyWindowLoadTimeout)
	require.Equal(t, 15*time.Second, usageStatsCacheLoadTimeout)
}

// A part that ran out of time is remembered as long as its class: five minutes for the closed days (28 seconds of
// scan that did not fit will not fit again half a minute later), 30 seconds for today.
func TestKeyDailyTrendSplitRemembersATimeoutAsLongAsItsPart(t *testing.T) {
	timeout := &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}
	for _, tc := range []struct {
		name         string
		closedFailed bool
		want         time.Duration
	}{
		{"the closed days time out", true, 5 * time.Minute},
		{"today times out", false, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
			start, end := dailyWindow(t, env.now, 15, "UTC")
			splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
			if tc.closedFailed {
				env.expectError(start, splitAt, timeout)
			} else {
				env.expect(start, splitAt, pt("2026-11-25", 1))
				env.expectError(splitAt, end, timeout)
			}
			_, err := env.ask(start, end)
			require.ErrorIs(t, err, timeout)

			items := env.repo.statsCache.failures.Items()
			require.Len(t, items, 1)
			for _, item := range items {
				remaining := time.Until(time.Unix(0, item.Expiration))
				require.Greater(t, remaining, tc.want-2*time.Second)
				require.LessOrEqual(t, remaining, tc.want)
			}
			// Remembered, so the database is not asked again: no expectation is left to answer it.
			_, err = env.ask(start, end)
			require.ErrorIs(t, err, timeout)
			require.NoError(t, env.mock.ExpectationsWereMet())
		})
	}
}

// Either part failing fails the whole call, with no half of a trend. A failed today does not cost the closed
// days: they stay cached, and the next call asks for today only.
func TestKeyDailyTrendSplitFailureOfEitherPartFailsTheCall(t *testing.T) {
	// A statement somebody stopped is asked again at once (it is not remembered), which keeps the test fast.
	stopped := &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	start, end := dailyWindow(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), 15, "UTC")
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	t.Run("today fails", func(t *testing.T) {
		env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
		env.expect(start, splitAt, pt("2026-11-25", 20))
		env.expectError(splitAt, end, stopped)
		got, err := env.ask(start, end)
		require.ErrorIs(t, err, stopped)
		require.Nil(t, got, "the closed days alone are not an answer")
		require.Equal(t, []time.Duration{26 * time.Hour}, env.ttls(), "the closed days stay cached, nothing else is")

		env.expect(splitAt, end, pt("2026-11-26", 3)) // only today is asked again
		env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-25", 20), pt("2026-11-26", 3)}, "the next call")
		require.NoError(t, env.mock.ExpectationsWereMet())
	})

	t.Run("the closed days fail", func(t *testing.T) {
		env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
		env.expectError(start, splitAt, stopped) // no expectation for today: asking for it would fail the call another way
		got, err := env.ask(start, end)
		require.ErrorIs(t, err, stopped)
		require.Nil(t, got)
		require.Empty(t, env.server.Keys())
		require.NoError(t, env.mock.ExpectationsWereMet(), "today is not read for a call that has already failed")
	})
}

// The window and the database need not agree on where a day begins: the window is cut at the user's midnight, the
// day labels by the database session's zone. A label can then be found in both parts, and its two rows are one day:
// they are added, never listed twice. Here the window is Shanghai's and the labels are UTC's, so the cut falls at
// 16:00 UTC and 11-25 is in both parts.
func TestKeyDailyTrendSplitAddsALabelFoundInBothParts(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, shanghai), time.Minute)
	start, end := dailyWindow(t, env.now, 15, "Asia/Shanghai")
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	require.Equal(t, 16, splitAt.UTC().Hour())

	env.expect(start, splitAt, pt("2026-11-24", 10), pt("2026-11-25", 20))
	env.expect(splitAt, end, pt("2026-11-25", 5), pt("2026-11-26", 3))
	env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-24", 10), pt("2026-11-25", 25), pt("2026-11-26", 3)}, "")
}

// A part with no rows is an empty list, and so is a call whose parts are both empty: never nil, as the query's own
// answer is not.
func TestKeyDailyTrendSplitEmptyPartsGiveAnEmptyTrend(t *testing.T) {
	env := newSplitTrendEnv(t, time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Minute)
	start, end := dailyWindow(t, env.now, 15, "UTC")
	splitAt := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)

	env.expect(start, splitAt)
	env.expect(splitAt, end, pt("2026-11-26", 2))
	env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-26", 2)}, "nothing before today")

	env2 := newSplitTrendEnv(t, env.now, time.Minute)
	env2.expect(start, splitAt, pt("2026-11-20", 4))
	env2.expect(splitAt, end)
	env2.requireAsked(start, end, []TrendDataPoint{pt("2026-11-20", 4)}, "nothing today")

	env3 := newSplitTrendEnv(t, env.now, time.Minute)
	env3.expect(start, splitAt)
	env3.expect(splitAt, end)
	env3.requireAsked(start, end, []TrendDataPoint{}, "nothing at all")
}

// The cut is today's midnight in the window's own zone. On the day of a DST change that is not end-24h: the day has
// 23 or 25 hours (23.5 on Lord Howe Island, whose clocks move by half an hour), and a cut an hour off would put a
// closed hour in today, or today's first hour in the closed days that are kept for a day.
func TestKeyDailyTrendSplitCutsAtLocalMidnightNotAtMinus24Hours(t *testing.T) {
	for _, tc := range []struct {
		name string
		zone string
		now  [3]int // year, month, day; the clock says noon
		dst  bool   // the day is not 24 hours long
	}{
		{"Los Angeles, 25 hour day (clocks back)", "America/Los_Angeles", [3]int{2026, 11, 1}, true},
		{"Los Angeles, 23 hour day (clocks forward)", "America/Los_Angeles", [3]int{2026, 3, 8}, true},
		{"London, 23 hour day", "Europe/London", [3]int{2026, 3, 29}, true},
		{"London, 25 hour day", "Europe/London", [3]int{2026, 10, 25}, true},
		{"Sydney, 23 hour day", "Australia/Sydney", [3]int{2026, 10, 4}, true},
		{"Lord Howe Island, half an hour shift", "Australia/Lord_Howe", [3]int{2026, 10, 4}, true},
		{"Los Angeles, an ordinary day", "America/Los_Angeles", [3]int{2026, 11, 26}, false},
		{"Kolkata, UTC+5:30", "Asia/Kolkata", [3]int{2026, 11, 26}, false},
		{"Kathmandu, UTC+5:45", "Asia/Kathmandu", [3]int{2026, 11, 26}, false},
		{"Shanghai", "Asia/Shanghai", [3]int{2026, 11, 26}, false},
		{"UTC", "UTC", [3]int{2026, 11, 26}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc := loadZone(t, tc.zone)
			y, m, d := tc.now[0], time.Month(tc.now[1]), tc.now[2]
			env := newSplitTrendEnv(t, time.Date(y, m, d, 12, 0, 0, 0, loc), time.Minute)
			start, end := dailyWindow(t, env.now, 15, tc.zone)
			splitAt := time.Date(y, m, d, 0, 0, 0, 0, loc)
			require.Equal(t, !tc.dst, end.Add(-24*time.Hour).Equal(splitAt), "the case must discriminate between local midnight and end-24h")

			env.expect(start, splitAt, pt("2026-01-01", 1))
			env.expect(splitAt, end, pt("2026-01-02", 2))
			env.requireAsked(start, end, []TrendDataPoint{pt("2026-01-01", 1), pt("2026-01-02", 2)}, "")
			require.NoError(t, env.mock.ExpectationsWereMet())
		})
	}
}

// The moment of the cut itself: a clock reading exactly local midnight, or one nanosecond before the next, is in
// today either way.
func TestKeyDailyTrendSplitCutAtTheEdgesOfTheDay(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	midnight := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	for _, tc := range []struct {
		name string
		now  time.Time
	}{
		{"exactly midnight", midnight},
		{"a nanosecond after midnight", midnight.Add(time.Nanosecond)},
		{"a nanosecond before the next midnight", midnight.AddDate(0, 0, 1).Add(-time.Nanosecond)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newSplitTrendEnv(t, tc.now, time.Minute)
			start, end := dailyWindow(t, tc.now, 15, "Asia/Shanghai")
			env.expect(start, midnight, pt("2026-11-25", 1))
			env.expect(midnight, end, pt("2026-11-26", 1))
			env.requireAsked(start, end, []TrendDataPoint{pt("2026-11-25", 1), pt("2026-11-26", 1)}, "")
		})
	}
}

// Windows with nothing to cut, and every other kind of question, are answered whole, exactly as before the split:
// one read, the TTL keyAggregateTTL gives the window.
func TestKeyDailyTrendSplitLeavesOtherWindowsWhole(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return time.Date(2026, 11, n, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name        string
		start, end  time.Time
		keyID       int64
		granularity string
		ttl         time.Duration
	}{
		{"the days are all over", day(1), day(20), splitTrendKey, "day", keyWindowTTL},
		{"the window ends where today begins", day(12), day(26), splitTrendKey, "day", keyWindowTTL},
		{"the window begins where today begins", day(26), day(28), splitTrendKey, "day", keyWindowTTL},
		{"the window begins after today", day(27), day(30), splitTrendKey, "day", keyWindowTTL},
		{"one natural day", day(26), day(27), splitTrendKey, "day", time.Minute},
		{"25 hours", day(26), day(26).Add(25 * time.Hour), splitTrendKey, "day", time.Minute},
		{"no key", day(12), day(27), 0, "day", time.Minute},
		{"hourly", day(12), day(27), splitTrendKey, "hour", keyWindowTTL},
		{"weekly", day(12), day(27), splitTrendKey, "week", keyWindowTTL},
		{"monthly", day(12), day(27), splitTrendKey, "month", keyWindowTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newSplitTrendEnv(t, now, time.Minute)
			args := []driver.Value{instant(tc.start), instant(tc.end), splitTrendUser}
			if tc.keyID > 0 {
				args = append(args, tc.keyID)
			}
			env.mock.ExpectQuery(keyWindowTrendSQL).WithArgs(args...).WillReturnRows(trendRows(pt("2026-11-26", 4)))
			got, err := env.repo.GetUsageTrendWithFilters(context.Background(), tc.start, tc.end, tc.granularity, splitTrendUser, tc.keyID, 0, 0, "", nil, nil, nil)
			require.NoError(t, err)
			require.Equal(t, []TrendDataPoint{pt("2026-11-26", 4)}, got)
			require.NoError(t, env.mock.ExpectationsWereMet(), "one read of the whole window")
			require.Equal(t, []time.Duration{tc.ttl}, env.ttls())
		})
	}

	// Without a cache two reads would only cost more than one.
	t.Run("no cache", func(t *testing.T) {
		db, mock := newSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.clock = func() time.Time { return now }
		mock.ExpectQuery(keyWindowTrendSQL).
			WithArgs(instant(day(12)), instant(day(27)), splitTrendUser, splitTrendKey).
			WillReturnRows(trendRows(pt("2026-11-26", 4)))
		got, err := repo.GetUsageTrendWithFilters(context.Background(), day(12), day(27), "day", splitTrendUser, splitTrendKey, 0, 0, "", nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []TrendDataPoint{pt("2026-11-26", 4)}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// Only the by-day trend is cut. The other aggregates of one key over the same window (the model report and the key
// summary) are read whole and keep the quarter hour: they are the same window, with today in it.
func TestKeyDailyTrendSplitDoesNotTouchTheOtherKeyAggregates(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	for _, agg := range keyWindowAggregates()[1:] { // model report, key summary
		t.Run(agg.name, func(t *testing.T) {
			repo, mock, server := newKeyWindowRepo(t, time.Minute)
			repo.clock = func() time.Time { return now }
			agg.expect(mock, 5)
			got, err := agg.ask(repo, splitTrendUser, splitTrendKey, start, end)
			require.NoError(t, err)
			require.EqualValues(t, 5, got)
			require.NoError(t, mock.ExpectationsWereMet(), "one read of the whole window")
			keys := server.Keys()
			require.Len(t, keys, 1)
			require.Equal(t, keyWindowTTL, server.TTL(keys[0]))
		})
	}
}

func TestMergeTrendPointsByDate(t *testing.T) {
	a := []TrendDataPoint{pt("2026-11-24", 1), pt("2026-11-25", 2)}
	b := []TrendDataPoint{pt("2026-11-25", 4), pt("2026-11-26", 8)}
	aCopy, bCopy := slices.Clone(a), slices.Clone(b)

	require.Equal(t, []TrendDataPoint{pt("2026-11-24", 1), pt("2026-11-25", 6), pt("2026-11-26", 8)}, mergeTrendPointsByDate(a, b))
	require.Equal(t, aCopy, a, "the parts are not changed")
	require.Equal(t, bCopy, b, "the parts are not changed")

	require.Equal(t, []TrendDataPoint{pt("2026-11-24", 1), pt("2026-11-25", 2)}, mergeTrendPointsByDate(a, nil))
	require.Equal(t, []TrendDataPoint{pt("2026-11-24", 1), pt("2026-11-25", 2)}, mergeTrendPointsByDate(nil, a))
	require.Equal(t, []TrendDataPoint{pt("2026-11-25", 8)}, mergeTrendPointsByDate([]TrendDataPoint{pt("2026-11-25", 8)}, []TrendDataPoint{}))
	// The order is the label's, whatever the order the parts come in.
	require.Equal(t, []TrendDataPoint{pt("2026-11-24", 1), pt("2026-11-25", 6), pt("2026-11-26", 8)}, mergeTrendPointsByDate(b, a))

	empty := mergeTrendPointsByDate(nil, nil)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}

// Every figure of a trend point is added. A field that is not a sum (an average, a rate) would be wrong to add: the
// test fails for any field kind it does not know, so that whoever adds one decides how the parts of a trend merge it.
func TestMergeTrendPointsByDateAddsEveryFigure(t *testing.T) {
	typ := reflect.TypeFor[TrendDataPoint]()
	var a, b TrendDataPoint
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	for i := range typ.NumField() {
		field := typ.Field(i)
		switch va.Field(i).Kind() {
		case reflect.String:
			require.Equal(t, "Date", field.Name, "a text field other than the label")
			va.Field(i).SetString("2026-11-25")
			vb.Field(i).SetString("2026-11-25")
		case reflect.Int64:
			va.Field(i).SetInt(int64(i + 1))
			vb.Field(i).SetInt(int64(1000 * (i + 1)))
		case reflect.Float64:
			va.Field(i).SetFloat(float64(i+1) + 0.25)
			vb.Field(i).SetFloat(float64(1000*(i+1)) + 0.5)
		default:
			t.Fatalf("TrendDataPoint.%s is a %s: decide how the two parts of a split trend merge it, in mergeTrendPointsByDate and here", field.Name, va.Field(i).Kind())
		}
	}
	merged := mergeTrendPointsByDate([]TrendDataPoint{a}, []TrendDataPoint{b})
	require.Len(t, merged, 1)
	got := reflect.ValueOf(merged[0])
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		switch got.Field(i).Kind() {
		case reflect.String:
			require.Equal(t, "2026-11-25", got.Field(i).String(), name)
		case reflect.Int64:
			require.Equal(t, va.Field(i).Int()+vb.Field(i).Int(), got.Field(i).Int(), name)
		case reflect.Float64:
			require.Equal(t, va.Field(i).Float()+vb.Field(i).Float(), got.Field(i).Float(), name)
		}
	}
}
