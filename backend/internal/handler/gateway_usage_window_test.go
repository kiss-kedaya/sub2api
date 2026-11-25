package handler

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/repository" //nolint:depguard // 端到端夹具：要真实的聚合缓存（Redis TTL / 缓存键）才能数「同一桶内查了几次库」。
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9" //nolint:depguard // 端到端夹具：miniredis 客户端，交给真实仓库做聚合缓存。
	"github.com/stretchr/testify/require"
)

func mustLoadZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err, name)
	return loc
}

// onKeyGrid reports whether at sits on the bucket grid of one API key: bucket steps shifted by the key's phase.
func onKeyGrid(at time.Time, apiKeyID int64) bool {
	shifted := at.Add(-usageKeyPhase(apiKeyID))
	return shifted.Equal(shifted.Truncate(usageDefaultRangeBucket))
}

// The default window of /v1/usage moves in 15 minute steps on a grid that is private to each API key: every poll
// of one key inside a bucket asks the identical question, the end is never in the future, and nothing breaks
// across DST changes or odd UTC offsets, whatever the key's phase.
func TestUsageDefaultRangeIsStableWithinABucketAndNeverInTheFuture(t *testing.T) {
	zones := []string{"UTC", "Asia/Shanghai", "America/New_York", "America/Los_Angeles", "Europe/London", "Asia/Kolkata", "Asia/Kathmandu", "Australia/Lord_Howe", "Pacific/Chatham"}
	instants := []time.Time{
		time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),                    // on a boundary of the shared grid (phase 0)
		time.Date(2026, 11, 26, 10, 14, 59, 999999999, time.UTC),          // last nanosecond of a bucket of the shared grid
		time.Date(2026, 11, 26, 18, 2, 3, 456, time.UTC),                  // somewhere inside
		time.Date(2026, 3, 8, 6, 59, 30, 0, time.UTC),                     // New York, a minute before spring forward
		time.Date(2026, 3, 8, 7, 3, 0, 0, time.UTC),                       // ... and just after
		time.Date(2026, 3, 8, 9, 59, 30, 0, time.UTC),                     // Los Angeles, a minute before spring forward
		time.Date(2026, 3, 8, 10, 3, 0, 0, time.UTC),                      // ... and just after
		time.Date(2026, 11, 1, 5, 59, 30, 0, time.UTC),                    // New York, before the clocks go back
		time.Date(2026, 11, 1, 6, 2, 0, 0, time.UTC),                      // ... the repeated hour
		time.Date(2026, 11, 1, 8, 59, 30, 0, time.UTC),                    // Los Angeles, before the clocks go back
		time.Date(2026, 11, 1, 9, 2, 0, 0, time.UTC),                      // ... the repeated hour
		time.Date(2026, 4, 4, 15, 29, 0, 0, time.UTC),                     // Lord Howe, half hour DST change
		time.Date(2026, 4, 7, 2, 30, 0, 0, time.UTC),                      // 30 days after New York's gap
		time.Date(2026, 4, 7, 9, 40, 0, 0, time.UTC),                      // 30 days after Los Angeles' gap: the window starts in it
		time.Date(2026, 10, 25, 0, 59, 0, 0, time.UTC),                    // London, around the clocks going back
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond), // year boundary
	}
	// 0 is no key at all (the shared grid, phase 0); the rest are ordinary ids and one far beyond int32.
	for _, apiKeyID := range []int64{0, 1, 7, 9, 12345, 1<<40 + 3} {
		for _, zone := range zones {
			loc := mustLoadZone(t, zone)
			for _, instant := range instants {
				now := instant.In(loc)
				start, end := usageDefaultRange(now, apiKeyID)
				where := fmt.Sprintf("%s key %d %v", zone, apiKeyID, now)

				require.False(t, end.After(now), "%s: the window must not end in the future", where)
				require.Less(t, now.Sub(end), usageDefaultRangeBucket, "%s: end is the latest grid point", where)
				require.True(t, onKeyGrid(end, apiKeyID), "%s: end is on the key's grid", where)
				require.True(t, onKeyGrid(start, apiKeyID), "%s: start is on the key's grid", where)
				require.True(t, start.Before(end), "%s", where)
				// 30 calendar days; a DST change inside the window makes it up to an hour or so longer or shorter.
				require.InDelta(t, float64(30*24), end.Sub(start).Hours(), 2, "%s", where)

				// Every instant of the same bucket gives the same window ...
				for _, offset := range []time.Duration{0, time.Nanosecond, 90 * time.Second, 2*time.Minute + 30*time.Second, 7 * time.Minute, usageDefaultRangeBucket - time.Nanosecond} {
					sameStart, sameEnd := usageDefaultRange(end.Add(offset), apiKeyID)
					require.True(t, sameStart.Equal(start) && sameEnd.Equal(end), "%s +%v: same bucket, same window", where, offset)
				}
				// ... the next bucket is a different one, exactly one step later ...
				nextStart, nextEnd := usageDefaultRange(end.Add(usageDefaultRangeBucket), apiKeyID)
				require.True(t, nextEnd.Equal(end.Add(usageDefaultRangeBucket)), "%s", where)
				require.False(t, nextStart.Equal(start), "%s: the start moves with the end", where)
				// ... and the instant before the boundary still belongs to the previous one.
				_, previousEnd := usageDefaultRange(end.Add(-time.Nanosecond), apiKeyID)
				require.True(t, previousEnd.Equal(end.Add(-usageDefaultRangeBucket)), "%s", where)
			}
		}
	}
}

