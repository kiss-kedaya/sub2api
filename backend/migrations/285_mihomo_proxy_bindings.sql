-- A host-local node is never resolved using another host's loopback port.
CREATE TABLE IF NOT EXISTS mihomo_proxy_bindings (
    host_id VARCHAR(64) NOT NULL,
    node_id VARCHAR(256) NOT NULL,
    proxy_id BIGINT NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (host_id,node_id),
    UNIQUE (proxy_id)
);
