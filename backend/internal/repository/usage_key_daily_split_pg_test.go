package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Differential test of one API key's split by-day trend against a real PostgreSQL.
//
// GetUsageTrendWithFilters answers a key's by-day trend over a window that holds today in two parts (the days that
// are over, and today), each cached on its own, and adds the two answers up by day label. Whatever the cut, that
// has to be the answer of one plain query over the whole window, item by item. The fixture lives in one transaction
// (a temp table shadows usage_logs, nothing outside it is touched): rows at random times of a whole year, rows on, one
// microsecond before and one after every local midnight of every zone involved (so that days, labels and cuts
// meet exactly on rows), and rows on and around the cut of every window. The day labels are cut in the database
// session's zone and the windows in the user's, and the two are crossed on purpose: where they differ, one label
// is found on both sides of the cut and has to be added, not listed twice. DST days (23, 25 and 23.5 hours), half
// hour zones, windows that hold no closed day or no today, windows that start or end between midnights and other
// granularities are all in. Every window is answered twice, by the code under test and by the plain query, and the
// statements it sent and the lifetimes it gave its entries are checked too, not only the sums.
//
// The integration job runs it with the Docker PostgreSQL. To run it anywhere else, point
// ROLLUP_DIFF_TEST_POSTGRES_DSN at a scratch database that has the migrations applied (everything the test writes
// goes to a temp table inside one transaction that is rolled back):
//
//	ROLLUP_DIFF_TEST_POSTGRES_DSN='postgres://user@127.0.0.1:5432/db?sslmode=disable' \
//	KEY_DAILY_DIFF_WINDOWS=300 go test ./internal/repository/ -run TestKeyDailyTrendSplitDifferentialPostgres -v

var (
	// keyDailySessionZones are the zones the day labels are cut in (the database session's).
	keyDailySessionZones = []string{"UTC", "Asia/Shanghai", "America/Los_Angeles"}
	// keyDailyWindowZones are the zones the windows are cut in (the user's).
	keyDailyWindowZones = []string{"Asia/Shanghai", "UTC", "America/Los_Angeles", "Europe/London", "Asia/Kolkata", "Asia/Kathmandu", "Australia/Lord_Howe"}
	// keyDailyDSTDays are local days of a window zone that are not 24 hours long.
	keyDailyDSTDays = map[string][][3]int{
		"America/Los_Angeles": {{2026, 3, 8}, {2026, 11, 1}},
		"Europe/London":       {{2026, 3, 29}, {2026, 10, 25}},
		"Australia/Lord_Howe": {{2026, 4, 5}, {2026, 10, 4}},
	}
)

const keyDailyIdleKey = int64(9199999) // a key with no rows at all

type keyDailyDiffConfig struct {
	windows int // random windows per window zone, on top of the hand-picked ones
	rows    int // random usage_logs rows; the boundary rows come on top
	seed    int64
}

func TestKeyDailyTrendSplitDifferentialPostgres(t *testing.T) {
	dsn := os.Getenv("ROLLUP_DIFF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ROLLUP_DIFF_TEST_POSTGRES_DSN not set; the integration job runs the same check on its PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	runKeyDailyDifferential(t, db, keyDailyDiffConfig{
		windows: rollupDiffEnvInt("KEY_DAILY_DIFF_WINDOWS", 100),
		rows:    rollupDiffEnvInt("KEY_DAILY_DIFF_ROWS", 40000),
		seed:    int64(rollupDiffEnvInt("KEY_DAILY_DIFF_SEED", 20261126)),
	})
}

type keyDailyKey struct{ id, user int64 }

type keyDailyWindow struct {
	label       string
	zone        string    // the zone the window is cut in
	now         time.Time // what the repository's clock says
	start, end  time.Time // in zone
	granularity string
	key, user   int64
}

func (w keyDailyWindow) String() string {
	layout := "2006-01-02 15:04:05.999999 MST"
	return fmt.Sprintf("%s key=%d user=%d %s [%s, %s) now=%s", w.label, w.key, w.user, w.granularity, w.start.Format(layout), w.end.Format(layout), w.now.Format(layout))
}

