package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/alicebob/miniredis/v2"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Differential test of one API key's split model report and key summary against a real PostgreSQL.
//
// GetModelStatsWithFiltersBySource, GetModelStatsWithUsageFiltersBySource and GetAPIKeyStatsAggregated answer a key's
// report over a window that holds today in parts (keyWindowParts): the head, the whole days that are over, and today,
// each cached on its own, and add the answers up. Whatever the cut, that has to be the answer of one plain query over
// the whole window, model by model and figure by figure. The fixture lives in one transaction (a temp table shadows
// usage_logs, nothing outside it is touched): rows at random times of a whole year, with the model columns NULL, empty,
// blank and filled, durations NULL and set, account costs NULL and set; rows on, one microsecond before and one after
// every local midnight of every zone involved; and rows on and around the cut and the first midnight of every window
// (worked out here with the time zone helper the handlers use, not with the code under test). The windows are the
// shapes the callers send (the default 30 days of /v1/usage that moves in 15 minute steps on a key's own grid, days from
// midnight to midnight, explicit dates, rolling presets), DST days (23, 25 and 23.5 hours), half hour zones, windows
// that start or end between midnights, windows with no whole day over, with no today, shorter than a day. Every
// window is answered by the code under test and by the plain query, for the three model sources, with and without an
// owner and other filters, and the statements it sent and the lifetimes it gave its entries are checked too, not only
// the sums.
//
// The integration job runs it with the Docker PostgreSQL. To run it anywhere else, point
// ROLLUP_DIFF_TEST_POSTGRES_DSN at a scratch database that has the migrations applied (everything the test writes
// goes to a temp table inside one transaction that is rolled back):
//
//	ROLLUP_DIFF_TEST_POSTGRES_DSN='postgres://user@127.0.0.1:5432/db?sslmode=disable' \
//	KEY_WINDOW_DIFF_WINDOWS=300 go test ./internal/repository/ -run TestKeyWindowSplitDifferentialPostgres -v

var keyWindowModelSources = []string{usagestats.ModelSourceRequested, usagestats.ModelSourceUpstream, usagestats.ModelSourceMapping}

// keyWindowTieKey is a key whose models always tie (see loadKeyWindowFixture).
var keyWindowTieKey = keyDailyKey{id: 9199998, user: 9100001}

type keyWindowDiffConfig struct {
	windows int // random windows per window zone, on top of the hand-picked ones
	rows    int // random usage_logs rows; the boundary rows come on top
	seed    int64
}

func TestKeyWindowSplitDifferentialPostgres(t *testing.T) {
	dsn := os.Getenv("ROLLUP_DIFF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ROLLUP_DIFF_TEST_POSTGRES_DSN not set; the integration job runs the same check on its PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	runKeyWindowDifferential(t, db, keyWindowDiffConfig{
		windows: rollupDiffEnvInt("KEY_WINDOW_DIFF_WINDOWS", 100),
		rows:    rollupDiffEnvInt("KEY_WINDOW_DIFF_ROWS", 40000),
		seed:    int64(rollupDiffEnvInt("KEY_WINDOW_DIFF_SEED", 20261126)),
	})
}

type keyWindowDiff struct {
	label      string
	zone       string    // the zone the window is cut in
	now        time.Time // what the repository's clock says
	start, end time.Time // in zone
	key, user  int64
}

func (w keyWindowDiff) String() string {
	layout := "2006-01-02 15:04:05.999999 MST"
	return fmt.Sprintf("%s key=%d user=%d [%s, %s) now=%s", w.label, w.key, w.user, w.start.Format(layout), w.end.Format(layout), w.now.Format(layout))
}

// wanted is how the window has to be read, worked out with the helper the handlers use for the start of a day (it
// loads the zone by name) and by its definition, not with keyWindowParts: nil for whole, otherwise the parts, oldest
// first.
func (w keyWindowDiff) wanted() []keyWindowPart {
	if w.key <= 0 || w.end.Sub(w.start) <= 25*time.Hour {
		return nil
	}
	cut := timezone.StartOfDayInUserLocation(w.now, w.zone)
	if !cut.After(w.start) || !cut.Before(w.end) {
		return nil
	}
	// The first midnight at or after the start.
	first := timezone.StartOfDayInUserLocation(w.start, w.zone)
	if first.Before(w.start) {
		first = timezone.StartOfDayInUserLocation(w.start.AddDate(0, 0, 1), w.zone)
	}
	if !first.Before(cut) {
		return nil
	}
	var parts []keyWindowPart
	if first.After(w.start) {
		parts = append(parts, keyWindowPart{start: w.start, end: first, ttl: keyWindowTTL})
	}
	return append(parts, keyWindowPart{start: first, end: cut, ttl: 26 * time.Hour}, keyWindowPart{start: cut, end: w.end})
}

