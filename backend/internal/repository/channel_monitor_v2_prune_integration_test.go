//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 渠道监控 v2 稳态保留期清理（channelMonitorV2Repository.PruneRetention）对真实 PostgreSQL 的测试。
//
// 问题：历史回填走完以后稳态每分钟只调 RecomputeLiveRange（不清理），保留期清理只存在于 RecomputeRange 里，
// 于是 1m 事实表和汇总表一直涨过保留期。PruneRetention 在稳态下按 channelMonitorV2RetentionRules 分批、
// 有界地清。每个用例在一个私有 schema 里（与聚合差分测试同一套），表结构直接取自建表迁移。

// 固定的「现在」：分钟对齐，保留期边界因此是整分钟，±1 分钟的行落在边界两侧。
var cmv2PruneNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

type cmv2PruneEnv struct {
	t    *testing.T
	ctx  context.Context
	side *cmv2Side
	repo *channelMonitorV2Repository
}

func newCMV2PruneEnv(t *testing.T) *cmv2PruneEnv {
	t.Helper()
	e := &cmv2PruneEnv{t: t, ctx: context.Background()}
	e.side = newCMV2Side(t, "prune", "UTC", func(db *sql.DB) (cmv2Impl, func()) {
		e.repo = &channelMonitorV2Repository{db: db}
		return e.repo, func() {}
	})
	// 历史回填已走完：游标在 90 天边缘之外（稳态）。
	e.setCursor(cmv2PruneNow.Add(-91 * 24 * time.Hour))
	return e
}

func (e *cmv2PruneEnv) exec(query string, args ...any) {
	e.t.Helper()
	e.side.exec(e.t, query, args...)
}

func (e *cmv2PruneEnv) setCursor(cursor time.Time) {
	e.t.Helper()
	e.exec(`UPDATE channel_monitor_v2_watermarks SET backfill_cursor = $1 WHERE id = 1`, cursor)
}

// insertRow 往一张表里插一行，bucket_start 取 at，model 取 tag（让每行主键唯一、也方便按行数点名）。
func (e *cmv2PruneEnv) insertRow(table string, bucketSeconds int, at time.Time, tag string) {
	e.t.Helper()
	cols := map[string]string{
		"channel_monitor_v2_metrics_1m":                `(bucket_start, platform, group_id, model) VALUES ($1, 'anthropic', 1, $2)`,
		"channel_monitor_v2_user_metrics_1m":           `(bucket_start, platform, group_id, model, user_id) VALUES ($1, 'anthropic', 1, $2, 7)`,
		"channel_monitor_v2_error_metrics_1m":          `(bucket_start, platform, group_id, model, error_category, taxonomy_version) VALUES ($1, 'anthropic', 1, $2, 'timeout', 1)`,
		"channel_monitor_v2_latency_histograms_1m":     `(bucket_start, platform, group_id, model, user_id, metric, upper_bound_ms) VALUES ($1, 'anthropic', 1, $2, 0, 'ttft', 100)`,
		"channel_monitor_v2_metrics_rollup":            `(bucket_start, bucket_seconds, platform, group_id, model) VALUES ($1, $3, 'anthropic', 1, $2)`,
		"channel_monitor_v2_user_metrics_rollup":       `(bucket_start, bucket_seconds, platform, group_id, model, user_id) VALUES ($1, $3, 'anthropic', 1, $2, 7)`,
		"channel_monitor_v2_error_metrics_rollup":      `(bucket_start, bucket_seconds, platform, group_id, model, error_category, taxonomy_version) VALUES ($1, $3, 'anthropic', 1, $2, 'timeout', 1)`,
		"channel_monitor_v2_latency_histograms_rollup": `(bucket_start, bucket_seconds, platform, group_id, model, user_id, metric, upper_bound_ms) VALUES ($1, $3, 'anthropic', 1, $2, 0, 'ttft', 100)`,
	}
	values, ok := cols[table]
	require.True(e.t, ok, "no insert template for %s", table)
	args := []any{at, tag}
	if bucketSeconds != 0 {
		args = append(args, bucketSeconds)
	}
	e.exec("INSERT INTO "+table+" "+values, args...)
}