// expectedSplit is where the window has to be cut, worked out with the helper the handlers use for the start of a
// day (it loads the zone by name), and whether it has to be cut at all.
func (w keyDailyWindow) expectedSplit() (time.Time, bool) {
	// Every spelling the SQL does not know is grouped by day, so it is cut like a day.
	if usageGranularityKey(w.granularity) != "day" || w.end.Sub(w.start) <= 25*time.Hour {
		return time.Time{}, false
	}
	midnight := timezone.StartOfDayInUserLocation(w.now, w.zone)
	return midnight, midnight.After(w.start) && midnight.Before(w.end)
}

// keyDailyWindows are the hand-picked windows of one zone (the shapes the usage pages send, DST days, the edges of
// a day, windows with no closed day or no today) followed by random ones.
func keyDailyWindows(t *testing.T, rnd *rand.Rand, zone string, random int, keys []keyDailyKey) []keyDailyWindow {
	t.Helper()
	loc := loadZone(t, zone)
	var out []keyDailyWindow
	add := func(label string, now, start, end time.Time, granularity string) {
		k := keys[rnd.Intn(len(keys))]
		user := k.user
		switch rnd.Intn(12) {
		case 0:
			user = 0 // a call without the owner
		case 1:
			user = k.user + 1 // somebody else's key: nothing to find
		}
		if rnd.Intn(15) == 0 {
			k = keyDailyKey{id: keyDailyIdleKey, user: k.user}
		}
		out = append(out, keyDailyWindow{label: label, zone: zone, now: now.In(loc), start: start.In(loc), end: end.In(loc), granularity: granularity, key: k.id, user: user})
	}
	daily := func(label string, now time.Time, days int) {
		start, end := dailyWindow(t, now, days, zone)
		add(label, now, start, end, "day")
	}

	nows := []time.Time{time.Date(2026, 6, 15, 12, 0, 0, 0, loc), time.Date(2026, 11, 26, 10, 0, 0, 0, loc)}
	for _, d := range keyDailyDSTDays[zone] {
		nows = append(nows, time.Date(d[0], time.Month(d[1]), d[2], 12, 0, 0, 0, loc))
	}
	for _, base := range slices.Clone(nows) {
		midnight := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, loc)
		nows = append(nows, midnight, midnight.Add(time.Microsecond), midnight.AddDate(0, 0, 1).Add(-time.Microsecond))
	}
	for _, now := range nows {
		for _, days := range []int{2, 3, 15, 30, 90} {
			daily(fmt.Sprintf("last %d days", days), now, days)
		}
	}

	// The shapes of an explicit start_date / end_date, around "today" of 2026-11-26.
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, loc)
	today := time.Date(2026, 11, 26, 0, 0, 0, 0, loc)
	day := func(n int) time.Time { return today.AddDate(0, 0, n) }
	for _, g := range []struct {
		label      string
		start, end time.Time
	}{
		{"start_date to today", day(-9), day(1)},
		{"start_date to yesterday (no today)", day(-9), day(0)},
		{"start_date to tomorrow", day(-9), day(2)},
		{"one day, today", day(0), day(1)},
		{"from today for three days (no closed day)", day(0), day(3)},
		{"all in the future", day(1), day(9)},
		{"two days ending today", day(-1), day(2)},
		{"unaligned start", day(-9).Add(7*time.Hour + 13*time.Minute), day(1)},
		{"ends now", day(-9), now},
		{"one microsecond of today", day(-9), day(0).Add(time.Microsecond)},
		{"ends a microsecond before today", day(-9), day(0).Add(-time.Microsecond)},
		{"starts a microsecond after the cut", day(0).Add(time.Microsecond), day(3)},
		{"starts a microsecond before the cut", day(0).Add(-time.Microsecond), day(3)},
		{"far future end", day(-3), day(400)},
	} {
		add(g.label, now, g.start, g.end, "day")
	}
	for _, granularity := range []string{"hour", "week", "month", "fortnight"} {
		add("other granularity "+granularity, now, day(-20), day(1), granularity)
	}

	for range random {
		at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rnd.Int63n(int64(333 * 24 * time.Hour))))
		at = at.Truncate(time.Microsecond)
		switch rnd.Intn(6) {
		case 0, 1, 2:
			daily("random last days", at, 1+rnd.Intn(90))
		case 3: // arbitrary instants
			start := at.Add(-time.Duration(rnd.Int63n(int64(60 * 24 * time.Hour)))).Truncate(time.Microsecond)
			add("random instants", at, start, start.Add(time.Hour+time.Duration(rnd.Int63n(int64(60*24*time.Hour)))).Truncate(time.Microsecond), "day")
		case 4: // aligned start, end anywhere around now
			start, _ := dailyWindow(t, at, 1+rnd.Intn(60), zone)
			add("random aligned start", at, start, at.Add(time.Duration(rnd.Int63n(int64(6*24*time.Hour))-int64(3*24*time.Hour))).Truncate(time.Microsecond), "day")
		default: // aligned, shifted by a few minutes or microseconds
			start, end := dailyWindow(t, at, 1+rnd.Intn(45), zone)
			shift := []time.Duration{time.Microsecond, -time.Microsecond, 17 * time.Minute, -time.Hour}[rnd.Intn(4)]
			add("random shifted", at, start.Add(shift), end.Add(shift), "day")
		}
	}
	return out
}