// keyGridWindow is the default window of /v1/usage (handler.usageDefaultRange): 30 days that move in 15 minute steps
// on a grid shifted by the key's phase.
func keyGridWindow(now time.Time, phase time.Duration) (start, end time.Time) {
	floor := func(at time.Time) time.Time { return at.Add(-phase).Truncate(15 * time.Minute).Add(phase) }
	end = floor(now)
	return floor(end.AddDate(0, 0, -30)), end
}

// keyWindowDiffWindows are the hand-picked windows of one zone followed by random ones.
func keyWindowDiffWindows(t *testing.T, rnd *rand.Rand, zone string, random int, keys []keyDailyKey) []keyWindowDiff {
	t.Helper()
	loc := loadZone(t, zone)
	var out []keyWindowDiff
	add := func(label string, now, start, end time.Time) {
		k := keys[rnd.Intn(len(keys))]
		user := k.user
		switch rnd.Intn(12) {
		case 0, 1, 2, 3, 4:
			user = 0 // a call without the owner, as /v1/usage asks
		case 5:
			user = k.user + 1 // somebody else's key: nothing to find
		}
		if rnd.Intn(15) == 0 {
			k = keyDailyKey{id: keyDailyIdleKey, user: k.user}
		}
		if rnd.Intn(8) == 0 {
			k = keyWindowTieKey
			user = []int64{0, k.user}[rnd.Intn(2)]
		}
		if rnd.Intn(25) == 0 {
			k.id, user = 0, k.user // no key: a user's own report, read whole
		}
		out = append(out, keyWindowDiff{label: label, zone: zone, now: now.In(loc), start: start.In(loc), end: end.In(loc), key: k.id, user: user})
	}
	daily := func(label string, now time.Time, days int) {
		start, end := dailyWindow(t, now, days, zone)
		add(label, now, start, end)
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
		for _, phase := range []time.Duration{0, 4*time.Minute + 53*time.Second, 14*time.Minute + 59*time.Second} {
			start, end := keyGridWindow(now, phase)
			add(fmt.Sprintf("the default window, phase %v", phase), now, start, end)
		}
		minute := now.Truncate(time.Minute)
		add("a rolling week", now, minute.AddDate(0, 0, -7), minute)
		add("a rolling month", now, minute.AddDate(0, -1, 0), minute)
		add("a rolling 30 hours", now, minute.Add(-30*time.Hour), minute)
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
		{"from today for three days (no day over)", day(0), day(3)},
		{"all in the future", day(1), day(9)},
		{"two days ending today (one day over)", day(-1), day(2)},
		{"yesterday to the middle of tomorrow (no whole day over)", day(-1).Add(7 * time.Hour), day(1).Add(12 * time.Hour)},
		{"unaligned start", day(-9).Add(7*time.Hour + 13*time.Minute), day(1)},
		{"unaligned start, ends now", day(-9).Add(7*time.Hour + 13*time.Minute), now},
		{"ends now", day(-9), now},
		{"one microsecond of today", day(-9), day(0).Add(time.Microsecond)},
		{"ends a microsecond before today", day(-9), day(0).Add(-time.Microsecond)},
		{"starts a microsecond after the cut", day(0).Add(time.Microsecond), day(3)},
		{"starts a microsecond before the cut", day(0).Add(-time.Microsecond), day(3)},
		{"starts a microsecond after a midnight", day(-9).Add(time.Microsecond), day(1)},
		{"starts a microsecond before a midnight", day(-9).Add(-time.Microsecond), day(1)},
		{"far future end", day(-3), day(400)},
		// A window has to be longer than 25 hours to be cut: the three around that length, all across the cut.
		{"14 hours across midnight (not cut)", day(0).Add(-4 * time.Hour), day(0).Add(10 * time.Hour)},
		{"exactly 25 hours across midnight (not cut)", day(0).Add(-10 * time.Hour), day(0).Add(15 * time.Hour)},
		{"25 hours and a microsecond across midnight (no whole day over)", day(0).Add(-10 * time.Hour), day(0).Add(15*time.Hour + time.Microsecond)},
		{"26 hours, starting at midnight yesterday", day(-1), day(0).Add(2 * time.Hour)},
	} {
		add(g.label, now, g.start, g.end)
	}

	for range random {
		at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rnd.Int63n(int64(333 * 24 * time.Hour))))
		at = at.Truncate(time.Microsecond)
		switch rnd.Intn(7) {
		case 0, 1:
			daily("random last days", at, 1+rnd.Intn(90))
		case 2: // arbitrary instants
			start := at.Add(-time.Duration(rnd.Int63n(int64(60 * 24 * time.Hour)))).Truncate(time.Microsecond)
			add("random instants", at, start, start.Add(time.Hour+time.Duration(rnd.Int63n(int64(60*24*time.Hour)))).Truncate(time.Microsecond))
		case 3: // aligned start, end anywhere around now
			start, _ := dailyWindow(t, at, 1+rnd.Intn(60), zone)
			add("random aligned start", at, start, at.Add(time.Duration(rnd.Int63n(int64(6*24*time.Hour))-int64(3*24*time.Hour))).Truncate(time.Microsecond))
		case 4: // the default window of some key at some time
			start, end := keyGridWindow(at, time.Duration(rnd.Int63n(int64(15*time.Minute/time.Second)))*time.Second)
			add("random default window", at, start, end)
		case 5: // a rolling preset
			add("random rolling window", at, at.Truncate(time.Minute).Add(-time.Duration(rnd.Int63n(int64(45*24*time.Hour)))), at.Truncate(time.Minute))
		default: // aligned, shifted by a few minutes or microseconds
			start, end := dailyWindow(t, at, 1+rnd.Intn(45), zone)
			shift := []time.Duration{time.Microsecond, -time.Microsecond, 17 * time.Minute, -time.Hour}[rnd.Intn(4)]
			add("random shifted", at, start.Add(shift), end.Add(shift))
		}
	}
	return out
}

