package repository

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// One API key's by-model report and key summary over a window that still holds today are read in parts
// (keyWindowParts): the head, the part of the first day before the first local midnight (kept a quarter hour); the
// whole days that are over (kept for 26 hours); and today (kept for the cache's own TTL). These tests pin what is asked
// of the database and when, with sqlmock and miniredis; the answer itself, against a real PostgreSQL, is in
// usage_key_window_split_pg_test.go. The helpers of the by-day trend (newSplitTrendEnv, loadZone, instant,
// deadlineExecutor) are in usage_key_daily_split_test.go and the files it names.

// The three model sources group by different expressions; each statement is told apart by the one it ends with.
var keyModelsSQL = map[string]string{
	usagestats.ModelSourceRequested: `(?s)FROM usage_logs.*GROUP BY COALESCE\(NULLIF\(TRIM\(requested_model\), ''\), model\)`,
	usagestats.ModelSourceUpstream:  `(?s)FROM usage_logs.*GROUP BY COALESCE\(NULLIF\(TRIM\(upstream_model\), ''\), model\)`,
	usagestats.ModelSourceMapping: `(?s)FROM usage_logs.*GROUP BY \(COALESCE\(NULLIF\(TRIM\(requested_model\), ''\), model\)` +
		` \|\| ' -> ' \|\| COALESCE\(NULLIF\(TRIM\(upstream_model\), ''\), model\)\)`,
}

// ms is a model row whose figures all follow from its request count (and are exact in binary), so the sum of two rows
// of one model is the row of the summed count.
func ms(model string, requests int64) ModelStat {
	return ModelStat{
		Model: model, Requests: requests, InputTokens: 10 * requests, OutputTokens: 20 * requests,
		CacheCreationTokens: requests, CacheReadTokens: 2 * requests, TotalTokens: 33 * requests,
		Cost: 0.5 * float64(requests), ActualCost: 0.25 * float64(requests), AccountCost: 0.125 * float64(requests),
	}
}

func modelRows(stats ...ModelStat) *sqlmock.Rows {
	rows := sqlmock.NewRows(keyWindowModelCols)
	for _, m := range stats {
		rows.AddRow(m.Model, m.Requests, m.InputTokens, m.OutputTokens, m.CacheCreationTokens, m.CacheReadTokens, m.TotalTokens, m.Cost, m.ActualCost, m.AccountCost)
	}
	return rows
}

// splitModelEnv is the environment of the by-day trend tests, asked for the model report of the same key (no owner:
// /v1/usage asks without one).
type splitModelEnv struct {
	*splitTrendEnv
	source string
}

func newSplitModelEnv(t *testing.T, now time.Time, cacheTTL time.Duration) *splitModelEnv {
	t.Helper()
	return &splitModelEnv{splitTrendEnv: newSplitTrendEnv(t, now, cacheTTL), source: usagestats.ModelSourceRequested}
}

// expectModels registers one read of the key's model report over [start, end), answered with the given rows.
func (e *splitModelEnv) expectModels(start, end time.Time, rows ...ModelStat) {
	e.mock.ExpectQuery(keyModelsSQL[e.source]).WithArgs(instant(start), instant(end), splitTrendKey).WillReturnRows(modelRows(rows...))
}

func (e *splitModelEnv) expectModelsError(start, end time.Time, err error) {
	e.mock.ExpectQuery(keyModelsSQL[e.source]).WithArgs(instant(start), instant(end), splitTrendKey).WillReturnError(err)
}

func (e *splitModelEnv) askModels(start, end time.Time) ([]ModelStat, error) {
	return e.repo.GetModelStatsWithFiltersBySource(context.Background(), start, end, 0, splitTrendKey, 0, 0, nil, nil, nil, e.source)
}

func (e *splitModelEnv) requireModels(start, end time.Time, want []ModelStat, msg string) {
	e.t.Helper()
	got, err := e.askModels(start, end)
	require.NoError(e.t, err, msg)
	require.Equal(e.t, want, got, msg)
}

// span writes one part of a window the way spans does, for a part that is not the whole window.
func span(start, end time.Time, ttl time.Duration) string {
	return fmt.Sprintf("%s .. %s ttl %v", start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), ttl)
}

func spans(parts []keyWindowPart) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = span(p.start, p.end, p.ttl)
		if p.whole {
			out[i] += " whole"
		}
	}
	return out
}

func partsRepo(now time.Time) *usageLogRepository {
	return &usageLogRepository{statsCache: &usageStatsCache{}, clock: func() time.Time { return now }}
}

