package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

// The user usage page, the key list and /v1/usage ask the same few aggregates over and over. These tests pin how
// the shared cache and the concurrency gate treat them: an answer is only ever reused for the exact same
// question (same user, window and filters), a burst of identical questions costs one scan, a cached answer is
// identical to the uncached one, and a saturated database turns end-user aggregates away without ever touching
// the scans that rate-limit decisions depend on.

var (
	userModelStatCols = []string{"model", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost", "account_cost"}
	userTrendCols     = []string{"date", "requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "total_tokens", "cost", "actual_cost"}
)

func userModelRows(requests int64) *sqlmock.Rows {
	return sqlmock.NewRows(userModelStatCols).
		AddRow("claude-opus", requests, 100, 200, 30, 40, 370, 1.25, 1.0, 0.0).
		AddRow("gpt-5", requests+1, 1, 2, 3, 4, 10, 0.5, 0.4, 0.0)
}

func userTrendRows(requests int64) *sqlmock.Rows {
	return sqlmock.NewRows(userTrendCols).AddRow("2026-11-23", requests, 100, 200, 30, 40, 370, 1.25, 1.0)
}

// userPageFilters is the filter set the user usage handler builds for one user and window.
func userPageFilters(userID int64, start, end time.Time) UsageLogFilters {
	return UsageLogFilters{
		UserID: userID, ModelFilterSource: usagestats.ModelSourceRequested,
		StartTime: &start, EndTime: &end, OmitAccountCost: true,
	}
}

func newCachedUsageRepo(t *testing.T) (*usageLogRepository, sqlmock.Sqlmock, *usageStatsCache) {
	t.Helper()
	cache, _ := newUsageStatsTestCache(t)
	db, mock := newSQLMock(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	repo.statsCache = cache
	return repo, mock, cache
}

func TestUserUsagePageAggregatesNeverCrossUsers(t *testing.T) {
	repo, mock, _ := newCachedUsageRepo(t)
	start, end := usagePageWindow()
	ctx := context.Background()

	for _, userID := range []int64{1, 2} {
		mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE created_at >= \$1 AND created_at < \$2\s+AND user_id = \$3`).
			WithArgs(start, end, userID).WillReturnRows(userModelRows(userID * 10))
		mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE created_at >= \$1 AND created_at < \$2\s+AND user_id = \$3`).
			WithArgs(start, end, userID).WillReturnRows(userTrendRows(userID * 10))
	}
	// Each user twice: the second round is answered from the cache and must still be that user's own numbers.
	for range 2 {
		for _, userID := range []int64{1, 2} {
			models, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, userPageFilters(userID, start, end), usagestats.ModelSourceRequested)
			require.NoError(t, err)
			require.EqualValues(t, userID*10, models[0].Requests)
			trend, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "day", userPageFilters(userID, start, end))
			require.NoError(t, err)
			require.EqualValues(t, userID*10, trend[0].Requests)
		}
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserUsagePageAggregatesSplitByEveryQuestionAndOnlyBy(t *testing.T) {
	start, end := usagePageWindow()
	ctx := context.Background()
	boolPtr := func(v bool) *bool { return &v }
	int16Ptr := func(v int16) *int16 { return &v }
	int8Ptr := func(v int8) *int8 { return &v }

	type variant struct {
		name  string
		mod   func(f *UsageLogFilters)
		shift time.Duration // moves the window end
		same  bool          // true: must be served by the base entry
	}
	variants := []variant{
		{name: "other group", mod: func(f *UsageLogFilters) { f.GroupID = 3 }},
		{name: "other api key", mod: func(f *UsageLogFilters) { f.APIKeyID = 5 }},
		{name: "model filter", mod: func(f *UsageLogFilters) { f.Model = "gpt-5" }},
		{name: "stream", mod: func(f *UsageLogFilters) { f.Stream = boolPtr(true) }},
		{name: "request type", mod: func(f *UsageLogFilters) { f.RequestType = int16Ptr(2) }},
		{name: "billing type", mod: func(f *UsageLogFilters) { f.BillingType = int8Ptr(1) }},
		{name: "billing mode", mod: func(f *UsageLogFilters) { f.BillingMode = "token" }},
		{name: "native compaction", mod: func(f *UsageLogFilters) { f.NativeCompactionV2 = boolPtr(true) }},
		{name: "later window end", mod: func(*UsageLogFilters) {}, shift: 24 * time.Hour},
		{name: "admin shaped (account cost kept)", mod: func(f *UsageLogFilters) { f.OmitAccountCost = false }},
		// The list search box sends request_id to every aggregate too, but no aggregate applies it: it must
		// neither recompute the cards nor give a client a free way around the cache.
		{name: "request id search", mod: func(f *UsageLogFilters) { f.RequestID = "req-123" }, same: true},
	}

	for _, aggregate := range []string{"model stats", "trend"} {
		t.Run(aggregate, func(t *testing.T) {
			repo, mock, _ := newCachedUsageRepo(t)
			ask := func(f UsageLogFilters, e time.Time) {
				var err error
				if aggregate == "model stats" {
					_, err = repo.GetModelStatsWithUsageFiltersBySource(ctx, start, e, f, usagestats.ModelSourceRequested)
				} else {
					_, err = repo.GetUsageTrendWithUsageFilters(ctx, start, e, "day", f)
				}
				require.NoError(t, err)
			}
			expectOne := func() {
				if aggregate == "model stats" {
					mock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userModelRows(1))
				} else {
					mock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userTrendRows(1))
				}
			}

			expectOne()
			ask(userPageFilters(7, start, end), end)
			ask(userPageFilters(7, start, end), end) // base entry reused
			for _, v := range variants {
				f := userPageFilters(7, start, end)
				v.mod(&f)
				e := end.Add(v.shift)
				if !v.same {
					expectOne() // a new question: exactly one more scan
				}
				ask(f, e)
				ask(f, e)
			}
			require.NoError(t, mock.ExpectationsWereMet(), "every distinct question scans once, every repeat is a hit")
		})
	}
}

