-- 232_billing_balance_settlements.sql
-- Persist balance charges as compact durable state records. A cluster-wide worker
-- coalesces pending rows by user before touching users.balance, avoiding one
-- contended row update per completed request.

CREATE TABLE IF NOT EXISTS billing_balance_settlements (
    id BIGSERIAL PRIMARY KEY,
    request_id TEXT NOT NULL,
    api_key_id BIGINT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    hold_usd NUMERIC(20, 8) NOT NULL DEFAULT 0,
    authorization_fingerprint TEXT NOT NULL DEFAULT '',
    status SMALLINT NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    applied_at TIMESTAMPTZ,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT billing_balance_settlements_identity UNIQUE (request_id, api_key_id),
    CONSTRAINT billing_balance_settlements_amount_nonnegative CHECK (
        amount_usd >= 0 AND amount_usd <> 'NaN'::numeric
    ),
    CONSTRAINT billing_balance_settlements_hold_nonnegative CHECK (
        hold_usd >= 0 AND hold_usd <> 'NaN'::numeric
    ),
    CONSTRAINT billing_balance_settlements_status_valid CHECK (status BETWEEN 0 AND 6),
    CONSTRAINT billing_balance_settlements_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT billing_balance_settlements_identity_valid CHECK (
        BTRIM(request_id) <> '' AND api_key_id > 0 AND user_id > 0
    ),
    CONSTRAINT billing_balance_settlements_authorization_state_valid CHECK (
        status NOT IN (0, 1, 2)
        OR (
            BTRIM(authorization_fingerprint) <> ''
            AND expires_at IS NOT NULL
        )
    ),
    CONSTRAINT billing_balance_settlements_charge_state_valid CHECK (
        status NOT IN (3, 4)
        OR (BTRIM(request_fingerprint) <> '' AND amount_usd > 0)
    ),
    CONSTRAINT billing_balance_settlements_final_state_valid CHECK (
        status NOT IN (4, 5) OR applied_at IS NOT NULL
    )
);

CREATE INDEX IF NOT EXISTS idx_billing_balance_settlements_pending
    ON billing_balance_settlements (available_at, id)
    WHERE status = 3;

CREATE INDEX IF NOT EXISTS idx_billing_balance_settlements_authorization_recovery
    ON billing_balance_settlements (expires_at, id)
    WHERE status IN (0, 1);

CREATE INDEX IF NOT EXISTS idx_billing_balance_settlements_finalization_recovery
    ON billing_balance_settlements (updated_at, id)
    WHERE status = 2;

CREATE INDEX IF NOT EXISTS idx_billing_balance_settlements_applied_retention
    ON billing_balance_settlements (applied_at, id)
    WHERE status IN (4, 5);

ALTER TABLE billing_balance_settlements
    SET (fillfactor = 90,
         autovacuum_vacuum_scale_factor = 0.02,
         autovacuum_vacuum_threshold = 5000,
         autovacuum_analyze_scale_factor = 0.01,
         autovacuum_analyze_threshold = 5000);

COMMENT ON TABLE billing_balance_settlements IS
    'Compact durable queue for coalesced users.balance settlement; identity remains in usage_billing_dedup after applied rows are retired.';
COMMENT ON COLUMN billing_balance_settlements.status IS
    '0=prepared, 1=authorized, 2=finalization_pending (amount decides settle/refund), 3=settlement_pending, 4=applied, 5=refunded, 6=terminal/manual reconciliation required';
