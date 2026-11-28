-- 非事务执行（*_notx.sql）：仅含纯 CREATE INDEX CONCURRENTLY IF NOT EXISTS，不阻塞热表写入。
-- 说明：migrations_runner.go 校验 *_notx.sql 仅允许并发索引语句并由执行器统一管理会话超时，故此处不混入 SET 语句。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_cmv2_hist_rollup_global_bucket
    ON channel_monitor_v2_latency_histograms_rollup (bucket_seconds, bucket_start, platform, group_id, model, metric, upper_bound_ms)
    INCLUDE (sample_count)
    WHERE user_id = 0;
