CREATE TABLE ops_automation_outcomes (
 id TEXT PRIMARY KEY, account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 revision TEXT NOT NULL, succeeded BOOLEAN NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ops_automation_outcomes_created ON ops_automation_outcomes(created_at);
CREATE TABLE ops_automation_events (
 id BIGSERIAL PRIMARY KEY, account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind TEXT NOT NULL, old_concurrency INTEGER NOT NULL, new_concurrency INTEGER NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ops_automation_events_account ON ops_automation_events(account_id,created_at DESC);
