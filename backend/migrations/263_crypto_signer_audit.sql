-- Signer audit trail: every signature request lands here instead of a
-- plaintext log file, so the record lives with the rest of the data and
-- inherits the same access control and backup story.
CREATE TABLE IF NOT EXISTS crypto_signer_audit (
    id          BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    op          TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',
    derivation_index INTEGER NOT NULL DEFAULT 0,
    chain_id    BIGINT NOT NULL DEFAULT 0,
    to_address  TEXT NOT NULL DEFAULT '',
    token       TEXT NOT NULL DEFAULT '',
    amount      TEXT NOT NULL DEFAULT '',
    result      TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_signer_audit_time ON crypto_signer_audit (occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_signer_audit_result ON crypto_signer_audit (result, occurred_at DESC);