// The decision, for the windows the callers send: where the window is cut, what each part is kept for, and what is
// left whole.
func TestKeyWindowPartsCutsTheWindowsTheCallersSend(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	at := func(month time.Month, day, hour, minute, second int) time.Time {
		return time.Date(2026, month, day, hour, minute, second, 0, shanghai)
	}
	now := at(11, 26, 10, 20, 0)
	today := at(11, 26, 0, 0, 0)
	cases := []struct {
		name       string
		start, end time.Time
		keyID      int64
		want       []string // nil: read whole
	}{
		{
			// /v1/usage: 30 days that move in 15 minute steps on the key's own grid, so the start is never a midnight.
			name: "the default window of /v1/usage", start: at(10, 27, 10, 19, 53), end: at(11, 26, 10, 19, 53), keyID: 7,
			want: []string{
				span(at(10, 27, 10, 19, 53), at(10, 28, 0, 0, 0), usageStatsKeyWindowTTL),
				span(at(10, 28, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, at(11, 26, 10, 19, 53), 0),
			},
		},
		{
			// The usage pages and the key panels: midnight to midnight, so there is no head.
			name: "days from midnight to the end of today", start: at(11, 12, 0, 0, 0), end: at(11, 27, 0, 0, 0), keyID: 7,
			want: []string{
				span(at(11, 12, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, at(11, 27, 0, 0, 0), 0),
			},
		},
		{
			name: "a start a nanosecond past a midnight", start: at(11, 12, 0, 0, 0).Add(time.Nanosecond), end: at(11, 27, 0, 0, 0), keyID: 7,
			want: []string{
				span(at(11, 12, 0, 0, 0).Add(time.Nanosecond), at(11, 13, 0, 0, 0), usageStatsKeyWindowTTL),
				span(at(11, 13, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, at(11, 27, 0, 0, 0), 0),
			},
		},
		{
			name: "a start a nanosecond before a midnight", start: at(11, 12, 0, 0, 0).Add(-time.Nanosecond), end: at(11, 27, 0, 0, 0), keyID: 7,
			want: []string{
				span(at(11, 12, 0, 0, 0).Add(-time.Nanosecond), at(11, 12, 0, 0, 0), usageStatsKeyWindowTTL),
				span(at(11, 12, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, at(11, 27, 0, 0, 0), 0),
			},
		},
		{
			name: "one whole day is over", start: at(11, 25, 0, 0, 0), end: at(11, 27, 0, 0, 0), keyID: 7,
			want: []string{
				span(at(11, 25, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, at(11, 27, 0, 0, 0), 0),
			},
		},
		{
			name: "a window that ends now, like a rolling preset", start: at(11, 19, 10, 20, 0), end: now, keyID: 7,
			want: []string{
				span(at(11, 19, 10, 20, 0), at(11, 20, 0, 0, 0), usageStatsKeyWindowTTL),
				span(at(11, 20, 0, 0, 0), today, usageStatsKeyClosedDaysTTL),
				span(today, now, 0),
			},
		},
		// Read whole, as before.
		{name: "no whole day is over: it starts yesterday", start: at(11, 25, 15, 0, 0), end: at(11, 27, 9, 0, 0), keyID: 7},
		{name: "no whole day is over: it starts a second after yesterday's midnight", start: at(11, 25, 0, 0, 1), end: at(11, 27, 9, 0, 0), keyID: 7},
		{name: "the window ends where today begins", start: at(11, 12, 0, 0, 0), end: today, keyID: 7},
		{name: "the window ends before today", start: at(11, 1, 0, 0, 0), end: at(11, 20, 0, 0, 0), keyID: 7},
		{name: "the window begins where today begins", start: today, end: at(11, 28, 0, 0, 0), keyID: 7},
		{name: "the window begins after today", start: at(11, 27, 0, 0, 0), end: at(11, 30, 0, 0, 0), keyID: 7},
		{name: "one natural day", start: today, end: at(11, 27, 0, 0, 0), keyID: 7},
		{name: "25 hours across midnight", start: at(11, 25, 15, 0, 0), end: at(11, 26, 16, 0, 0), keyID: 7},
		{name: "no key", start: at(11, 12, 0, 0, 0), end: at(11, 27, 0, 0, 0), keyID: 0},
	}
	repo := partsRepo(now)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := repo.keyWindowParts(tc.keyID, tc.start, tc.end)
			if tc.want == nil {
				require.Nil(t, parts)
				return
			}
			require.Equal(t, tc.want, spans(parts))
		})
	}

	t.Run("no cache", func(t *testing.T) {
		repo := partsRepo(now)
		repo.statsCache = nil
		require.Nil(t, repo.keyWindowParts(7, at(11, 12, 0, 0, 0), at(11, 27, 0, 0, 0)), "two reads would only cost more than one")
	})
}

// The cut must be the local midnight, whatever the day is like: the parts must cover the window exactly once, the
// cuts must be midnights, and a window is read whole exactly when no whole day is over in it. Checked over every
// zone with DST days, half hour zones included, against a count of civil days that does not use the code under test.
func TestKeyWindowPartsCoverTheWindowOnceAtLocalMidnights(t *testing.T) {
	type day struct {
		zone string
		date [3]int
	}
	days := []day{
		{"America/Los_Angeles", [3]int{2026, 3, 8}},  // 23 hours
		{"America/Los_Angeles", [3]int{2026, 11, 1}}, // 25 hours
		{"Europe/London", [3]int{2026, 3, 29}},
		{"Europe/London", [3]int{2026, 10, 25}},
		{"Australia/Sydney", [3]int{2026, 10, 4}},
		{"Australia/Lord_Howe", [3]int{2026, 10, 4}}, // half an hour
		{"Australia/Lord_Howe", [3]int{2026, 4, 5}},
		{"Asia/Kolkata", [3]int{2026, 11, 26}},
		{"Asia/Kathmandu", [3]int{2026, 11, 26}},
		{"Asia/Shanghai", [3]int{2026, 11, 26}},
		{"UTC", [3]int{2026, 11, 26}},
		// Midnight does not exist on this day: the clocks go from 00:00 to 01:00. A time of that hour does not either, so
		// only that the parts cover the window once is checked there, not where they are cut.
		{"America/Sao_Paulo", [3]int{2018, 11, 4}},
	}
	civil := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	checked, cut := 0, 0
	for _, d := range days {
		loc := loadZone(t, d.zone)
		y, m, dd := d.date[0], time.Month(d.date[1]), d.date[2]
		skipped := d.zone == "America/Sao_Paulo"
		for _, hour := range []int{0, 1, 12, 23} {
			now := time.Date(y, m, dd, hour, 30, 0, 0, loc)
			todayStart := time.Date(y, m, dd, 0, 0, 0, 0, loc)
			repo := partsRepo(now)
			for back := 0; back <= 40; back++ {
				midnight := time.Date(y, m, dd-back, 0, 0, 0, 0, loc)
				for _, shift := range []time.Duration{0, time.Nanosecond, -time.Nanosecond, 7*time.Hour + 13*time.Minute, 15*time.Hour + 45*time.Minute} {
					start := midnight.Add(shift)
					for _, end := range []time.Time{
						time.Date(y, m, dd+1, 0, 0, 0, 0, loc), now, now.Add(-17*time.Minute - 13*time.Second),
						time.Date(y, m, dd, 0, 0, 0, 0, loc).Add(25*time.Hour + time.Nanosecond), todayStart,
					} {
						checked++
						parts := repo.keyWindowParts(7, start, end)
						interior := todayStart.After(start) && todayStart.Before(end)
						long := end.Sub(start) > 25*time.Hour
						if !skipped {
							// Whole local days over: the civil days from the first midnight at or after start to today.
							startDay, todayDay := start.In(loc), now.In(loc)
							over := int(civil(todayDay.Year(), todayDay.Month(), todayDay.Day()).Sub(civil(startDay.Year(), startDay.Month(), startDay.Day())).Hours() / 24)
							if !start.Equal(time.Date(startDay.Year(), startDay.Month(), startDay.Day(), 0, 0, 0, 0, loc)) {
								over--
							}
							require.Equal(t, long && interior && over >= 1, parts != nil, "%s %v: window [%v, %v) at %v, %d whole days over", d.zone, d.date, start, end, now, over)
						}
						if parts == nil {
							continue
						}
						cut++
						require.GreaterOrEqual(t, len(parts), 2)
						require.LessOrEqual(t, len(parts), 3)
						require.True(t, parts[0].start.Equal(start), "the parts start where the window does")
						require.True(t, parts[len(parts)-1].end.Equal(end), "and end where it ends")
						for i, p := range parts {
							require.True(t, p.start.Before(p.end), "%s %v part %d: %v is empty", d.zone, d.date, i, spans(parts))
							if i > 0 {
								require.True(t, parts[i-1].end.Equal(p.start), "%s %v: the parts are one after the other, none twice: %v", d.zone, d.date, spans(parts))
							}
						}
						closed, last := parts[len(parts)-2], parts[len(parts)-1]
						require.Equal(t, usageStatsKeyClosedDaysTTL, closed.ttl)
						require.Zero(t, last.ttl)
						if len(parts) == 3 {
							require.Equal(t, usageStatsKeyWindowTTL, parts[0].ttl)
							require.Less(t, parts[0].end.Sub(parts[0].start), 25*time.Hour+time.Nanosecond, "a head is less than a day")
						}
						if !skipped {
							require.True(t, last.start.Equal(todayStart), "today starts at the local midnight, not at end-24h: %v", spans(parts))
							require.True(t, closed.start.Equal(time.Date(closed.start.Year(), closed.start.Month(), closed.start.Day(), 0, 0, 0, 0, loc)), "the closed days start at a midnight: %v", spans(parts))
						}
					}
				}
			}
		}
	}
	require.Greater(t, cut, checked/4, "too few windows were cut for the check to mean anything")
}

func TestKeyModelsSplitReadsThePartsOfTheDefaultWindowOnce(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	start, end := time.Date(2026, 10, 27, 10, 19, 53, 0, shanghai), time.Date(2026, 11, 26, 10, 19, 53, 0, shanghai)
	first, today := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)

	env.expectModels(start, first, ms("claude-opus", 1))
	env.expectModels(first, today, ms("claude-opus", 100), ms("gpt-5", 50))
	env.expectModels(today, end, ms("claude-opus", 3), ms("gemini", 7))
	env.requireModels(start, end, []ModelStat{ms("claude-opus", 104), ms("gpt-5", 50), ms("gemini", 7)}, "three reads, added up by model")
	require.NoError(t, env.mock.ExpectationsWereMet())
	require.Equal(t, []time.Duration{time.Minute, usageStatsKeyWindowTTL, usageStatsKeyClosedDaysTTL}, env.ttls(), "today keeps the cache's TTL, the head a quarter hour, the closed days a day")

	// Nothing is asked inside the minute: no expectation is registered, a query would fail the call.
	env.server.FastForward(59 * time.Second)
	env.requireModels(start, end, []ModelStat{ms("claude-opus", 104), ms("gpt-5", 50), ms("gemini", 7)}, "59 s later")
}

// The point of cutting at midnights at both ends, over time. The default window of /v1/usage moves in 15 minute steps,
// so the part that costs, the whole days that are over, must not be asked a new question at each step: it is read
// once for the day, and each step reads the head of its own start and today. A cut at the window's own start would read the whole
// month again at every step, and this fails on the first of them.
func TestKeyModelsSplitKeepsTheClosedDaysWhileTheWindowMovesInSteps(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	phase := 4*time.Minute + 53*time.Second // the grid of key 7 in the handler (usageKeyPhase)
	floor := func(at time.Time) time.Time { return at.Add(-phase).Truncate(15 * time.Minute).Add(phase) }
	first := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	base := time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai)
	env := newSplitModelEnv(t, base, time.Minute)
	closed := []ModelStat{ms("claude-opus", 100), ms("gpt-5", 50)}

	for cell := range 8 { // two hours
		env.now = base.Add(time.Duration(cell) * 15 * time.Minute)
		end := floor(env.now)
		start := floor(end.AddDate(0, 0, -30))
		require.True(t, start.After(time.Date(2026, 10, 27, 0, 0, 0, 0, shanghai)) && start.Before(first), "the start moves inside one day: %v", start)

		env.expectModels(start, first, ms("claude-opus", int64(cell)))
		if cell == 0 {
			env.expectModels(first, today, closed...)
		}
		env.expectModels(today, end, ms("claude-opus", int64(10+cell)))
		want := []ModelStat{ms("claude-opus", 100+int64(cell)+int64(10+cell)), ms("gpt-5", 50)}
		env.requireModels(start, end, want, "the first poll of the step")

		// A minute and a bit later the same step is asked again: today's entry is over, the head's is not.
		env.server.FastForward(61 * time.Second)
		env.expectModels(today, end, ms("claude-opus", int64(10+cell)))
		env.requireModels(start, end, want, "the same step, after today's TTL")
		env.server.FastForward(15*time.Minute - 61*time.Second)
	}
	require.NoError(t, env.mock.ExpectationsWereMet(), "the closed days were read once in two hours")
}

