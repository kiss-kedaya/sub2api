-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
-- Application payload bytes, independent of token billing and diagnostic retention.
-- No cascading foreign keys: deleting a key must not erase historical metering.
CREATE TABLE IF NOT EXISTS user_traffic_events (
    event_id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    endpoint VARCHAR(256) NOT NULL,
    method VARCHAR(16) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    upload_bytes BIGINT NOT NULL CHECK (upload_bytes >= 0),
    download_bytes BIGINT NOT NULL CHECK (download_bytes >= 0),
    status_code INTEGER NOT NULL,
    outcome VARCHAR(32) NOT NULL,
    request_complete BOOLEAN NOT NULL,
    measurement_version SMALLINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS user_traffic_events_user_time_idx
    ON user_traffic_events (user_id, started_at DESC, event_id);
CREATE INDEX IF NOT EXISTS user_traffic_events_key_time_idx
    ON user_traffic_events (api_key_id, started_at DESC);
CREATE INDEX IF NOT EXISTS user_traffic_events_time_idx
    ON user_traffic_events (started_at DESC, event_id);

CREATE TABLE IF NOT EXISTS user_traffic_hourly (
    bucket_start TIMESTAMPTZ NOT NULL,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL DEFAULT 0,
    upload_bytes BIGINT NOT NULL DEFAULT 0 CHECK (upload_bytes >= 0),
    download_bytes BIGINT NOT NULL DEFAULT 0 CHECK (download_bytes >= 0),
    requests BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, api_key_id, group_id, bucket_start)
);
CREATE INDEX IF NOT EXISTS user_traffic_hourly_time_idx ON user_traffic_hourly (bucket_start);
COMMENT ON TABLE user_traffic_events IS 'HTTP/SSE application read/write payload bytes; not wire traffic or proof of client receipt. Version 1 attributes a request to its start time.';
