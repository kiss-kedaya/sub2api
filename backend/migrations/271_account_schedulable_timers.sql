-- One-shot admin scheduling. Independent of quality/cooldown jobs. Forward only.
-- No credentials or account/user financial state are stored or modified here.
CREATE TABLE IF NOT EXISTS account_schedulable_timers (
    account_id BIGINT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('open', 'close')),
    version BIGINT NOT NULL CHECK (version > 0),
    execute_at TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL,
    requested_by BIGINT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','executed','skipped','cancelled','failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    actual_at TIMESTAMPTZ,
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, action)
);
CREATE INDEX IF NOT EXISTS account_schedulable_timers_due_idx
    ON account_schedulable_timers (next_attempt_at, account_id) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS account_schedulable_timers_conflict_idx
    ON account_schedulable_timers (account_id, execute_at) WHERE status = 'pending';
-- Intentionally no cascading account/user FK: execution evidence survives deletion.
CREATE TABLE IF NOT EXISTS account_schedulable_timer_audit (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL,
    action TEXT NOT NULL,
    version BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    execute_at TIMESTAMPTZ NOT NULL,
    timezone TEXT NOT NULL,
    actual_at TIMESTAMPTZ NOT NULL,
    result TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS account_schedulable_timer_audit_account_idx
    ON account_schedulable_timer_audit (account_id, id DESC);