func TestUsageTrendGranularitySpellingsShareTheEntryTheSQLSees(t *testing.T) {
	repo, mock, _ := newCachedUsageRepo(t)
	start, end := usagePageWindow()
	ctx := context.Background()
	f := userPageFilters(7, start, end)
	for range 4 { // day, hour, week, month are four different groupings
		mock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userTrendRows(1))
	}
	// Anything safeDateFormat does not know is grouped by day, so it must not mint a key of its own.
	for _, granularity := range []string{"day", "", "DAY", "fortnight", "day; DROP TABLE usage_logs", "day"} {
		_, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, granularity, f)
		require.NoError(t, err)
	}
	for _, granularity := range []string{"hour", "week", "month"} {
		_, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, granularity, f)
		require.NoError(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUserUsagePageAggregateBurstReachesTheDatabaseOnce(t *testing.T) {
	repo, mock, _ := newCachedUsageRepo(t)
	start, end := usagePageWindow()
	mock.ExpectQuery(`FROM usage_logs`).WillDelayFor(200 * time.Millisecond).WillReturnRows(userModelRows(5))

	const n = 100
	var wg sync.WaitGroup
	results := make(chan int64, n)
	errs := make(chan error, n)
	startGun := make(chan struct{})
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGun
			models, err := repo.GetModelStatsWithUsageFiltersBySource(context.Background(), start, end, userPageFilters(7, start, end), usagestats.ModelSourceRequested)
			if err == nil {
				results <- models[0].Requests
			}
			errs <- err
		}()
	}
	close(startGun)
	wg.Wait()
	for range n {
		require.NoError(t, <-errs)
		require.EqualValues(t, 5, <-results)
	}
	require.NoError(t, mock.ExpectationsWereMet(), "100 identical questions inside the TTL cost exactly one scan")
}

