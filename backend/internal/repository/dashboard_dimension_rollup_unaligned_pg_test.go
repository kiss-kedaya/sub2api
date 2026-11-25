package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// Differential test of the rollup reads against a real PostgreSQL.
//
// One transaction holds a randomised usage_logs fixture (several users, keys, groups and models; requested_model
// empty, blank or different from model; NULL durations and groups; costs with a decimal tail; rows exactly on,
// one microsecond before and one after every hour boundary) and the rollups the production aggregation job
// computes from it. Temp tables shadow the real ones, so nothing outside the transaction is touched and other
// tests' data cannot leak in. Random windows (hour-aligned or not, ending in the open hour, in the future or in a
// closed hour, shorter than an hour, spanning days) are then answered twice: by the rollup reader (the code under
// test) and by the plain raw query the caller would otherwise run. The answers must match item by item.
//
// The integration job runs it with the Docker PostgreSQL. To run it anywhere else, point
// ROLLUP_DIFF_TEST_POSTGRES_DSN at a scratch database that has the migrations applied (everything the test writes
// goes to temp tables inside one transaction that is rolled back):
//
//	ROLLUP_DIFF_TEST_POSTGRES_DSN='postgres://user@127.0.0.1:5432/db?sslmode=disable' \
//	ROLLUP_DIFF_WINDOWS=1000 go test ./internal/repository/ -run TestRollupUnalignedDifferentialPostgres -v

var rollupDiffZones = []string{"Asia/Shanghai", "UTC", "America/Los_Angeles"}

type rollupDiffConfig struct {
	zone    string
	windows int // random and hand-picked windows
	rows    int // random usage_logs rows (boundary rows come on top)
	seed    int64
}

func TestRollupUnalignedDifferentialPostgres(t *testing.T) {
	dsn := os.Getenv("ROLLUP_DIFF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ROLLUP_DIFF_TEST_POSTGRES_DSN not set; the integration job runs the same check on its PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	seed := int64(rollupDiffEnvInt("ROLLUP_DIFF_SEED", 20261125))
	for i, zone := range rollupDiffZones {
		t.Run(zone, func(t *testing.T) {
			runRollupDifferential(t, db, rollupDiffConfig{
				zone:    zone,
				windows: rollupDiffEnvInt("ROLLUP_DIFF_WINDOWS", 200),
				rows:    rollupDiffEnvInt("ROLLUP_DIFF_ROWS", 40000),
				seed:    seed + int64(i),
			})
		})
	}
}

func rollupDiffEnvInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// statementProbe records the statements a repository sends, so the test can tell which path answered.
type statementProbe struct {
	inner   sqlExecutor
	mu      sync.Mutex
	queries []string
}

func (p *statementProbe) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.inner.ExecContext(ctx, query, args...)
}

func (p *statementProbe) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	p.mu.Lock()
	p.queries = append(p.queries, query)
	p.mu.Unlock()
	return p.inner.QueryContext(ctx, query, args...)
}

// take returns what was recorded since the last call.
func (p *statementProbe) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	queries := p.queries
	p.queries = nil
	return queries
}

// classify counts the statements that read the rollups and the ones that read usage_logs.
func classifyStatements(queries []string) (rollupReads, rawReads int) {
	for _, query := range queries {
		switch {
		case strings.Contains(query, "ORDER BY bucket_start"):
			rollupReads++
		case strings.Contains(query, "FROM usage_logs"):
			rawReads++
		}
	}
	return rollupReads, rawReads
}

type diffWindow struct {
	label      string
	start, end time.Time
}

func (w diffWindow) String() string {
	layout := "01-02 15:04:05.000000"
	loc := timezone.Location()
	return fmt.Sprintf("%s [%s, %s)", w.label, w.start.In(loc).Format(layout), w.end.In(loc).Format(layout))
}

type rollupDiffReport struct {
	windows, comparisons                   int
	rollupWithHeadAndTail, rollupHeadOnly  int
	rollupTailOnly, rollupOnly, fellBack   int
	engagedWhereExpected                   int
	userScopedRollup, notEligibleStayedRaw int
}

type rollupDiffEnv struct {
	t        *testing.T
	ctx      context.Context
	cancel   context.CancelFunc
	tx       *sql.Tx
	probe    *statementProbe
	repo     *usageLogRepository // reads through the rollups: the code under test
	oracle   *usageLogRepository // only its raw queries are used
	cfg      rollupDiffConfig
	t0, now  time.Time
	open     time.Time // start of the open hour
	firstHr  time.Time // first bucket with rollup rows
	users    []int64
	idleUser int64
	report   rollupDiffReport
}

func runRollupDifferential(t *testing.T, db *sql.DB, cfg rollupDiffConfig) {
	t.Helper()
	useRollupTestTimezone(t, cfg.zone)
	for attempt := 1; attempt <= 3; attempt++ {
		if !runRollupDifferentialOnce(t, db, cfg) {
			return
		}
		t.Logf("attempt %d: the local hour rolled over during the run; starting again", attempt)
	}
	t.Fatal("the local hour rolled over in three attempts in a row")
}

