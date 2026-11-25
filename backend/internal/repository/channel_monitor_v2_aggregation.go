package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Platform is derived from group/account (usage_logs has no provider column on upstream schema).
const channelMonitorV2PlatformSQL = `lower(` + usageLogEffectivePlatformExpr + `)`
const channelMonitorV2ModelSQL = `COALESCE(NULLIF(TRIM(ul.requested_model), ''), NULLIF(TRIM(ul.model), ''), 'unknown')`

// Channel health should be measured from substantial streaming text traffic.
// Tiny prompts, synchronous calls and media requests have different latency and
// cache characteristics and otherwise make channel comparisons misleading.
const channelMonitorV2UsageSampleFilterUL = `(` + usageLogSuccessFilterUL + `
  AND ul.stream IS TRUE
  AND COALESCE(ul.request_type, 0) NOT IN (4, 6)
  AND COALESCE(ul.billing_mode, 'token') <> 'image'
  AND COALESCE(ul.image_count, 0) = 0
  AND COALESCE(ul.image_input_tokens, 0) = 0
  AND COALESCE(ul.image_output_tokens, 0) = 0
  AND COALESCE(ul.input_tokens, 0)
      + COALESCE(ul.cache_creation_tokens, 0)
      + COALESCE(ul.cache_read_tokens, 0) > 10000
)`

// Error rows do not carry token counts, so retain substantive streaming
// failures while applying every eligibility condition the error schema can
// prove. This avoids hiding real provider failures from the success rate.
const channelMonitorV2ErrorSampleFilter = `(
  current_error.stream IS TRUE
  AND COALESCE(current_error.request_type, 0) NOT IN (4, 6)
  AND COALESCE(current_error.inbound_endpoint, current_error.request_path, '') NOT LIKE '/v1/images%'
)`

// Tiered retention balances UI windows against storage:
//
//	1m facts  → short (late writes + rebuild rollups)
//	5m/1h/12h/1d rollups → longer, aligned to 90m / 24h / 7d / 30d(+audit)
//
// Backfill may still write short-lived 1m rows for old windows so rollups can be
// built; prune at end of each recompute drops them past their TTL while rollups remain.
const (
	channelMonitorV2RetentionUser1m      = 3 * 24 * time.Hour
	channelMonitorV2RetentionMetrics1m   = 7 * 24 * time.Hour
	channelMonitorV2RetentionError1m     = 7 * 24 * time.Hour
	channelMonitorV2RetentionHistogram1m = 7 * 24 * time.Hour
	channelMonitorV2RetentionRollup5m    = 7 * 24 * time.Hour  // bucket_seconds=300
	channelMonitorV2RetentionRollup1h    = 30 * 24 * time.Hour // 3600
	channelMonitorV2RetentionRollup12h   = 45 * 24 * time.Hour // 43200
	channelMonitorV2RetentionRollup1d    = 90 * 24 * time.Hour // 86400
	channelMonitorV2RetentionMax         = channelMonitorV2RetentionRollup1d
)

// channelMonitorV2MaxRetention is the longest stored window (1d rollup). Used to
// clamp recompute/backfill so we never scan older than product history needs.
func channelMonitorV2MaxRetention() time.Duration {
	return channelMonitorV2RetentionMax
}

func channelMonitorV2RetentionCutoff(now time.Time, retention time.Duration) time.Time {
	return now.UTC().Truncate(time.Minute).Add(-retention)
}

type channelMonitorV2RetentionRule struct {
	table         string
	retention     time.Duration
	bucketSeconds int // 0 = fact table (no bucket_seconds column)
}