// keyDailyProbe records the statements a repository sends, with their arguments.
type keyDailyProbe struct {
	inner sqlExecutor
	mu    sync.Mutex
	calls []keyDailyCall
}

type keyDailyCall struct {
	query string
	args  []any
}

func (p *keyDailyProbe) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.inner.ExecContext(ctx, query, args...)
}

func (p *keyDailyProbe) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	p.mu.Lock()
	p.calls = append(p.calls, keyDailyCall{query: query, args: args})
	p.mu.Unlock()
	return p.inner.QueryContext(ctx, query, args...)
}

// usageLogReads returns what was sent to usage_logs since the last call, oldest first.
func (p *keyDailyProbe) usageLogReads() []keyDailyCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	var reads []keyDailyCall
	for _, call := range p.calls {
		if strings.Contains(call.query, "FROM usage_logs") {
			reads = append(reads, call)
		}
	}
	p.calls = nil
	return reads
}

func (c keyDailyCall) window() (start, end time.Time, ok bool) {
	if len(c.args) < 2 {
		return time.Time{}, time.Time{}, false
	}
	start, okStart := c.args[0].(time.Time)
	end, okEnd := c.args[1].(time.Time)
	return start, end, okStart && okEnd
}

type keyDailyReport struct {
	comparisons, split, noClosedDay, noToday, otherGranularity int
	labelInBothParts, notTwentyFourHourToday, nonEmpty         int
}

type keyDailyEnv struct {
	t      *testing.T
	ctx    context.Context
	probe  *keyDailyProbe
	repo   *usageLogRepository // the code under test: a cache in front, a clock the test moves
	oracle *usageLogRepository // only its plain whole-window query is used
	server *miniredis.Miniredis
	now    time.Time
	report keyDailyReport
}