// The phase is what keeps the grid from being one shared set of instants: it stays inside one bucket, is the
// same on every call and every machine (the two gateways share one Redis), and spreads neighbouring and distant
// ids over the whole bucket instead of clumping them.
func TestUsageKeyPhaseIsStableAndSpreadAcrossTheBucket(t *testing.T) {
	// The cache entry of a multi-day key aggregate lives 15 minutes (repository.usageStatsKeyWindowTTL): a
	// wider grid would let an answer expire before its bucket ends and bring the rescans back.
	require.Equal(t, 15*time.Minute, usageDefaultRangeBucket, "keep in step with repository.usageStatsKeyWindowTTL")

	// Pinned on purpose: a phase that changed between builds or processes would split one key's cache entries.
	require.Zero(t, usageKeyPhase(0))
	require.Equal(t, 9*time.Minute+16*time.Second, usageKeyPhase(1))
	require.Equal(t, 4*time.Minute+53*time.Second, usageKeyPhase(7))

	perMinute := make([]int, int(usageDefaultRangeBucket/time.Minute))
	distinct := map[time.Duration]struct{}{}
	ids := []int64{1 << 40, 1<<40 + 1, 1<<62 + 12345}
	for id := int64(1); id <= 1000; id++ {
		ids = append(ids, id)
	}
	for _, id := range ids {
		phase := usageKeyPhase(id)
		require.GreaterOrEqual(t, phase, time.Duration(0), "key %d", id)
		require.Less(t, phase, usageDefaultRangeBucket, "key %d", id)
		require.Zero(t, phase%time.Second, "key %d: whole seconds", id)
		require.Equal(t, phase, usageKeyPhase(id), "key %d: the same on every call", id)
		if id <= 1000 {
			perMinute[int(phase/time.Minute)]++
			distinct[phase] = struct{}{}
		}
	}
	filled := 0
	for minute, n := range perMinute {
		if n > 0 {
			filled++
		}
		// 1000 ids over 15 minutes is 66.7 a minute; a usable hash stays within a factor of two of that.
		require.LessOrEqual(t, n, 134, "minute %d of the bucket holds too many of ids 1..1000: %v", minute, perMinute)
	}
	require.GreaterOrEqual(t, filled, 10, "ids 1..1000 must land in at least 10 different minutes of the bucket: %v", perMinute)
	require.GreaterOrEqual(t, len(distinct), 400, "ids 1..1000 must land on many different seconds of the bucket")
}