func TestCachedUserAggregatesEqualTheUncachedPath(t *testing.T) {
	start, end := usagePageWindow()
	ctx := context.Background()
	filters := userPageFilters(7, start, end)

	plainDB, plainMock := newSQLMock(t)
	plain := newUsageLogRepositoryWithSQL(nil, plainDB)
	cached, cachedMock, _ := newCachedUsageRepo(t)

	t.Run("model stats", func(t *testing.T) {
		plainMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userModelRows(5))
		cachedMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userModelRows(5))
		want, err := plain.GetModelStatsWithUsageFiltersBySource(ctx, start, end, filters, usagestats.ModelSourceRequested)
		require.NoError(t, err)
		for range 2 { // first call fills the cache, second is served from it
			got, err := cached.GetModelStatsWithUsageFiltersBySource(ctx, start, end, filters, usagestats.ModelSourceRequested)
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
	})
	t.Run("model stats without rows", func(t *testing.T) {
		other := userPageFilters(8, start, end)
		plainMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(sqlmock.NewRows(userModelStatCols))
		cachedMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(sqlmock.NewRows(userModelStatCols))
		want, err := plain.GetModelStatsWithUsageFiltersBySource(ctx, start, end, other, usagestats.ModelSourceRequested)
		require.NoError(t, err)
		for range 2 {
			got, err := cached.GetModelStatsWithUsageFiltersBySource(ctx, start, end, other, usagestats.ModelSourceRequested)
			require.NoError(t, err)
			require.Equal(t, want, got, "an empty answer stays an empty list, not null")
		}
	})
	t.Run("trend", func(t *testing.T) {
		plainMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userTrendRows(5))
		cachedMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(userTrendRows(5))
		want, err := plain.GetUsageTrendWithUsageFilters(ctx, start, end, "hour", filters)
		require.NoError(t, err)
		for range 2 {
			got, err := cached.GetUsageTrendWithUsageFilters(ctx, start, end, "hour", filters)
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
	})
	require.NoError(t, plainMock.ExpectationsWereMet())
	require.NoError(t, cachedMock.ExpectationsWereMet())
}

