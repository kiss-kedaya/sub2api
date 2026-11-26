package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"go.uber.org/zap"
)

// The usage part of the user dashboard (GetUserDashboardStats) is answered in two parts once the local day is an
// hour old: the days that are over, and today.
//
//   - The closed part is everything before the start of today: the lifetime rollup (or, where the rollup has
//     nothing, usage_logs since the start of the window) and the per-platform breakdown of the window. A day that is
//     over only changes through a late row, so it is read once and kept for 26 hours; the question names the cut,
//     the cut moves at every local midnight, and the next day asks a new question, so nothing is ever invalidated.
//   - The today part is everything from the start of today: today's hours of the rollup, the open hour from
//     usage_logs, today's per-platform breakdown and the request/token rate. It is read again every cache TTL.
//
// Before, one entry held both and every refresh scanned the user's whole window again for the per-platform
// breakdown (a heavy user's 15 days of usage_logs); now what is scanned again and again is today's rows only.
// Every figure of a part is a sum or a count, so the two parts add up to what one pass over the whole window
// answers (mergeUserDashboardParts); an average is rebuilt from sums and counts, never averaged.

const (
	// userDashboardClosedTTL is how long the closed part is kept: as long as the closed days of a by-day trend
	// (usageStatsKeyClosedDaysTTL, 26 hours: the longest natural day plus an hour of margin). It is above zero on
	// purpose: that gives the closed part the multi-day budget (28 seconds) and the five minute memory of a timeout
	// of that class (keyWindowAggregate), which a month of one user's rows needs when the disk is busy.
	userDashboardClosedTTL = usageStatsKeyClosedDaysTTL

	// userDashboardClosedGrace is how long into a local day the day before is still answered together with today,
	// whole, as before the cut existed. The totals come from an hourly rollup written by a job that re-reads the last
	// two minutes every minute and the last three hours once an hour, and some rows are saved long after their
	// created_at (a Live call is stamped with its start and saved at its end, up to an hour later). Closed at midnight
	// sharp, the last hour of the day that just ended would be kept as it stood until the next midnight; an hour
	// later nearly everything has landed. It also keeps the open hour, which the whole answer cuts on absolute hours,
	// from starting before the cut: in a half-hour zone it does in the first half hour of the day.
	userDashboardClosedGrace = time.Hour
)