// What the phase is for, seen from the database: when the window of each key rolls over to a new cache entry
// (that is when it is aggregated again), the rollovers of many keys are spread over the whole bucket and do not
// pile up on one grid point. 600 keys polled every 10 seconds for an hour change window 4 times each: 40 a
// minute on average, and not one minute may see more than twice that. With one shared grid it would be 600 in
// each of four minutes and nothing in the rest.
func TestUsageDefaultRangeRolloversOfManyKeysAreSpread(t *testing.T) {
	const keys = 600
	base := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	rollovers := make([]int, 60)
	total := 0
	for id := int64(1); id <= keys; id++ {
		_, previous := usageDefaultRange(base, id)
		// Up to and including base+1h: a key whose phase is just under a bucket rolls over its 4th time in the
		// last seconds of the hour, and is seen at the poll that follows.
		for at := base.Add(10 * time.Second); !at.After(base.Add(time.Hour)); at = at.Add(10 * time.Second) {
			if _, end := usageDefaultRange(at, id); !end.Equal(previous) {
				rollovers[at.Minute()]++
				total++
				previous = end
			}
		}
	}
	require.Equal(t, keys*4, total, "every key changes window once per bucket")
	mean := total / len(rollovers)
	for minute, n := range rollovers {
		require.LessOrEqual(t, n, 2*mean, "minute %d sees %d rollovers: %v", minute, n, rollovers)
		require.GreaterOrEqual(t, n, mean/2, "minute %d sees %d rollovers: %v", minute, n, rollovers)
	}
}

// Explicit dates keep their whole-day boundaries; only the bound the caller left out is the moving default, cut on
// the grid of the key that asks.
func TestUsageDateRangeKeepsExplicitBoundsOnDayBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const apiKeyID = 7
	parse := func(query string) (time.Time, time.Time) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage"+query, nil)
		return (&GatewayHandler{}).parseUsageDateRange(c, apiKeyID)
	}
	dayStart := func(day string) time.Time {
		at, err := timezone.ParseInLocation("2006-01-02", day)
		require.NoError(t, err)
		return at
	}
	onGrid := func(at time.Time) bool { return onKeyGrid(at, apiKeyID) }

	start, end := parse("?start_date=2026-11-01&end_date=2026-11-03")
	require.True(t, start.Equal(dayStart("2026-11-01")))
	require.True(t, end.Equal(dayStart("2026-11-04")), "end_date is inclusive: the bound is the next midnight")

	start, end = parse("?start_date=2026-11-01")
	require.True(t, start.Equal(dayStart("2026-11-01")))
	require.True(t, onGrid(end) && !end.After(time.Now()), "an open end is the moving default")

	start, end = parse("?end_date=2026-11-03")
	require.True(t, end.Equal(dayStart("2026-11-04")))
	require.True(t, onGrid(start), "an open start is the moving default")

	// A malformed date is ignored, as before: the default window stands in for it.
	start, end = parse("?start_date=yesterday&end_date=soon")
	require.True(t, onGrid(start) && onGrid(end))
	require.True(t, start.Before(end))

	// The key is what the default window is cut for: asked as another key, the same query gets that key's grid.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	_, other := (&GatewayHandler{}).parseUsageDateRange(c, 9)
	require.True(t, onKeyGrid(other, 9) && !onKeyGrid(other, apiKeyID), "key 9 is not on key 7's grid")
}