func (e *cmv2PruneEnv) count(table string, bucketSeconds int, tagPrefix string) int {
	e.t.Helper()
	query := "SELECT count(*) FROM " + table + " WHERE model LIKE $1"
	args := []any{tagPrefix + "%"}
	if bucketSeconds != 0 {
		query += " AND bucket_seconds = $2"
		args = append(args, bucketSeconds)
	}
	var n int
	require.NoError(e.t, e.side.db.QueryRowContext(e.ctx, query, args...).Scan(&n))
	return n
}

func TestChannelMonitorV2PruneRetentionDropsExpiredRowsAndKeepsTheBoundary(t *testing.T) {
	e := newCMV2PruneEnv(t)

	type probe struct {
		rule channelMonitorV2RetentionRule
		tag  string
		keep bool
	}
	var probes []probe
	for i, rule := range channelMonitorV2RetentionRules {
		cutoff := cmv2PruneNow.Add(-rule.retention)
		for _, p := range []struct {
			name string
			at   time.Time
			keep bool
		}{
			{"far", cutoff.Add(-30 * 24 * time.Hour), false},
			{"minus1m", cutoff.Add(-time.Minute), false},
			{"edge", cutoff, true}, // bucket_start < cutoff：恰好在边界上的行保留
			{"plus1m", cutoff.Add(time.Minute), true},
			{"fresh", cmv2PruneNow, true},
		} {
			tag := fmt.Sprintf("r%02d-%s", i, p.name)
			e.insertRow(rule.table, rule.bucketSeconds, p.at, tag)
			probes = append(probes, probe{rule: rule, tag: tag, keep: p.keep})
		}
	}

	res, err := e.repo.PruneRetention(e.ctx, cmv2PruneNow)
	require.NoError(t, err)
	require.False(t, res.Backlog)
	require.Equal(t, len(channelMonitorV2RetentionRules), res.Batches, "每条规则一批：都不到一整批，一轮就删完")
	require.Equal(t, int64(2*len(channelMonitorV2RetentionRules)), res.Deleted)

	for _, p := range probes {
		want := 0
		if p.keep {
			want = 1
		}
		require.Equal(t, want, e.count(p.rule.table, p.rule.bucketSeconds, p.tag),
			"%s bucket_seconds=%d row %s (retention %s)", p.rule.table, p.rule.bucketSeconds, p.tag, p.rule.retention)
	}
}

func TestChannelMonitorV2PruneRetentionOnEmptyTablesIsANoOp(t *testing.T) {
	e := newCMV2PruneEnv(t)
	res, err := e.repo.PruneRetention(e.ctx, cmv2PruneNow)
	require.NoError(t, err)
	require.Zero(t, res.Deleted)
	require.False(t, res.Backlog)
	require.Equal(t, len(channelMonitorV2RetentionRules), res.Batches)
}

func TestChannelMonitorV2PruneRetentionWaitsForTheHistoryWalk(t *testing.T) {
	e := newCMV2PruneEnv(t)
	table := "channel_monitor_v2_metrics_1m"
	e.insertRow(table, 0, cmv2PruneNow.Add(-30*24*time.Hour), "expired")
	edge90 := cmv2PruneNow.Add(-channelMonitorV2RetentionMax)

	prune := func(label string) {
		res, err := e.repo.PruneRetention(e.ctx, cmv2PruneNow)
		require.NoError(t, err, label)
		require.Zero(t, res.Deleted, label)
		require.Zero(t, res.Batches, label)
		require.Equal(t, 1, e.count(table, 0, "expired"), "%s: 回填还在走，要用旧的 1m 行重建日汇总，不能清", label)
	}
	e.setCursor(edge90.Add(time.Minute))
	prune("cursor one minute inside the 90d edge")
	e.setCursor(cmv2PruneNow.Add(-3 * time.Hour))
	prune("cursor three hours back (history walk just started)")
	e.exec(`UPDATE channel_monitor_v2_watermarks SET backfill_cursor = NULL WHERE id = 1`)
	prune("no cursor yet")
	e.exec(`DELETE FROM channel_monitor_v2_watermarks`)
	prune("no watermark row")

	e.exec(`INSERT INTO channel_monitor_v2_watermarks (id, backfill_cursor) VALUES (1, $1)`, edge90)
	res, err := e.repo.PruneRetention(e.ctx, cmv2PruneNow)
	require.NoError(t, err)
	require.Equal(t, int64(1), res.Deleted, "游标恰好在 90 天边缘：回填走完，可以清")
	require.Zero(t, e.count(table, 0, "expired"))
}