func runKeyDailyDifferential(t *testing.T, db *sql.DB, cfg keyDailyDiffConfig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	t.Cleanup(cancel)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	exec := func(query string, args ...any) {
		t.Helper()
		_, execErr := tx.ExecContext(ctx, query, args...)
		require.NoError(t, execErr, query)
	}
	exec(`CREATE TEMP TABLE usage_logs (LIKE public.usage_logs INCLUDING DEFAULTS INCLUDING CONSTRAINTS) ON COMMIT DROP`)
	exec(`CREATE INDEX ON usage_logs (api_key_id, created_at)`)

	rnd := rand.New(rand.NewSource(cfg.seed))
	var keys []keyDailyKey
	for u := int64(9100001); u <= 9100003; u++ {
		keys = append(keys, keyDailyKey{id: u*10 + 1, user: u}, keyDailyKey{id: u*10 + 2, user: u})
	}
	var windows []keyDailyWindow
	for _, zone := range keyDailyWindowZones {
		windows = append(windows, keyDailyWindows(t, rnd, zone, cfg.windows, keys)...)
	}
	loadKeyDailyFixture(t, ctx, tx, rnd, cfg, keys, windows)
	exec(`ANALYZE usage_logs`)

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	e := &keyDailyEnv{t: t, ctx: ctx, probe: &keyDailyProbe{inner: tx}, server: server}
	e.repo = newUsageLogRepositoryWithSQL(nil, e.probe)
	e.repo.statsCache = newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}})
	e.repo.clock = func() time.Time { return e.now }
	e.oracle = newUsageLogRepositoryWithSQL(nil, tx)

	var failures []string
	for _, session := range keyDailySessionZones {
		t.Run("labels in "+session, func(t *testing.T) {
			useRollupTestTimezone(t, session)
			// The statements format the day labels in the session's zone; the cache key follows the application's.
			_, setErr := tx.ExecContext(ctx, `SELECT set_config('TimeZone', $1, true)`, session)
			require.NoError(t, setErr)
			for _, w := range windows {
				failures = append(failures, e.compare(session, w)...)
			}
		})
	}

	r := e.report
	t.Logf("%d windows x %d label zones = %d comparisons against the plain query; cut %d, no closed day %d, no today %d, other granularity %d; "+
		"a label in both parts %d times; a today that is not 24 hours %d times; %d answers had rows",
		len(windows), len(keyDailySessionZones), r.comparisons, r.split, r.noClosedDay, r.noToday, r.otherGranularity,
		r.labelInBothParts, r.notTwentyFourHourToday, r.nonEmpty)
	if len(failures) > 10 {
		failures = append(failures[:10], fmt.Sprintf("... and %d more", len(failures)-10))
	}
	require.Empty(t, failures, strings.Join(failures, "\n"))
	// The point of the test is the cut and the merge: make sure they were really exercised.
	require.Greater(t, r.split, r.comparisons/3, "too few windows were cut")
	require.Greater(t, r.noClosedDay, 0)
	require.Greater(t, r.noToday, 0)
	require.Greater(t, r.labelInBothParts, r.split/10, "too few cuts put one label on both sides")
	require.Greater(t, r.notTwentyFourHourToday, 0, "no window had a DST day as today")
	require.Greater(t, r.nonEmpty, r.comparisons/3, "too many empty answers")
}