// channelMonitorV2RetentionRules is ordered coarse→fine for predictable prune plans.
var channelMonitorV2RetentionRules = []channelMonitorV2RetentionRule{
	{table: "channel_monitor_v2_user_metrics_1m", retention: channelMonitorV2RetentionUser1m},
	{table: "channel_monitor_v2_metrics_1m", retention: channelMonitorV2RetentionMetrics1m},
	{table: "channel_monitor_v2_error_metrics_1m", retention: channelMonitorV2RetentionError1m},
	{table: "channel_monitor_v2_latency_histograms_1m", retention: channelMonitorV2RetentionHistogram1m},
	{table: "channel_monitor_v2_metrics_rollup", retention: channelMonitorV2RetentionRollup5m, bucketSeconds: 300},
	{table: "channel_monitor_v2_user_metrics_rollup", retention: channelMonitorV2RetentionRollup5m, bucketSeconds: 300},
	{table: "channel_monitor_v2_error_metrics_rollup", retention: channelMonitorV2RetentionRollup5m, bucketSeconds: 300},
	{table: "channel_monitor_v2_latency_histograms_rollup", retention: channelMonitorV2RetentionRollup5m, bucketSeconds: 300},
	{table: "channel_monitor_v2_metrics_rollup", retention: channelMonitorV2RetentionRollup1h, bucketSeconds: 3600},
	{table: "channel_monitor_v2_user_metrics_rollup", retention: channelMonitorV2RetentionRollup1h, bucketSeconds: 3600},
	{table: "channel_monitor_v2_error_metrics_rollup", retention: channelMonitorV2RetentionRollup1h, bucketSeconds: 3600},
	{table: "channel_monitor_v2_latency_histograms_rollup", retention: channelMonitorV2RetentionRollup1h, bucketSeconds: 3600},
	{table: "channel_monitor_v2_metrics_rollup", retention: channelMonitorV2RetentionRollup12h, bucketSeconds: 43200},
	{table: "channel_monitor_v2_user_metrics_rollup", retention: channelMonitorV2RetentionRollup12h, bucketSeconds: 43200},
	{table: "channel_monitor_v2_error_metrics_rollup", retention: channelMonitorV2RetentionRollup12h, bucketSeconds: 43200},
	{table: "channel_monitor_v2_latency_histograms_rollup", retention: channelMonitorV2RetentionRollup12h, bucketSeconds: 43200},
	{table: "channel_monitor_v2_metrics_rollup", retention: channelMonitorV2RetentionRollup1d, bucketSeconds: 86400},
	{table: "channel_monitor_v2_user_metrics_rollup", retention: channelMonitorV2RetentionRollup1d, bucketSeconds: 86400},
	{table: "channel_monitor_v2_error_metrics_rollup", retention: channelMonitorV2RetentionRollup1d, bucketSeconds: 86400},
	{table: "channel_monitor_v2_latency_histograms_rollup", retention: channelMonitorV2RetentionRollup1d, bucketSeconds: 86400},
}

