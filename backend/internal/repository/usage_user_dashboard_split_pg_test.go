package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
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

// Differential test of the split user dashboard against a real PostgreSQL.
//
// GetUserDashboardStats answers the usage part of a user's dashboard in two parts once the local day is an hour old
// (userDashboardUsage): the days that are over, and today, each cached on its own, and adds the two answers up.
// Whatever the cut, that has to be the answer of one plain pass over the whole window, field by field. The fixture
// lives in one transaction (temp tables shadow usage_logs, groups, accounts and the hourly rollup of users, nothing
// outside them is touched): rows at random times of a whole year; rows on, one microsecond before and one after the
// cut, the start of the window and the open hour of every case, so that days, rollup hours and windows meet exactly
// on rows; failed requests (cost 0, left out of the platforms), rows without a duration, groups of every platform
// kind (their own, none, composite, unknown) and rows whose group and account have no platform. The rollup is
// written by the real aggregation statement in the zone of the case, so its hours are cut where production's are.
// Users come in every shape the two paths tell apart: with a rollup, with none (usage_logs only), with rows that
// carry no usage (daily-active-user rows), new today, and idle. The zones are the usage pages' (the application's
// own, UTC, DST days of 23, 25 and 23.5 hours, half-hour zones). Every case is answered twice, by the code under test
// (cold, warm and after the short entry lapsed) and by the plain whole-window pass, and the statements it sent and
// the lifetimes it gave its entries are checked too, not only the sums.
//
// The integration job runs it with the Docker PostgreSQL. To run it anywhere else, point
// ROLLUP_DIFF_TEST_POSTGRES_DSN at a scratch database that has the migrations applied (everything the test writes
// goes to temp tables inside one transaction that is rolled back):
//
//	ROLLUP_DIFF_TEST_POSTGRES_DSN='postgres://user@127.0.0.1:5432/db?sslmode=disable' \
//	USER_DASHBOARD_DIFF_CASES=40 go test ./internal/repository/ -run TestUserDashboardSplitDifferentialPostgres -v

var (
	// userDashboardDiffZones are the zones the application is set to (its days, and the cut of its rollup hours).
	userDashboardDiffZones = []string{"Asia/Shanghai", "UTC", "America/Los_Angeles", "Europe/London", "Asia/Kolkata", "Australia/Lord_Howe"}
	// userDashboardDiffDSTDays are local days of a zone that are not 24 hours long.
	userDashboardDiffDSTDays = map[string][][3]int{
		"America/Los_Angeles": {{2026, 3, 8}, {2026, 11, 1}},
		"Europe/London":       {{2026, 3, 29}, {2026, 10, 25}},
		"Australia/Lord_Howe": {{2026, 4, 5}, {2026, 10, 4}},
	}
)

type userDashboardDiffConfig struct {
	cases int // random instants per zone, on top of the hand-picked ones
	rows  int // random usage_logs rows; the boundary rows come on top
	seed  int64
}