// A part is asked as the cache's own class says: the closed days and the head are a key's multi-day aggregate and
// scan under 28 seconds, today is a small read and keeps the usual 15.
func TestKeyModelsSplitScansUnderTheBudgetOfItsPart(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	recorder := &deadlineExecutor{sqlExecutor: env.repo.sql}
	env.repo.sql = recorder
	start, end := time.Date(2026, 10, 27, 10, 19, 53, 0, shanghai), time.Date(2026, 11, 26, 10, 19, 53, 0, shanghai)
	first, today := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env.expectModels(start, first, ms("a", 1))
	env.expectModels(first, today, ms("a", 1))
	env.expectModels(today, end, ms("a", 1))
	env.requireModels(start, end, []ModelStat{ms("a", 3)}, "")

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	require.Len(t, recorder.deadlines, 3, "one scan per part, oldest first")
	require.WithinDuration(t, time.Now().Add(usageStatsKeyWindowLoadTimeout), recorder.deadlines[0], time.Second)
	require.WithinDuration(t, time.Now().Add(usageStatsKeyWindowLoadTimeout), recorder.deadlines[1], time.Second)
	require.WithinDuration(t, time.Now().Add(usageStatsCacheLoadTimeout), recorder.deadlines[2], time.Second)
}

// A part that ran out of time is remembered as long as its class: five minutes for the head and the closed days (a
// scan that did not fit 28 seconds will not fit again half a minute later), 30 seconds for today.
func TestKeyModelsSplitRemembersATimeoutAsLongAsItsPart(t *testing.T) {
	timeout := &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 10, 27, 10, 19, 53, 0, shanghai), time.Date(2026, 11, 26, 10, 19, 53, 0, shanghai)
	first, today := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	for _, tc := range []struct {
		name   string
		failed int // 0 head, 1 closed days, 2 today
		want   time.Duration
	}{
		{"the head times out", 0, 5 * time.Minute},
		{"the closed days time out", 1, 5 * time.Minute},
		{"today times out", 2, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
			windows := [][2]time.Time{{start, first}, {first, today}, {today, end}}
			for i, w := range windows[:tc.failed+1] {
				if i == tc.failed {
					env.expectModelsError(w[0], w[1], timeout)
				} else {
					env.expectModels(w[0], w[1], ms("a", 1))
				}
			}
			_, err := env.askModels(start, end)
			require.ErrorIs(t, err, timeout)

			items := env.repo.statsCache.failures.Items()
			require.Len(t, items, 1)
			for _, item := range items {
				remaining := time.Until(time.Unix(0, item.Expiration))
				require.Greater(t, remaining, tc.want-2*time.Second)
				require.LessOrEqual(t, remaining, tc.want)
			}
			// Remembered, so the database is not asked again for the part that failed.
			_, err = env.askModels(start, end)
			require.ErrorIs(t, err, timeout)
			require.NoError(t, env.mock.ExpectationsWereMet())
		})
	}
}