// userDashboardPart is what the dashboard adds up over one range of a user's usage. Every figure is a sum or a
// count (the average duration is DurationMs over DurationCount), so parts add up. It is what the cache keeps, as
// JSON. ByPlatform is the per-platform breakdown over the same range: a part that ends at midnight carries no
// "today" columns, a part that starts there carries the same figures in both.
type userDashboardPart struct {
	Requests            int64   `json:"requests"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	Cost                float64 `json:"cost"`
	ActualCost          float64 `json:"actual_cost"`
	DurationMs          int64   `json:"duration_ms"`
	DurationCount       int64   `json:"duration_count"`
	// Rpm and Tpm are what is happening right now, so they live in the part that is read again, not in the closed one.
	Rpm        int64                    `json:"rpm"`
	Tpm        int64                    `json:"tpm"`
	ByPlatform []PlatformDashboardStats `json:"by_platform,omitempty"`
}

// add counts what another range of the same user adds to the usage figures (not the rate, not the platforms).
func (p *userDashboardPart) add(o userDashboardPart) {
	p.Requests += o.Requests
	p.InputTokens += o.InputTokens
	p.OutputTokens += o.OutputTokens
	p.CacheCreationTokens += o.CacheCreationTokens
	p.CacheReadTokens += o.CacheReadTokens
	p.Cost += o.Cost
	p.ActualCost += o.ActualCost
	p.DurationMs += o.DurationMs
	p.DurationCount += o.DurationCount
}

// dashboardNow is the moment the user dashboard counts its days by, in the application's time zone. Tests move it
// through r.clock.
func (r *usageLogRepository) dashboardNow() time.Time {
	if r.clock != nil {
		return r.clock().In(timezone.Location())
	}
	return timezone.Now()
}

// userDashboardCut says where to cut the user's usage: the start of today in the application's zone (not now-24h:
// a day with a DST change has 23 or 25 hours), and whether to cut at all. The answer is left whole, as before,
// when there is no cache (two reads would only cost more), when the window holds no closed day (today starts at or
// before its start), and in the first userDashboardClosedGrace of the local day.
func (r *usageLogRepository) userDashboardCut(now, today, windowStart time.Time) (time.Time, bool) {
	if r.statsCache == nil || !today.After(windowStart) || now.Sub(today) < userDashboardClosedGrace {
		return time.Time{}, false
	}
	return today, true
}

// userDashboardClosedKey and userDashboardTodayKey are the cache entries of the two parts. The closed one names its
// window and the cut (an instant in the application's zone, which is also named), so a new local day asks a new
// question; the today one names the cut it starts at and keeps the cache's own TTL.
func userDashboardClosedKey(userID int64, windowStart, cut time.Time) usageStatsCacheKey {
	return usageStatsCacheKey{
		Query: "user_dashboard_usage_closed", UserID: userID, Timezone: resolveUsageStatsTimezone(),
		Start: windowStart.UTC().Format(time.RFC3339Nano), End: cut.UTC().Format(time.RFC3339Nano),
		TTL: userDashboardClosedTTL,
	}
}

func userDashboardTodayKey(userID int64, cut time.Time) usageStatsCacheKey {
	return usageStatsCacheKey{
		Query: "user_dashboard_usage_today", UserID: userID, Timezone: resolveUsageStatsTimezone(),
		Start: cut.UTC().Format(time.RFC3339Nano),
	}
}

// userDashboardUsage answers the usage part of the dashboard: in two cached parts when the day can be cut, whole
// otherwise. Either part failing fails the call: half a dashboard would be taken for the whole of it.
func (r *usageLogRepository) userDashboardUsage(ctx context.Context, userID int64, now, today time.Time) (*UserDashboardStats, error) {
	windowStart := dashboardSubjectWindowStart(now, today)
	cut, split := r.userDashboardCut(now, today, windowStart)
	if !split {
		return loadUsageStatsCached(r.statsCache, ctx, usageStatsCacheKey{
			Query: "user_dashboard_usage", UserID: userID,
			Start: today.UTC().Format(time.RFC3339Nano),
		}, func(ctx context.Context) (*UserDashboardStats, error) {
			return r.getUserDashboardUsageUncached(ctx, userID, now, windowStart)
		})
	}
	// The window of the closed part starts a lookback before the cut, not before now: it has to be the same for
	// every request of the day, or the entry could not be shared.
	closedStart := dashboardSubjectWindowStart(cut, cut)
	closed, err := loadUsageStatsCached(r.statsCache, ctx, userDashboardClosedKey(userID, closedStart, cut),
		func(ctx context.Context) (*userDashboardPart, error) {
			return r.userDashboardClosedPart(ctx, userID, closedStart, cut)
		})
	if err != nil {
		return nil, err
	}
	// Default TTL, scan budget and memory of a timeout: today holds a fraction of the window's rows.
	todayPart, err := loadUsageStatsCached(r.statsCache, ctx, userDashboardTodayKey(userID, cut),
		func(ctx context.Context) (*userDashboardPart, error) {
			return r.userDashboardTodayPart(ctx, userID, cut)
		})
	if err != nil {
		return nil, err
	}
	return mergeUserDashboardParts(closed, todayPart), nil
}

// userDashboardClosedPart reads everything before the cut: the hourly rollup of the user (lifetime, whole local
// hours, which is how the whole answer reads it), or usage_logs since windowStart where the rollup has nothing for
// the user, and the per-platform breakdown since windowStart.
func (r *usageLogRepository) userDashboardClosedPart(ctx context.Context, userID int64, windowStart, cut time.Time) (*userDashboardPart, error) {
	part := &userDashboardPart{}
	usedRollup, err := r.addUserDashboardRollup(ctx, part, userID, time.Time{}, cut)
	if err != nil {
		return nil, err
	}
	if !usedRollup {
		if err := r.addUserDashboardRaw(ctx, part, userID, windowStart, cut); err != nil {
			return nil, err
		}
	}
	if part.ByPlatform, err = r.userPlatformUsage(ctx, userID, cut, windowStart, cut); err != nil {
		return nil, err
	}
	return part, nil
}

// userDashboardTodayPart reads everything from the cut on: today's whole hours of the rollup and the open hour from
// usage_logs (or all of today from usage_logs where the rollup has nothing for today), the request and token rate,
// and today's per-platform breakdown. The open hour is cut the way the whole answer cuts it.
func (r *usageLogRepository) userDashboardTodayPart(ctx context.Context, userID int64, cut time.Time) (*userDashboardPart, error) {
	part := &userDashboardPart{}
	hourStart := r.dashboardNow().Truncate(time.Hour)
	if hourStart.Before(cut) {
		hourStart = cut
	}
	usedRollup := false
	if hourStart.After(cut) {
		var err error
		if usedRollup, err = r.addUserDashboardRollup(ctx, part, userID, cut, hourStart); err != nil {
			return nil, err
		}
	}
	rawFrom := cut
	if usedRollup {
		rawFrom = hourStart
	}
	if err := r.addUserDashboardRaw(ctx, part, userID, rawFrom, time.Time{}); err != nil {
		return nil, err
	}
	var err error
	if part.Rpm, part.Tpm, err = r.getPerformanceStats(ctx, userID); err != nil {
		return nil, err
	}
	if part.ByPlatform, err = r.userPlatformUsage(ctx, userID, cut, cut, time.Time{}); err != nil {
		return nil, err
	}
	return part, nil
}

// userDashboardRollupSQL and userDashboardRawSQL are the sums of the usage figures from the hourly rollup and from
// usage_logs, in front of a time range (usageRangeSQL). The rollup has no lower bound for the closed part: the whole
// answer reads all of it too.
const (
	userDashboardRollupSQL = `
		SELECT
			COUNT(*),
			COALESCE(SUM(total_requests), 0),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(total_duration_ms), 0),
			COALESCE(SUM(duration_count), 0)
		FROM usage_dashboard_hourly_users
		WHERE user_id = $1`
	userDashboardRawSQL = `
		SELECT
			COUNT(*),
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_cost), 0),
			COALESCE(SUM(actual_cost), 0),
			COALESCE(SUM(COALESCE(duration_ms, 0)), 0),
			COUNT(duration_ms)
		FROM usage_logs
		WHERE user_id = $1`
)

// usageRangeSQL is the " AND column >= $n AND column < $m" of the bounds that are set (a zero time is no bound),
// with their arguments appended to args.
func usageRangeSQL(column string, from, to time.Time, args []any) (string, []any) {
	clause := ""
	if !from.IsZero() {
		args = append(args, from)
		clause += " AND " + column + " >= $" + strconv.Itoa(len(args))
	}
	if !to.IsZero() {
		args = append(args, to)
		clause += " AND " + column + " < $" + strconv.Itoa(len(args))
	}
	return clause, args
}

// addUserDashboardRollup adds the user's hourly rollup rows from [from, to) to part and says whether there was
// anything to use. No rows, and rows without usage (daily-active-user rows can exist with every usage column at
// zero), are a miss and add nothing, as in fillUserDashboardUsageFromHourlyUsers: the caller reads usage_logs instead.
func (r *usageLogRepository) addUserDashboardRollup(ctx context.Context, part *userDashboardPart, userID int64, from, to time.Time) (bool, error) {
	clause, args := usageRangeSQL("bucket_start", from, to, []any{userID})
	var (
		hourlyRows int64
		got        userDashboardPart
	)
	if err := scanSingleRow(ctx, r.sql, userDashboardRollupSQL+clause, args,
		&hourlyRows, &got.Requests, &got.InputTokens, &got.OutputTokens, &got.CacheCreationTokens, &got.CacheReadTokens,
		&got.Cost, &got.ActualCost, &got.DurationMs, &got.DurationCount,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) || isMissingRelationError(err) {
			return false, nil
		}
		return false, err
	}
	if hourlyRows == 0 || got.Requests == 0 {
		return false, nil
	}
	part.add(got)
	return true, nil
}

// addUserDashboardRaw adds the user's rows of usage_logs created in [from, to) to part.
func (r *usageLogRepository) addUserDashboardRaw(ctx context.Context, part *userDashboardPart, userID int64, from, to time.Time) error {
	clause, args := usageRangeSQL("created_at", from, to, []any{userID})
	var got userDashboardPart
	if err := scanSingleRow(ctx, r.sql, userDashboardRawSQL+clause, args,
		&got.Requests, &got.InputTokens, &got.OutputTokens, &got.CacheCreationTokens, &got.CacheReadTokens,
		&got.Cost, &got.ActualCost, &got.DurationMs, &got.DurationCount,
	); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	part.add(got)
	return nil
}

// mergeUserDashboardParts adds the closed part and the today part up into the answer of the whole window. Today's
// figures are the today part's own; the totals are the two parts added; the average duration is rebuilt from the
// summed duration and its count; the platforms are added by name.
func mergeUserDashboardParts(closed, today *userDashboardPart) *UserDashboardStats {
	total := *closed
	total.add(*today)
	stats := &UserDashboardStats{
		TotalRequests:            total.Requests,
		TotalInputTokens:         total.InputTokens,
		TotalOutputTokens:        total.OutputTokens,
		TotalCacheCreationTokens: total.CacheCreationTokens,
		TotalCacheReadTokens:     total.CacheReadTokens,
		TotalCost:                total.Cost,
		TotalActualCost:          total.ActualCost,

		TodayRequests:            today.Requests,
		TodayInputTokens:         today.InputTokens,
		TodayOutputTokens:        today.OutputTokens,
		TodayCacheCreationTokens: today.CacheCreationTokens,
		TodayCacheReadTokens:     today.CacheReadTokens,
		TodayCost:                today.Cost,
		TodayActualCost:          today.ActualCost,

		Rpm:        today.Rpm,
		Tpm:        today.Tpm,
		ByPlatform: mergePlatformStats(closed.ByPlatform, today.ByPlatform),
	}
	stats.TotalTokens = stats.TotalInputTokens + stats.TotalOutputTokens + stats.TotalCacheCreationTokens + stats.TotalCacheReadTokens
	stats.TodayTokens = stats.TodayInputTokens + stats.TodayOutputTokens + stats.TodayCacheCreationTokens + stats.TodayCacheReadTokens
	if total.DurationCount > 0 {
		stats.AverageDurationMs = float64(total.DurationMs) / float64(total.DurationCount)
	}
	return stats
}

// mergePlatformStats adds the per-platform rows of several parts up by platform name, most expensive first like the
// statement's ORDER BY (equal costs by name, so the order does not depend on the map). It is nil when there is
// nothing, as the statement's result is.
func mergePlatformStats(parts ...[]PlatformDashboardStats) []PlatformDashboardStats {
	byName := make(map[string]PlatformDashboardStats)
	for _, part := range parts {
		for _, p := range part {
			row := byName[p.Platform]
			row.Platform = p.Platform
			row.TotalRequests += p.TotalRequests
			row.TotalTokens += p.TotalTokens
			row.TotalActualCost += p.TotalActualCost
			row.TodayRequests += p.TodayRequests
			row.TodayTokens += p.TodayTokens
			row.TodayActualCost += p.TodayActualCost
			byName[p.Platform] = row
		}
	}
	if len(byName) == 0 {
		return nil
	}
	merged := make([]PlatformDashboardStats, 0, len(byName))
	for _, p := range byName {
		merged = append(merged, p)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].TotalActualCost != merged[j].TotalActualCost {
			return merged[i].TotalActualCost > merged[j].TotalActualCost
		}
		return merged[i].Platform < merged[j].Platform
	})
	return merged
}

// userDashboardBusyLogged is the Unix nanos of the last "ran out of budget" log line.
var userDashboardBusyLogged atomic.Int64

// userDashboardBusy answers a dashboard whose database budget ran out, or whose statement was cancelled, with the
// busy answer the concurrency gate already gives (ErrUsageStatsBusy: 429 with Retry-After, not logged as an
// error), instead of a 500. The database is slow right now, not broken, and nothing is lost: the closed part is kept
// for a day, so what is asked again a moment later is today's small part only. Every other error is returned as it is.
func userDashboardBusy(err error) error {
	if !errors.Is(err, context.DeadlineExceeded) && !usagestats.IsStatementTimeout(err) && !usagestats.IsStatementCanceledByRequest(err) {
		return err
	}
	// One line per usageStatsGateLogEvery, like the gate's: during a pile-up every refresh lands here.
	now := time.Now().UnixNano()
	if last := userDashboardBusyLogged.Load(); now-last >= int64(usageStatsGateLogEvery) && userDashboardBusyLogged.CompareAndSwap(last, now) {
		logger.L().Warn("user dashboard usage ran out of database budget, answering busy", zap.Error(err))
	}
	return ErrUsageStatsBusy
}