func TestUserDashboardSplitDifferentialPostgres(t *testing.T) {
	dsn := os.Getenv("ROLLUP_DIFF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("ROLLUP_DIFF_TEST_POSTGRES_DSN not set; the integration job runs the same check on its PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	runUserDashboardDifferential(t, db, userDashboardDiffConfig{
		cases: rollupDiffEnvInt("USER_DASHBOARD_DIFF_CASES", 24),
		rows:  rollupDiffEnvInt("USER_DASHBOARD_DIFF_ROWS", 40000),
		seed:  int64(rollupDiffEnvInt("USER_DASHBOARD_DIFF_SEED", 20261126)),
	})
}

type userDashboardDiffUser struct {
	id     int64
	rollup string // "full": every hour in the rollup; "none": no rollup rows; "dau": rollup rows with no usage in them
	from   time.Time
	to     time.Time // the span of the user's random rows
	weight int       // share of the random rows
}

type userDashboardDiffCase struct {
	label string
	zone  string
	now   time.Time // what the repository's clock says
	user  int64
}

func (c userDashboardDiffCase) String() string {
	return fmt.Sprintf("%s user=%d zone=%s now=%s", c.label, c.user, c.zone, c.now.Format("2006-01-02 15:04:05.999999 MST"))
}

// midnight is the start of the local day of the case, worked out with the helper the handlers use for it (it loads
// the zone by name), apart from the code under test.
func (c userDashboardDiffCase) midnight() time.Time {
	return timezone.StartOfDayInUserLocation(c.now, c.zone)
}

// split says whether the day is old enough to be cut: an hour into the local day.
func (c userDashboardDiffCase) split() bool { return c.now.Sub(c.midnight()) >= time.Hour }

// windowStart is where the window of the answer starts: a lookback before the cut when it is cut (so the window is
// the same all day, and can be shared), a lookback before now when it is not.
func (c userDashboardDiffCase) windowStart() time.Time {
	if c.split() {
		return c.midnight().Add(-30 * 24 * time.Hour)
	}
	return c.now.Add(-30 * 24 * time.Hour)
}

func userDashboardDiffUsers(first, last time.Time) []userDashboardDiffUser {
	return []userDashboardDiffUser{
		{id: 9300001, rollup: "full", from: first, to: last, weight: 6},
		{id: 9300002, rollup: "full", from: first, to: last, weight: 2},
		{id: 9300003, rollup: "none", from: first, to: last, weight: 2},
		{id: 9300004, rollup: "dau", from: first, to: last, weight: 1},
		// Starts on the day of the main case: nothing before its midnight there, whole hours of rollup today.
		{id: 9300005, rollup: "full", from: time.Date(2026, 11, 25, 17, 0, 0, 0, time.UTC), to: last, weight: 1},
		{id: 9300006, rollup: "full", from: first, to: last, weight: 0}, // idle: no rows at all
	}
}

// userDashboardDiffCases are the hand-picked instants of one zone (the shapes the dashboard is asked in, the edges of
// a day and of its first hour, DST days) for every user, followed by random ones.
func userDashboardDiffCases(t *testing.T, rnd *rand.Rand, zone string, random int, users []userDashboardDiffUser) []userDashboardDiffCase {
	t.Helper()
	loc := loadZone(t, zone)
	var out []userDashboardDiffCase
	add := func(label string, now time.Time, user int64) {
		out = append(out, userDashboardDiffCase{label: label, zone: zone, now: now.In(loc), user: user})
	}
	var nows []time.Time
	day := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, loc) }
	for _, d := range [][3]int{{2026, 6, 15}, {2026, 11, 26}, {2026, 11, 27}} {
		midnight := day(d[0], time.Month(d[1]), d[2])
		nows = append(nows,
			midnight.Add(12*time.Hour+30*time.Minute),
			midnight.Add(time.Hour),                  // the first instant of the cut
			midnight.Add(time.Hour-time.Microsecond), // the last one of the whole answer
			midnight.Add(time.Hour+time.Microsecond),
			midnight,
			midnight.Add(time.Microsecond),
			midnight.AddDate(0, 0, 1).Add(-time.Microsecond), // the last instant of the day
		)
	}
	for _, d := range userDashboardDiffDSTDays[zone] {
		midnight := day(d[0], time.Month(d[1]), d[2])
		nows = append(nows, midnight.Add(12*time.Hour), midnight.Add(time.Hour), midnight.AddDate(0, 0, 1).Add(-time.Microsecond), midnight.AddDate(0, 0, 1).Add(time.Hour))
	}
	for _, now := range nows {
		for _, u := range users {
			add("picked", now, u.id)
		}
	}
	for range random {
		at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(rnd.Int63n(int64(290 * 24 * time.Hour)))).Truncate(time.Microsecond)
		add("random", at, users[rnd.Intn(len(users))].id)
	}
	return out
}

// userDashboardDiffProbe records the statements a repository sends.
type userDashboardDiffProbe struct {
	inner   sqlExecutor
	mu      sync.Mutex
	queries []string
}

func (p *userDashboardDiffProbe) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.inner.ExecContext(ctx, query, args...)
}

func (p *userDashboardDiffProbe) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	p.mu.Lock()
	p.queries = append(p.queries, strings.Join(strings.Fields(query), " "))
	p.mu.Unlock()
	return p.inner.QueryContext(ctx, query, args...)
}

// take returns the statements sent since the last call, oldest first.
func (p *userDashboardDiffProbe) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.queries
	p.queries = nil
	return out
}

// closedPlatformStatements counts the statements that stop at the cut: the per-platform breakdown of the closed part.
func closedPlatformStatements(queries []string) int {
	n := 0
	for _, q := range queries {
		if strings.Contains(q, "ul.created_at < $4") {
			n++
		}
	}
	return n
}