// Any part failing fails the whole call, with no half of a report; what was read stays cached, and the next call asks
// for what is missing only.
func TestKeyModelsSplitFailureOfAnyPartFailsTheCall(t *testing.T) {
	// A statement somebody stopped is asked again at once (it is not remembered), which keeps the test fast.
	stopped := &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 10, 27, 10, 19, 53, 0, shanghai), time.Date(2026, 11, 26, 10, 19, 53, 0, shanghai)
	first, today := time.Date(2026, 10, 28, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	newEnv := func(t *testing.T) *splitModelEnv {
		return newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	}

	t.Run("today fails", func(t *testing.T) {
		env := newEnv(t)
		env.expectModels(start, first, ms("a", 1))
		env.expectModels(first, today, ms("a", 20))
		env.expectModelsError(today, end, stopped)
		got, err := env.askModels(start, end)
		require.ErrorIs(t, err, stopped)
		require.Nil(t, got, "the days that are over alone are not a report")
		require.Equal(t, []time.Duration{usageStatsKeyWindowTTL, usageStatsKeyClosedDaysTTL}, env.ttls(), "what was read stays cached, nothing else is")

		env.expectModels(today, end, ms("a", 3)) // only today is asked again
		env.requireModels(start, end, []ModelStat{ms("a", 24)}, "the next call")
		require.NoError(t, env.mock.ExpectationsWereMet())
	})
	t.Run("the closed days fail", func(t *testing.T) {
		env := newEnv(t)
		env.expectModels(start, first, ms("a", 1))
		env.expectModelsError(first, today, stopped) // no expectation for today: asking for it would fail the call another way
		got, err := env.askModels(start, end)
		require.ErrorIs(t, err, stopped)
		require.Nil(t, got)
		require.NoError(t, env.mock.ExpectationsWereMet(), "today is not read for a call that has already failed")
	})
	t.Run("the head fails", func(t *testing.T) {
		env := newEnv(t)
		env.expectModelsError(start, first, stopped)
		got, err := env.askModels(start, end)
		require.ErrorIs(t, err, stopped)
		require.Nil(t, got)
		require.Empty(t, env.server.Keys())
		require.NoError(t, env.mock.ExpectationsWereMet())
	})
}

