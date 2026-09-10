CREATE TABLE IF NOT EXISTS user_cf_ip_allowlist (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip TEXT NOT NULL,
    cf_rule_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, ip)
);
CREATE INDEX IF NOT EXISTS idx_user_cf_ip_allowlist_user_id ON user_cf_ip_allowlist (user_id);
