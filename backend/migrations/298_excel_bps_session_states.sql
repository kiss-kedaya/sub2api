CREATE TABLE IF NOT EXISTS excel_bps_session_states(
 scope VARCHAR(64) NOT NULL,
 state_key VARCHAR(64) NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 payload_cipher TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(scope,state_key)
);
CREATE INDEX IF NOT EXISTS idx_excel_bps_session_expiry ON excel_bps_session_states(expires_at);
