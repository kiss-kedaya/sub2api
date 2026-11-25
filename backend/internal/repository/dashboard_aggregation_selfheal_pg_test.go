package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 迟到的 usage_logs 行与每小时自愈（service.DashboardAggregationService.runSelfHeal）的回归测试，
// 跑在真实 PostgreSQL 上。
//
// 机理：每分钟的聚合只重算 [水位-lookback, now)（生产 lookback=120 秒、间隔 60 秒）。created_at 在某小时
// 最后一秒、却在该小时结束约 2 分钟以后才提交的行，之后没有任何一次增量聚合再覆盖那个小时：usage_logs
// 里有、小时汇总里没有，而用量页几乎所有窗口都读小时汇总，于是永久少算。自愈每小时把最近 3 小时重新聚合
// 一遍，把这类行补回来。
//
// 测试按服务的真实调度节奏逐个事件推演：行在各自的提交时刻出现，分钟聚合按「上一次时刻 - lookback」取窗口，
// 自愈取 service.DashboardAggregationSelfHealRange；窗口里的 SQL 走仓储真实的 AggregateRange。
// 一个事务里用临时表盖住真实表（与 rollup 差分测试同一做法），不碰事务以外的数据。
//
// 在别处运行（要求库里已经跑过迁移，测试写入的东西都在被回滚的事务里）：
//
//	ROLLUP_DIFF_TEST_POSTGRES_DSN='postgres://user@127.0.0.1:5432/db?sslmode=disable' \
//	  go test ./internal/repository/ -run 'TestDashboardSelfHeal.*Postgres' -v
//
// 集成测试任务用 dashboard_aggregation_selfheal_integration_test.go 在它自己的 PostgreSQL 上跑同一份。

const selfHealPGDSNEnv = "ROLLUP_DIFF_TEST_POSTGRES_DSN"

func TestDashboardSelfHealLateRowsPostgres(t *testing.T) {
	runDashboardSelfHealLateRows(t, openSelfHealPGFromEnv(t))
}

func TestDashboardSelfHealKeepsDailyActiveUsersPostgres(t *testing.T) {
	runDashboardSelfHealKeepsDailyActiveUsers(t, openSelfHealPGFromEnv(t))
}