// loadKeyDailyFixture inserts the random rows, the rows on the edges of every day of every zone, and the rows on
// and around the cut of every window, for every key.
func loadKeyDailyFixture(t *testing.T, ctx context.Context, tx *sql.Tx, rnd *rand.Rand, cfg keyDailyDiffConfig, keys []keyDailyKey, windows []keyDailyWindow) {
	t.Helper()
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("usage_logs",
		"id", "user_id", "api_key_id", "account_id", "group_id", "model",
		"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost", "created_at"))
	require.NoError(t, err)
	var id int64
	insert := func(key keyDailyKey, at time.Time) {
		id++
		total := math.Round(rnd.Float64()*5e10) / 1e10 // exact decimals: the database adds them without error
		actual := math.Round(total*(0.2+rnd.Float64()*1.6)*1e10) / 1e10
		cacheCreation, cacheRead := int64(0), int64(0)
		if rnd.Intn(2) == 0 {
			cacheCreation, cacheRead = int64(rnd.Intn(800)), int64(rnd.Intn(8000))
		}
		var group driver.Value
		if rnd.Intn(8) != 0 {
			group = int64(9000001 + rnd.Intn(4))
		}
		_, execErr := stmt.ExecContext(ctx, id, key.user, key.id, int64(1+rnd.Intn(5)), group,
			[]string{"claude-opus-4-5", "gpt-5", "gemini-2.5-pro"}[rnd.Intn(3)],
			int64(rnd.Intn(3000)), int64(rnd.Intn(1500)), cacheCreation, cacheRead,
			strconv.FormatFloat(total, 'f', 10, 64), strconv.FormatFloat(actual, 'f', 10, 64), at.UTC().Truncate(time.Microsecond))
		require.NoError(t, execErr)
	}
	pick := func() keyDailyKey { return keys[rnd.Intn(rnd.Intn(len(keys))+1)] } // the first keys are the heavy ones

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC).Sub(first)
	for range cfg.rows {
		insert(pick(), first.Add(time.Duration(rnd.Int63n(int64(span)))))
	}
	edges := []time.Duration{-time.Microsecond, 0, time.Microsecond}
	zones := slices.Compact(slices.Sorted(slices.Values(slices.Concat(keyDailySessionZones, keyDailyWindowZones))))
	for _, zone := range zones {
		loc := loadZone(t, zone)
		for midnight := time.Date(2026, 1, 1, 0, 0, 0, 0, loc); midnight.Before(first.Add(span)); midnight = midnight.AddDate(0, 0, 1) {
			for _, edge := range edges {
				insert(pick(), midnight.Add(edge))
			}
		}
	}
	seen := make(map[time.Time]bool)
	for _, w := range windows {
		cut, split := w.expectedSplit()
		if !split || seen[cut] {
			continue
		}
		seen[cut] = true
		for _, k := range keys {
			for _, edge := range edges {
				insert(k, cut.Add(edge))
			}
		}
	}
	_, err = stmt.ExecContext(ctx)
	require.NoError(t, err)
	require.NoError(t, stmt.Close())
}

// compare answers one window through the code under test, cold and warm, and through the plain query, and returns
// what differs.
func (e *keyDailyEnv) compare(session string, w keyDailyWindow) []string {
	e.t.Helper()
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf("[labels in %s] %s: %s", session, w, fmt.Sprintf(format, args...)))
	}
	e.report.comparisons++
	e.server.FlushAll()
	e.now = w.now
	e.probe.usageLogReads()
	call := func() ([]TrendDataPoint, error) {
		return e.repo.GetUsageTrendWithFilters(e.ctx, w.start, w.end, w.granularity, w.user, w.key, 0, 0, "", nil, nil, nil)
	}
	got, err := call()
	reads := e.probe.usageLogReads()
	if err != nil {
		fail("error: %v", err)
		return failures
	}
	want, err := e.oracle.getUsageTrendWithFilters(e.ctx, w.start, w.end, w.granularity, w.user, w.key, 0, 0, "", "", nil, nil, nil, "", nil, nil)
	if err != nil {
		fail("plain query: %v", err)
		return failures
	}
	if diff := keyDailyTrendDiff(got, want); diff != "" {
		fail("%s", diff)
	}
	if len(want) > 0 {
		e.report.nonEmpty++
	}

	// Which path answered, and what it asked.
	cut, split := w.expectedSplit()
	ttls := []time.Duration{time.Minute}
	if w.end.Sub(w.start) > 25*time.Hour {
		ttls = []time.Duration{keyWindowTTL}
	}
	if split {
		ttls = []time.Duration{time.Minute, usageStatsKeyClosedDaysTTL}
	}
	switch {
	case split:
		e.report.split++
		if cut.AddDate(0, 0, 1).Sub(cut) != 24*time.Hour {
			e.report.notTwentyFourHourToday++
		}
	case usageGranularityKey(w.granularity) != "day":
		e.report.otherGranularity++
	case w.end.Sub(w.start) > 25*time.Hour && !timezone.StartOfDayInUserLocation(w.now, w.zone).After(w.start):
		e.report.noClosedDay++
	case w.end.Sub(w.start) > 25*time.Hour:
		e.report.noToday++
	}
	if split {
		if len(reads) != 2 {
			fail("cut window sent %d statements to usage_logs, want 2", len(reads))
		} else {
			s0, e0, ok0 := reads[0].window()
			s1, e1, ok1 := reads[1].window()
			if !ok0 || !ok1 || !s0.Equal(w.start) || !e0.Equal(cut) || !s1.Equal(cut) || !e1.Equal(w.end) {
				fail("cut window read [%v, %v) then [%v, %v), want [%v, %v) then [%v, %v)", s0, e0, s1, e1, w.start, cut, cut, w.end)
			}
			// What the parts have in common: a day label found on both sides of the cut.
			closed, closedErr := e.oracle.getUsageTrendWithFilters(e.ctx, w.start, cut, w.granularity, w.user, w.key, 0, 0, "", "", nil, nil, nil, "", nil, nil)
			today, todayErr := e.oracle.getUsageTrendWithFilters(e.ctx, cut, w.end, w.granularity, w.user, w.key, 0, 0, "", "", nil, nil, nil, "", nil, nil)
			if closedErr != nil || todayErr != nil {
				fail("parts: %v %v", closedErr, todayErr)
			} else if keyDailyShareALabel(closed, today) {
				e.report.labelInBothParts++
			}
		}
	} else {
		if len(reads) != 1 {
			fail("window left whole sent %d statements to usage_logs, want 1", len(reads))
		} else if s, en, ok := reads[0].window(); !ok || !s.Equal(w.start) || !en.Equal(w.end) {
			fail("window left whole read [%v, %v)", s, en)
		}
	}
	if got, want := e.ttlsInRedis(), ttls; !slices.Equal(got, want) {
		fail("cache lifetimes %v, want %v", got, want)
	}

	// The same call again is answered from the cache, and is the same answer.
	again, err := call()
	if err != nil {
		fail("warm call: %v", err)
	} else if !reflect.DeepEqual(got, again) {
		fail("warm call differs from the cold one: %+v vs %+v", again, got)
	}
	if extra := e.probe.usageLogReads(); len(extra) != 0 {
		fail("warm call sent %d statements to usage_logs", len(extra))
	}
	return failures
}