type keyWindowReport struct {
	comparisons, cut, withHead, noWholeDay, noToday, short   int
	modelInManyParts, notTwentyFourHourToday, nonEmpty, ties int
}

type keyWindowEnv struct {
	t      *testing.T
	ctx    context.Context
	probe  *keyDailyProbe
	repo   *usageLogRepository // the code under test: a cache in front, a clock the test moves
	oracle *usageLogRepository // only its plain whole-window queries are used
	server *miniredis.Miniredis
	now    time.Time
	report keyWindowReport
}

func runKeyWindowDifferential(t *testing.T, db *sql.DB, cfg keyWindowDiffConfig) {
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
	var windows []keyWindowDiff
	for _, zone := range keyDailyWindowZones {
		windows = append(windows, keyWindowDiffWindows(t, rnd, zone, cfg.windows, keys)...)
	}
	loadKeyWindowFixture(t, ctx, tx, rnd, cfg, keys, windows)
	exec(`ANALYZE usage_logs`)

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	e := &keyWindowEnv{t: t, ctx: ctx, probe: &keyDailyProbe{inner: tx}, server: server}
	e.repo = newUsageLogRepositoryWithSQL(nil, e.probe)
	e.repo.statsCache = newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}})
	e.repo.clock = func() time.Time { return e.now }
	e.oracle = newUsageLogRepositoryWithSQL(nil, tx)

	var failures []string
	for _, w := range windows {
		failures = append(failures, e.compare(w)...)
	}

	r := e.report
	t.Logf("%d windows, %d comparisons against the plain query: cut %d (with a head %d), no whole day over %d, no today %d, "+
		"shorter than 25 hours or no key %d; a model in several parts %d times, a tie in tokens %d times; "+
		"a today that is not 24 hours %d times; %d answers had rows",
		len(windows), r.comparisons, r.cut, r.withHead, r.noWholeDay, r.noToday, r.short, r.modelInManyParts, r.ties, r.notTwentyFourHourToday, r.nonEmpty)
	if len(failures) > 10 {
		failures = append(failures[:10], fmt.Sprintf("... and %d more", len(failures)-10))
	}
	require.Empty(t, failures, strings.Join(failures, "\n"))
	// The point of the test is the cut, the head and the merge: make sure they were really exercised.
	require.Greater(t, r.cut, r.comparisons/4, "too few windows were cut")
	require.Greater(t, r.withHead, r.cut/4, "too few cuts had a head")
	require.Greater(t, r.noWholeDay, 0)
	require.Greater(t, r.noToday, 0)
	require.Greater(t, r.short, 0)
	require.Greater(t, r.modelInManyParts, r.cut/4, "too few cuts put one model in several parts")
	require.Greater(t, r.ties, r.cut/20, "too few reports had a tie in tokens")
	require.Greater(t, r.notTwentyFourHourToday, 0, "no window had a DST day as today")
	require.Greater(t, r.nonEmpty, r.comparisons/3, "too many empty answers")
}