type userDashboardDiffReport struct {
	comparisons, split, whole, nonEmpty    int
	closedFromRollup, closedFromUsageLogs  int
	platformsInBothParts, notTwentyFourDay int
	newUserToday                           int
}

type userDashboardDiffEnv struct {
	t      *testing.T
	ctx    context.Context
	probe  *userDashboardDiffProbe
	repo   *usageLogRepository // the code under test: a cache in front, a clock the test moves
	oracle *usageLogRepository // only its plain whole-window pass is used
	server *miniredis.Miniredis
	now    time.Time
	report userDashboardDiffReport
}

func runUserDashboardDifferential(t *testing.T, db *sql.DB, cfg userDashboardDiffConfig) {
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
	exec(`CREATE INDEX ON usage_logs (user_id, created_at)`)
	exec(`CREATE TEMP TABLE usage_dashboard_hourly_users (LIKE public.usage_dashboard_hourly_users INCLUDING ALL) ON COMMIT DROP`)
	exec(`CREATE TEMP TABLE groups (id BIGINT PRIMARY KEY, platform TEXT) ON COMMIT DROP`)
	exec(`CREATE TEMP TABLE accounts (id BIGINT PRIMARY KEY, platform TEXT) ON COMMIT DROP`)
	// A group's platform is its own, empty, "composite" (the account's is used) or unknown (NULL: the account's);
	// an account's can be unknown too, and then a row whose group has none is left out of the platforms.
	exec(`INSERT INTO groups (id, platform) VALUES (9000001, 'anthropic'), (9000002, 'openai'), (9000003, 'gemini'), (9000004, ''), (9000005, 'composite'), (9000006, NULL)`)
	exec(`INSERT INTO accounts (id, platform) VALUES (1, 'anthropic'), (2, 'openai'), (3, 'gemini'), (4, 'grok'), (5, NULL)`)

	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	rnd := rand.New(rand.NewSource(cfg.seed))
	users := userDashboardDiffUsers(first, last)
	cases := make(map[string][]userDashboardDiffCase, len(userDashboardDiffZones))
	var all []userDashboardDiffCase
	for _, zone := range userDashboardDiffZones {
		cases[zone] = userDashboardDiffCases(t, rnd, zone, cfg.cases, users)
		all = append(all, cases[zone]...)
	}
	loadUserDashboardDiffFixture(t, ctx, tx, rnd, cfg, users, all)
	exec(`ANALYZE usage_logs`)

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	e := &userDashboardDiffEnv{t: t, ctx: ctx, probe: &userDashboardDiffProbe{inner: tx}, server: server}
	e.repo = newUsageLogRepositoryWithSQL(nil, e.probe)
	e.repo.statsCache = newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}})
	e.repo.clock = func() time.Time { return e.now }
	e.oracle = newUsageLogRepositoryWithSQL(nil, tx)

	var failures []string
	for _, zone := range userDashboardDiffZones {
		t.Run(zone, func(t *testing.T) {
			useRollupTestTimezone(t, zone)
			// The rollup is written the way production writes it, in the zone of the application: its hours are the
			// local ones of this zone.
			exec(`DELETE FROM usage_dashboard_hourly_users`)
			require.NoError(t, newDashboardAggregationRepositoryWithSQL(tx).insertHourlyActiveUsers(ctx, first, last))
			for _, u := range users {
				switch u.rollup {
				case "none":
					exec(`DELETE FROM usage_dashboard_hourly_users WHERE user_id = $1`, u.id)
				case "dau":
					exec(`UPDATE usage_dashboard_hourly_users SET total_requests = 0, input_tokens = 0, output_tokens = 0,
						cache_creation_tokens = 0, cache_read_tokens = 0, total_cost = 0, actual_cost = 0, account_cost = 0, total_duration_ms = 0, duration_count = 0
						WHERE user_id = $1`, u.id)
				}
			}
			for _, c := range cases[zone] {
				failures = append(failures, e.compare(c)...)
			}
		})
	}

	r := e.report
	t.Logf("%d cases (%d cut, %d whole) against the plain whole-window pass; closed part from the rollup %d times, from usage_logs %d times; "+
		"a platform in both parts %d times; a day that is not 24 hours %d times; %d answers had requests; %d for a user new today",
		r.comparisons, r.split, r.whole, r.closedFromRollup, r.closedFromUsageLogs, r.platformsInBothParts, r.notTwentyFourDay, r.nonEmpty, r.newUserToday)
	if len(failures) > 10 {
		failures = append(failures[:10], fmt.Sprintf("... and %d more", len(failures)-10))
	}
	require.Empty(t, failures, strings.Join(failures, "\n"))
	// The point of the test is the cut and the merge: make sure they were really exercised.
	require.Greater(t, r.split, r.comparisons/2, "too few cases were cut")
	require.Greater(t, r.whole, 0, "no case fell in the first hour of its day")
	require.Greater(t, r.closedFromRollup, r.split/3)
	require.Greater(t, r.closedFromUsageLogs, 0)
	require.Greater(t, r.platformsInBothParts, r.split/10, "too few cuts put one platform on both sides")
	require.Greater(t, r.notTwentyFourDay, 0, "no case had a DST day as today")
	require.Greater(t, r.nonEmpty, r.comparisons/3, "too many empty answers")
	require.Greater(t, r.newUserToday, 0, "no case had a user whose history starts today")
}