// runRollupDifferentialOnce reports true when the clock crossed an hour boundary mid-run: the rollups and the
// open hour then no longer line up with what the fixture assumed, and the whole run is repeated.
func runRollupDifferentialOnce(t *testing.T, db *sql.DB, cfg rollupDiffConfig) (rolledOver bool) {
	t.Helper()
	// Leave room for the run: a window that straddles the top of the hour would see a moving open hour.
	if untilNext := floorLocalHour(time.Now()).Add(time.Hour).Sub(time.Now()); untilNext < 90*time.Second {
		time.Sleep(untilNext + 2*time.Second)
	}
	e := newRollupDiffEnv(t, db, cfg)
	defer e.close() // a repeated run must not keep the previous transaction open
	rolled := func() bool { return floorLocalHour(time.Now()).After(e.open) }

	rnd := rand.New(rand.NewSource(cfg.seed))
	var failures []string
	for _, w := range e.windows(rnd, cfg.windows) {
		found := e.compareWindow(rnd, w)
		if rolled() {
			return true
		}
		e.report.windows++
		failures = append(failures, found...)
	}
	scenarioFailures := e.runBrokenRollupScenarios()
	if rolled() {
		return true
	}
	failures = append(failures, scenarioFailures...)

	r := e.report
	t.Logf("%s: %d windows, %d comparisons against the raw query; rollup path engaged for %d (head+tail %d, head only %d, tail only %d, rollups only %d), "+
		"raw fallback %d; engaged exactly where expected %d/%d; user-scoped rollup answers %d; key-scoped queries left on the raw path %d",
		cfg.zone, r.windows, r.comparisons, r.rollupWithHeadAndTail+r.rollupHeadOnly+r.rollupTailOnly+r.rollupOnly,
		r.rollupWithHeadAndTail, r.rollupHeadOnly, r.rollupTailOnly, r.rollupOnly, r.fellBack, r.engagedWhereExpected, r.windows, r.userScopedRollup, r.notEligibleStayedRaw)
	if len(failures) > 10 {
		failures = append(failures[:10], fmt.Sprintf("... and %d more", len(failures)-10))
	}
	require.Empty(t, failures, strings.Join(failures, "\n"))
	// The point of the test is the unaligned path: make sure it was really exercised.
	require.Greater(t, r.rollupWithHeadAndTail+r.rollupHeadOnly, r.windows/10, "too few windows read an unaligned head")
	require.Greater(t, r.fellBack, 0, "no window fell back to the raw path")
	return false
}

func newRollupDiffEnv(t *testing.T, db *sql.DB, cfg rollupDiffConfig) *rollupDiffEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	t.Cleanup(cancel)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	exec := func(query string, args ...any) {
		t.Helper()
		_, execErr := tx.ExecContext(ctx, query, args...)
		require.NoError(t, execErr, query)
	}

	// The raw queries format dates in the session zone; the aggregation job and the reader use the app zone.
	exec(`SELECT set_config('TimeZone', $1, true)`, cfg.zone)
	for _, name := range []string{
		"usage_dashboard_hourly", "usage_dashboard_hourly_users", "usage_dashboard_hourly_dimensions",
		"usage_dashboard_hourly_dimension_coverage", "usage_dashboard_daily", "usage_dashboard_daily_users",
		"usage_dashboard_aggregation_watermark",
	} {
		exec(fmt.Sprintf("CREATE TEMP TABLE %s (LIKE public.%s INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES) ON COMMIT DROP", name, name))
	}
	for _, name := range []string{"usage_logs", "users", "groups"} {
		exec(fmt.Sprintf("CREATE TEMP TABLE %s (LIKE public.%s INCLUDING DEFAULTS INCLUDING CONSTRAINTS) ON COMMIT DROP", name, name))
	}
	exec(`CREATE INDEX ON usage_logs (created_at)`)
	exec(`CREATE INDEX ON usage_logs (user_id, created_at)`)

	now := time.Now()
	open := floorLocalHour(now)
	t0 := open.Add(-5*24*time.Hour - 97*time.Minute) // the first bucket starts in the middle of its hour of data
	tEnd := now.Add(-3 * time.Second)
	rows := func(source, tsExpr string, args ...any) { exec(rollupFixtureSQL(source, tsExpr), args...) }
	exec(`SELECT setseed($1::double precision)`, float64(cfg.seed%1000)/1000)
	rows(`(SELECT random() AS r_time FROM generate_series(1, $3::int))`,
		`$1::timestamptz + ($2::timestamptz - $1::timestamptz) * d.r_time`, t0, tEnd, cfg.rows)
	// Rows on, one microsecond before and one after every hour boundary of the fixture (local midnights included).
	boundaryHours := int(tEnd.Sub(ceilLocalHour(t0)) / time.Hour)
	rows(`(SELECT $1::timestamptz + k * interval '1 hour' + jitter * interval '1 microsecond' AS ts
		FROM generate_series(0, $2::int) AS k CROSS JOIN unnest(ARRAY[-1, 0, 1]) AS jitter)`,
		`d.ts`, ceilLocalHour(t0), boundaryHours)
	// A trickle in the last minutes, which only the open hour's raw read can see.
	rows(`(SELECT $1::timestamptz - g * interval '61 seconds' AS ts FROM generate_series(1, $2::int) AS g)`,
		`d.ts`, tEnd, 40)
	exec(`ANALYZE usage_logs`)

	agg := newDashboardAggregationRepositoryWithSQL(tx)
	agg.SetDashboardDimensionRollupsEnabled(true)
	require.NoError(t, agg.AggregateRange(ctx, t0, now))
	require.NoError(t, agg.UpdateAggregationWatermark(ctx, time.Now()))

	probe := &statementProbe{inner: tx}
	repo := newUsageLogRepositoryWithSQL(nil, probe)
	repo.SetDashboardRollupReadEnabled(true)
	oracle := newUsageLogRepositoryWithSQL(nil, tx)
	oracle.SetDashboardRollupReadEnabled(true) // keeps the legacy hourly aggregates out of the raw trend query

	e := &rollupDiffEnv{t: t, ctx: ctx, cancel: cancel, tx: tx, probe: probe, repo: repo, oracle: oracle, cfg: cfg, t0: t0, now: now, open: open, idleUser: 9000999}
	for u := int64(9000101); u <= 9000108; u++ {
		e.users = append(e.users, u)
	}
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT MIN(bucket_start) FROM usage_dashboard_hourly`).Scan(&e.firstHr))
	var total int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs`).Scan(&total))
	require.Greater(t, total, cfg.rows)
	return e
}