func (r *channelMonitorV2Repository) pruneChannelMonitorV2Retention(ctx context.Context, tx *sql.Tx, now time.Time) error {
	// During historical bootstrap, retain all 1m facts until the cursor reaches
	// the oldest rollup boundary. Otherwise adjacent chunks would rebuild the
	// same daily bucket from source rows already pruned by the prior chunk.
	var backfillCursor time.Time
	if err := tx.QueryRowContext(ctx, `SELECT backfill_cursor FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&backfillCursor); err == nil && backfillCursor.After(channelMonitorV2RetentionCutoff(now, channelMonitorV2RetentionMax)) {
		return nil
	}
	for _, rule := range channelMonitorV2RetentionRules {
		cutoff := channelMonitorV2RetentionCutoff(now, rule.retention)
		var err error
		if rule.bucketSeconds == 0 {
			_, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE bucket_start < $1`, rule.table), cutoff)
		} else {
			_, err = tx.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM %s WHERE bucket_seconds = $1 AND bucket_start < $2`, rule.table),
				rule.bucketSeconds, cutoff,
			)
		}
		if err != nil {
			return fmt.Errorf("prune %s (bucket_seconds=%d): %w", rule.table, rule.bucketSeconds, err)
		}
	}
	return nil
}

// 稳态保留期清理。
//
// pruneChannelMonitorV2Retention 只在 RecomputeRange（首次种子和历史回填）里跑；历史走完以后稳态每分钟
// 只调 RecomputeLiveRange（prune=false），保留期清理就再也没人调了：生产上 1m 事实表最老的行是 11 天前
// （保留 7 天）、error_metrics_1m 跨度 23.7 天、汇总表跨度 104 天（保留 90 天），每多留一天 1m 事实加索引
// 约 1.5GB。PruneRetention 补上这一环，用同一份 channelMonitorV2RetentionRules，但是有界的：
//
//   - 每条 DELETE 独立自动提交（不进聚合器的写事务，也就没有跨表锁顺序问题），一批最多 batchSize 行，
//     从最老的 bucket_start 开始，子查询走 bucket_start 的索引（1m 事实表的主键/时间索引，汇总表的
//     (bucket_seconds, bucket_start, ...) 主键），不全表扫；
//   - 每次调用的总时长、总批数有上限，轮询各张表各删一批，没删完的下个周期接着删：积压慢慢清，
//     不会一次写爆 WAL/IO；
//   - 每条语句有自己的超时（小于集群 30 秒的 statement_timeout），失败只丢这张表这一轮，其他表照删；
//   - 历史回填还没走完时不清（与事务内的版本同一个门槛）：回填要拿旧的 1m 行重建日汇总。
//
// 与实时窗口刷新的互斥沿用聚合器的 leader 锁：调用方在持锁的同一轮 tick 里、同一个 goroutine 中
// 顺序调用，本方法不额外加锁。

// channelMonitorV2PruneLimits 是一次 PruneRetention 调用的预算。
type channelMonitorV2PruneLimits struct {
	batchSize        int           // 每条 DELETE 最多删多少行
	maxBatches       int           // 一次调用最多执行多少条 DELETE
	maxDuration      time.Duration // 一次调用的总时长上限
	statementTimeout time.Duration // 每条语句的超时
	pause            time.Duration // 删满一整批之后让出 IO 的间隔
}

var channelMonitorV2DefaultPruneLimits = channelMonitorV2PruneLimits{
	batchSize:        20000,
	maxBatches:       100, // 最多 200 万行/次
	maxDuration:      60 * time.Second,
	statementTimeout: 20 * time.Second,
	pause:            150 * time.Millisecond,
}

// PruneRetention 实现 service.ChannelMonitorV2Repository：用默认上限做一次有界清理。
func (r *channelMonitorV2Repository) PruneRetention(ctx context.Context, now time.Time) (service.ChannelMonitorV2PruneResult, error) {
	return r.pruneRetentionBatched(ctx, now, channelMonitorV2DefaultPruneLimits)
}

func (r *channelMonitorV2Repository) pruneRetentionBatched(ctx context.Context, now time.Time, limits channelMonitorV2PruneLimits) (service.ChannelMonitorV2PruneResult, error) {
	var res service.ChannelMonitorV2PruneResult
	finished, err := r.channelMonitorV2HistoryWalkFinished(ctx, now, limits.statementTimeout)
	if err != nil || !finished {
		return res, err
	}

	runCtx, cancel := context.WithTimeout(ctx, limits.maxDuration)
	defer cancel()
	var errs []error
	active := append([]channelMonitorV2RetentionRule(nil), channelMonitorV2RetentionRules...)
	for len(active) > 0 {
		next := active[:0]
		for _, rule := range active {
			if res.Batches >= limits.maxBatches || runCtx.Err() != nil {
				res.Backlog = true
				return res, errors.Join(errs...)
			}
			deleted, execErr := r.deleteExpiredBatch(runCtx, rule, now, limits)
			if execErr != nil {
				if runCtx.Err() != nil {
					// 总时长用完，这条语句被取消：不算这张表的错，下个周期接着删。
					res.Backlog = true
					return res, errors.Join(errs...)
				}
				errs = append(errs, execErr)
				continue // 这张表这一轮不再试，其他表照删
			}
			res.Batches++
			res.Deleted += deleted
			if deleted >= int64(limits.batchSize) {
				next = append(next, rule) // 删满一批：多半还有，下一轮接着删
				sleepContext(runCtx, limits.pause)
			}
		}
		active = next
	}
	return res, errors.Join(errs...)
}

// channelMonitorV2HistoryWalkFinished 与 pruneChannelMonitorV2Retention 同一个门槛：
// backfill_cursor 走到 90 天的边缘才说明历史回填结束。读不到游标（没有行、为 NULL）按没结束处理。
func (r *channelMonitorV2Repository) channelMonitorV2HistoryWalkFinished(ctx context.Context, now time.Time, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cursor sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT backfill_cursor FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read channel monitor v2 backfill cursor: %w", err)
	}
	return cursor.Valid && !cursor.Time.After(channelMonitorV2RetentionCutoff(now, channelMonitorV2RetentionMax)), nil
}

// channelMonitorV2PruneBatchSQL 删一张表里最老的至多 LIMIT 行过期数据。ctid + ARRAY(子查询) 让外层
// DELETE 走 TID 扫描、只碰子查询选出来的行；子查询 ORDER BY bucket_start LIMIT 走 bucket_start 的索引
// （1m 事实表的主键或时间索引，汇总表以 bucket_seconds 等值开头的主键），按时间从最老的读起，
// 不全表扫。参数见 channelMonitorV2PruneBatchArgs。
func channelMonitorV2PruneBatchSQL(rule channelMonitorV2RetentionRule) string {
	if rule.bucketSeconds == 0 {
		return fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid = ANY(ARRAY(SELECT ctid FROM %[1]s WHERE bucket_start < $1 ORDER BY bucket_start LIMIT $2))`, rule.table)
	}
	return fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid = ANY(ARRAY(SELECT ctid FROM %[1]s WHERE bucket_seconds = $1 AND bucket_start < $2 ORDER BY bucket_start LIMIT $3))`, rule.table)
}

func channelMonitorV2PruneBatchArgs(rule channelMonitorV2RetentionRule, now time.Time, batchSize int) []any {
	cutoff := channelMonitorV2RetentionCutoff(now, rule.retention)
	if rule.bucketSeconds == 0 {
		return []any{cutoff, batchSize}
	}
	return []any{rule.bucketSeconds, cutoff, batchSize}
}

func (r *channelMonitorV2Repository) deleteExpiredBatch(ctx context.Context, rule channelMonitorV2RetentionRule, now time.Time, limits channelMonitorV2PruneLimits) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.statementTimeout)
	defer cancel()
	result, err := r.db.ExecContext(ctx, channelMonitorV2PruneBatchSQL(rule), channelMonitorV2PruneBatchArgs(rule, now, limits.batchSize)...)
	if err != nil {
		return 0, fmt.Errorf("prune %s (bucket_seconds=%d): %w", rule.table, rule.bucketSeconds, err)
	}
	return result.RowsAffected()
}

func sleepContext(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

const (
	channelMonitorV2ErrorLookbackBackfill = 90 * time.Minute
	channelMonitorV2ErrorLookbackLive     = 5 * time.Minute
)

type channelMonitorV2RecomputeOpts struct {
	errorLookback time.Duration
	prune         bool
}

func (r *channelMonitorV2Repository) RecomputeRange(ctx context.Context, start, end time.Time) error {
	return r.recomputeRange(ctx, start, end, channelMonitorV2RecomputeOpts{
		errorLookback: channelMonitorV2ErrorLookbackBackfill,
		prune:         true,
	})
}

func (r *channelMonitorV2Repository) RecomputeLiveRange(ctx context.Context, start, end time.Time) error {
	return r.recomputeRange(ctx, start, end, channelMonitorV2RecomputeOpts{
		errorLookback: channelMonitorV2ErrorLookbackLive,
		prune:         false,
	})
}

func (r *channelMonitorV2Repository) recomputeRange(ctx context.Context, start, end time.Time, opts channelMonitorV2RecomputeOpts) (err error) {
	start = start.UTC().Truncate(time.Minute)
	end = end.UTC().Truncate(time.Minute)
	now := time.Now().UTC().Truncate(time.Minute)
	// Clamp to longest rollup TTL so backfill does not scan beyond product history.
	maxCutoff := channelMonitorV2RetentionCutoff(now, channelMonitorV2MaxRetention())
	if start.Before(maxCutoff) {
		start = maxCutoff
	}
	if !start.Before(end) {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	// Cluster default statement_timeout is 30s; error overlap scans 90 minutes
	// of ops_error_logs and times out under load, freezing the UI watermark.
	if _, err = tx.ExecContext(ctx, `SET LOCAL statement_timeout = '180s'`); err != nil {
		return err
	}
	// The sync joins the rows of the window with their recomputed values on the whole
	// key. The newest minutes are always newer than the last ANALYZE, so the planner
	// expects 1-2 rows from them and picks a nested loop, or a merge join on bucket_start
	// alone; every row of a 5m/1h bucket shares one bucket_start, so either turns into
	// rows x rows comparisons (tens of seconds to minutes at production density).
	// Hash joins on the full key do not care about the estimates; this transaction only
	// runs such set-based statements, and SET LOCAL ends with it.
	for _, setting := range []string{"enable_nestloop", "enable_mergejoin"} {
		if _, err = tx.ExecContext(ctx, `SET LOCAL `+setting+` = off`); err != nil {
			return err
		}
	}

	// Idempotent window refresh: bring the 1m facts of [start,end) and the rollup
	// buckets covering it in line with the sources. Only rows whose values changed
	// are written and only rows that disappeared are deleted — see
	// channel_monitor_v2_aggregation_sync.go for why this replaced delete + insert.
	lookback := opts.errorLookback
	if lookback < time.Minute {
		lookback = channelMonitorV2ErrorLookbackBackfill
	}
	if _, err = tx.ExecContext(ctx, channelMonitorV2FactSyncSQL, start, end, int64(lookback/time.Second)); err != nil {
		return fmt.Errorf("sync channel monitor v2 usage and error facts: %w", err)
	}
	if _, err = tx.ExecContext(ctx, channelMonitorV2HistogramSyncSQL, start, end); err != nil {
		return fmt.Errorf("sync channel monitor v2 histograms: %w", err)
	}
	if err = r.recomputeFixedRollups(ctx, tx, start, end); err != nil {
		return err
	}
	// Drop rows past per-tier TTL (1m short, coarse rollups long). Safe after rollup
	// so a backfill chunk can build 1d rollups from temporary 1m rows then discard 1m.
	if opts.prune {
		if err = r.pruneChannelMonitorV2Retention(ctx, tx, now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, channelMonitorV2WatermarkSQL, start, end); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// The usage aggregations are plain SELECTs: channel_monitor_v2_aggregation_sync.go
// feeds them to the sync that writes only what changed.
const channelMonitorV2UsageMetricsSQL = `
SELECT date_trunc('minute', ul.created_at) AS bucket_start, %s AS platform, COALESCE(ul.group_id, 0) AS group_id, %s AS model,
       COUNT(DISTINCT COALESCE(NULLIF(ul.request_id, ''), 'usage:' || ul.id::text))
         FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS success_requests,
       COALESCE(SUM(ul.input_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS input_tokens,
       COALESCE(SUM(ul.output_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS output_tokens,
       COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS cache_creation_tokens,
       COALESCE(SUM(ul.cache_read_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS cache_read_tokens,
       COALESCE(SUM(ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL AND ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS ttft_sum_ms,
       COUNT(ul.first_token_ms) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS ttft_count,
       COALESCE(SUM(ul.duration_ms) FILTER (WHERE ul.duration_ms IS NOT NULL AND ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS duration_sum_ms,
       COUNT(ul.duration_ms) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS duration_count
FROM usage_logs ul
LEFT JOIN groups g ON g.id = ul.group_id
LEFT JOIN accounts a ON a.id = ul.account_id
WHERE ul.created_at >= $1 AND ul.created_at < $2
GROUP BY 1, 2, 3, 4`

const channelMonitorV2UserMetricsSQL = `
SELECT date_trunc('minute', ul.created_at) AS bucket_start, %s AS platform, COALESCE(ul.group_id, 0) AS group_id, %s AS model, ul.user_id AS user_id,
       COUNT(DISTINCT COALESCE(NULLIF(ul.request_id, ''), 'usage:' || ul.id::text))
         FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS success_requests,
       COALESCE(SUM(ul.input_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS input_tokens,
       COALESCE(SUM(ul.output_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS output_tokens,
       COALESCE(SUM(ul.cache_creation_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS cache_creation_tokens,
       COALESCE(SUM(ul.cache_read_tokens) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS cache_read_tokens,
       COALESCE(SUM(ul.first_token_ms) FILTER (WHERE ul.first_token_ms IS NOT NULL AND ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS ttft_sum_ms,
       COUNT(ul.first_token_ms) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS ttft_count,
       COALESCE(SUM(ul.duration_ms) FILTER (WHERE ul.duration_ms IS NOT NULL AND ` + channelMonitorV2UsageSampleFilterUL + `), 0) AS duration_sum_ms,
       COUNT(ul.duration_ms) FILTER (WHERE ` + channelMonitorV2UsageSampleFilterUL + `) AS duration_count
FROM usage_logs ul
LEFT JOIN groups g ON g.id = ul.group_id
LEFT JOIN accounts a ON a.id = ul.account_id
WHERE ul.created_at >= $1 AND ul.created_at < $2 AND ul.user_id IS NOT NULL
GROUP BY 1, 2, 3, 4, 5`

const channelMonitorV2HistogramSQL = `
SELECT date_trunc('minute', ul.created_at) AS bucket_start, %s AS platform, COALESCE(ul.group_id, 0) AS group_id, %s AS model,
       audience.user_id AS user_id, latency.metric AS metric, %s AS upper_bound_ms, COUNT(*) AS sample_count
FROM usage_logs ul
LEFT JOIN groups g ON g.id = ul.group_id
LEFT JOIN accounts a ON a.id = ul.account_id
CROSS JOIN LATERAL (VALUES (0::bigint), (ul.user_id)) audience(user_id)
CROSS JOIN LATERAL (VALUES ('ttft'::text, ul.first_token_ms), ('duration'::text, ul.duration_ms)) latency(metric, value_ms)
WHERE ul.created_at >= $1 AND ul.created_at < $2
  AND audience.user_id IS NOT NULL AND latency.value_ms IS NOT NULL AND latency.value_ms >= 0
  AND ` + channelMonitorV2UsageSampleFilterUL + `
GROUP BY 1, 2, 3, 4, 5, 6, 7`

func channelMonitorV2HistogramBoundSQL(column string) string {
	return `CASE
WHEN ` + column + ` <= 50 THEN 50 WHEN ` + column + ` <= 100 THEN 100
WHEN ` + column + ` <= 250 THEN 250 WHEN ` + column + ` <= 500 THEN 500
WHEN ` + column + ` <= 1000 THEN 1000 WHEN ` + column + ` <= 2000 THEN 2000
WHEN ` + column + ` <= 3000 THEN 3000 WHEN ` + column + ` <= 5000 THEN 5000
WHEN ` + column + ` <= 8000 THEN 8000 WHEN ` + column + ` <= 10000 THEN 10000
WHEN ` + column + ` <= 15000 THEN 15000 WHEN ` + column + ` <= 30000 THEN 30000
WHEN ` + column + ` <= 60000 THEN 60000 WHEN ` + column + ` <= 120000 THEN 120000
WHEN ` + column + ` <= 300000 THEN 300000 WHEN ` + column + ` <= 600000 THEN 600000
ELSE 2147483647 END`
}

// Error dedup lookback: request_id branch is bounded by chunk start minus 90
// minutes so candidate_ids never forces a full-history scan of ops_error_logs.
//
// This is the dedup + classification half of the error aggregation (two CTEs,
// spliced into channelMonitorV2FactSyncSQL); the per-table aggregations of
// `classified` live there.
const channelMonitorV2ErrorClassifiedCTEs = `
dedup AS (
  WITH candidate_ids AS MATERIALIZED (
    SELECT DISTINCT request_id
    FROM ops_error_logs
    WHERE created_at >= $1 AND created_at < $2 AND NULLIF(request_id, '') IS NOT NULL
  )
  SELECT DISTINCT ON (COALESCE(NULLIF(current_error.request_id, ''), 'error:' || current_error.id::text))
    date_trunc('minute', current_error.created_at) AS bucket_start,
    -- Composite groups are a routing layer: resolve the concrete account
    -- platform (mirrors usageLogEffectivePlatformExpr on the usage side) so
    -- error facts share the usage facts' platform key. Without this, composite
    -- group errors aggregate under platform 'composite', which is never an
    -- enabled config platform, and are filtered out of every monitor v2 query.
    lower(CASE
      WHEN g.platform = 'composite' THEN COALESCE(NULLIF(TRIM(a.platform), ''), NULLIF(NULLIF(lower(TRIM(current_error.platform)), ''), 'composite'), 'unknown')
      ELSE COALESCE(NULLIF(TRIM(current_error.platform), ''), 'unknown')
    END) AS platform,
    COALESCE(current_error.group_id, 0) AS group_id,
    COALESCE(NULLIF(TRIM(current_error.requested_model), ''), NULLIF(TRIM(current_error.model), ''), 'unknown') AS model,
    current_error.user_id, current_error.error_type, current_error.error_owner, COALESCE(current_error.status_code, 0) AS status_code,
    COALESCE(current_error.upstream_status_code, 0) AS upstream_status_code,
    lower(CONCAT_WS(' ', current_error.error_type, current_error.error_source, current_error.error_message, current_error.upstream_error_message, current_error.upstream_error_detail, current_error.error_body)) AS text,
    (CASE WHEN jsonb_typeof(current_error.upstream_errors) = 'array' THEN jsonb_array_length(current_error.upstream_errors) > 0 ELSE FALSE END
      OR current_error.error_owner = 'provider' OR current_error.upstream_status_code IS NOT NULL) AS upstream_affected,
    CASE WHEN jsonb_typeof(current_error.upstream_errors) = 'array' THEN jsonb_array_length(current_error.upstream_errors) ELSE 0 END AS upstream_attempts
  FROM ops_error_logs current_error
  LEFT JOIN groups g ON g.id = current_error.group_id
  LEFT JOIN accounts a ON a.id = current_error.account_id
  WHERE (
      (NULLIF(current_error.request_id, '') IS NULL AND current_error.created_at >= $1 AND current_error.created_at < $2)
      OR (
        current_error.request_id IN (SELECT request_id FROM candidate_ids)
        AND current_error.created_at >= $1 - ($3 * INTERVAL '1 second')
        AND current_error.created_at < $2
      )
    )
    AND NOT current_error.is_count_tokens
    AND ` + channelMonitorV2ErrorSampleFilter + `
    AND (COALESCE(current_error.status_code, 0) >= 400 OR current_error.error_type = 'cyber_policy')
  ORDER BY COALESCE(NULLIF(current_error.request_id, ''), 'error:' || current_error.id::text), current_error.created_at DESC, current_error.id DESC
), classified AS MATERIALIZED (
  SELECT *, CASE
    -- Keep in lockstep with service.ClassifyChannelMonitorV2Error needles.
    WHEN error_type = 'cyber_policy' OR text LIKE ANY(ARRAY['%content policy%','%content_policy%','%safety policy%','%moderation%','%blocked keyword%']) THEN 'content_policy'
    WHEN status_code = 401 OR upstream_status_code = 401 OR text LIKE ANY(ARRAY['%unauthorized%','%invalid api key%','%invalid_api_key%','%authentication%','%api_key_disabled%']) THEN 'authentication'
    WHEN text LIKE ANY(ARRAY['%context window%','%context length%','%maximum prompt length%','%too many tokens%','%max_tokens%']) THEN 'context_limit'
    WHEN text LIKE ANY(ARRAY['%failed to deserialize%','%missing required parameter%','%invalid request%','%invalid_request%','%tool_choice%']) THEN 'invalid_request'
    WHEN text LIKE ANY(ARRAY['%does not support the requested model%','%not supported by any configured account%','%model not supported%','%unsupported model%']) THEN 'model_unsupported'
    WHEN text LIKE ANY(ARRAY['%group not allowed%','%group_not_allowed%','%group access%']) THEN 'group_access'
    WHEN text LIKE ANY(ARRAY['%run out of credits%','%insufficient balance%','%insufficient quota%','%subscription%','%quota exceeded%','%billing hard limit%']) THEN 'quota_or_balance'
    WHEN text LIKE ANY(ARRAY['%no available accounts%','%no healthy account%','%no healthy upstream account%','%failover budget exhausted%','%account pool%']) THEN 'account_pool_unavailable'
    WHEN status_code = 429 OR upstream_status_code = 429 OR text LIKE ANY(ARRAY['%rate limit%','%rate_limit%','%high demand%','%overloaded%','%concurrency limit%','%capacity%']) THEN 'rate_or_capacity'
    WHEN status_code IN (408,504) OR text LIKE ANY(ARRAY['%timeout%','%deadline exceeded%','%error code: 524%','%gateway time-out%','%gateway timeout%']) THEN 'timeout'
    WHEN text LIKE ANY(ARRAY['%transport%','%stream_read_error%','%connection reset%','%connection refused%','%tls%','%http2%','%missing terminal event%','%unexpected eof%']) THEN 'transport_or_stream'
    WHEN status_code = 403 OR upstream_status_code = 403 THEN 'upstream_forbidden'
    WHEN status_code = 404 OR upstream_status_code = 404 THEN 'not_found'
    WHEN status_code = 499 OR text LIKE ANY(ARRAY['%client cancelled%','%client canceled%','%context canceled%']) THEN 'client_cancelled'
    WHEN upstream_status_code >= 500 OR (error_owner = 'provider' AND status_code >= 500) THEN 'upstream_5xx'
    WHEN status_code >= 500 OR error_type = 'internal' OR error_owner = 'system' THEN 'internal'
    ELSE 'other' END AS category
  FROM dedup
  WHERE bucket_start >= $1 AND bucket_start < $2
)`

// Floor matches channelMonitorV2RetentionMax (90d). Keep the INTERVAL literal in
// sync when changing channelMonitorV2RetentionRollup1d.
//
// Coverage starts track how far back recompute has walked ($1 = chunk start), not
// "min(source_log.created_at)". Using global min(ops_error_logs) pins
// error_coverage_start to the first real error forever and collapses UI windows
// when errors only exist in a recent slice (common on first upgrade).
const channelMonitorV2WatermarkSQL = `
INSERT INTO channel_monitor_v2_watermarks (id, usage_coverage_start, error_coverage_start, data_through, last_successful_at, backfill_cursor, updated_at)
VALUES (
  1,
  $1,
  $1,
  $2, NOW(), $1, NOW()
)
ON CONFLICT (id) DO UPDATE SET
  usage_coverage_start = GREATEST(
    date_trunc('minute', NOW()) - INTERVAL '90 days',
    LEAST(COALESCE(channel_monitor_v2_watermarks.usage_coverage_start, EXCLUDED.usage_coverage_start), EXCLUDED.usage_coverage_start)
  ),
  error_coverage_start = GREATEST(
    date_trunc('minute', NOW()) - INTERVAL '90 days',
    LEAST(COALESCE(channel_monitor_v2_watermarks.error_coverage_start, EXCLUDED.error_coverage_start), EXCLUDED.error_coverage_start)
  ),
  data_through = GREATEST(COALESCE(channel_monitor_v2_watermarks.data_through, EXCLUDED.data_through), EXCLUDED.data_through),
  last_successful_at = NOW(),
  backfill_cursor = LEAST(COALESCE(channel_monitor_v2_watermarks.backfill_cursor, EXCLUDED.backfill_cursor), EXCLUDED.backfill_cursor),
  updated_at = NOW()`

var channelMonitorV2FixedRollupSeconds = []int{300, 3600, 43200, 86400}

func (r *channelMonitorV2Repository) recomputeFixedRollups(ctx context.Context, tx *sql.Tx, start, end time.Time) error {
	for _, seconds := range channelMonitorV2FixedRollupSeconds {
		// Coarse buckets are immutable between boundaries during the normal
		// trailing refresh. Historical backfills and boundary-crossing windows
		// still rebuild them; this avoids repeatedly regrouping the full current
		// day/user table every few minutes.
		if seconds >= 43200 && sameFixedRollupBucket(start, end, seconds) {
			if err := dropCoarseRollupsStartingIn(ctx, tx, seconds, start, end); err != nil {
				return err
			}
			continue
		}
		interval := fmt.Sprintf("%d seconds", seconds)
		for _, rollup := range channelMonitorV2RollupSyncs {
			if _, err := tx.ExecContext(ctx, rollup.sql, interval, seconds, start, end); err != nil {
				return fmt.Errorf("roll up %s %ds: %w", rollup.table, seconds, err)
			}
		}
	}
	return nil
}

// dropCoarseRollupsStartingIn deletes the rollup rows of a skipped coarse tier
// whose bucket starts inside the window. The delete + insert version wiped every
// rollup row with bucket_start in [start,end) before rebuilding, and a skipped
// tier was wiped without being rebuilt: the in-progress 12h/1d bucket disappears
// from the second minute it exists until the window crosses the next boundary,
// when it is rebuilt complete. That is what the 7d/30d views have always shown,
// so it is kept exactly (changing it is a product decision, not a tuning one).
func dropCoarseRollupsStartingIn(ctx context.Context, tx *sql.Tx, seconds int, start, end time.Time) error {
	size := time.Duration(seconds) * time.Second
	first := start.Truncate(size)
	if first.Before(start) {
		first = first.Add(size)
	}
	if !first.Before(end) {
		return nil
	}
	for _, rollup := range channelMonitorV2RollupSyncs {
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE bucket_seconds = $1 AND bucket_start >= $2 AND bucket_start < $3`, rollup.table),
			seconds, start, end,
		); err != nil {
			return fmt.Errorf("drop in-progress %s %ds: %w", rollup.table, seconds, err)
		}
	}
	return nil
}

func sameFixedRollupBucket(start, end time.Time, seconds int) bool {
	if !end.After(start) {
		return true
	}
	interval := time.Duration(seconds) * time.Second
	return start.Truncate(interval).Equal(end.Add(-time.Nanosecond).Truncate(interval))
}

// PostgreSQL interprets a TIMESTAMPTZ literal without an explicit offset in
// the current session timezone. Keep date_bin's origin fixed in UTC so bucket
// boundaries do not shift when the database session runs in Asia/Shanghai (or
// any other non-UTC timezone).
const channelMonitorV2DateBinOrigin = "TIMESTAMPTZ '1970-01-01 00:00:00+00'"

func channelMonitorV2DateBinExpr(column string) string {
	return "date_bin($1::interval," + column + "," + channelMonitorV2DateBinOrigin + ")"
}

// The buckets covering the window: from the bucket holding its first minute to
// the end of the bucket holding its last.
const channelMonitorV2FixedRollupBoundsCTE = `bounds AS (
  SELECT
    date_bin($1::interval, $3::timestamptz, ` + channelMonitorV2DateBinOrigin + `) AS start_at,
    date_bin($1::interval, $4::timestamptz - INTERVAL '1 microsecond', ` + channelMonitorV2DateBinOrigin + `) + $1::interval AS end_at
)`

// The rollup aggregations are plain SELECTs over the 1m tables, one row per
// rollup primary key; sums are cast back to bigint (SUM of bigint is numeric) so
// they compare and store as the column type.
const channelMonitorV2MetricsRollupSQL = `
SELECT date_bin($1::interval, m.bucket_start, ` + channelMonitorV2DateBinOrigin + `) AS bucket_start, $2::integer AS bucket_seconds,
       platform, group_id, model,
       SUM(success_requests)::bigint AS success_requests, SUM(error_requests)::bigint AS error_requests,
       SUM(upstream_affected_requests)::bigint AS upstream_affected_requests, SUM(upstream_attempt_count)::bigint AS upstream_attempt_count,
       SUM(input_tokens)::bigint AS input_tokens, SUM(output_tokens)::bigint AS output_tokens,
       SUM(cache_creation_tokens)::bigint AS cache_creation_tokens, SUM(cache_read_tokens)::bigint AS cache_read_tokens,
       SUM(ttft_sum_ms)::bigint AS ttft_sum_ms, SUM(ttft_count)::bigint AS ttft_count,
       SUM(duration_sum_ms)::bigint AS duration_sum_ms, SUM(duration_count)::bigint AS duration_count
FROM channel_monitor_v2_metrics_1m m, bounds
WHERE m.bucket_start >= bounds.start_at AND m.bucket_start < bounds.end_at
GROUP BY 1, 2, 3, 4, 5`

const channelMonitorV2UserMetricsRollupSQL = `
SELECT date_bin($1::interval, m.bucket_start, ` + channelMonitorV2DateBinOrigin + `) AS bucket_start, $2::integer AS bucket_seconds,
       platform, group_id, model, user_id,
       SUM(success_requests)::bigint AS success_requests, SUM(error_requests)::bigint AS error_requests,
       SUM(input_tokens)::bigint AS input_tokens, SUM(output_tokens)::bigint AS output_tokens,
       SUM(cache_creation_tokens)::bigint AS cache_creation_tokens, SUM(cache_read_tokens)::bigint AS cache_read_tokens,
       SUM(ttft_sum_ms)::bigint AS ttft_sum_ms, SUM(ttft_count)::bigint AS ttft_count,
       SUM(duration_sum_ms)::bigint AS duration_sum_ms, SUM(duration_count)::bigint AS duration_count
FROM channel_monitor_v2_user_metrics_1m m, bounds
WHERE m.bucket_start >= bounds.start_at AND m.bucket_start < bounds.end_at
GROUP BY 1, 2, 3, 4, 5, 6`

const channelMonitorV2HistogramRollupSQL = `
SELECT date_bin($1::interval, h.bucket_start, ` + channelMonitorV2DateBinOrigin + `) AS bucket_start, $2::integer AS bucket_seconds,
       platform, group_id, model, user_id, metric, upper_bound_ms, SUM(sample_count)::bigint AS sample_count
FROM channel_monitor_v2_latency_histograms_1m h, bounds
WHERE h.bucket_start >= bounds.start_at AND h.bucket_start < bounds.end_at
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8`

const channelMonitorV2ErrorRollupSQL = `
SELECT date_bin($1::interval, e.bucket_start, ` + channelMonitorV2DateBinOrigin + `) AS bucket_start, $2::integer AS bucket_seconds,
       platform, group_id, model, error_category, taxonomy_version, SUM(error_requests)::bigint AS error_requests
FROM channel_monitor_v2_error_metrics_1m e, bounds
WHERE e.bucket_start >= bounds.start_at AND e.bucket_start < bounds.end_at
GROUP BY 1, 2, 3, 4, 5, 6, 7`