// loadUserDashboardDiffFixture inserts the random rows of every user, and the rows on and around the cut, the
// start of the window and the open hour of every case, for every user.
func loadUserDashboardDiffFixture(t *testing.T, ctx context.Context, tx *sql.Tx, rnd *rand.Rand, cfg userDashboardDiffConfig,
	users []userDashboardDiffUser, cases []userDashboardDiffCase) {
	t.Helper()
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("usage_logs",
		"id", "user_id", "api_key_id", "account_id", "group_id", "model",
		"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_cost", "actual_cost", "duration_ms", "created_at"))
	require.NoError(t, err)
	var id int64
	insert := func(user int64, at time.Time) {
		id++
		total := math.Round(rnd.Float64()*5e10) / 1e10 // exact decimals: the database adds them without error
		actual := math.Round(total*(0.2+rnd.Float64()*1.6)*1e10) / 1e10
		if rnd.Intn(10) == 0 {
			actual = 0 // a failed request: counted in the totals, not in the platforms
		}
		cacheCreation, cacheRead := int64(0), int64(0)
		if rnd.Intn(2) == 0 {
			cacheCreation, cacheRead = int64(rnd.Intn(800)), int64(rnd.Intn(8000))
		}
		var group, duration driver.Value
		if rnd.Intn(8) != 0 {
			group = int64(9000001 + rnd.Intn(6))
		}
		if rnd.Intn(4) != 0 {
			duration = int64(50 + rnd.Intn(60000))
		}
		_, execErr := stmt.ExecContext(ctx, id, user, user*10+1, int64(1+rnd.Intn(5)), group, "claude-opus-4-5",
			int64(rnd.Intn(3000)), int64(rnd.Intn(1500)), cacheCreation, cacheRead,
			strconv.FormatFloat(total, 'f', 10, 64), strconv.FormatFloat(actual, 'f', 10, 64), duration, at.UTC().Truncate(time.Microsecond))
		require.NoError(t, execErr)
	}

	totalWeight := 0
	for _, u := range users {
		totalWeight += u.weight
	}
	for _, u := range users {
		for range cfg.rows * u.weight / totalWeight {
			insert(u.id, u.from.Add(time.Duration(rnd.Int63n(int64(u.to.Sub(u.from))))))
		}
	}
	// On and around every instant the answer is cut at: for every user that has rows.
	edges := []time.Duration{-time.Microsecond, 0, time.Microsecond}
	seen := make(map[time.Time]bool)
	boundary := func(at time.Time) {
		if seen[at] {
			return
		}
		seen[at] = true
		for _, u := range users {
			if u.weight == 0 || at.Before(u.from) {
				continue
			}
			for _, edge := range edges {
				insert(u.id, at.Add(edge))
			}
		}
	}
	for _, c := range cases {
		midnight := c.midnight()
		boundary(midnight)
		boundary(midnight.Add(-30 * 24 * time.Hour)) // the start of the window of a cut answer
		boundary(c.now.Truncate(time.Hour))          // the open hour the rollup stops at
		boundary(midnight.Add(time.Hour))
	}
	_, err = stmt.ExecContext(ctx)
	require.NoError(t, err)
	require.NoError(t, stmt.Close())
}