// rollupFixtureSQL inserts usage_logs rows whose timestamp comes from the source. The random draws are made once
// per row in a materialised CTE so that every column of the row sees the same ones.
func rollupFixtureSQL(source, tsExpr string) string {
	return `
WITH d AS MATERIALIZED (
	SELECT src.*,
		9000101 + floor(random() * random() * 8)::bigint AS uid,
		floor(random() * 3)::int AS key_slot,
		floor(random() * 5)::int AS account_slot,
		CASE WHEN random() < 0.12 THEN NULL ELSE 9000001 + floor(random() * 4)::bigint END AS gid,
		(ARRAY['claude-opus-4-5', 'claude-sonnet-4-5', 'gpt-5', 'gemini-2.5-pro', 'glm-4.6'])[1 + floor(random() * 5)::int] AS model,
		random() AS r_req, random() AS r_up, random() AS r_in_ep, random() AS r_up_ep, random() AS r_in, random() AS r_out,
		random() AS r_cc, random() AS r_cr, random() AS r_cost, random() AS r_act, random() AS r_asc, random() AS r_arm, random() AS r_dur
	FROM ` + source + ` AS src
)
INSERT INTO usage_logs (
	user_id, api_key_id, account_id, group_id, model, requested_model, upstream_model, inbound_endpoint, upstream_endpoint,
	input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost,
	account_stats_cost, account_rate_multiplier, stream, duration_ms, created_at
)
SELECT
	d.uid, d.uid * 10 + d.key_slot, 1 + d.account_slot, d.gid, d.model,
	CASE WHEN d.r_req < 0.18 THEN '' WHEN d.r_req < 0.28 THEN '   ' WHEN d.r_req < 0.40 THEN ' ' || d.model || ' '
		WHEN d.r_req < 0.60 THEN (ARRAY['alias-a', 'alias-b', 'alias-c'])[1 + floor(d.r_req * 1000)::int % 3]
		WHEN d.r_req < 0.66 THEN NULL ELSE d.model END,
	CASE WHEN d.r_up < 0.15 THEN NULL WHEN d.r_up < 0.25 THEN '' WHEN d.r_up < 0.30 THEN '  '
		WHEN d.r_up < 0.60 THEN (ARRAY['up-x', 'up-y'])[1 + floor(d.r_up * 1000)::int % 2] ELSE d.model END,
	CASE WHEN d.r_in_ep < 0.08 THEN NULL WHEN d.r_in_ep < 0.15 THEN '' WHEN d.r_in_ep < 0.20 THEN '  '
		ELSE (ARRAY['/v1/messages', '/v1/chat/completions', '/v1/responses', ' /v1/embeddings '])[1 + floor(d.r_in_ep * 1000)::int % 4] END,
	CASE WHEN d.r_up_ep < 0.08 THEN NULL WHEN d.r_up_ep < 0.15 THEN ''
		ELSE (ARRAY['/v1/messages', '/responses', '/chat/completions'])[1 + floor(d.r_up_ep * 1000)::int % 3] END,
	floor(d.r_in * 3000)::int, floor(d.r_out * 1500)::int,
	CASE WHEN d.r_cc < 0.5 THEN 0 ELSE floor(d.r_cc * 800)::int END,
	CASE WHEN d.r_cr < 0.5 THEN 0 ELSE floor(d.r_cr * 8000)::int END,
	round((d.r_cost * 5)::numeric, 10),
	round((d.r_cost * 5 * (0.2 + d.r_act * 1.6))::numeric, 10),
	CASE WHEN d.r_asc < 0.5 THEN NULL ELSE round((d.r_asc * 3)::numeric, 10) END,
	CASE WHEN d.r_arm < 0.35 THEN NULL ELSE round((d.r_arm * 2)::numeric, 4) END,
	d.r_dur > 0.5,
	CASE WHEN d.r_dur < 0.2 THEN NULL ELSE floor(d.r_dur * 50000)::int END,
	` + tsExpr + `
FROM d`
}