// 积压很大时单次运行有界，多次运行后清空；先删最老的。
func TestChannelMonitorV2PruneBacklogIsBoundedPerRunAndDrainsOverRuns(t *testing.T) {
	e := newCMV2PruneEnv(t)
	table := "channel_monitor_v2_metrics_1m"
	cutoff := cmv2PruneNow.Add(-channelMonitorV2RetentionMetrics1m)
	// expired-g：g 越大越老（g=1000 最老）。
	e.exec(`INSERT INTO `+table+` (bucket_start, platform, group_id, model)
		SELECT $1::timestamptz - (g * interval '1 minute'), 'anthropic', 1, 'expired-' || g FROM generate_series(1, 1000) g`, cutoff)
	e.exec(`INSERT INTO `+table+` (bucket_start, platform, group_id, model)
		SELECT $1::timestamptz + (g * interval '1 minute'), 'anthropic', 1, 'kept-' || g FROM generate_series(0, 299) g`, cutoff)

	rules := len(channelMonitorV2RetentionRules)
	limits := channelMonitorV2PruneLimits{batchSize: 100, maxBatches: rules + 4, maxDuration: time.Minute, statementTimeout: 10 * time.Second}
	// 第一轮：每条规则一批（只有 metrics_1m 有数据、删满 100），之后只剩它在循环：共 rules+4 条语句 = 5 批满的。
	res, err := e.repo.pruneRetentionBatched(e.ctx, cmv2PruneNow, limits)
	require.NoError(t, err)
	require.Equal(t, rules+4, res.Batches, "批数上限生效")
	require.Equal(t, int64(500), res.Deleted, "每批至多 batchSize 行，一次运行最多 5 批")
	require.True(t, res.Backlog, "预算用完时还有积压，要告诉调用方")
	require.Equal(t, 500, e.count(table, 0, "expired-"))
	require.Equal(t, 1, e.count(table, 0, "expired-500"), "先删最老的：expired-501..1000 已经没了，expired-500 还在")
	require.Zero(t, e.count(table, 0, "expired-501"))
	require.Equal(t, 300, e.count(table, 0, "kept-"), "没过期的一行不动")

	for i := 0; i < 10 && res.Backlog; i++ {
		res, err = e.repo.pruneRetentionBatched(e.ctx, cmv2PruneNow, limits)
		require.NoError(t, err)
		require.LessOrEqual(t, res.Batches, limits.maxBatches)
	}
	require.False(t, res.Backlog, "多次运行之后清空")
	require.Zero(t, e.count(table, 0, "expired-"))
	require.Equal(t, 300, e.count(table, 0, "kept-"))
}

func TestChannelMonitorV2PruneStopsWhenItsTimeBudgetIsSpent(t *testing.T) {
	e := newCMV2PruneEnv(t)
	table := "channel_monitor_v2_metrics_1m"
	cutoff := cmv2PruneNow.Add(-channelMonitorV2RetentionMetrics1m)
	e.exec(`INSERT INTO `+table+` (bucket_start, platform, group_id, model)
		SELECT $1::timestamptz - (g * interval '1 minute'), 'anthropic', 1, 'expired-' || g FROM generate_series(1, 2000) g`, cutoff)

	// 每批 10 行、满批之后让出 100ms：2000 行要 200 批 ≥ 20 秒；时间预算 500ms 必须先到。
	limits := channelMonitorV2PruneLimits{batchSize: 10, maxBatches: 100000, maxDuration: 500 * time.Millisecond, statementTimeout: 10 * time.Second, pause: 100 * time.Millisecond}
	started := time.Now()
	res, err := e.repo.pruneRetentionBatched(e.ctx, cmv2PruneNow, limits)
	require.NoError(t, err, "时间预算用完不是错误")
	require.Less(t, time.Since(started), 3*time.Second)
	require.True(t, res.Backlog)
	require.Positive(t, res.Deleted)
	require.Less(t, res.Deleted, int64(2000))
	require.Equal(t, 2000-int(res.Deleted), e.count(table, 0, "expired-"))
}