// Whatever the model source, the parts are the same windows, and each source has entries of its own: asked for the
// upstream model, a part is never answered with the requested model's, whichever of the two was read first.
func TestKeyModelsSplitCoversEveryModelSource(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)

	for i, source := range []string{usagestats.ModelSourceRequested, usagestats.ModelSourceUpstream, usagestats.ModelSourceMapping} {
		env.source = source
		name := "model-" + source
		env.expectModels(start, today, ms(name, int64(10+i)))
		env.expectModels(today, end, ms(name, 1))
		env.requireModels(start, end, []ModelStat{ms(name, int64(11+i))}, source)
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
	require.Len(t, env.server.Keys(), 6, "two parts for each of the three sources")

	// Again, from the cache and in another order: each source finds its own answer.
	for i, source := range []string{usagestats.ModelSourceMapping, usagestats.ModelSourceRequested, usagestats.ModelSourceUpstream} {
		env.source = source
		name := "model-" + source
		want := map[string]int64{usagestats.ModelSourceRequested: 11, usagestats.ModelSourceUpstream: 12, usagestats.ModelSourceMapping: 13}[source]
		env.requireModels(start, end, []ModelStat{ms(name, want)}, fmt.Sprintf("%s, again (%d)", source, i))
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// Everything that is not one API key's multi-day window holding today is read whole, as before, with the TTL
// keyAggregateTTL gives it; so is every window when there is no cache.
func TestKeyModelsSplitLeavesOtherReadsWhole(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return time.Date(2026, 11, n, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		name       string
		start, end time.Time
		keyID      int64
		ttl        time.Duration
	}{
		{"the days are all over", day(1), day(20), splitTrendKey, keyWindowTTL},
		{"the window ends where today begins", day(12), day(26), splitTrendKey, keyWindowTTL},
		{"the window begins where today begins", day(26), day(28), splitTrendKey, keyWindowTTL},
		{"no whole day is over", day(25).Add(15 * time.Hour), day(27).Add(9 * time.Hour), splitTrendKey, keyWindowTTL},
		{"one natural day", day(26), day(27), splitTrendKey, time.Minute},
		{"25 hours", day(26), day(26).Add(25 * time.Hour), splitTrendKey, time.Minute},
		{"no key", day(12), day(27), 0, time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newSplitModelEnv(t, now, time.Minute)
			args := []driver.Value{instant(tc.start), instant(tc.end)}
			if tc.keyID > 0 {
				args = append(args, tc.keyID)
			}
			env.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).WithArgs(args...).WillReturnRows(modelRows(ms("a", 4)))
			got, err := env.repo.GetModelStatsWithFilters(context.Background(), tc.start, tc.end, 0, tc.keyID, 0, 0, nil, nil, nil)
			require.NoError(t, err)
			require.Equal(t, []ModelStat{ms("a", 4)}, got)
			require.NoError(t, env.mock.ExpectationsWereMet(), "one read of the whole window")
			require.Equal(t, []time.Duration{tc.ttl}, env.ttls())
		})
	}

	// Without a cache two reads would only cost more than one.
	t.Run("no cache", func(t *testing.T) {
		db, mock := newSQLMock(t)
		repo := newUsageLogRepositoryWithSQL(nil, db)
		repo.clock = func() time.Time { return now }
		mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).
			WithArgs(instant(day(12)), instant(day(27)), splitTrendKey).WillReturnRows(modelRows(ms("a", 4)))
		got, err := repo.GetModelStatsWithFilters(context.Background(), day(12), day(27), 0, splitTrendKey, 0, 0, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []ModelStat{ms("a", 4)}, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// With the rollup reads on, every part still goes the way it did: the rollups are asked first and, having no key
// dimension, answer "not covered" before they ask the database anything, so each part is read from usage_logs and
// nothing else is.
func TestKeyModelsSplitStillGoesThroughTheRollupReadPath(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	probe := &keyDailyProbe{inner: env.repo.sql}
	env.repo.sql = probe
	env.repo.rollupReadEnabled.Store(true)

	env.expectModels(start, today, ms("a", 10))
	env.expectModels(today, end, ms("a", 1))
	env.requireModels(start, end, []ModelStat{ms("a", 11)}, "")
	require.NoError(t, env.mock.ExpectationsWereMet())
	require.Len(t, probe.calls, 2, "one statement per part, none for the rollups")
	for _, call := range probe.calls {
		require.Contains(t, call.query, "FROM usage_logs")
	}
}

// The per-account model totals drive rate-limit admission and stay live: no cache, no parts, whatever the window.
// A window that holds today and is longer than a day, for an account without a key, is the case a split would hit.
func TestKeyModelsSplitNeverTouchesAccountTotals(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	env := newSplitModelEnv(t, now, time.Minute)
	for _, requests := range []int64{1, 2} { // live: the second call is a query again
		env.mock.ExpectQuery(`(?s)FROM usage_logs.*AND account_id = \$3`).
			WithArgs(instant(start), instant(end), int64(9)).WillReturnRows(modelRows(ms("a", requests)))
		got, err := env.repo.GetModelStatsWithFilters(context.Background(), start, end, 0, 0, 9, 0, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, []ModelStat{ms("a", requests)}, got)
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
	require.Empty(t, env.server.Keys(), "nothing is cached")
}

// A user's own model report (no key) and the report of one key are told apart, and a part of a key's report is never
// the user's.
func TestKeyModelsSplitPartsNeverCrossKeysOrUsers(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	ask := func(user, key int64) ([]ModelStat, error) {
		return env.repo.GetModelStatsWithFilters(context.Background(), start, end, user, key, 0, 0, nil, nil, nil)
	}
	expect := func(from, to time.Time, args []driver.Value, requests int64) {
		env.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).
			WithArgs(append([]driver.Value{instant(from), instant(to)}, args...)...).WillReturnRows(modelRows(ms("a", requests)))
	}
	// key 7 alone, key 9 alone, key 7 asked as the user 42's, and 42's own: four questions, two reads each, but the
	// last, which has no key and so is read whole.
	expect(start, today, []driver.Value{int64(7)}, 1)
	expect(today, end, []driver.Value{int64(7)}, 1)
	expect(start, today, []driver.Value{int64(9)}, 10)
	expect(today, end, []driver.Value{int64(9)}, 10)
	expect(start, today, []driver.Value{int64(42), int64(7)}, 100)
	expect(today, end, []driver.Value{int64(42), int64(7)}, 100)
	expect(start, end, []driver.Value{int64(42)}, 1000)
	for _, tc := range []struct {
		user, key int64
		want      int64
	}{{0, 7, 2}, {0, 9, 20}, {42, 7, 200}, {42, 0, 1000}} {
		got, err := ask(tc.user, tc.key)
		require.NoError(t, err)
		require.Equal(t, []ModelStat{ms("a", tc.want)}, got, "user %d key %d", tc.user, tc.key)
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
	for _, tc := range []struct {
		user, key int64
		want      int64
	}{{0, 7, 2}, {0, 9, 20}, {42, 7, 200}, {42, 0, 1000}} { // again, from the cache
		got, err := ask(tc.user, tc.key)
		require.NoError(t, err)
		require.Equal(t, []ModelStat{ms("a", tc.want)}, got, "user %d key %d, again", tc.user, tc.key)
	}
	require.NoError(t, env.mock.ExpectationsWereMet())
}

// The report of the usage pages: the shared filter shape, which declares its window in the filters as well. A window read
// whole keeps its old identity and the cache's own TTL. Cut, each part is known by its own window alone, so the
// whole days that are over are the same entry whatever the declared window, a rolling preset's end that moves
// every minute included.
func TestKeyModelsSplitCoversTheUsageFiltersVariant(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 11, 12, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	filters := func(declaredEnd time.Time) UsageLogFilters {
		s := start
		return UsageLogFilters{UserID: 42, APIKeyID: 7, ModelFilterSource: usagestats.ModelSourceRequested, StartTime: &s, EndTime: &declaredEnd, OmitAccountCost: true}
	}
	args := func(from, to time.Time) []driver.Value {
		return []driver.Value{instant(from), instant(to), int64(42), int64(7)}
	}
	ask := func(f UsageLogFilters, from, to time.Time) ([]ModelStat, error) {
		return env.repo.GetModelStatsWithUsageFiltersBySource(context.Background(), from, to, f, usagestats.ModelSourceRequested)
	}

	env.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).WithArgs(args(start, today)...).WillReturnRows(modelRows(ms("a", 10), ms("b", 5)))
	env.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).WithArgs(args(today, end)...).WillReturnRows(modelRows(ms("a", 1)))
	got, err := ask(filters(end), start, end)
	require.NoError(t, err)
	require.Equal(t, []ModelStat{ms("a", 11), ms("b", 5)}, got)
	require.Equal(t, []time.Duration{time.Minute, usageStatsKeyClosedDaysTTL}, env.ttls())

	// The same days that are over, asked with another declared window and another end: only today is read again.
	end2 := end.Add(-time.Hour)
	env.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).WithArgs(args(today, end2)...).WillReturnRows(modelRows(ms("a", 2)))
	got, err = ask(filters(end2), start, end2)
	require.NoError(t, err)
	require.Equal(t, []ModelStat{ms("a", 12), ms("b", 5)}, got)
	require.NoError(t, env.mock.ExpectationsWereMet())

	// Read whole (no key), the declared window is still part of the question, and the TTL is the cache's.
	env2 := newSplitModelEnv(t, env.now, time.Minute)
	ask2 := func(declaredEnd time.Time) {
		f := filters(declaredEnd)
		f.APIKeyID = 0
		_, err := env2.repo.GetModelStatsWithUsageFiltersBySource(context.Background(), start, end, f, usagestats.ModelSourceRequested)
		require.NoError(t, err)
	}
	for range 2 {
		env2.mock.ExpectQuery(keyModelsSQL[usagestats.ModelSourceRequested]).WithArgs(instant(start), instant(end), int64(42)).WillReturnRows(modelRows(ms("a", 1)))
	}
	ask2(end)
	ask2(end2)
	require.NoError(t, env2.mock.ExpectationsWereMet(), "another declared window is another question when the window is read whole, as before")
	require.Equal(t, []time.Duration{time.Minute, time.Minute}, env2.ttls())
}

func TestMergeModelStats(t *testing.T) {
	a := []ModelStat{ms("claude", 1), ms("gpt", 2)}
	b := []ModelStat{ms("gpt", 4), ms("gemini", 8)}
	aCopy, bCopy := slices.Clone(a), slices.Clone(b)

	// By tokens, most first; a model in both parts is added, a model in one is that row.
	require.Equal(t, []ModelStat{ms("gemini", 8), ms("gpt", 6), ms("claude", 1)}, mergeModelStats(a, b))
	require.Equal(t, []ModelStat{ms("gemini", 8), ms("gpt", 6), ms("claude", 1)}, mergeModelStats(b, a), "whatever the order of the parts")
	require.Equal(t, aCopy, a, "the parts are not changed")
	require.Equal(t, bCopy, b, "the parts are not changed")
	require.Equal(t, []ModelStat{ms("gpt", 2), ms("claude", 1)}, mergeModelStats(a, nil))
	require.Equal(t, []ModelStat{ms("gpt", 2), ms("claude", 1)}, mergeModelStats(nil, a, []ModelStat{}))
	require.Equal(t, []ModelStat{ms("gpt", 2), ms("claude", 1)}, mergeModelStats(a), "one part is itself, ordered")

	// Three parts, as with a head.
	require.Equal(t, []ModelStat{ms("gpt", 7), ms("claude", 3)}, mergeModelStats([]ModelStat{ms("claude", 1)}, []ModelStat{ms("gpt", 7)}, []ModelStat{ms("claude", 2)}))

	// A tie in tokens is broken by the model name, whatever the order of rows and parts.
	tie := []ModelStat{ms("b", 5), ms("c", 5), ms("a", 5), ms("d", 6)}
	require.Equal(t, []ModelStat{ms("d", 6), ms("a", 5), ms("b", 5), ms("c", 5)}, mergeModelStats(tie))
	require.Equal(t, []ModelStat{ms("d", 6), ms("a", 5), ms("b", 5), ms("c", 5)}, mergeModelStats(tie[:2], tie[2:]))
	// ... and by the sum, not by the row: two rows of 3 tokens-worth outrank one of 5.
	require.Equal(t, []ModelStat{ms("b", 6), ms("a", 5)}, mergeModelStats([]ModelStat{ms("a", 5), ms("b", 3)}, []ModelStat{ms("b", 3)}))

	empty := mergeModelStats(nil, nil)
	require.NotNil(t, empty)
	require.Empty(t, empty)
	require.NotNil(t, mergeModelStats())
}

// Every figure of a model row is added. A field that is not a sum (an average, a rate) would be wrong to add: the
// test fails for any field kind it does not know, so that whoever adds one decides how the parts of a report merge it.
func TestMergeModelStatsAddsEveryFigure(t *testing.T) {
	typ := reflect.TypeFor[ModelStat]()
	var a, b ModelStat
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	for i := range typ.NumField() {
		field := typ.Field(i)
		switch va.Field(i).Kind() {
		case reflect.String:
			require.Equal(t, "Model", field.Name, "a text field other than the name")
			va.Field(i).SetString("claude")
			vb.Field(i).SetString("claude")
		case reflect.Int64:
			va.Field(i).SetInt(int64(i + 1))
			vb.Field(i).SetInt(int64(1000 * (i + 1)))
		case reflect.Float64:
			va.Field(i).SetFloat(float64(i+1) + 0.25)
			vb.Field(i).SetFloat(float64(1000*(i+1)) + 0.5)
		default:
			t.Fatalf("ModelStat.%s is a %s: decide how the parts of a split report merge it, in mergeModelStats and here", field.Name, va.Field(i).Kind())
		}
	}
	merged := mergeModelStats([]ModelStat{a}, []ModelStat{b})
	require.Len(t, merged, 1)
	got := reflect.ValueOf(merged[0])
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		switch got.Field(i).Kind() {
		case reflect.String:
			require.Equal(t, "claude", got.Field(i).String(), name)
		case reflect.Int64:
			require.Equal(t, va.Field(i).Int()+vb.Field(i).Int(), got.Field(i).Int(), name)
		case reflect.Float64:
			require.Equal(t, va.Field(i).Float()+vb.Field(i).Float(), got.Field(i).Float(), name)
		}
	}
}