// The values the model columns take: the model of the row is never NULL, the requested and the upstream model are
// NULL, empty, blank, or filled, so that every expression the three sources group by is exercised.
var (
	keyWindowModels    = []string{"claude-opus-4-5", "gpt-5", "gemini-2.5-pro", "deepseek-v3"}
	keyWindowRequested = []driver.Value{nil, "", "   ", "claude-opus-4-5", "gpt-5", "Claude-Alias", " gemini-2.5-pro "}
	keyWindowUpstream  = []driver.Value{nil, "", "claude-opus-4-5-20251101", "gpt-5-mini", "claude-opus-4-5"}
	keyWindowBilling   = []driver.Value{nil, "token", "per_request", "image"}
)

// loadKeyWindowFixture inserts the random rows, the rows on the edges of every day of every zone, and the rows on and
// around the cut and the first midnight of every window, for every key.
func loadKeyWindowFixture(t *testing.T, ctx context.Context, tx *sql.Tx, rnd *rand.Rand, cfg keyWindowDiffConfig, keys []keyDailyKey, windows []keyWindowDiff) {
	t.Helper()
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("usage_logs",
		"id", "user_id", "api_key_id", "account_id", "group_id", "model", "requested_model", "upstream_model", "billing_mode",
		"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost",
		"account_stats_cost", "account_rate_multiplier", "duration_ms", "created_at"))
	require.NoError(t, err)
	decimal := func(v float64) string { return strconv.FormatFloat(v, 'f', 10, 64) }
	var id int64
	insert := func(key keyDailyKey, at time.Time) {
		id++
		total := math.Round(rnd.Float64()*5e10) / 1e10 // exact decimals: the database adds them without error
		actual := math.Round(total*(0.2+rnd.Float64()*1.6)*1e10) / 1e10
		cacheCreation, cacheRead := int64(0), int64(0)
		if rnd.Intn(2) == 0 {
			cacheCreation, cacheRead = int64(rnd.Intn(800)), int64(rnd.Intn(8000))
		}
		var group, accountCost, multiplier, duration driver.Value
		if rnd.Intn(8) != 0 {
			group = int64(9000001 + rnd.Intn(4))
		}
		if rnd.Intn(2) == 0 {
			accountCost = decimal(math.Round(total*(0.5+rnd.Float64())*1e10) / 1e10)
		}
		if rnd.Intn(3) != 0 {
			multiplier = strconv.FormatFloat(0.5+float64(rnd.Intn(16))/10, 'f', 4, 64)
		}
		if rnd.Intn(10) < 7 {
			duration = int64(1 + rnd.Intn(30000)) // 30% have none: the average of a part is over all its requests anyway
		}
		_, execErr := stmt.ExecContext(ctx, id, key.user, key.id, int64(1+rnd.Intn(5)), group,
			keyWindowModels[rnd.Intn(len(keyWindowModels))],
			keyWindowRequested[rnd.Intn(len(keyWindowRequested))], keyWindowUpstream[rnd.Intn(len(keyWindowUpstream))], keyWindowBilling[rnd.Intn(len(keyWindowBilling))],
			int64(rnd.Intn(3000)), int64(rnd.Intn(1500)), cacheCreation, cacheRead, decimal(total), decimal(actual),
			accountCost, multiplier, duration, at.UTC().Truncate(time.Microsecond))
		require.NoError(t, execErr)
	}
	pick := func() keyDailyKey { return keys[rnd.Intn(rnd.Intn(len(keys))+1)] } // the first keys are the heavy ones

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	span := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC).Sub(first)
	for range cfg.rows {
		insert(pick(), first.Add(time.Duration(rnd.Int63n(int64(span)))))
	}
	// The tie key has every model once a day, at noon UTC and with the same tokens: whatever the window holds, all the
	// models of the report have the same total, and the order of the report is decided by the model name alone.
	for noon := first.Add(12 * time.Hour); noon.Before(first.Add(span)); noon = noon.AddDate(0, 0, 1) {
		for _, model := range keyWindowModels {
			id++
			_, execErr := stmt.ExecContext(ctx, id, keyWindowTieKey.user, keyWindowTieKey.id, int64(1), nil, model, nil, nil, nil,
				int64(1000), int64(0), int64(0), int64(0), decimal(0.5), decimal(0.25), nil, nil, nil, noon)
			require.NoError(t, execErr)
		}
	}
	edges := []time.Duration{-time.Microsecond, 0, time.Microsecond}
	zones := slices.Compact(slices.Sorted(slices.Values(keyDailyWindowZones)))
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
		for _, p := range w.wanted() {
			for _, at := range []time.Time{p.start, p.end} {
				if seen[at] {
					continue
				}
				seen[at] = true
				for _, k := range keys {
					for _, edge := range edges {
						insert(k, at.Add(edge))
					}
				}
			}
		}
	}
	_, err = stmt.ExecContext(ctx)
	require.NoError(t, err)
	require.NoError(t, stmt.Close())
}

