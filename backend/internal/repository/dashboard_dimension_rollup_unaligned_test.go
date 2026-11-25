package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// The rollups hold whole local hours, but the usage pages ask for rolling windows ("last 24 hours" = now minus
// 24 h) that start at any minute. These tests pin how such a window is split: the part of the first hour before
// the next hour boundary is read from usage_logs, whole closed hours come from the rollups, and the open hour is
// read from usage_logs as before. Whether the three parts add up to the plain raw answer is checked against a
// real PostgreSQL in dashboard_dimension_rollup_unaligned_pg_test.go.

// instant matches a time argument by the moment it denotes, whatever its zone or monotonic reading.
type instant time.Time

func (i instant) Match(v driver.Value) bool {
	t, ok := v.(time.Time)
	return ok && t.Equal(time.Time(i))
}

func useRollupTestTimezone(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, timezone.Init(name))
	t.Cleanup(func() { _ = timezone.Init("UTC") })
}

func TestRollupHourBoundariesFollowTheLocalZone(t *testing.T) {
	for _, zone := range []string{"UTC", "Asia/Shanghai", "Asia/Kolkata", "Asia/Kathmandu"} {
		t.Run(zone, func(t *testing.T) {
			useRollupTestTimezone(t, zone)
			boundary := time.Date(2026, 11, 20, 10, 0, 0, 0, timezone.Location())
			for _, tc := range []struct {
				name        string
				at          time.Time
				floor, ceil time.Time
			}{
				{"on a boundary", boundary, boundary, boundary},
				{"one nanosecond after", boundary.Add(time.Nanosecond), boundary, boundary.Add(time.Hour)},
				{"mid hour", boundary.Add(17*time.Minute + 3*time.Second), boundary, boundary.Add(time.Hour)},
				{"one nanosecond before the next", boundary.Add(time.Hour - time.Nanosecond), boundary, boundary.Add(time.Hour)},
				{"next boundary", boundary.Add(time.Hour), boundary.Add(time.Hour), boundary.Add(time.Hour)},
			} {
				require.True(t, tc.floor.Equal(floorLocalHour(tc.at)), "%s: floor", tc.name)
				require.True(t, tc.ceil.Equal(ceilLocalHour(tc.at)), "%s: ceil", tc.name)
			}
			// The buckets are cut in local time; in a half-hour zone that is not where Truncate(time.Hour) lands.
			_, offset := boundary.Zone()
			require.Equal(t, offset%3600 == 0, boundary.Equal(boundary.Truncate(time.Hour)))
		})
	}
}

func TestRollupWindowRawRanges(t *testing.T) {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	head := timeRange{base.Add(17 * time.Minute), base.Add(time.Hour)}
	tail := timeRange{base.Add(10 * time.Hour), base.Add(10*time.Hour + 25*time.Minute)}
	for _, tc := range []struct {
		name   string
		window rollupWindow
		want   []timeRange
	}{
		{"unaligned start and open end", rollupWindow{start: head.start, from: head.end, cutoff: tail.start, to: tail.end}, []timeRange{head, tail}},
		{"aligned start, closed end: rollups only", rollupWindow{start: base, from: base, cutoff: tail.start, to: tail.start}, []timeRange{}},
		{"unaligned start, closed end: head only", rollupWindow{start: head.start, from: head.end, cutoff: tail.start, to: tail.start}, []timeRange{head}},
		{"aligned start, open end: tail only", rollupWindow{start: base, from: base, cutoff: tail.start, to: tail.end}, []timeRange{tail}},
	} {
		require.Equal(t, tc.want, tc.window.rawRanges(), tc.name)
	}
}

func newRollupReaderMock(t *testing.T) (*usageLogRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.SetDashboardRollupReadEnabled(true)
	return repo, mock
}

// expectRollupBounds scripts the three statements rollupBoundsForDimension runs when the rollups can be read.
func expectRollupBounds(mock sqlmock.Sqlmock, from, cutoff time.Time, dimensionType string, userID int64) {
	mock.ExpectQuery("usage_dashboard_aggregation_watermark").
		WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(time.Now()))
	mock.ExpectQuery(`SELECT COALESCE\(MIN\(bucket_start\)`).
		WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(from.Add(-48 * time.Hour)))
	args := []driver.Value{instant(from), instant(cutoff), dimensionType}
	if userID > 0 {
		args = append(args, userID)
	}
	mock.ExpectQuery(`SELECT \(`).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
}