func (e *keyDailyEnv) ttlsInRedis() []time.Duration {
	var out []time.Duration
	for _, key := range e.server.Keys() {
		out = append(out, e.server.TTL(key))
	}
	slices.Sort(out)
	return out
}

func keyDailyShareALabel(a, b []TrendDataPoint) bool {
	seen := make(map[string]bool, len(a))
	for _, p := range a {
		seen[p.Date] = true
	}
	for _, p := range b {
		if seen[p.Date] {
			return true
		}
	}
	return false
}

// keyDailyClose compares costs. The database adds exact decimals; the code under test adds two floats, one per
// part, so the last bits may differ. A missing or doubled row moves a total by at least the smallest fixture cost.
func keyDailyClose(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// keyDailyTrendDiff compares two trends point by point, in order: days, requests, every kind of token, both costs.
func keyDailyTrendDiff(got, want []TrendDataPoint) string {
	dates := func(points []TrendDataPoint) []string {
		out := make([]string, len(points))
		for i, p := range points {
			out[i] = p.Date
		}
		return out
	}
	if (got == nil) != (want == nil) {
		return fmt.Sprintf("got nil=%v, want nil=%v", got == nil, want == nil)
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d points %v, want %d %v", len(got), dates(got), len(want), dates(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Date != w.Date || g.Requests != w.Requests || g.InputTokens != w.InputTokens || g.OutputTokens != w.OutputTokens ||
			g.CacheCreationTokens != w.CacheCreationTokens || g.CacheReadTokens != w.CacheReadTokens || g.TotalTokens != w.TotalTokens ||
			!keyDailyClose(g.Cost, w.Cost) || !keyDailyClose(g.ActualCost, w.ActualCost) {
			return fmt.Sprintf("point %d: got %+v, want %+v", i, g, w)
		}
	}
	return ""
}