// compare answers one window through the code under test, cold and warm, and through the plain query, for every
// aggregate it applies to, and returns what differs.
func (e *keyWindowEnv) compare(w keyWindowDiff) []string {
	e.t.Helper()
	var failures []string
	fail := func(what, format string, args ...any) {
		failures = append(failures, fmt.Sprintf("%s %s: %s", what, w, fmt.Sprintf(format, args...)))
	}
	wanted := w.wanted()
	e.report.comparisons++
	switch {
	case wanted != nil:
		e.report.cut++
		if len(wanted) == 3 {
			e.report.withHead++
		}
		if cut := wanted[len(wanted)-1].start; cut.AddDate(0, 0, 1).Sub(cut) != 24*time.Hour {
			e.report.notTwentyFourHourToday++
		}
	case w.key <= 0 || w.end.Sub(w.start) <= 25*time.Hour:
		e.report.short++
	case !timezone.StartOfDayInUserLocation(w.now, w.zone).After(w.start) || !timezone.StartOfDayInUserLocation(w.now, w.zone).Before(w.end):
		e.report.noToday++
	default:
		e.report.noWholeDay++
	}

	// start asks one aggregate cold: an empty cache, the clock of the window, no statements recorded yet.
	start := func() {
		e.server.FlushAll()
		e.now = w.now
		e.probe.usageLogReads()
	}
	// shape checks the statements the aggregate sent and the lifetimes of what it cached; windowArgs is where the
	// window is among the arguments of its statement; wholeTTL is what the whole window is kept for.
	shape := func(what string, reads []keyDailyCall, windowArgs int, wholeTTL time.Duration) {
		want := wanted
		wantTTLs := []time.Duration{wholeTTL}
		if want == nil {
			want = []keyWindowPart{{start: w.start, end: w.end}}
		} else {
			wantTTLs = nil
			for _, p := range want {
				ttl := p.ttl
				if ttl == 0 {
					ttl = time.Minute
				}
				wantTTLs = append(wantTTLs, ttl)
			}
			slices.Sort(wantTTLs)
		}
		if len(reads) != len(want) {
			fail(what, "sent %d statements to usage_logs, want %d", len(reads), len(want))
		} else {
			for i, p := range want {
				got := reads[i].argTimes(windowArgs)
				if len(got) != 2 || !got[0].Equal(p.start) || !got[1].Equal(p.end) {
					fail(what, "statement %d read %v, want [%v, %v)", i, got, p.start, p.end)
				}
			}
		}
		if got := e.ttlsInRedis(); !slices.Equal(got, wantTTLs) {
			fail(what, "cache lifetimes %v, want %v", got, wantTTLs)
		}
	}
	warm := func(what string, again func() (any, error), first any) {
		got, err := again()
		if err != nil {
			fail(what, "warm call: %v", err)
		} else if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", first) {
			fail(what, "warm call differs from the cold one: %+v vs %+v", got, first)
		}
		if extra := e.probe.usageLogReads(); len(extra) != 0 {
			fail(what, "warm call sent %d statements to usage_logs", len(extra))
		}
	}

	// The model report of the three sources, asked as /v1/usage asks (no owner) or as a user's key, or a user alone.
	for _, source := range keyWindowModelSources {
		what := "models/" + source
		start()
		call := func() ([]ModelStat, error) {
			return e.repo.GetModelStatsWithFiltersBySource(e.ctx, w.start, w.end, w.user, w.key, 0, 0, nil, nil, nil, source)
		}
		got, err := call()
		reads := e.probe.usageLogReads()
		want, wantErr := e.oracle.getModelStatsWithFiltersBySource(e.ctx, w.start, w.end, w.user, w.key, 0, 0, "", nil, nil, nil, source, "", nil, nil)
		if err != nil || wantErr != nil {
			fail(what, "error %v, plain query %v", err, wantErr)
			continue
		}
		if diff := e.modelStatsDiff(got, want, wanted != nil); diff != "" {
			fail(what, "%s", diff)
		}
		if len(want) > 0 {
			e.report.nonEmpty++
		}
		if wanted != nil && source == usagestats.ModelSourceRequested {
			e.noteModelsInManyParts(w, wanted)
		}
		shape(what, reads, 0, e.wholeTTL(w))
		warm(what, func() (any, error) { return call() }, got)
	}

	// The same report asked in the shared filter shape, with other filters (a model, a billing mode) and with the
	// window declared in the filters as the user page declares it, or not, as the admin pages do.
	for variant, filters := range e.filterVariants(w) {
		what := fmt.Sprintf("usage filters/%d", variant)
		start()
		call := func() ([]ModelStat, error) {
			return e.repo.GetModelStatsWithUsageFiltersBySource(e.ctx, w.start, w.end, filters, usagestats.ModelSourceRequested)
		}
		got, err := call()
		reads := e.probe.usageLogReads()
		want, wantErr := e.oracle.getModelStatsWithFiltersBySource(e.ctx, w.start, w.end, filters.UserID, filters.APIKeyID, filters.AccountID, filters.GroupID,
			filters.Model, filters.RequestType, filters.Stream, filters.BillingType, usagestats.ModelSourceRequested, filters.BillingMode, filters.UpstreamModelMismatch, filters.NativeCompactionV2)
		if err != nil || wantErr != nil {
			fail(what, "error %v, plain query %v", err, wantErr)
			continue
		}
		if diff := e.modelStatsDiff(got, want, wanted != nil); diff != "" {
			fail(what, "%s", diff)
		}
		// Read whole, this variant keeps the cache's own TTL; only a key's window that is cut gets the longer ones.
		shape(what, reads, 0, time.Minute)
		warm(what, func() (any, error) { return call() }, got)
	}

	// The key summary.
	if w.key > 0 {
		what := "summary"
		start()
		call := func() (*usagestats.UsageStats, error) {
			return e.repo.GetAPIKeyStatsAggregated(e.ctx, w.key, w.start, w.end)
		}
		got, err := call()
		reads := e.probe.usageLogReads()
		want, wantErr := e.oracle.getAPIKeyStatsAggregatedUncached(e.ctx, w.key, w.start, w.end)
		if err != nil || wantErr != nil {
			fail(what, "error %v, plain query %v", err, wantErr)
			return failures
		}
		if diff := keyStatsDiff(got, want); diff != "" {
			fail(what, "%s", diff)
		}
		if want.TotalRequests > 0 {
			e.report.nonEmpty++
		}
		shape(what, reads, 1, e.wholeTTL(w))
		warm(what, func() (any, error) { return call() }, got)
	}
	return failures
}

