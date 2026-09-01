-- Repair the covering index if migration 236 was interrupted after PostgreSQL
-- created an invalid index but before the migration record was written. The
-- runner drops only an invalid index in prepareNonTransactionalMigration;
-- healthy installations therefore pay only the IF NOT EXISTS check.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_rollup_created_group_cost
    ON usage_logs (created_at, group_id)
    INCLUDE (actual_cost)
    WHERE group_id IS NOT NULL;