func TestRollupBoundsSplitAnUnalignedStart(t *testing.T) {
	useRollupTestTimezone(t, "Asia/Shanghai") // whole-hour offset: local hour boundaries are UTC hour boundaries
	base := time.Now().UTC().Truncate(time.Hour).Add(-10 * time.Hour)
	firstProcessedHour := base.Add(-48 * time.Hour)
	const (
		asksNothing = iota
		asksWatermark
		asksCoverage
		asksCompleteness
	)
	for _, tc := range []struct {
		name         string
		start, end   time.Time
		watermarkAge time.Duration
		asks         int
		wantFrom     time.Time
		wantCutoff   time.Time
		wantOK       bool
	}{
		{name: "unaligned start and end", start: base.Add(17*time.Minute + 3*time.Second), end: base.Add(6*time.Hour + 25*time.Minute),
			asks: asksCompleteness, wantFrom: base.Add(time.Hour), wantCutoff: base.Add(6 * time.Hour), wantOK: true},
		{name: "aligned start keeps the old shape: no head", start: base, end: base.Add(6*time.Hour + 25*time.Minute),
			asks: asksCompleteness, wantFrom: base, wantCutoff: base.Add(6 * time.Hour), wantOK: true},
		{name: "one nanosecond past a boundary", start: base.Add(time.Nanosecond), end: base.Add(6 * time.Hour),
			asks: asksCompleteness, wantFrom: base.Add(time.Hour), wantCutoff: base.Add(6 * time.Hour), wantOK: true},
		{name: "exactly one whole closed hour", start: base.Add(50 * time.Minute), end: base.Add(2*time.Hour + 20*time.Minute),
			asks: asksCompleteness, wantFrom: base.Add(time.Hour), wantCutoff: base.Add(2 * time.Hour), wantOK: true},
		{name: "inside one hour: plain raw query", start: base.Add(5 * time.Minute), end: base.Add(35 * time.Minute), asks: asksNothing},
		{name: "crosses one boundary but holds no whole hour", start: base.Add(50 * time.Minute), end: base.Add(time.Hour + 20*time.Minute), asks: asksNothing},
		{name: "head hour before the coverage, first rollup hour covered", start: firstProcessedHour.Add(-20 * time.Minute), end: base.Add(6 * time.Hour),
			asks: asksCompleteness, wantFrom: firstProcessedHour, wantCutoff: base.Add(6 * time.Hour), wantOK: true},
		{name: "first rollup hour before the coverage", start: firstProcessedHour.Add(-70 * time.Minute), end: base.Add(6 * time.Hour), asks: asksCoverage},
		{name: "stale watermark from an earlier hour", start: base.Add(17 * time.Minute), end: base.Add(6 * time.Hour), watermarkAge: 3 * time.Hour, asks: asksWatermark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, mock := newRollupReaderMock(t)
			if tc.asks >= asksWatermark {
				mock.ExpectQuery("usage_dashboard_aggregation_watermark").
					WillReturnRows(sqlmock.NewRows([]string{"last_aggregated_at"}).AddRow(time.Now().Add(-tc.watermarkAge)))
			}
			if tc.asks >= asksCoverage {
				mock.ExpectQuery(`SELECT COALESCE\(MIN\(bucket_start\)`).
					WillReturnRows(sqlmock.NewRows([]string{"coverage_start"}).AddRow(firstProcessedHour))
			}
			if tc.asks >= asksCompleteness {
				// The completeness proof covers the rollup hours only: the raw head never reads the rollups.
				mock.ExpectQuery(`SELECT \(`).WithArgs(instant(tc.wantFrom), instant(tc.wantCutoff), "user").
					WillReturnRows(sqlmock.NewRows([]string{"complete"}).AddRow(true))
			}

			window, ok := repo.rollupBoundsForDimension(context.Background(), tc.start, tc.end, "user", 0)

			require.Equal(t, tc.wantOK, ok)
			require.NoError(t, mock.ExpectationsWereMet())
			if !ok {
				return
			}
			require.True(t, tc.start.Equal(window.start))
			require.True(t, tc.wantFrom.Equal(window.from), "first rollup hour: %s", window.from)
			require.True(t, tc.wantCutoff.Equal(window.cutoff), "end of the closed hours: %s", window.cutoff)
			require.True(t, tc.end.Equal(window.to))
			for _, raw := range window.rawRanges() {
				require.True(t, raw.end.After(raw.start))
			}
		})
	}
}

