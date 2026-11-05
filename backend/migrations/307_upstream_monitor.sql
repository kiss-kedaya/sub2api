-- 307_upstream_monitor.sql
-- Upstream monitor: compact, time-windowed history for the upstream balance and
-- billing-rate snapshots that the account-level probe already stores in
-- accounts.extra. No credentials, no new upstream calls, no gateway changes.

CREATE TABLE IF NOT EXISTS upstream_balance_samples (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    balance     NUMERIC(20, 8) NOT NULL,
    unit        VARCHAR(16) NOT NULL DEFAULT '',
    sampled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_balance_samples_account_time
    ON upstream_balance_samples (account_id, sampled_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_balance_samples_time
    ON upstream_balance_samples (sampled_at);

CREATE TABLE IF NOT EXISTS upstream_rate_changes (
    id          BIGSERIAL PRIMARY KEY,
    account_id  BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    old_rate    NUMERIC(20, 6) NOT NULL,
    new_rate    NUMERIC(20, 6) NOT NULL,
    source      VARCHAR(32) NOT NULL DEFAULT 'probe',
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upstream_rate_changes_account_time
    ON upstream_rate_changes (account_id, changed_at DESC);
CREATE INDEX IF NOT EXISTS idx_upstream_rate_changes_time
    ON upstream_rate_changes (changed_at DESC);

CREATE TABLE IF NOT EXISTS upstream_monitor_state (
    account_id     BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    last_balance   NUMERIC(20, 8),
    last_unit      VARCHAR(16) NOT NULL DEFAULT '',
    last_rate      NUMERIC(20, 6),
    last_sample_at TIMESTAMPTZ,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
