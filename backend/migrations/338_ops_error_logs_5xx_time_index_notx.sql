-- 5xx 告警分源指标（upstream_5xx / gateway_5xx / upstream_5xx_spike_ratio）的
-- 只读聚合走 ops_error_logs 的 5xx 子集，需要按 created_at 做范围扫描。
--
-- 非事务迁移：CREATE INDEX CONCURRENTLY 不能跑在事务里，必须独立文件 _notx，
-- 且保留 IF NOT EXISTS 以便中断后重跑。谓词与查询里的 WHERE 完全一致，
-- 规划器才能命中该部分索引；索引列带 error_source 让分源计数走 index-only scan。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_error_logs_5xx_time_source
  ON ops_error_logs (created_at, error_source)
  WHERE COALESCE(status_code, 0) >= 500
    AND COALESCE(is_count_tokens, false) = false;