// wholeTTL is what a window of a key that is read whole is kept for: a quarter hour if it is longer than a day.
func (e *keyWindowEnv) wholeTTL(w keyWindowDiff) time.Duration {
	if w.key > 0 && w.end.Sub(w.start) > 25*time.Hour {
		return keyWindowTTL
	}
	return time.Minute
}

func (e *keyWindowEnv) filterVariants(w keyWindowDiff) []UsageLogFilters {
	declare := func(f UsageLogFilters) UsageLogFilters {
		s, en := w.start, w.end
		f.StartTime, f.EndTime = &s, &en
		return f
	}
	base := UsageLogFilters{UserID: w.user, APIKeyID: w.key, ModelFilterSource: usagestats.ModelSourceRequested}
	byModel, byBilling := base, base
	byModel.Model = "claude-opus-4-5"
	byBilling.BillingMode = "token"
	userPage := declare(base)
	userPage.OmitAccountCost = true
	return []UsageLogFilters{base, userPage, declare(byModel), byBilling}
}

// ttlsInRedis are the lifetimes of everything the cache holds, shortest first.
func (e *keyWindowEnv) ttlsInRedis() []time.Duration {
	var out []time.Duration
	for _, key := range e.server.Keys() {
		out = append(out, e.server.TTL(key))
	}
	slices.Sort(out)
	return out
}