func TestUnalignedWindowReadsBothEdgesFromUsageLogs(t *testing.T) {
	useRollupTestTimezone(t, "Asia/Shanghai")
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour).Add(-12 * time.Hour)
	start := hour.Add(17*time.Minute + 3*time.Second) // 17 minutes into the first hour
	from := hour.Add(time.Hour)                       // the first hour that comes from the rollups
	cutoff := hour.Add(10 * time.Hour)                // the last closed hour ends here
	end := cutoff.Add(25 * time.Minute)               // 25 minutes into an hour that is not rolled up
	metricCols := []string{
		"bucket_start", "dimension_key", "user_id", "group_id", "endpoint_type",
		"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"total_cost", "actual_cost", "account_cost", "duration_count", "total_duration_ms",
	}
	rollupRow := func(key string, userID, groupID, requests int64) *sqlmock.Rows {
		return sqlmock.NewRows(metricCols).AddRow(from, key, userID, groupID, "", requests, int64(10), int64(20), int64(2), int64(1), 0.5, 0.4, 0.6, requests, requests*100)
	}
	expectRollupRows := func(mock sqlmock.Sqlmock, rows *sqlmock.Rows, args ...driver.Value) {
		mock.ExpectQuery("FROM usage_dashboard_hourly_dimensions").WithArgs(args...).WillReturnRows(rows)
	}
	// Both raw reads are the query the caller runs for a whole range, narrowed to the two edges.
	headArgs := func(extra ...driver.Value) []driver.Value {
		return append([]driver.Value{instant(start), instant(from)}, extra...)
	}
	tailArgs := func(extra ...driver.Value) []driver.Value {
		return append([]driver.Value{instant(cutoff), instant(end)}, extra...)
	}

	t.Run("model stats", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user_model", 7)
		expectRollupRows(mock, rollupRow("m1", 7, 0, 4), "user_model", instant(from), instant(cutoff), int64(7))
		cols := []string{"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}
		mock.ExpectQuery("FROM usage_logs").WithArgs(headArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("m1", int64(1), int64(5), int64(5), int64(0), int64(0), int64(10), 0.1, 0.1, 0.1).
			AddRow("m2", int64(2), int64(1), int64(1), int64(0), int64(0), int64(2), 0.2, 0.2, 0.2))
		mock.ExpectQuery("FROM usage_logs").WithArgs(tailArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow("m1", int64(3), int64(5), int64(5), int64(0), int64(0), int64(10), 0.3, 0.3, 0.3))

		got, covered, err := repo.GetModelStatsWithRollups(ctx, start, end, UsageLogFilters{UserID: 7}, usagestats.ModelSourceRequested)

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, got, 2)
		require.Equal(t, "m1", got[0].Model)
		require.EqualValues(t, 4+1+3, got[0].Requests, "rollup hours + head + tail")
		require.EqualValues(t, 33+10+10, got[0].TotalTokens)
		require.InDelta(t, 0.5+0.1+0.3, got[0].Cost, 1e-9)
		require.Equal(t, "m2", got[1].Model)
		require.EqualValues(t, 2, got[1].Requests)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("usage trend", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user", 7)
		expectRollupRows(mock, rollupRow("7", 7, 0, 4), "user", instant(from), instant(cutoff), int64(7))
		cols := []string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}
		headDate, tailDate := formatRollupDate(start, "hour"), formatRollupDate(cutoff, "hour")
		mock.ExpectQuery("FROM usage_logs").WithArgs(headArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow(headDate, int64(1), int64(1), int64(1), int64(0), int64(0), int64(2), 0.1, 0.1))
		mock.ExpectQuery("FROM usage_logs").WithArgs(tailArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow(tailDate, int64(3), int64(3), int64(3), int64(0), int64(0), int64(6), 0.3, 0.3))

		got, covered, err := repo.GetUsageTrendWithRollups(ctx, start, end, "hour", UsageLogFilters{UserID: 7})

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, got, 3, "head hour, the rolled-up hour, tail hour")
		require.Equal(t, headDate, got[0].Date)
		require.EqualValues(t, 1, got[0].Requests)
		require.Equal(t, formatRollupDate(from, "hour"), got[1].Date)
		require.EqualValues(t, 4, got[1].Requests)
		require.Equal(t, tailDate, got[2].Date)
		require.EqualValues(t, 3, got[2].Requests)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("group stats", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user_group", 7)
		expectRollupRows(mock, rollupRow("3", 7, 3, 4), "user_group", instant(from), instant(cutoff), int64(7))
		cols := []string{"group_id", "group_name", "requests", "total_tokens", "cost", "actual_cost", "account_cost"}
		mock.ExpectQuery("FROM usage_logs ul").WithArgs(headArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).AddRow(int64(3), "G", int64(1), int64(4), 0.1, 0.1, 0.0))
		mock.ExpectQuery("FROM usage_logs ul").WithArgs(tailArgs(int64(7))...).WillReturnRows(sqlmock.NewRows(cols).AddRow(int64(3), "G", int64(3), int64(6), 0.3, 0.3, 0.0))
		mock.ExpectQuery("FROM groups").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(3), "G"))

		got, covered, err := repo.GetGroupStatsWithRollups(ctx, start, end, UsageLogFilters{UserID: 7, OmitAccountCost: true})

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, got, 1)
		require.EqualValues(t, 4+1+3, got[0].Requests)
		require.EqualValues(t, 33+4+6, got[0].TotalTokens)
		require.Equal(t, "G", got[0].GroupName)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("usage stats", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user_endpoint", 7)
		expectRollupRows(mock, rollupRow("7", 7, 0, 4), "user", instant(from), instant(cutoff), int64(7))
		summaryCols := []string{"c", "i", "o", "cc", "cr", "cost", "actual", "account", "avg"}
		// The stats query takes user, start, end; the duration summary takes start, end, user.
		mock.ExpectQuery(`AVG\(duration_ms\)`).WithArgs(int64(7), instant(start), instant(from)).WillReturnRows(sqlmock.NewRows(summaryCols).
			AddRow(int64(1), int64(5), int64(5), int64(0), int64(0), 0.1, 0.1, 0.0, 50.0))
		mock.ExpectQuery(`COUNT\(duration_ms\)`).WithArgs(instant(start), instant(from), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"sum", "count"}).AddRow(50.0, int64(1)))
		mock.ExpectQuery(`AVG\(duration_ms\)`).WithArgs(int64(7), instant(cutoff), instant(end)).WillReturnRows(sqlmock.NewRows(summaryCols).
			AddRow(int64(3), int64(15), int64(15), int64(0), int64(0), 0.3, 0.3, 0.0, 100.0))
		mock.ExpectQuery(`COUNT\(duration_ms\)`).WithArgs(instant(cutoff), instant(end), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"sum", "count"}).AddRow(300.0, int64(3)))

		got, covered, err := repo.GetStatsWithRollups(ctx, UsageLogFilters{UserID: 7, StartTime: &start, EndTime: &end, StatsScope: usagestats.StatsScopeSummary, OmitAccountCost: true})

		require.NoError(t, err)
		require.True(t, covered)
		require.EqualValues(t, 4+1+3, got.TotalRequests)
		require.EqualValues(t, 10+5+15, got.TotalInputTokens)
		require.InDelta(t, (400.0+50.0+300.0)/(4+1+3), got.AverageDurationMs, 1e-9, "duration sum and count are added over all three parts")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user usage trend", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user", 0)
		expectRollupRows(mock, rollupRow("7", 7, 0, 4), "user", instant(from), instant(cutoff))
		cols := []string{"date", "user_id", "requests", "tokens", "cost", "actual_cost"}
		headDate, tailDate := formatRollupDate(start, "hour"), formatRollupDate(cutoff, "hour")
		mock.ExpectQuery("FROM usage_logs").WithArgs(headArgs()...).WillReturnRows(sqlmock.NewRows(cols).AddRow(headDate, int64(7), int64(1), int64(2), 0.1, 0.1))
		mock.ExpectQuery("FROM usage_logs").WithArgs(tailArgs()...).WillReturnRows(sqlmock.NewRows(cols).AddRow(tailDate, int64(7), int64(3), int64(6), 0.3, 0.3))
		mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows([]string{"id", "email", "username"}).AddRow(int64(7), "u@example.com", "u"))

		got, covered, err := repo.GetUserUsageTrendWithRollups(ctx, start, end, "hour", 12)

		require.NoError(t, err)
		require.True(t, covered)
		requests := map[string]int64{}
		for _, p := range got {
			requests[p.Date] = p.Requests
			require.Equal(t, "u@example.com", p.Email)
		}
		require.Equal(t, map[string]int64{headDate: 1, formatRollupDate(from, "hour"): 4, tailDate: 3}, requests)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user spending ranking", func(t *testing.T) {
		repo, mock := newRollupReaderMock(t)
		expectRollupBounds(mock, from, cutoff, "user", 0)
		expectRollupRows(mock, rollupRow("7", 7, 0, 4), "user", instant(from), instant(cutoff))
		cols := []string{"user_id", "email", "username", "actual_cost", "requests", "tokens", "total_actual_cost", "total_requests", "total_tokens"}
		mock.ExpectQuery("FROM usage_logs").WithArgs(headArgs(1_000_000)...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow(int64(7), "", "", 0.1, int64(1), int64(2), 0.1, int64(1), int64(2)))
		mock.ExpectQuery("FROM usage_logs").WithArgs(tailArgs(1_000_000)...).WillReturnRows(sqlmock.NewRows(cols).
			AddRow(int64(7), "", "", 0.3, int64(3), int64(6), 0.3, int64(3), int64(6)))
		mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows([]string{"id", "email", "username"}).AddRow(int64(7), "u@example.com", "u"))

		got, covered, err := repo.GetUserSpendingRankingWithRollups(ctx, start, end, 12)

		require.NoError(t, err)
		require.True(t, covered)
		require.Len(t, got.Ranking, 1)
		require.EqualValues(t, 4+1+3, got.Ranking[0].Requests)
		require.EqualValues(t, 4+1+3, got.TotalRequests)
		require.InDelta(t, 0.4+0.1+0.3, got.TotalActualCost, 1e-9)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// The rollups keep one endpoint row per hour. A window with an hour-aligned start and a closed end has no raw
