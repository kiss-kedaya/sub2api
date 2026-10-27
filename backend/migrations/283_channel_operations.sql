-- Additive operational classification; never used by routing or billing.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';
ALTER TABLE channels ADD COLUMN IF NOT EXISTS operations_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS channel_operation_upstreams (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL CHECK (length(btrim(name)) > 0),
    UNIQUE (channel_id, name),
    UNIQUE (id, channel_id)
);
CREATE TABLE IF NOT EXISTS channel_operation_members (
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    upstream_id BIGINT NOT NULL,
    PRIMARY KEY (channel_id, account_id),
    FOREIGN KEY (upstream_id, channel_id) REFERENCES channel_operation_upstreams(id, channel_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_channel_operation_members_upstream ON channel_operation_members(upstream_id, account_id);
COMMENT ON TABLE channel_operation_members IS 'Current operational classification only; does not change account_groups, scheduler or historical usage attribution';
