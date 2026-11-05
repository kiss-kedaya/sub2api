-- 308_upstream_monitor_ops.sql
-- Second batch for the operations-center upstream monitor: spend tracking,
-- daily rollups, and low-balance / rate-jump alerts. Everything here is fed by
-- the snapshots accounts.extra already stores; no credentials, no upstream
-- calls, no gateway or billing changes.

-- Per-sample spend derived from the previous balance. Existing rows default to
-- 0 because their delta was never computed.
ALTER TABLE upstream_balance_samples
    ADD COLUMN IF NOT EXISTS spend NUMERIC(20, 8) NOT NULL DEFAULT 0;

-- One bounded row per account per Beijing day. The overview and the trend chart
-- read this table instead of scanning raw samples.
CREATE TABLE IF NOT EXISTS upstream_monitor_daily (
    day           DATE NOT NULL,
    account_id    BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    unit          VARCHAR(16) NOT NULL DEFAULT '',
    spend         NUMERIC(20, 8) NOT NULL DEFAULT 0,
    balance_close NUMERIC(20, 8),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (day, account_id)
);

CREATE INDEX IF NOT EXISTS idx_upstream_monitor_daily_day
    ON upstream_monitor_daily (day DESC);

-- Alert thresholds are per account and opt-in; no row means no alerting.
CREATE TABLE IF NOT EXISTS upstream_monitor_alert_rules (
    account_id   BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    threshold    NUMERIC(20, 8) NOT NULL,
    unit         VARCHAR(16) NOT NULL DEFAULT '',
    rate_min_pct NUMERIC(10, 2) NOT NULL DEFAULT 10,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS upstream_monitor_alerts (
    id         BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind       VARCHAR(32) NOT NULL,
    level      VARCHAR(16) NOT NULL DEFAULT 'warning',
    balance    NUMERIC(20, 8),
    threshold  NUMERIC(20, 8),
    unit       VARCHAR(16) NOT NULL DEFAULT '',
    old_rate   NUMERIC(20, 6),
    new_rate   NUMERIC(20, 6),
    message    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_time
    ON upstream_monitor_alerts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_account_kind
    ON upstream_monitor_alerts (account_id, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_monitor_alerts_open
    ON upstream_monitor_alerts (created_at DESC) WHERE acked_at IS NULL;