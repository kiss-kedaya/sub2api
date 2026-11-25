package handler

import (
	"context"
	"database/sql/driver"
	"encoding/json"
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

// The default window of /v1/usage moves in 5 minute steps: every poll inside a bucket asks the identical
// question, the end is never in the future, and nothing breaks across DST changes or odd UTC offsets.
func TestUsageDefaultRangeIsStableWithinABucketAndNeverInTheFuture(t *testing.T) {
	zones := []string{"UTC", "Asia/Shanghai", "America/New_York", "Europe/London", "Asia/Kolkata", "Asia/Kathmandu", "Australia/Lord_Howe", "Pacific/Chatham"}
	instants := []time.Time{
		time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC),                    // exactly on a boundary
		time.Date(2026, 11, 26, 10, 4, 59, 999999999, time.UTC),           // last nanosecond of a bucket
		time.Date(2026, 11, 26, 18, 2, 3, 456, time.UTC),                  // somewhere inside
		time.Date(2026, 3, 8, 6, 59, 30, 0, time.UTC),                     // New York, a minute before spring forward
		time.Date(2026, 3, 8, 7, 3, 0, 0, time.UTC),                       // ... and just after
		time.Date(2026, 11, 1, 5, 59, 30, 0, time.UTC),                    // New York, before the clocks go back
		time.Date(2026, 11, 1, 6, 2, 0, 0, time.UTC),                      // ... the repeated hour
		time.Date(2026, 4, 4, 15, 29, 0, 0, time.UTC),                     // Lord Howe, half hour DST change
		time.Date(2026, 4, 7, 2, 30, 0, 0, time.UTC),                      // 30 days after New York's gap
		time.Date(2026, 10, 25, 0, 59, 0, 0, time.UTC),                    // London, around the clocks going back
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond), // year boundary
	}
	for _, zone := range zones {
		loc := mustLoadZone(t, zone)
		for _, instant := range instants {
			now := instant.In(loc)
			start, end := usageDefaultRange(now)

			require.False(t, end.After(now), "%s %v: the window must not end in the future", zone, now)
			require.Less(t, now.Sub(end), usageDefaultRangeBucket, "%s %v: end is the latest boundary", zone, now)
			require.True(t, end.Equal(end.Truncate(usageDefaultRangeBucket)), "%s %v", zone, now)
			require.True(t, start.Equal(start.Truncate(usageDefaultRangeBucket)), "%s %v", zone, now)
			require.True(t, start.Before(end), "%s %v", zone, now)
			// 30 calendar days; a DST change inside the window makes it up to an hour or so longer or shorter.
			require.InDelta(t, float64(30*24), end.Sub(start).Hours(), 2, "%s %v", zone, now)

			// Every instant of the same bucket gives the same window ...
			for _, offset := range []time.Duration{0, time.Nanosecond, 90 * time.Second, 2*time.Minute + 30*time.Second, usageDefaultRangeBucket - time.Nanosecond} {
				sameStart, sameEnd := usageDefaultRange(end.Add(offset))
				require.True(t, sameStart.Equal(start) && sameEnd.Equal(end), "%s %v +%v: same bucket, same window", zone, now, offset)
			}
			// ... and the next bucket is a different one, exactly one step later.
			nextStart, nextEnd := usageDefaultRange(end.Add(usageDefaultRangeBucket))
			require.True(t, nextEnd.Equal(end.Add(usageDefaultRangeBucket)), "%s %v", zone, now)
			require.False(t, nextStart.Equal(start), "%s %v: the start moves with the end", zone, now)
		}
	}
}

// Explicit dates keep their whole-day boundaries; only the bound the caller left out is the moving default.
func TestUsageDateRangeKeepsExplicitBoundsOnDayBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parse := func(query string) (time.Time, time.Time) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/usage"+query, nil)
		return (&GatewayHandler{}).parseUsageDateRange(c)
	}
	dayStart := func(day string) time.Time {
		at, err := timezone.ParseInLocation("2006-01-02", day)
		require.NoError(t, err)
		return at
	}
	onGrid := func(at time.Time) bool { return at.Equal(at.Truncate(usageDefaultRangeBucket)) }

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
}

// The by-day window of /v1/usage and of the per-key panels is cut on the caller's own midnights, so it is the same
// for every poll of a day whatever the clock does inside the 5 minute buckets; the unit of the cache key is the day.
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

// onFiveMinuteGrid matches a window bound that sits on the 5 minute grid.
type onFiveMinuteGrid struct{}

func (onFiveMinuteGrid) Match(v driver.Value) bool {
	at, ok := v.(time.Time)
	return ok && at.Equal(at.Truncate(usageDefaultRangeBucket))
}

const (
	usageReportTrendSQL = `(?s)FROM usage_logs.*AND user_id = \$3 AND api_key_id = \$4`
	usageReportModelSQL = `(?s)FROM usage_logs.*AND api_key_id = \$3`
)

type usageKeyReportFixture struct {
	mock sqlmock.Sqlmock
	h    *GatewayHandler
	svc  *service.UsageService
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
		mock: mock,
		svc:  svc,
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
		WithArgs(onFiveMinuteGrid{}, onFiveMinuteGrid{}, apiKeyID).
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
func TestUsageSecondPollInTheSameBucketReadsNoDatabase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for attempt := 0; ; attempt++ {
		require.Less(t, attempt, 3, "the clock crossed a bucket boundary on every attempt")
		f := newUsageKeyReportFixture(t)
		f.expectTrend(11, 7, 21)
		f.expectModels(7, 34)

		_, endBefore := usageDefaultRange(timezone.Now())
		first, second := f.poll(t), f.poll(t)
		if _, endAfter := usageDefaultRange(timezone.Now()); !endBefore.Equal(endAfter) {
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
}

// The model report is scanned once per 5 minute bucket: every instant of a bucket shares one scan, the next
// bucket needs its own, and a bucket whose answer is still fresh is not scanned again when the clock is back in it.
func TestUsageModelReportIsScannedOncePerBucket(t *testing.T) {
	f := newUsageKeyReportFixture(t)
	base := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC) // on a bucket boundary
	models := func(at time.Time) int64 {
		t.Helper()
		start, end := usageDefaultRange(at)
		require.False(t, end.After(at))
		stats, err := f.svc.GetAPIKeyModelStats(context.Background(), 7, start, end)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		return stats[0].Requests
	}

	f.expectModels(7, 1)
	for _, offset := range []time.Duration{0, time.Nanosecond, 30 * time.Second, 2 * time.Minute, usageDefaultRangeBucket - time.Nanosecond} {
		require.EqualValues(t, 1, models(base.Add(offset)), "+%v: one scan for the whole bucket", offset)
	}

	f.expectModels(7, 2)
	require.EqualValues(t, 2, models(base.Add(usageDefaultRangeBucket)), "the next bucket is scanned once")
	require.EqualValues(t, 2, models(base.Add(usageDefaultRangeBucket+4*time.Minute)))
	require.EqualValues(t, 1, models(base), "the previous bucket's answer is still within its five minutes")
	require.NoError(t, f.mock.ExpectationsWereMet())

	// Another key never reads this key's answer, in the same bucket.
	f.expectModels(9, 3)
	start, end := usageDefaultRange(base)
	other, err := f.svc.GetAPIKeyModelStats(context.Background(), 9, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 3, other[0].Requests)
	require.NoError(t, f.mock.ExpectationsWereMet())
}
