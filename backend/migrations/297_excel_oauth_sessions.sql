CREATE TABLE IF NOT EXISTS excel_oauth_sessions(
 id UUID PRIMARY KEY,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 state_hash VARCHAR(64) NOT NULL,
 verifier_cipher TEXT NOT NULL,
 config_revision VARCHAR(64) NOT NULL,
 primary_hash VARCHAR(64) NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_excel_oauth_session_expiry ON excel_oauth_sessions(expires_at);
