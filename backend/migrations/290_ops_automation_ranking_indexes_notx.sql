-- Bounded group/model windows used by optional background ranking only.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_group_account_recent_rank
ON usage_logs(group_id,account_id,created_at DESC);