// edge whose merge would add the hours of one endpoint up, so the reader must do it itself.
func TestRollupEndpointRowsOfDifferentHoursAreAddedUp(t *testing.T) {
	useRollupTestTimezone(t, "Asia/Shanghai")
	repo, mock := newRollupReaderMock(t)
	start := time.Now().UTC().Truncate(time.Hour).Add(-12 * time.Hour)
	end := start.Add(4 * time.Hour)
	expectRollupBounds(mock, start, end, "user_endpoint", 7)
	cols := []string{
		"bucket_start", "dimension_key", "user_id", "group_id", "endpoint_type",
		"total_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"total_cost", "actual_cost", "account_cost", "duration_count", "total_duration_ms",
	}
	mock.ExpectQuery("FROM usage_dashboard_hourly_dimensions").WithArgs("user_endpoint", instant(start), instant(end), int64(7)).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(start, "/v1/messages", int64(7), int64(0), "inbound", int64(3), int64(10), int64(20), int64(0), int64(0), 0.5, 0.4, 0.0, int64(3), int64(300)).
			AddRow(start.Add(time.Hour), "/v1/messages", int64(7), int64(0), "inbound", int64(4), int64(10), int64(20), int64(0), int64(0), 0.25, 0.2, 0.0, int64(4), int64(400)).
			AddRow(start.Add(time.Hour), "/v1/responses", int64(7), int64(0), "inbound", int64(1), int64(1), int64(1), int64(0), int64(0), 0.1, 0.1, 0.0, int64(1), int64(10)))

	got, covered, err := repo.GetStatsWithRollups(context.Background(), UsageLogFilters{UserID: 7, StartTime: &start, EndTime: &end, StatsScope: usagestats.StatsScopeEndpoints})

	require.NoError(t, err)
	require.True(t, covered)
	require.Len(t, got.Endpoints, 2, "one entry per endpoint, not one per endpoint and hour")
	require.Equal(t, "/v1/messages", got.Endpoints[0].Endpoint)
	require.EqualValues(t, 7, got.Endpoints[0].Requests)
	require.EqualValues(t, 60, got.Endpoints[0].TotalTokens)
	require.InDelta(t, 0.75, got.Endpoints[0].Cost, 1e-9)
	require.Equal(t, "/v1/responses", got.Endpoints[1].Endpoint)
	require.NoError(t, mock.ExpectationsWereMet())
}

// A window that holds no whole closed hour never touches the rollups: the caller keeps its plain raw query.
func TestWindowWithoutAWholeHourStaysOnTheRawPath(t *testing.T) {
	useRollupTestTimezone(t, "Asia/Shanghai")
	repo, mock := newRollupReaderMock(t)
	base := time.Now().UTC().Truncate(time.Hour).Add(-6 * time.Hour)
	start, end := base.Add(5*time.Minute), base.Add(35*time.Minute)

	got, covered, err := repo.GetModelStatsWithRollups(context.Background(), start, end, UsageLogFilters{UserID: 7}, usagestats.ModelSourceRequested)

	require.NoError(t, err)
	require.False(t, covered)
	require.Nil(t, got)
	require.NoError(t, mock.ExpectationsWereMet(), "no statement at all")
}