// compare answers one case through the code under test, cold, warm and after the short entry lapsed, and through the
// plain whole-window pass, and returns what differs.
func (e *userDashboardDiffEnv) compare(c userDashboardDiffCase) []string {
	e.t.Helper()
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf("%s: %s", c, fmt.Sprintf(format, args...)))
	}
	e.report.comparisons++
	e.server.FlushAll()
	e.now = c.now
	e.probe.take()
	today := timezone.StartOfDay(c.now)
	call := func() (*UserDashboardStats, error) { return e.repo.userDashboardUsage(e.ctx, c.user, c.now, today) }

	got, err := call()
	cold := e.probe.take()
	if err != nil {
		fail("error: %v", err)
		return failures
	}
	want, err := e.oracle.getUserDashboardUsageUncached(e.ctx, c.user, c.now, c.windowStart())
	if err != nil {
		fail("plain pass: %v", err)
		return failures
	}
	if diff := userDashboardStatsDiff(got, want); diff != "" {
		fail("%s", diff)
	}
	if want.TotalRequests > 0 {
		e.report.nonEmpty++
	}

	// Which path answered, and how its entries are kept.
	if c.split() {
		e.report.split++
		if midnight := c.midnight(); midnight.AddDate(0, 0, 1).Sub(midnight) != 24*time.Hour {
			e.report.notTwentyFourDay++
		}
		if got, want := e.ttlsInRedis(), []time.Duration{time.Minute, usageStatsKeyClosedDaysTTL}; !slices.Equal(got, want) {
			fail("cache lifetimes %v, want %v", got, want)
		}
		if n := closedPlatformStatements(cold); n != 1 {
			fail("a cut answer sent %d closed platform statements, want 1", n)
		}
		e.countParts(c, cold, fail)
	} else {
		e.report.whole++
		if got, want := e.ttlsInRedis(), []time.Duration{time.Minute}; !slices.Equal(got, want) {
			fail("cache lifetimes %v, want %v", got, want)
		}
		if n := closedPlatformStatements(cold); n != 0 {
			fail("a whole answer sent %d closed platform statements", n)
		}
	}

	// The same call again is answered from the cache, and is the same answer.
	again, err := call()
	if err != nil {
		fail("warm call: %v", err)
	} else if !reflect.DeepEqual(got, again) {
		fail("warm call differs from the cold one: %+v vs %+v", again, got)
	}
	if extra := e.probe.take(); len(extra) != 0 {
		fail("warm call sent %d statements: %v", len(extra), extra)
	}

	// Once the short entry has lapsed only what it holds is read again: the days that are over stay cached.
	e.server.FastForward(61 * time.Second)
	refreshed, err := call()
	reread := e.probe.take()
	if err != nil {
		fail("refresh: %v", err)
		return failures
	}
	if diff := userDashboardStatsDiff(refreshed, want); diff != "" {
		fail("after the refresh: %s", diff)
	}
	if c.split() {
		if closedPlatformStatements(reread) != 0 {
			fail("the refresh read the closed days again")
		}
		if len(reread) == 0 || len(reread) >= len(cold) || !slices.Equal(reread, cold[len(cold)-len(reread):]) {
			fail("the refresh sent %v, which is not the tail (today's part) of the %d statements of the cold call", reread, len(cold))
		}
	} else if len(reread) != len(cold) {
		fail("a whole answer is read again whole: %d statements, then %d", len(cold), len(reread))
	}
	return failures
}