// ---- the key summary ----

var keyStatsCols = keyWindowSummaryCols

// stats is a summary row whose figures all follow from its request count; every request took 40 ms, unless told.
func keyStats(requests int64, durationMs float64) *usagestats.UsageStats {
	return &usagestats.UsageStats{
		TotalRequests: requests, TotalInputTokens: 10 * requests, TotalOutputTokens: 20 * requests,
		TotalCacheTokens: 3 * requests, TotalCacheCreationTokens: requests, TotalCacheReadTokens: 2 * requests,
		TotalTokens: 33 * requests, TotalCost: 0.5 * float64(requests), TotalActualCost: 0.25 * float64(requests),
		AverageDurationMs: durationMs,
	}
}

func keyStatsRows(s *usagestats.UsageStats) *sqlmock.Rows {
	return sqlmock.NewRows(keyStatsCols).AddRow(s.TotalRequests, s.TotalInputTokens, s.TotalOutputTokens, s.TotalCacheTokens,
		s.TotalCacheCreationTokens, s.TotalCacheReadTokens, s.TotalCost, s.TotalActualCost, s.AverageDurationMs)
}

func (e *splitModelEnv) expectKeyStats(start, end time.Time, s *usagestats.UsageStats) {
	e.mock.ExpectQuery(`COUNT\(\*\) as total_requests`).WithArgs(splitTrendKey, instant(start), instant(end)).WillReturnRows(keyStatsRows(s))
}

