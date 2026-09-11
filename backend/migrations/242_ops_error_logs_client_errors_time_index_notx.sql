-- Speed up /admin/ops/errors default view (client-visible, not business-limited).
-- Non-transactional: CREATE INDEX CONCURRENTLY cannot run in a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_error_logs_client_errors_time
  ON ops_error_logs (created_at DESC)
  WHERE (COALESCE(status_code, 0) >= 400 OR error_type = 'cyber_policy')
    AND COALESCE(is_business_limited, false) = false;