func openSelfHealPGFromEnv(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv(selfHealPGDSNEnv)
	if dsn == "" {
		t.Skip(selfHealPGDSNEnv + " not set; the integration job runs the same check on its PostgreSQL")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// selfHealPGEnv: 一个事务，临时表盖住真实的汇总表和来源表。
type selfHealPGEnv struct {
	t      *testing.T
	ctx    context.Context
	tx     *sql.Tx
	agg    *dashboardAggregationRepository
	repo   *usageLogRepository // 走汇总读取：用户在用量页上看到的
	oracle *usageLogRepository // 只用它的原始 SQL
}

func newSelfHealPGEnv(t *testing.T, db *sql.DB, zone string) *selfHealPGEnv {
	t.Helper()
	useRollupTestTimezone(t, zone)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	exec := func(query string, args ...any) {
		t.Helper()
		_, execErr := tx.ExecContext(ctx, query, args...)
		require.NoError(t, execErr, query)
	}
	exec(`SELECT set_config('TimeZone', $1, true)`, zone)
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
	agg := newDashboardAggregationRepositoryWithSQL(tx)
	agg.SetDashboardDimensionRollupsEnabled(true)
	repo := newUsageLogRepositoryWithSQL(nil, tx)
	repo.SetDashboardRollupReadEnabled(true)
	oracle := newUsageLogRepositoryWithSQL(nil, tx)
	oracle.SetDashboardRollupReadEnabled(true)
	return &selfHealPGEnv{t: t, ctx: ctx, tx: tx, agg: agg, repo: repo, oracle: oracle}
}

func (e *selfHealPGEnv) insert(at time.Time, user int64, model string, cost float64) {
	e.t.Helper()
	_, err := e.tx.ExecContext(e.ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, model, requested_model, input_tokens, output_tokens,
		cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, created_at)
		VALUES ($1::bigint, $1::bigint*10, 1, $2::text, $2::text, 10, 5, 0, 0, $3::numeric, $3::numeric, $4::timestamptz)`, user, model, cost, at)
	require.NoError(e.t, err)
}

func (e *selfHealPGEnv) modelStats(start, end time.Time, viaRollup bool) []ModelStat {
	e.t.Helper()
	if viaRollup {
		stats, covered, err := e.repo.GetModelStatsWithRollups(e.ctx, start, end, UsageLogFilters{}, usagestats.ModelSourceRequested)
		require.NoError(e.t, err)
		require.True(e.t, covered, "the window [%s, %s) must be answered from the rollups, not fall back to the raw query", start, end)
		return stats
	}
	stats, err := e.oracle.getModelStatsWithFiltersBySource(e.ctx, start, end, 0, 0, 0, 0, "", nil, nil, nil, usagestats.ModelSourceRequested, "", nil, nil)
	require.NoError(e.t, err)
	return stats
}

func requestsByModel(stats []ModelStat) map[string]int64 {
	out := make(map[string]int64, len(stats))
	for _, s := range stats {
		out[s.Model] = s.Requests
	}
	return out
}

// lateRow 在 hourEnd-1 秒创建，在小时结束 delay 之后才提交。每一行自己一个模型、一个用户，
// 这样每个延迟档位各自有结论。
type lateRow struct {
	label string
	delay time.Duration
	model string
	user  int64
	cost  float64
}

var selfHealLateRows = []lateRow{
	{"20s", 20 * time.Second, "m-late-20s", 9300101, 100},
	{"1m57s", 117 * time.Second, "m-late-1m57s", 9300102, 200},
	{"2m17s", 137 * time.Second, "m-late-2m17s", 9300103, 300},
	{"10m", 10 * time.Minute, "m-late-10m", 9300104, 400},
	{"50m", 50 * time.Minute, "m-late-50m", 9300105, 500},
}

const (
	selfHealOnTimeModel = "m-ontime"
	selfHealOnTimeUser  = int64(9300001)
	// 生产的分钟聚合：DASHBOARD_AGGREGATION_LOOKBACK_SECONDS=120，间隔 60 秒。
	selfHealMinuteLookback = 120 * time.Second
	selfHealMinuteInterval = 60 * time.Second
	selfHealMinutePhase    = 7 * time.Second
)

// lastMinutePassCoveringHour: 小时结束后，最后一次还会重算该小时的分钟聚合发生在多久以后。
// 分钟聚合的窗口起点是上一次时刻减 lookback，起点落进下一个小时就不再重算上一个小时。
func lastMinutePassCoveringHour() time.Duration {
	var last time.Duration
	for off := selfHealMinutePhase; off-selfHealMinuteInterval-selfHealMinuteLookback < 0; off += selfHealMinuteInterval {
		last = off
	}
	return last
}

type selfHealEvent struct {
	at   time.Time
	kind int // 先提交、再跑作业
	do   func()
}

const (
	selfHealCommit = iota
	selfHealJob
)

// replayLateRows 复现一个已关闭的小时 hour：60 行按时到达的、一行在最后一秒按时到达的，
// 然后按服务的真实节奏推演到 hour 结束后 2 小时。healPhase<0 表示不跑自愈（只剩分钟聚合）。
// 返回 hour 与 hour 结束时刻。
func replayLateRows(e *selfHealPGEnv, healPhase time.Duration) (hour, hourEnd time.Time) {
	t := e.t
	t.Helper()
	open := floorLocalHour(time.Now())
	hour = open.Add(-5 * time.Hour) // 远在过去的已关闭小时：读取路径只会把它当成已结束的小时
	hourEnd = hour.Add(time.Hour)

	for i := 0; i < 60; i++ {
		e.insert(hour.Add(time.Duration(i)*time.Minute+time.Second), selfHealOnTimeUser, selfHealOnTimeModel, 1)
	}
	e.insert(hourEnd.Add(-time.Second), selfHealOnTimeUser, selfHealOnTimeModel, 1)

	var events []selfHealEvent
	for _, row := range selfHealLateRows {
		events = append(events, selfHealEvent{at: hourEnd.Add(row.delay), kind: selfHealCommit, do: func() {
			e.insert(hourEnd.Add(-time.Second), row.user, row.model, row.cost)
		}})
	}

	end := hourEnd.Add(2 * time.Hour)
	// 分钟聚合（service.runScheduledAggregation）：[上一次时刻-lookback, 这一次时刻)。
	prev := hourEnd.Add(-5*time.Minute + selfHealMinutePhase - selfHealMinuteInterval)
	for at := hourEnd.Add(-5*time.Minute + selfHealMinutePhase); !at.After(end); at = at.Add(selfHealMinuteInterval) {
		start, passAt := prev.Add(-selfHealMinuteLookback), at
		events = append(events, selfHealEvent{at: passAt, kind: selfHealJob, do: func() {
			require.NoError(t, e.agg.AggregateRange(e.ctx, start, passAt))
		}})
		prev = at
	}
	if healPhase >= 0 {
		for at := hourEnd.Add(healPhase); !at.After(end); at = at.Add(time.Hour) {
			passAt := at
			events = append(events, selfHealEvent{at: passAt, kind: selfHealJob, do: func() {
				start, healEnd := service.DashboardAggregationSelfHealRange(passAt)
				require.NoError(t, e.agg.AggregateRange(e.ctx, start, healEnd))
			}})
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].at.Equal(events[j].at) {
			return events[i].at.Before(events[j].at)
		}
		return events[i].kind < events[j].kind
	})
	for _, ev := range events {
		ev.do()
	}
	// 读取路径要求水位新鲜；其余小时由真实运行里的后续分钟聚合负责。
	require.NoError(t, e.agg.UpdateAggregationWatermark(e.ctx, time.Now()))
	return hour, hourEnd
}

func runDashboardSelfHealLateRows(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, zone := range []string{"Asia/Shanghai", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			// 自愈相位：第一次自愈落在某一行提交之前不久，让那一行要等满一个小时才被下一次自愈碰到。
			// 49m50s 对 50 分钟那行最坏，9m50s 对 10 分钟那行最坏，30s 是第一次自愈在大多数行提交之前。
			for _, phase := range []time.Duration{49*time.Minute + 50*time.Second, 9*time.Minute + 50*time.Second, 30 * time.Second} {
				t.Run("heal phase "+phase.String(), func(t *testing.T) {
					e := newSelfHealPGEnv(t, db, zone)
					hour, hourEnd := replayLateRows(e, phase)
					assertHourRollupsMatchRaw(e, hour, hourEnd)
				})
			}

			// 场景的灵敏度：只有分钟聚合时，2 分 07 秒以后才提交的行确实进不了汇总。
			// 这条失败说明场景不再复现原问题（比如分钟聚合的回看窗口变了），回归测试就空了，要先改场景。
			t.Run("minute job alone loses the late rows", func(t *testing.T) {
				e := newSelfHealPGEnv(t, db, zone)
				hour, hourEnd := replayLateRows(e, -1)
				raw := requestsByModel(e.modelStats(hour, hourEnd, false))
				rollup := requestsByModel(e.modelStats(hour, hourEnd, true))
				lastCovering := lastMinutePassCoveringHour()
				t.Logf("the last minute pass that still re-aggregates the closed hour runs %s after it closed", lastCovering)
				for _, row := range selfHealLateRows {
					lost := row.delay > lastCovering
					t.Logf("late row committed %-5s after the hour closed: raw=%d rollup=%d", row.label, raw[row.model], rollup[row.model])
					require.Equal(t, int64(1), raw[row.model], row.label)
					if lost {
						require.Zero(t, rollup[row.model], "%s: expected to be missing without the self-heal", row.label)
					} else {
						require.Equal(t, int64(1), rollup[row.model], "%s: still covered by the minute job", row.label)
					}
				}
			})
		})
	}
}

// assertHourRollupsMatchRaw: 用量页读到的（汇总）与原始表逐项相等，每个延迟档位单独报。
func assertHourRollupsMatchRaw(e *selfHealPGEnv, hour, hourEnd time.Time) {
	t := e.t
	t.Helper()
	rawStats := e.modelStats(hour, hourEnd, false)
	rollupStats := e.modelStats(hour, hourEnd, true)
	raw, rollup := requestsByModel(rawStats), requestsByModel(rollupStats)
	for _, row := range selfHealLateRows {
		require.Equal(t, int64(1), raw[row.model], "fixture: late row %s must exist in usage_logs", row.label)
		require.Equal(t, raw[row.model], rollup[row.model], "late row committed %s after the hour closed is missing from the rollup read", row.label)
	}
	require.Equal(t, int64(61), raw[selfHealOnTimeModel])
	require.Empty(t, diffModelStats(rollupStats, rawStats), "per-model rollup answer differs from usage_logs")

	// 汇总表本身：小时桶、当天日桶与原始表一致（请求数、费用、活跃用户）。
	var wantRequests, wantUsers int64
	var wantCost float64
	require.NoError(t, e.tx.QueryRowContext(e.ctx,
		`SELECT COUNT(*), COUNT(DISTINCT user_id), COALESCE(SUM(actual_cost), 0)::float8 FROM usage_logs`).Scan(&wantRequests, &wantUsers, &wantCost))
	var gotRequests, gotUsers int64
	var gotCost float64
	require.NoError(t, e.tx.QueryRowContext(e.ctx,
		`SELECT total_requests, active_users, actual_cost::float8 FROM usage_dashboard_hourly WHERE bucket_start = $1`, hour).Scan(&gotRequests, &gotUsers, &gotCost))
	require.Equal(t, wantRequests, gotRequests, "hourly total_requests")
	require.Equal(t, wantUsers, gotUsers, "hourly active_users")
	require.True(t, rollupFloatClose(wantCost, gotCost), "hourly actual_cost: got %v want %v", gotCost, wantCost)

	day := hour.In(timezone.Location()).Format("2006-01-02")
	require.NoError(t, e.tx.QueryRowContext(e.ctx,
		`SELECT total_requests, active_users, actual_cost::float8 FROM usage_dashboard_daily WHERE bucket_date = $1::date`, day).Scan(&gotRequests, &gotUsers, &gotCost))
	require.Equal(t, wantRequests, gotRequests, "daily total_requests")
	require.Equal(t, wantUsers, gotUsers, "daily active_users")
	require.True(t, rollupFloatClose(wantCost, gotCost), "daily actual_cost: got %v want %v", gotCost, wantCost)
}

// 自愈只补窗口里的小时，不能把当天窗口以外的活跃用户弄丢（RecomputeRange 会先删掉窗口所在整天的
// 日活用户、再只按窗口内的小时重建，3 小时窗口会把一天的日活压成窗口内的人数）。
func runDashboardSelfHealKeepsDailyActiveUsers(t *testing.T, db *sql.DB) {
	t.Helper()
	e := newSelfHealPGEnv(t, db, "Asia/Shanghai")
	day := timezone.StartOfDay(time.Now().AddDate(0, 0, -2)) // 本地时区一个整天之前的日子
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	for i, hm := range [][2]int{{1, 10}, {5, 10}, {10, 10}, {20, 10}} {
		e.insert(at(hm[0], hm[1]), int64(9400001+i), "m-day", 1)
	}
	require.NoError(t, e.agg.AggregateRange(e.ctx, day, day.Add(24*time.Hour)))

	// 11:55 来了一个新用户的迟到行；12:25 自愈。窗口是 [09:00, 12:25)，01:10/05:10 的用户在窗口以外。
	e.insert(at(11, 55), 9400099, "m-day", 1)
	start, end := service.DashboardAggregationSelfHealRange(at(12, 25))
	require.True(t, at(9, 0).Equal(start), "window starts on the hour: %s", start)
	require.NoError(t, e.agg.AggregateRange(e.ctx, start, end))

	var requests, users int64
	require.NoError(t, e.tx.QueryRowContext(e.ctx,
		`SELECT total_requests, active_users FROM usage_dashboard_daily WHERE bucket_date = $1::date`, day.Format("2006-01-02")).Scan(&requests, &users))
	require.Equal(t, int64(5), requests, "daily total_requests")
	require.Equal(t, int64(5), users, "daily active_users: 窗口以外的用户不能丢，窗口里的新用户要进来")
	var hourly int64
	require.NoError(t, e.tx.QueryRowContext(e.ctx,
		`SELECT total_requests FROM usage_dashboard_hourly WHERE bucket_start = $1`, at(11, 0)).Scan(&hourly))
	require.Equal(t, int64(1), hourly, "11:00 的小时桶补进了 11:55 的迟到行")
}
