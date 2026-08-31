-- Dimension-level dashboard rollups. This migration is additive: usage_logs and
-- the existing dashboard aggregate tables are intentionally left untouched.

ALTER TABLE usage_dashboard_hourly_users
    ADD COLUMN IF NOT EXISTS total_requests BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS actual_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_duration_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS duration_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS duration_count BIGINT NOT NULL DEFAULT 0;

-- A single compact table stores all supported dimensions. Nullable dimensions
-- use zero/empty sentinels so the composite primary key remains deterministic.
CREATE TABLE IF NOT EXISTS usage_dashboard_hourly_dimensions (
    bucket_start TIMESTAMPTZ NOT NULL,
    dimension_type TEXT NOT NULL,
    dimension_key TEXT NOT NULL,
    user_id BIGINT NOT NULL DEFAULT 0,
    group_id BIGINT NOT NULL DEFAULT 0,
    endpoint_type TEXT NOT NULL DEFAULT '',
    total_requests BIGINT NOT NULL DEFAULT 0,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    cache_creation_tokens BIGINT NOT NULL DEFAULT 0,
    cache_read_tokens BIGINT NOT NULL DEFAULT 0,
    total_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    actual_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    account_cost DECIMAL(20, 10) NOT NULL DEFAULT 0,
    total_duration_ms BIGINT NOT NULL DEFAULT 0,
    duration_count BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (bucket_start, dimension_type, dimension_key, user_id, group_id, endpoint_type),
    CONSTRAINT usage_dashboard_hourly_dimensions_type_check
        CHECK (dimension_type IN (
            'user', 'model', 'model_upstream', 'model_mapping',
            'group', 'endpoint', 'user_model', 'user_model_upstream',
            'user_model_mapping', 'user_group', 'user_endpoint'
        ))
);

CREATE INDEX IF NOT EXISTS idx_usage_dashboard_hourly_dimensions_lookup
    ON usage_dashboard_hourly_dimensions (dimension_type, bucket_start);

CREATE INDEX IF NOT EXISTS idx_usage_dashboard_hourly_dimensions_user_lookup
    ON usage_dashboard_hourly_dimensions (dimension_type, user_id, bucket_start)
    WHERE user_id <> 0;

-- One row per processed hour lets readers distinguish a complete backfill from
-- a partial dimension table. Rows are written in the same transaction as the
-- dimension upsert and are never derived from usage_logs at read time.
CREATE TABLE IF NOT EXISTS usage_dashboard_hourly_dimension_coverage (
    bucket_start TIMESTAMPTZ PRIMARY KEY,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_dashboard_hourly_dimension_coverage_bucket
    ON usage_dashboard_hourly_dimension_coverage (bucket_start);

COMMENT ON TABLE usage_dashboard_hourly_dimensions IS
    'Compact hourly usage rollups for dashboard dimensions; raw usage_logs remains the source of truth.';