// windows are the ranges to compare: the shapes the usage pages send first, then random ones.
func (e *rollupDiffEnv) windows(rnd *rand.Rand, n int) []diffWindow {
	now, open := e.now, e.open
	minute := now.Truncate(time.Minute)
	var out []diffWindow
	add := func(label string, start, end time.Time) {
		if end.After(start) {
			out = append(out, diffWindow{label: label, start: start, end: end})
		}
	}
	midnight := timezone.StartOfDay(now)
	add("last 24h", minute.Add(-24*time.Hour), minute)
	add("last 3d", minute.Add(-72*time.Hour), minute)
	add("last 7d (starts before the data)", minute.Add(-7*24*time.Hour), minute)
	add("24h from a boundary", open.Add(-24*time.Hour), now)
	add("closed hours, aligned", open.Add(-30*time.Hour), open.Add(-6*time.Hour))
	add("closed hours, unaligned", open.Add(-30*time.Hour+17*time.Minute), open.Add(-6*time.Hour+25*time.Minute))
	add("one whole hour", open.Add(-10*time.Hour), open.Add(-9*time.Hour))
	add("one hour shifted by a nanosecond", open.Add(-10*time.Hour+time.Nanosecond), open.Add(-9*time.Hour+time.Nanosecond))
	add("inside one hour", open.Add(-10*time.Hour+5*time.Minute), open.Add(-10*time.Hour+35*time.Minute))
	add("crosses one boundary", open.Add(-10*time.Hour+50*time.Minute), open.Add(-9*time.Hour+20*time.Minute))
	add("one closed hour between two edges", open.Add(-10*time.Hour+50*time.Minute), open.Add(-8*time.Hour+20*time.Minute))
	add("open hour only", open.Add(time.Second), now)
	add("ends in the future", open.Add(-12*time.Hour+7*time.Minute), now.Add(4*time.Hour))
	add("whole local days", midnight.AddDate(0, 0, -3), midnight.AddDate(0, 0, -1))
	add("local days, unaligned", midnight.AddDate(0, 0, -3).Add(17*time.Minute), midnight.AddDate(0, 0, -1).Add(25*time.Minute))
	add("from local midnight to now", midnight, minute)
	add("across local midnight", midnight.Add(-time.Hour-13*time.Minute), midnight.Add(2*time.Hour+7*time.Minute))
	add("right after local midnight", midnight.Add(time.Microsecond), midnight.AddDate(0, 0, 1))

	for len(out) < n {
		var start time.Time
		switch rnd.Intn(6) {
		case 0:
			start = open.Add(-time.Duration(1+rnd.Intn(118)) * time.Hour)
		case 1:
			start = now.Add(-time.Duration(rnd.Int63n(int64(5*24*time.Hour+3*time.Hour))) - time.Minute)
		case 2:
			start = now.Add(-time.Duration(1+rnd.Intn(7200)) * time.Minute).Truncate(time.Minute)
		case 3:
			start = open.Add(-time.Duration(1+rnd.Intn(118))*time.Hour + time.Microsecond)
		case 4:
			start = open.Add(-time.Duration(1+rnd.Intn(118))*time.Hour - time.Microsecond)
		default:
			start = e.t0.Add(-time.Duration(rnd.Intn(180)) * time.Minute)
		}
		var end time.Time
		switch rnd.Intn(7) {
		case 0:
			end = now
		case 1:
			end = now.Add(time.Duration(1+rnd.Intn(360)) * time.Minute)
		case 2:
			end = open.Add(-time.Duration(rnd.Intn(100)) * time.Hour)
		case 3:
			end = open.Add(-time.Duration(rnd.Intn(100))*time.Hour + time.Duration(1+rnd.Intn(59))*time.Minute + time.Duration(rnd.Intn(60))*time.Second)
		case 4:
			end = start.Add(time.Duration(1+rnd.Intn(180)) * time.Minute)
		case 5:
			end = start.Add(time.Hour + time.Duration(rnd.Intn(3)-1))
		default:
			end = now.Truncate(time.Minute)
		}
		add("random", start, end)
	}
	return out
}

func (e *rollupDiffEnv) pickUser(rnd *rand.Rand) int64 {
	switch rnd.Intn(6) {
	case 0, 1:
		return 0
	case 2:
		return e.users[0] // the heaviest user
	case 3:
		return e.idleUser
	default:
		return e.users[rnd.Intn(len(e.users))]
	}
}

// expectRollupPath says whether the reader should answer from the rollups: there has to be a whole closed hour
// inside the window, and the first of them must not start before the first processed hour.
func (e *rollupDiffEnv) expectRollupPath(w diffWindow) bool {
	effectiveEnd := w.end
	if effectiveEnd.After(time.Now()) {
		effectiveEnd = time.Now()
	}
	from := ceilLocalHour(w.start)
	cutoff := floorLocalHour(effectiveEnd)
	if cutoff.After(e.open) {
		cutoff = e.open
	}
	return cutoff.After(from) && !from.Before(e.firstHr)
}