// The by-day window of /v1/usage and of the per-key panels is cut on the caller's own midnights, so it is the same
// for every poll of a day whatever the clock does inside the 15 minute buckets; the unit of the cache key is the day.
func TestAPIKeyDailyUsageRangeIsAlignedToTheUsersDayBoundary(t *testing.T) {
	for _, tz := range []string{"", "UTC", "Asia/Shanghai", "America/New_York", "Asia/Kolkata", "Pacific/Chatham", "Australia/Lord_Howe", "Not/AZone"} {
		for _, days := range []int{1, 7, 30, 90} {
			start, end := apiKeyDailyUsageRange(days, tz)
			for _, bound := range []time.Time{start, end} {
				hour, minute, second := bound.Clock()
				require.Zero(t, hour+minute+second+bound.Nanosecond(), "tz %q days %d: %v is not a midnight", tz, days, bound)
			}
			require.Equal(t, start.Location().String(), end.Location().String(), "tz %q", tz)
			now := timezone.NowInUserLocation(tz)
			require.False(t, now.Before(start), "tz %q days %d: today is inside the window", tz, days)
			require.True(t, now.Before(end), "tz %q days %d: today is inside the window", tz, days)
			// `days` natural days; a DST change inside them makes one of them 23 or 25 hours.
			require.InDelta(t, float64(days*24), end.Sub(start).Hours(), 2, "tz %q days %d", tz, days)
		}
	}
}

// ---- end to end: /v1/usage -> UsageService -> the real aggregate cache (Redis) -> database ----

// usageKeyReportRepo is the real, cached repository for the two key reports; only the today / total summary
// (cached on its own, and not what this is about) is stubbed so its SQL need not be mocked.
type usageKeyReportRepo struct {
	service.UsageLogRepository
}

func (r *usageKeyReportRepo) GetAPIKeyDashboardStats(context.Context, int64) (*usagestats.UserDashboardStats, error) {
	return &usagestats.UserDashboardStats{TodayRequests: 2}, nil
}

// onGridOfKey matches a window bound that sits on the bucket grid of the given API key.
type onGridOfKey int64

func (id onGridOfKey) Match(v driver.Value) bool {
	at, ok := v.(time.Time)
	return ok && onKeyGrid(at, int64(id))
}

const (
	usageReportTrendSQL = `(?s)FROM usage_logs.*AND user_id = \$3 AND api_key_id = \$4`
	usageReportModelSQL = `(?s)FROM usage_logs.*AND api_key_id = \$3`
)

type usageKeyReportFixture struct {
	mock   sqlmock.Sqlmock
	server *miniredis.Miniredis
	h      *GatewayHandler
	svc    *service.UsageService
}

func newUsageKeyReportFixture(t *testing.T) *usageKeyReportFixture {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}}
	repo := &usageKeyReportRepo{UsageLogRepository: repository.NewUsageLogRepository(nil, db, rdb, cfg)}
	svc := service.NewUsageService(repo, nil, nil, nil)
	events := []string{}
	return &usageKeyReportFixture{
		mock:   mock,
		server: server,
		svc:    svc,
		h: &GatewayHandler{
			userService:  service.NewUserService(&usageWalletUserRepo{events: &events}, nil, nil, nil),
			usageService: svc,
		},
	}
}

func (f *usageKeyReportFixture) expectTrend(userID, apiKeyID, requests int64) {
	f.mock.ExpectQuery(usageReportTrendSQL).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), userID, apiKeyID).
		WillReturnRows(sqlmock.NewRows([]string{
			"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost",
		}).AddRow("2026-11-26", requests, 0, 0, 0, 0, 0, 0, 0))
}

func (f *usageKeyReportFixture) expectModels(apiKeyID, requests int64) {
	f.mock.ExpectQuery(usageReportModelSQL).
		WithArgs(onGridOfKey(apiKeyID), onGridOfKey(apiKeyID), apiKeyID).
		WillReturnRows(sqlmock.NewRows([]string{
			"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost",
		}).AddRow("claude-test", requests, 0, 0, 0, 0, 0, 0, 0, 0))
}