// The key summary over days is read in the same parts, added up, with the average duration rebuilt from the
// durations and the requests of the parts. A part with no request has no average (0) and weighs nothing.
func TestKeyStatsSplitAddsThePartsAndRebuildsTheAverage(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	start, end := time.Date(2026, 11, 20, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)

	env.expectKeyStats(start, today, keyStats(30, 100)) // 3000 ms
	env.expectKeyStats(today, end, keyStats(10, 300))   // 3000 ms
	got, err := env.repo.GetAPIKeyStatsAggregated(context.Background(), splitTrendKey, start, end)
	require.NoError(t, err)
	want := keyStats(40, 150) // 6000 ms over 40 requests, not the mean of 100 and 300
	require.Equal(t, want, got)
	require.NoError(t, env.mock.ExpectationsWereMet())
	require.Equal(t, []time.Duration{time.Minute, usageStatsKeyClosedDaysTTL}, env.ttls())

	// Today is empty (no average, no weight), and then the days that are over are.
	env.server.FastForward(61 * time.Second)
	env.expectKeyStats(today, end, keyStats(0, 0))
	got, err = env.repo.GetAPIKeyStatsAggregated(context.Background(), splitTrendKey, start, end)
	require.NoError(t, err)
	require.Equal(t, keyStats(30, 100), got)

	env2 := newSplitModelEnv(t, env.now, time.Minute)
	env2.expectKeyStats(start, today, keyStats(0, 0))
	env2.expectKeyStats(today, end, keyStats(0, 0))
	got, err = env2.repo.GetAPIKeyStatsAggregated(context.Background(), splitTrendKey, start, end)
	require.NoError(t, err)
	require.Equal(t, keyStats(0, 0), got, "no request at all: zeros, as the query gives")
}