// compareWindow answers one window through every entry point and returns the differences found.
func (e *rollupDiffEnv) compareWindow(rnd *rand.Rand, w diffWindow) []string {
	var found []string
	check := func(shape string, diff string) {
		e.report.comparisons++
		if diff != "" {
			found = append(found, fmt.Sprintf("[%s] %s %s: %s", e.cfg.zone, w, shape, diff))
		}
	}
	run := func(shape string, answer func() (string, error)) (rollupReads, rawReads int) {
		e.probe.take()
		diff, err := answer()
		rollupReads, rawReads = classifyStatements(e.probe.take())
		if err != nil {
			diff = "error: " + err.Error()
		}
		check(shape, diff)
		return rollupReads, rawReads
	}
	ctx, start, end := e.ctx, w.start, w.end

	// The reference shape (everyone's models): the path taken must match what the window allows.
	rollupReads, rawReads := run("global models", func() (string, error) {
		got, err := e.repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, UsageLogFilters{}, usagestats.ModelSourceRequested)
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getModelStatsWithFiltersBySource(ctx, start, end, 0, 0, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", nil, nil)
		return diffModelStats(got, want), err
	})
	engaged := rollupReads > 0
	expected := e.expectRollupPath(w)
	if engaged == expected {
		e.report.engagedWhereExpected++
	} else {
		found = append(found, fmt.Sprintf("[%s] %s: rollup path engaged=%v, expected=%v", e.cfg.zone, w, engaged, expected))
	}
	head := ceilLocalHour(w.start).After(w.start.UTC())
	switch {
	case !engaged:
		e.report.fellBack++
	case head && rawReads >= 2:
		e.report.rollupWithHeadAndTail++
	case head:
		e.report.rollupHeadOnly++
	case rawReads >= 1:
		e.report.rollupTailOnly++
	default:
		e.report.rollupOnly++
	}

	user := e.pickUser(rnd)
	source := []string{usagestats.ModelSourceRequested, usagestats.ModelSourceUpstream, usagestats.ModelSourceMapping}[rnd.Intn(3)]
	rollupReads, _ = run(fmt.Sprintf("models(user=%d, %s)", user, source), func() (string, error) {
		got, err := e.repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, UsageLogFilters{UserID: user}, source)
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getModelStatsWithFiltersBySource(ctx, start, end, user, 0, 0, 0, "", nil, nil, nil, source, "", nil, nil)
		return diffModelStats(got, want), err
	})
	if user > 0 && rollupReads > 0 {
		e.report.userScopedRollup++
	}

	for _, granularity := range []string{"hour", "day"} {
		trendUser := e.pickUser(rnd)
		run(fmt.Sprintf("trend(user=%d, %s)", trendUser, granularity), func() (string, error) {
			got, err := e.repo.GetUsageTrendWithUsageFilters(ctx, start, end, granularity, UsageLogFilters{UserID: trendUser})
			if err != nil {
				return "", err
			}
			want, err := e.oracle.getUsageTrendWithFilters(ctx, start, end, granularity, trendUser, 0, 0, 0, "", "", nil, nil, nil, "", nil, nil)
			return diffTrend(got, want), err
		})
	}

	user = e.pickUser(rnd)
	omit := rnd.Intn(2) == 0
	run(fmt.Sprintf("groups(user=%d, omit=%v)", user, omit), func() (string, error) {
		got, err := e.repo.GetGroupStatsWithUsageFilters(ctx, start, end, UsageLogFilters{UserID: user, OmitAccountCost: omit})
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getGroupStatsWithFilters(ctx, start, end, user, 0, 0, 0, "", nil, nil, nil, "", nil, nil, omit)
		return diffGroups(got, want, omit), err
	})

	user = e.pickUser(rnd)
	scope := []string{"", usagestats.StatsScopeSummary, usagestats.StatsScopeEndpoints}[rnd.Intn(3)]
	omit = rnd.Intn(2) == 0
	run(fmt.Sprintf("stats(user=%d, scope=%q, omit=%v)", user, scope, omit), func() (string, error) {
		filters := UsageLogFilters{UserID: user, StartTime: &start, EndTime: &end, StatsScope: scope, OmitAccountCost: omit}
		got, err := e.repo.GetStatsWithFilters(ctx, filters)
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getStatsWithFiltersRaw(ctx, filters)
		return diffStats(got, want, scope, omit), err
	})

	granularity := []string{"hour", "day"}[rnd.Intn(2)]
	run("user trend ("+granularity+")", func() (string, error) {
		got, err := e.repo.GetUserUsageTrend(ctx, start, end, granularity, 3)
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getUserUsageTrendRaw(ctx, start, end, granularity, 3)
		return diffUserTrend(got, want), err
	})
	run("spending ranking", func() (string, error) {
		got, err := e.repo.GetUserSpendingRanking(ctx, start, end, 3)
		if err != nil {
			return "", err
		}
		want, err := e.oracle.getUserSpendingRankingRaw(ctx, start, end, 3)
		return diffRanking(got, want), err
	})

	// Queries scoped to one API key have no rollup dimension: they must not be answered from the rollups.
	if rnd.Intn(4) == 0 {
		key := e.users[rnd.Intn(len(e.users))]*10 + int64(rnd.Intn(3))
		rollupReads, _ = run(fmt.Sprintf("models(key=%d)", key), func() (string, error) {
			got, err := e.repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, UsageLogFilters{APIKeyID: key}, usagestats.ModelSourceRequested)
			if err != nil {
				return "", err
			}
			want, err := e.oracle.getModelStatsWithFiltersBySource(ctx, start, end, 0, key, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", nil, nil)
			return diffModelStats(got, want), err
		})
		if rollupReads == 0 {
			e.report.notEligibleStayedRaw++
		} else {
			found = append(found, fmt.Sprintf("[%s] %s: a key-scoped query read the rollups", e.cfg.zone, w))
		}
	}
	return found
}