// noteModelsInManyParts counts a window whose parts have a model in common: the case in which adding by name matters.
func (e *keyWindowEnv) noteModelsInManyParts(w keyWindowDiff, wanted []keyWindowPart) {
	seen := map[string]int{}
	for _, p := range wanted {
		part, err := e.oracle.getModelStatsWithFiltersBySource(e.ctx, p.start, p.end, w.user, w.key, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", nil, nil)
		if err != nil {
			return
		}
		for _, m := range part {
			seen[m.Model]++
		}
	}
	for _, n := range seen {
		if n > 1 {
			e.report.modelInManyParts++
			return
		}
	}
}

func (c keyDailyCall) argTimes(from int) []time.Time {
	var out []time.Time
	for _, arg := range c.args[from:min(from+2, len(c.args))] {
		at, ok := arg.(time.Time)
		if !ok {
			return nil
		}
		out = append(out, at)
	}
	return out
}

// modelStatsDiff compares two model reports model by model, figure by figure, whatever the order, and then looks at the
// order: it must be by tokens, most first, and when the report was read in parts (cut) the model name breaks a tie.
func (e *keyWindowEnv) modelStatsDiff(got, want []ModelStat, cut bool) string {
	if (got == nil) != (want == nil) {
		return fmt.Sprintf("got nil=%v, want nil=%v", got == nil, want == nil)
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d models %v, want %d %v", len(got), modelNames(got), len(want), modelNames(want))
	}
	byName := make(map[string]ModelStat, len(want))
	for _, m := range want {
		byName[m.Model] = m
	}
	for _, g := range got {
		w, ok := byName[g.Model]
		if !ok {
			return fmt.Sprintf("model %q is not in the plain query's answer %v", g.Model, modelNames(want))
		}
		if g.Requests != w.Requests || g.InputTokens != w.InputTokens || g.OutputTokens != w.OutputTokens ||
			g.CacheCreationTokens != w.CacheCreationTokens || g.CacheReadTokens != w.CacheReadTokens || g.TotalTokens != w.TotalTokens ||
			!keyDailyClose(g.Cost, w.Cost) || !keyDailyClose(g.ActualCost, w.ActualCost) || !keyDailyClose(g.AccountCost, w.AccountCost) {
			return fmt.Sprintf("model %q: got %+v, want %+v", g.Model, g, w)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].TotalTokens < got[i].TotalTokens {
			return fmt.Sprintf("not ordered by tokens: %v", modelNames(got))
		}
		if got[i-1].TotalTokens == got[i].TotalTokens {
			e.report.ties++
			if cut && got[i-1].Model > got[i].Model {
				return fmt.Sprintf("a tie in tokens is not broken by the model name: %v", modelNames(got))
			}
		}
	}
	return ""
}

func modelNames(stats []ModelStat) []string {
	out := make([]string, len(stats))
	for i, m := range stats {
		out[i] = fmt.Sprintf("%q:%d", m.Model, m.TotalTokens)
	}
	return out
}

// keyStatsDiff compares two key summaries figure by figure; the average duration is compared as a decimal.
func keyStatsDiff(got, want *usagestats.UsageStats) string {
	if got == nil || want == nil {
		return fmt.Sprintf("got %v, want %v", got, want)
	}
	if got.TotalRequests != want.TotalRequests || got.TotalInputTokens != want.TotalInputTokens || got.TotalOutputTokens != want.TotalOutputTokens ||
		got.TotalCacheTokens != want.TotalCacheTokens || got.TotalCacheCreationTokens != want.TotalCacheCreationTokens ||
		got.TotalCacheReadTokens != want.TotalCacheReadTokens || got.TotalTokens != want.TotalTokens ||
		!keyDailyClose(got.TotalCost, want.TotalCost) || !keyDailyClose(got.TotalActualCost, want.TotalActualCost) ||
		!keyDailyClose(got.AverageDurationMs, want.AverageDurationMs) || (got.TotalAccountCost == nil) != (want.TotalAccountCost == nil) {
		return fmt.Sprintf("got %+v, want %+v", *got, *want)
	}
	return ""
}
