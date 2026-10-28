-- Recurring account schedulable windows. Additive and independent from the
-- existing one-shot timer tables so old plans remain rollback-safe.
CREATE TABLE IF NOT EXISTS account_schedulable_recurring (
    account_id BIGINT PRIMARY KEY,
    version BIGINT NOT NULL CHECK (version > 0),
    timezone TEXT NOT NULL,
    weekdays_only BOOLEAN NOT NULL DEFAULT FALSE,
    periods JSONB NOT NULL,
    requested_by BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_applied_state BOOLEAN,
    last_applied_at TIMESTAMPTZ,
    last_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS account_schedulable_recurring_enabled_idx
    ON account_schedulable_recurring (account_id) WHERE enabled = TRUE;
