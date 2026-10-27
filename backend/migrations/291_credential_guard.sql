CREATE TABLE IF NOT EXISTS credential_guard_accounts (
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 config JSONB NOT NULL,
 revision VARCHAR(64) NOT NULL,
 secret_cipher TEXT NOT NULL DEFAULT '',
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 state VARCHAR(32) NOT NULL DEFAULT 'pending',
 code VARCHAR(64) NOT NULL DEFAULT '',
 http_status INTEGER NOT NULL DEFAULT 0,
 failures INTEGER NOT NULL DEFAULT 0,
 blocked BOOLEAN NOT NULL DEFAULT FALSE,
 last_checked_at TIMESTAMPTZ,
 next_check_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 next_relogin_at TIMESTAMPTZ,
 lease VARCHAR(64) NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_credential_guard_due ON credential_guard_accounts(next_check_at) WHERE enabled=TRUE AND blocked=FALSE;
