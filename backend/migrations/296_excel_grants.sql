CREATE TABLE IF NOT EXISTS excel_grants(
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 config JSONB NOT NULL DEFAULT '{"enabled":false,"models":[]}',
 revision VARCHAR(64) NOT NULL,
 credentials_cipher TEXT NOT NULL DEFAULT '',
 expires_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
