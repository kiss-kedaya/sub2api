-- Cover GetUserDashboardStats platform breakdowns, including the group/account
-- join keys, plus the cost and duration totals of the same user/time window,
-- so it can use an index-only scan when visible.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_user_created_dims
    ON usage_logs (user_id, created_at)
    INCLUDE (group_id, account_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms);

-- Cover fillSubjectDashboardUsageFromWindow (api_key_id),
-- GetAPIKeyStatsAggregated and GetBatchAPIKeyUsageStats, including duration_ms.
-- user_id is included because the per-key daily trend also filters on it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_api_key_created_stats
    ON usage_logs (api_key_id, created_at)
    INCLUDE (user_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, total_cost, actual_cost, duration_ms);
