CREATE TABLE IF NOT EXISTS credential_guard_events (
 id BIGSERIAL PRIMARY KEY,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 state VARCHAR(32) NOT NULL,
 code VARCHAR(64) NOT NULL,
 http_status INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_credential_guard_events_account_time ON credential_guard_events(account_id,created_at DESC);
