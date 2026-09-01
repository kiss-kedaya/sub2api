-- Cover the historical group rollup scan with the timestamp range and the
-- grouping key. actual_cost is included so PostgreSQL can use an index-only
-- scan when visibility maps permit it, without widening the index key.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_rollup_created_group_cost
    ON usage_logs (created_at, group_id)
    INCLUDE (actual_cost)
    WHERE group_id IS NOT NULL;