func TestAPIKeysUsage24hIsSharedWithinTheTTL(t *testing.T) {
	repo, mock, _ := newCachedUsageRepo(t)
	ctx := context.Background()
	minute := time.Date(2026, 11, 25, 12, 34, 0, 0, time.UTC)
	cols := []string{"api_key_id", "requests", "actual_cost"}
	expect := func(since time.Time, ids string, rows *sqlmock.Rows) {
		mock.ExpectQuery(`(?s)FROM usage_logs\s+WHERE api_key_id = ANY\(\$1\)\s+AND created_at >= \$2\s+GROUP BY api_key_id`).
			WithArgs(ids, since).WillReturnRows(rows)
	}

	// Two visits within the same minute (their `since` differs by seconds) share one scan, and the scan starts at
	// the minute boundary so the cached answer is exactly the answer to its own question.
	expect(minute, "{7,9}", sqlmock.NewRows(cols).AddRow(int64(7), int64(12), 0.25))
	first, err := repo.GetAPIKeysUsage24h(ctx, []int64{9, 7, 7, 0, -3}, minute.Add(12*time.Second))
	require.NoError(t, err)
	second, err := repo.GetAPIKeysUsage24h(ctx, []int64{7, 9}, minute.Add(47*time.Second))
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, int64(12), first[7].Requests)
	require.InDelta(t, 0.25, first[7].Cost, 1e-9)
	require.Contains(t, first, int64(9), "a key without usage is still listed, with zeros")
	require.Equal(t, int64(0), first[9].Requests)
	require.Len(t, first, 2)

	// The next minute and another set of keys are different questions.
	expect(minute.Add(time.Minute), "{7,9}", sqlmock.NewRows(cols).AddRow(int64(7), int64(13), 0.5))
	next, err := repo.GetAPIKeysUsage24h(ctx, []int64{7, 9}, minute.Add(time.Minute+5*time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(13), next[7].Requests)

	expect(minute, "{7}", sqlmock.NewRows(cols).AddRow(int64(7), int64(1), 0.01))
	subset, err := repo.GetAPIKeysUsage24h(ctx, []int64{7}, minute)
	require.NoError(t, err)
	require.Equal(t, int64(1), subset[7].Requests)
	require.NotContains(t, subset, int64(9))

	empty, err := repo.GetAPIKeysUsage24h(ctx, []int64{0, -1}, minute)
	require.NoError(t, err)
	require.Empty(t, empty)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAPIKeysUsage24hCachedEqualsUncached(t *testing.T) {
	ctx := context.Background()
	since := time.Date(2026, 11, 25, 12, 34, 0, 0, time.UTC)
	cols := []string{"api_key_id", "requests", "actual_cost"}
	rows := func() *sqlmock.Rows {
		return sqlmock.NewRows(cols).AddRow(int64(7), int64(12), 0.25).AddRow(int64(11), int64(3), 1.5)
	}
	plainDB, plainMock := newSQLMock(t)
	plain := newUsageLogRepositoryWithSQL(nil, plainDB)
	cached, cachedMock, _ := newCachedUsageRepo(t)
	plainMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(rows())
	cachedMock.ExpectQuery(`FROM usage_logs`).WillReturnRows(rows())

	want, err := plain.GetAPIKeysUsage24h(ctx, []int64{7, 9, 11}, since)
	require.NoError(t, err)
	for range 2 {
		got, err := cached.GetAPIKeysUsage24h(ctx, []int64{7, 9, 11}, since)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	require.NoError(t, plainMock.ExpectationsWereMet())
	require.NoError(t, cachedMock.ExpectationsWereMet())
}

func TestUsageGateGuardsUserAggregatesButNotAccountScans(t *testing.T) {
	repo, mock, cache := newCachedUsageRepo(t)
	cache.gate = newUsageStatsGate(1, 30*time.Millisecond)
	holdUsageStatsSlot(t, cache, 999)
	start, end := usagePageWindow()
	ctx := context.Background()
	f := userPageFilters(7, start, end)

	summary := f
	summary.StatsScope = usagestats.StatsScopeSummary
	calls := map[string]func() error{
		"model stats": func() error {
			_, err := repo.GetModelStatsWithUsageFiltersBySource(ctx, start, end, f, usagestats.ModelSourceRequested)
			return err
		},
		"trend": func() error {
			_, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "day", f)
			return err
		},
		"stats": func() error {
			_, err := repo.GetStatsWithFilters(ctx, summary)
			return err
		},
		"group stats": func() error {
			_, err := repo.GetGroupStatsWithUsageFilters(ctx, start, end, f)
			return err
		},
		"api key daily usage": func() error {
			_, err := repo.GetUsageTrendWithFilters(ctx, start, end, "day", 7, 5, 0, 0, "", nil, nil, nil)
			return err
		},
		"api key model stats": func() error {
			_, err := repo.GetModelStatsWithFilters(ctx, start, end, 0, 5, 0, 0, nil, nil, nil)
			return err
		},
		"keys overview": func() error {
			_, err := repo.GetAPIKeysUsage24h(ctx, []int64{5}, start)
			return err
		},
		"keys 30 day totals": func() error {
			_, err := repo.GetBatchAPIKeyUsageStats(ctx, []int64{5}, time.Time{}, time.Time{})
			return err
		},
		// The COUNT behind the user's record list is the heaviest query of the page; it used to bypass the gate.
		"user record list total": func() error {
			_, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, f)
			return err
		},
	}
	// No database expectation is registered for any of them: being turned away must happen before the scan.
	for name, call := range calls {
		require.ErrorIs(t, call(), ErrUsageStatsBusy, name)
	}
	require.NoError(t, mock.ExpectationsWereMet())

	// The per-account model totals drive rate-limit admission and must never queue behind users' pages.
	mock.ExpectQuery(`(?s)FROM usage_logs.*AND account_id = \$3`).WithArgs(start, end, int64(9)).WillReturnRows(userModelRows(2))
	stats, err := repo.GetModelStatsWithFilters(ctx, start, end, 0, 0, 9, 0, nil, nil, nil)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}