// 语句超时（表被别的事务锁住）：只丢这张表这一轮，其他表照删，下一次不受影响。
func TestChannelMonitorV2PruneStatementTimeoutLeavesOtherTablesAndTheNextRunAlone(t *testing.T) {
	e := newCMV2PruneEnv(t)
	blocked := "channel_monitor_v2_metrics_1m"
	e.insertRow(blocked, 0, cmv2PruneNow.Add(-30*24*time.Hour), "a-expired")
	e.insertRow("channel_monitor_v2_error_metrics_1m", 0, cmv2PruneNow.Add(-30*24*time.Hour), "b-expired")
	e.insertRow("channel_monitor_v2_latency_histograms_1m", 0, cmv2PruneNow.Add(-30*24*time.Hour), "c-expired")

	lockTx, err := e.side.db.BeginTx(e.ctx, nil)
	require.NoError(t, err)
	released := false
	t.Cleanup(func() {
		if !released {
			_ = lockTx.Rollback()
		}
	})
	_, err = lockTx.ExecContext(e.ctx, `LOCK TABLE `+blocked+` IN ACCESS EXCLUSIVE MODE`)
	require.NoError(t, err)

	limits := channelMonitorV2PruneLimits{batchSize: 100, maxBatches: 100, maxDuration: 30 * time.Second, statementTimeout: 500 * time.Millisecond}
	started := time.Now()
	res, err := e.repo.pruneRetentionBatched(e.ctx, cmv2PruneNow, limits)
	require.Error(t, err)
	require.Contains(t, err.Error(), blocked, "错误里点名是哪张表")
	require.Less(t, time.Since(started), 5*time.Second, "语句超时生效，不是一直等锁")
	require.Equal(t, int64(2), res.Deleted, "别的表照删")
	require.Zero(t, e.count("channel_monitor_v2_error_metrics_1m", 0, "b-"))
	require.Zero(t, e.count("channel_monitor_v2_latency_histograms_1m", 0, "c-"))

	require.NoError(t, lockTx.Rollback())
	released = true
	res, err = e.repo.pruneRetentionBatched(e.ctx, cmv2PruneNow, limits)
	require.NoError(t, err, "锁释放以后的下一次运行不受上一次失败影响")
	require.Equal(t, int64(1), res.Deleted)
	require.Zero(t, e.count(blocked, 0, "a-"))
}

// 每条清理语句都能走 bucket_start 的索引：关掉顺序扫描以后计划里不能出现 Seq Scan。
func TestChannelMonitorV2PruneStatementsUseTheBucketStartIndex(t *testing.T) {
	e := newCMV2PruneEnv(t)
	conn, err := e.side.db.Conn(e.ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.ExecContext(e.ctx, `SET enable_seqscan = off`)
	require.NoError(t, err)

	for _, rule := range channelMonitorV2RetentionRules {
		var plan string
		args := channelMonitorV2PruneBatchArgs(rule, cmv2PruneNow, channelMonitorV2DefaultPruneLimits.batchSize)
		require.NoError(t, conn.QueryRowContext(e.ctx, "EXPLAIN (FORMAT JSON) "+channelMonitorV2PruneBatchSQL(rule), args...).Scan(&plan))
		require.Contains(t, plan, "Index Scan", "%s bucket_seconds=%d", rule.table, rule.bucketSeconds)
		require.False(t, strings.Contains(plan, "Seq Scan"), "%s bucket_seconds=%d scans the whole table:\n%s", rule.table, rule.bucketSeconds, plan)
	}
}