// countParts measures what the cases exercised, from what the code under test cached under the keys the production
// code names for its two parts: which source the closed part came from (the rollup, or usage_logs where the rollup
// has nothing for the user: the statements say), and platforms found in both parts.
func (e *userDashboardDiffEnv) countParts(c userDashboardDiffCase, cold []string, fail func(string, ...any)) {
	cut := c.midnight()
	var closed, today userDashboardPart
	read := func(key usageStatsCacheKey, into *userDashboardPart) bool {
		raw, err := e.server.Get(usageStatsRedisKey(key))
		if err != nil {
			fail("a part is not in the cache under its key: %v", err)
			return false
		}
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			fail("a part is not what the cache keeps: %v", err)
			return false
		}
		return true
	}
	if !read(userDashboardClosedKey(c.user, cut.Add(-30*24*time.Hour), cut), &closed) || !read(userDashboardTodayKey(c.user, cut), &today) {
		return
	}
	// The closed part read usage_logs between its two bounds only where the rollup had nothing for the user.
	fromUsageLogs := slices.ContainsFunc(cold, func(q string) bool {
		return strings.Contains(q, "FROM usage_logs WHERE user_id = $1 AND created_at >= $2 AND created_at < $3")
	})
	switch {
	case closed.Requests == 0:
	case fromUsageLogs == (c.userRollup() == "full"):
		fail("the closed part of a user with rollup %q was read from usage_logs: %v", c.userRollup(), fromUsageLogs)
	case fromUsageLogs:
		e.report.closedFromUsageLogs++
	default:
		e.report.closedFromRollup++
	}
	if closed.Requests == 0 && today.Requests > 0 {
		e.report.newUserToday++
	}
	names := make(map[string]bool)
	for _, p := range closed.ByPlatform {
		names[p.Platform] = true
	}
	for _, p := range today.ByPlatform {
		if names[p.Platform] {
			e.report.platformsInBothParts++
			break
		}
	}
}

// userRollup is the rollup shape of the case's user, by the id the fixture gives them.
func (c userDashboardDiffCase) userRollup() string {
	switch c.user {
	case 9300003:
		return "none"
	case 9300004:
		return "dau"
	}
	return "full"
}

func (e *userDashboardDiffEnv) ttlsInRedis() []time.Duration {
	var out []time.Duration
	for _, key := range e.server.Keys() {
		out = append(out, e.server.TTL(key))
	}
	slices.Sort(out)
	return out
}

// userDashboardDiffClose compares sums. The database adds exact decimals; the code under test adds two floats, one
// per part, so the last bits may differ. A missing or doubled row moves a sum by at least the smallest fixture cost.
func userDashboardDiffClose(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// userDashboardStatsDiff compares two answers field by field (counts exactly, costs and the average within the
// rounding of float sums), and the platforms by name; their order must be most expensive first.
func userDashboardStatsDiff(got, want *UserDashboardStats) string {
	var diffs []string
	gv, wv := reflect.ValueOf(*got), reflect.ValueOf(*want)
	for i := 0; i < gv.NumField(); i++ {
		name := gv.Type().Field(i).Name
		switch g := gv.Field(i); g.Kind() {
		case reflect.Int64:
			if g.Int() != wv.Field(i).Int() {
				diffs = append(diffs, fmt.Sprintf("%s got %d, want %d", name, g.Int(), wv.Field(i).Int()))
			}
		case reflect.Float64:
			if !userDashboardDiffClose(g.Float(), wv.Field(i).Float()) {
				diffs = append(diffs, fmt.Sprintf("%s got %v, want %v", name, g.Float(), wv.Field(i).Float()))
			}
		}
	}
	byName := func(list []PlatformDashboardStats) map[string]PlatformDashboardStats {
		out := make(map[string]PlatformDashboardStats, len(list))
		for _, p := range list {
			out[p.Platform] = p
		}
		return out
	}
	gp, wp := byName(got.ByPlatform), byName(want.ByPlatform)
	if len(got.ByPlatform) != len(want.ByPlatform) || len(gp) != len(wp) {
		diffs = append(diffs, fmt.Sprintf("platforms got %+v, want %+v", got.ByPlatform, want.ByPlatform))
	}
	for name, w := range wp {
		g, ok := gp[name]
		if !ok || g.TotalRequests != w.TotalRequests || g.TotalTokens != w.TotalTokens || g.TodayRequests != w.TodayRequests || g.TodayTokens != w.TodayTokens ||
			!userDashboardDiffClose(g.TotalActualCost, w.TotalActualCost) || !userDashboardDiffClose(g.TodayActualCost, w.TodayActualCost) {
			diffs = append(diffs, fmt.Sprintf("platform %s got %+v, want %+v", name, g, w))
		}
	}
	for i := 1; i < len(got.ByPlatform); i++ {
		if got.ByPlatform[i-1].TotalActualCost < got.ByPlatform[i].TotalActualCost {
			diffs = append(diffs, fmt.Sprintf("platforms are not most expensive first: %+v", got.ByPlatform))
			break
		}
	}
	return strings.Join(diffs, "; ")
}
