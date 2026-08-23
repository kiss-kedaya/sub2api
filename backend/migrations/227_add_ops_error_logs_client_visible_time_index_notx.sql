-- Speed up the admin/user error list count query for the rows that are
-- actually exposed by the default client-visible filter.
-- Non-transactional migration: CREATE INDEX CONCURRENTLY cannot run in a
-- transaction and must remain idempotent for interrupted deployments.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_error_logs_client_visible_time
  ON ops_error_logs (created_at DESC)
  WHERE (COALESCE(status_code, 0) >= 400 OR error_type = 'cyber_policy');