// runBrokenRollupScenarios damages the rollups one way at a time (savepoint, damage, ask, roll back). The reader
// must notice damage inside the part of the window it would read from the rollups and fall back to the raw query,
// and it must not be put off by damage in the parts it reads raw: the hour before the first whole hour (the head)
// and the open hour.
func (e *rollupDiffEnv) runBrokenRollupScenarios() []string {
	var found []string
	start := e.now.Add(-30*time.Hour + 17*time.Minute + 3*time.Second).Truncate(time.Microsecond)
	w := diffWindow{label: "scenario window", start: start, end: e.now}
	from := ceilLocalHour(start)
	headHour := floorLocalHour(start)
	middle := from.Add(5 * time.Hour)
	lastClosed := e.open.Add(-time.Hour)
	heavy := e.users[0]

	type scenario struct {
		name       string
		damage     string
		args       []any
		wantGlobal bool // reader still answers the everyone's-models question from the rollups
		wantHeavy  bool // ... and the heavy user's
	}
	scenarios := []scenario{
		{name: "undamaged", damage: `SELECT 1`, wantGlobal: true, wantHeavy: true},
		{name: "coverage row missing in the middle", damage: `DELETE FROM usage_dashboard_hourly_dimension_coverage WHERE bucket_start = $1`, args: []any{middle}},
		{name: "model rows missing in the middle", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start = $1 AND dimension_type = 'model'`, args: []any{middle}, wantHeavy: true},
		{name: "heavy user's model rows missing in the middle", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start = $1 AND dimension_type = 'user_model' AND user_id = $2`, args: []any{middle, heavy}, wantGlobal: true},
		{name: "first rollup hour missing", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start = $1 AND dimension_type IN ('model', 'user_model')`, args: []any{from}},
		{name: "last closed hour missing", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start = $1 AND dimension_type IN ('model', 'user_model')`, args: []any{lastClosed}},
		{name: "head hour's rollup rows missing (read raw anyway)", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start = $1`, args: []any{headHour}, wantGlobal: true, wantHeavy: true},
		{name: "head hour's coverage row missing (read raw anyway)", damage: `DELETE FROM usage_dashboard_hourly_dimension_coverage WHERE bucket_start = $1`, args: []any{headHour}, wantGlobal: true, wantHeavy: true},
		{name: "open hour's rollup rows missing (read raw anyway)", damage: `DELETE FROM usage_dashboard_hourly_dimensions WHERE bucket_start >= $1`, args: []any{e.open}, wantGlobal: true, wantHeavy: true},
		{name: "rollups start after the window's first whole hour", damage: `DELETE FROM usage_dashboard_hourly WHERE bucket_start < $1`, args: []any{from.Add(time.Hour)}},
		{name: "watermark stale from an earlier hour", damage: `UPDATE usage_dashboard_aggregation_watermark SET last_aggregated_at = $1`, args: []any{e.open.Add(-3 * time.Hour)}},
	}
	for _, sc := range scenarios {
		e.mustExec("SAVEPOINT broken")
		e.mustExec(sc.damage, sc.args...)
		for _, shape := range []struct {
			name   string
			user   int64
			expect bool
		}{{"global", 0, sc.wantGlobal}, {"heavy user", heavy, sc.wantHeavy}} {
			e.report.comparisons++
			filters := UsageLogFilters{UserID: shape.user}
			_, covered, err := e.repo.GetModelStatsWithRollups(e.ctx, w.start, w.end, filters, usagestats.ModelSourceRequested)
			if err != nil {
				found = append(found, fmt.Sprintf("[%s] %s / %s: %v", e.cfg.zone, sc.name, shape.name, err))
			} else if covered != shape.expect {
				found = append(found, fmt.Sprintf("[%s] %s / %s: answered from the rollups=%v, expected %v", e.cfg.zone, sc.name, shape.name, covered, shape.expect))
			}
			// Whatever the path, the public answer must be the raw one.
			got, err := e.repo.GetModelStatsWithUsageFiltersBySource(e.ctx, w.start, w.end, filters, usagestats.ModelSourceRequested)
			if err == nil {
				var want []ModelStat
				want, err = e.oracle.getModelStatsWithFiltersBySource(e.ctx, w.start, w.end, shape.user, 0, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", nil, nil)
				if err == nil {
					if diff := diffModelStats(got, want); diff != "" {
						found = append(found, fmt.Sprintf("[%s] %s / %s: %s", e.cfg.zone, sc.name, shape.name, diff))
					}
				}
			}
			if err != nil {
				found = append(found, fmt.Sprintf("[%s] %s / %s: %v", e.cfg.zone, sc.name, shape.name, err))
			}
		}
		e.mustExec("ROLLBACK TO SAVEPOINT broken")
		e.mustExec("RELEASE SAVEPOINT broken")
	}
	return found
}

func (e *rollupDiffEnv) close() {
	_ = e.tx.Rollback()
	e.cancel()
}

func (e *rollupDiffEnv) mustExec(query string, args ...any) {
	e.t.Helper()
	_, err := e.tx.ExecContext(e.ctx, query, args...)
	require.NoError(e.t, err, query)
}

// rollupFloatClose compares costs. The sums are exact decimals in the database; the reader adds one float per
// rolled-up hour, and the rollup stores account_cost rounded to 10 decimals, so a few 1e-11 per hour may differ.
// A missing or doubled row moves a total by at least the smallest fixture cost (1e-4 or more).
func rollupFloatClose(a, b float64) bool {
	return math.Abs(a-b) <= 1e-7+1e-10*math.Max(math.Abs(a), math.Abs(b))
}

func diffModelStats(got, want []ModelStat) string {
	index := func(rows []ModelStat) map[string]ModelStat {
		m := make(map[string]ModelStat, len(rows))
		for _, row := range rows {
			m[row.Model] = row
		}
		return m
	}
	g, w := index(got), index(want)
	if len(g) != len(w) {
		return fmt.Sprintf("%d models, want %d", len(g), len(w))
	}
	for key, wantRow := range w {
		gotRow, ok := g[key]
		if !ok {
			return fmt.Sprintf("model %q missing", key)
		}
		if gotRow.Requests != wantRow.Requests || gotRow.InputTokens != wantRow.InputTokens || gotRow.OutputTokens != wantRow.OutputTokens ||
			gotRow.CacheCreationTokens != wantRow.CacheCreationTokens || gotRow.CacheReadTokens != wantRow.CacheReadTokens || gotRow.TotalTokens != wantRow.TotalTokens ||
			!rollupFloatClose(gotRow.Cost, wantRow.Cost) || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) || !rollupFloatClose(gotRow.AccountCost, wantRow.AccountCost) {
			return fmt.Sprintf("model %q: got %+v, want %+v", key, gotRow, wantRow)
		}
	}
	return ""
}

func diffTrend(got, want []TrendDataPoint) string {
	index := func(rows []TrendDataPoint) map[string]TrendDataPoint {
		m := make(map[string]TrendDataPoint, len(rows))
		for _, row := range rows {
			m[row.Date] = row
		}
		return m
	}
	g, w := index(got), index(want)
	if len(g) != len(w) {
		return fmt.Sprintf("%d points, want %d", len(g), len(w))
	}
	for key, wantRow := range w {
		gotRow, ok := g[key]
		if !ok {
			return fmt.Sprintf("point %q missing", key)
		}
		if gotRow.Requests != wantRow.Requests || gotRow.InputTokens != wantRow.InputTokens || gotRow.OutputTokens != wantRow.OutputTokens ||
			gotRow.CacheCreationTokens != wantRow.CacheCreationTokens || gotRow.CacheReadTokens != wantRow.CacheReadTokens || gotRow.TotalTokens != wantRow.TotalTokens ||
			!rollupFloatClose(gotRow.Cost, wantRow.Cost) || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) {
			return fmt.Sprintf("point %q: got %+v, want %+v", key, gotRow, wantRow)
		}
	}
	return ""
}

func diffGroups(got, want []usagestats.GroupStat, omitAccountCost bool) string {
	index := func(rows []usagestats.GroupStat) map[int64]usagestats.GroupStat {
		m := make(map[int64]usagestats.GroupStat, len(rows))
		for _, row := range rows {
			m[row.GroupID] = row
		}
		return m
	}
	g, w := index(got), index(want)
	if len(g) != len(w) {
		return fmt.Sprintf("%d groups, want %d", len(g), len(w))
	}
	for key, wantRow := range w {
		gotRow, ok := g[key]
		if !ok {
			return fmt.Sprintf("group %d missing", key)
		}
		// A user-facing query leaves the account cost out of the raw read; the rollups always carry it.
		if gotRow.Requests != wantRow.Requests || gotRow.TotalTokens != wantRow.TotalTokens ||
			!rollupFloatClose(gotRow.Cost, wantRow.Cost) || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) ||
			(!omitAccountCost && !rollupFloatClose(gotRow.AccountCost, wantRow.AccountCost)) {
			return fmt.Sprintf("group %d: got %+v, want %+v", key, gotRow, wantRow)
		}
	}
	return ""
}

func diffEndpoints(name string, got, want []EndpointStat) string {
	index := func(rows []EndpointStat) map[string]EndpointStat {
		m := make(map[string]EndpointStat, len(rows))
		for _, row := range rows {
			m[row.Endpoint] = row
		}
		return m
	}
	g, w := index(got), index(want)
	if len(g) != len(w) {
		return fmt.Sprintf("%d %s endpoints, want %d", len(g), name, len(w))
	}
	for key, wantRow := range w {
		gotRow, ok := g[key]
		if !ok {
			return fmt.Sprintf("%s endpoint %q missing", name, key)
		}
		if gotRow.Requests != wantRow.Requests || gotRow.TotalTokens != wantRow.TotalTokens ||
			!rollupFloatClose(gotRow.Cost, wantRow.Cost) || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) {
			return fmt.Sprintf("%s endpoint %q: got %+v, want %+v", name, key, gotRow, wantRow)
		}
	}
	return ""
}

func diffStats(got, want *UsageStats, scope string, omitAccountCost bool) string {
	if got == nil || want == nil {
		return fmt.Sprintf("nil stats: got %v, want %v", got, want)
	}
	if scope != usagestats.StatsScopeEndpoints {
		if got.TotalRequests != want.TotalRequests || got.TotalInputTokens != want.TotalInputTokens || got.TotalOutputTokens != want.TotalOutputTokens ||
			got.TotalCacheCreationTokens != want.TotalCacheCreationTokens || got.TotalCacheReadTokens != want.TotalCacheReadTokens ||
			got.TotalCacheTokens != want.TotalCacheTokens || got.TotalTokens != want.TotalTokens ||
			!rollupFloatClose(got.TotalCost, want.TotalCost) || !rollupFloatClose(got.TotalActualCost, want.TotalActualCost) {
			return fmt.Sprintf("totals: got %+v, want %+v", *got, *want)
		}
		if !omitAccountCost && !rollupFloatClose(derefFloat64(got.TotalAccountCost), derefFloat64(want.TotalAccountCost)) {
			return fmt.Sprintf("account cost: got %v, want %v", derefFloat64(got.TotalAccountCost), derefFloat64(want.TotalAccountCost))
		}
		if math.Abs(got.AverageDurationMs-want.AverageDurationMs) > 1e-9*math.Max(1, math.Abs(want.AverageDurationMs)) {
			return fmt.Sprintf("average duration: got %v, want %v", got.AverageDurationMs, want.AverageDurationMs)
		}
	}
	if scope == usagestats.StatsScopeSummary {
		return ""
	}
	if diff := diffEndpoints("inbound", got.Endpoints, want.Endpoints); diff != "" {
		return diff
	}
	if scope == usagestats.StatsScopeEndpoints {
		return "" // the raw endpoint-only read has no upstream or path breakdown
	}
	if diff := diffEndpoints("upstream", got.UpstreamEndpoints, want.UpstreamEndpoints); diff != "" {
		return diff
	}
	return diffEndpoints("path", got.EndpointPaths, want.EndpointPaths)
}

func diffUserTrend(got, want []UserUsageTrendPoint) string {
	index := func(rows []UserUsageTrendPoint) map[string]UserUsageTrendPoint {
		m := make(map[string]UserUsageTrendPoint, len(rows))
		for _, row := range rows {
			m[fmt.Sprintf("%s|%d", row.Date, row.UserID)] = row
		}
		return m
	}
	g, w := index(got), index(want)
	if len(g) != len(w) {
		return fmt.Sprintf("%d points, want %d", len(g), len(w))
	}
	for key, wantRow := range w {
		gotRow, ok := g[key]
		if !ok {
			return fmt.Sprintf("point %q missing", key)
		}
		if gotRow.Requests != wantRow.Requests || gotRow.Tokens != wantRow.Tokens ||
			!rollupFloatClose(gotRow.Cost, wantRow.Cost) || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) {
			return fmt.Sprintf("point %q: got %+v, want %+v", key, gotRow, wantRow)
		}
	}
	return ""
}

func diffRanking(got, want *UserSpendingRankingResponse) string {
	if got == nil || want == nil {
		return fmt.Sprintf("nil ranking: got %v, want %v", got, want)
	}
	if got.TotalRequests != want.TotalRequests || got.TotalTokens != want.TotalTokens || !rollupFloatClose(got.TotalActualCost, want.TotalActualCost) {
		return fmt.Sprintf("totals: got %+v, want %+v", *got, *want)
	}
	if len(got.Ranking) != len(want.Ranking) {
		return fmt.Sprintf("%d rows, want %d", len(got.Ranking), len(want.Ranking))
	}
	for i, wantRow := range want.Ranking {
		gotRow := got.Ranking[i]
		if gotRow.UserID != wantRow.UserID || gotRow.Requests != wantRow.Requests || gotRow.Tokens != wantRow.Tokens || !rollupFloatClose(gotRow.ActualCost, wantRow.ActualCost) {
			return fmt.Sprintf("rank %d: got %+v, want %+v", i+1, gotRow, wantRow)
		}
	}
	return ""
}