// poll is one GET /v1/usage of API key 7 (owned by user 11).
func (f *usageKeyReportFixture) poll(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 11})
	f.h.Usage(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

// A client that polls /v1/usage every few seconds costs the database one scan per report per bucket, not one per
// poll: the second poll of the same bucket is answered from Redis. The mock has exactly one answer per report, so
// a second scan would be an unexpected query, the report would be missing from the response, and this fails.
//
// Besides two polls back to back there is the case that is hardest on the cache: the first poll is the first of
// its bucket and the second the last, so the entry written by the first must live the whole bucket. Redis' clock
// is moved by the bucket minus a second while the handler's own is not, so both polls ask for the same window,
// as two real polls at the two ends of one bucket do.
func TestUsageSecondPollInTheSameBucketReadsNoDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, betweenPolls := range map[string]time.Duration{"back to back": 0, "a whole bucket apart": usageDefaultRangeBucket - time.Second} {
		t.Run(name, func(t *testing.T) {
			for attempt := 0; ; attempt++ {
				require.Less(t, attempt, 3, "the clock crossed a bucket boundary on every attempt")
				f := newUsageKeyReportFixture(t)
				f.expectTrend(11, 7, 21)
				f.expectModels(7, 34)

				_, endBefore := usageDefaultRange(timezone.Now(), 7)
				first := f.poll(t)
				f.server.FastForward(betweenPolls)
				second := f.poll(t)
				if _, endAfter := usageDefaultRange(timezone.Now(), 7); !endBefore.Equal(endAfter) {
					continue // a bucket boundary fell between the two polls: that is the other test's case, go again
				}

				for _, report := range []string{"daily_usage", "model_stats"} {
					require.Contains(t, first, report)
					require.Contains(t, second, report, "the second poll must still carry the report (from the cache)")
					require.JSONEq(t, string(first[report]), string(second[report]))
				}
				require.JSONEq(t, `[{"date":"2026-11-26","requests":21,"input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"total_tokens":0,"cost":0,"actual_cost":0}]`, string(second["daily_usage"]))
				require.NoError(t, f.mock.ExpectationsWereMet())
				return
			}
		})
	}
}

// The model report is scanned once per 15 minute bucket of the key: every instant of a bucket shares one scan, the
// next bucket needs its own, and a bucket whose answer is still fresh is not scanned again when the clock is back
// in it.
func TestUsageModelReportIsScannedOncePerBucket(t *testing.T) {
	f := newUsageKeyReportFixture(t)
	_, base := usageDefaultRange(time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), 7) // a grid point of key 7: its bucket opens here
	models := func(at time.Time) int64 {
		t.Helper()
		start, end := usageDefaultRange(at, 7)
		require.False(t, end.After(at))
		stats, err := f.svc.GetAPIKeyModelStats(context.Background(), 7, start, end)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		return stats[0].Requests
	}

	f.expectModels(7, 1)
	for _, offset := range []time.Duration{0, time.Nanosecond, 30 * time.Second, 2 * time.Minute, 11 * time.Minute, usageDefaultRangeBucket - time.Nanosecond} {
		require.EqualValues(t, 1, models(base.Add(offset)), "+%v: one scan for the whole bucket", offset)
	}

	f.expectModels(7, 2)
	require.EqualValues(t, 2, models(base.Add(usageDefaultRangeBucket)), "the next bucket is scanned once")
	require.EqualValues(t, 2, models(base.Add(usageDefaultRangeBucket+14*time.Minute)))
	require.EqualValues(t, 1, models(base), "the previous bucket's answer is still within its fifteen minutes")
	require.NoError(t, f.mock.ExpectationsWereMet())

	// Another key never reads this key's answer: it asks its own window, on its own grid.
	f.expectModels(9, 3)
	start, end := usageDefaultRange(base, 9)
	other, err := f.svc.GetAPIKeyModelStats(context.Background(), 9, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 3, other[0].Requests)
	require.NoError(t, f.mock.ExpectationsWereMet())
}
