-- 密钥级智能路由：一把密钥按用户排序尝试多个分组。
CREATE TABLE IF NOT EXISTS api_key_group_routes (
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    position   INT NOT NULL CHECK (position >= 1 AND position <= 10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (api_key_id, group_id),
    UNIQUE (api_key_id, position)
);

CREATE INDEX IF NOT EXISTS idx_api_key_group_routes_group_id
    ON api_key_group_routes(group_id);

COMMENT ON TABLE api_key_group_routes IS 'API 密钥智能路由候选分组（按 position 优先尝试）';