// The summary, like the other two aggregates, is read whole when it has nothing to cut, and the part that holds
// today is the same entry as the window of today alone: the panels ask for both, and the day is read once.
func TestKeyStatsSplitSharesTodayWithTheWindowOfToday(t *testing.T) {
	shanghai := loadZone(t, "Asia/Shanghai")
	today, tomorrow := time.Date(2026, 11, 26, 0, 0, 0, 0, shanghai), time.Date(2026, 11, 27, 0, 0, 0, 0, shanghai)
	env := newSplitModelEnv(t, time.Date(2026, 11, 26, 10, 20, 0, 0, shanghai), time.Minute)
	ask := func(start time.Time) *usagestats.UsageStats {
		got, err := env.repo.GetAPIKeyStatsAggregated(context.Background(), splitTrendKey, start, tomorrow)
		require.NoError(t, err)
		return got
	}

	// today alone is one natural day: read whole, for the cache's TTL
	env.expectKeyStats(today, tomorrow, keyStats(5, 10))
	require.Equal(t, keyStats(5, 10), ask(today))

	// the last 7 days and this month: the days that are over are read, today is the entry that is already there
	env.expectKeyStats(today.AddDate(0, 0, -6), today, keyStats(60, 10))
	require.Equal(t, keyStats(65, 10), ask(today.AddDate(0, 0, -6)))
	env.expectKeyStats(time.Date(2026, 11, 1, 0, 0, 0, 0, shanghai), today, keyStats(250, 10))
	require.Equal(t, keyStats(255, 10), ask(time.Date(2026, 11, 1, 0, 0, 0, 0, shanghai)))
	require.NoError(t, env.mock.ExpectationsWereMet(), "today was read once for the three windows")
}

// Every figure of a summary is added except the average, which is weighted by the requests. The test fails for any
// field kind it does not know, so that whoever adds one decides how the parts of a summary merge it.
func TestMergeAPIKeyStatsAddsEveryFigure(t *testing.T) {
	typ := reflect.TypeFor[usagestats.UsageStats]()
	var a, b usagestats.UsageStats
	va, vb := reflect.ValueOf(&a).Elem(), reflect.ValueOf(&b).Elem()
	// The query fills no account cost and no endpoint lists: they stay as they are in the parts, empty.
	unfilled := map[string]bool{"TotalAccountCost": true, "Endpoints": true, "UpstreamEndpoints": true, "EndpointPaths": true}
	for i := range typ.NumField() {
		field := typ.Field(i)
		if unfilled[field.Name] {
			continue
		}
		switch va.Field(i).Kind() {
		case reflect.Int64:
			va.Field(i).SetInt(int64(i + 1))
			vb.Field(i).SetInt(int64(1000 * (i + 1)))
		case reflect.Float64:
			va.Field(i).SetFloat(float64(i+1) + 0.25)
			vb.Field(i).SetFloat(float64(1000*(i+1)) + 0.5)
		default:
			t.Fatalf("UsageStats.%s is a %s: decide how the parts of a split summary merge it, in mergeAPIKeyStats and here", field.Name, va.Field(i).Kind())
		}
	}
	merged := mergeAPIKeyStats(&a, nil, &b)
	got := reflect.ValueOf(*merged)
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		switch {
		case unfilled[name]:
			require.True(t, got.Field(i).IsZero(), name)
		case name == "AverageDurationMs":
			wantMs := (a.AverageDurationMs*float64(a.TotalRequests) + b.AverageDurationMs*float64(b.TotalRequests)) / float64(a.TotalRequests+b.TotalRequests)
			require.InDelta(t, wantMs, got.Field(i).Float(), 1e-9, name)
		case got.Field(i).Kind() == reflect.Int64:
			require.Equal(t, va.Field(i).Int()+vb.Field(i).Int(), got.Field(i).Int(), name)
		case got.Field(i).Kind() == reflect.Float64:
			require.Equal(t, va.Field(i).Float()+vb.Field(i).Float(), got.Field(i).Float(), name)
		}
	}
	require.Equal(t, &usagestats.UsageStats{}, mergeAPIKeyStats(), "nothing to add is zeros")
}
