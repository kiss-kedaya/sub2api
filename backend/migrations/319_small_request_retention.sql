-- Small HTTP exchanges are independent from instance-local diagnostic captures.
-- Buckets never share blob references, so expired hours can be removed safely.
CREATE TABLE IF NOT EXISTS small_request_records (
    id BIGSERIAL PRIMARY KEY,
    bucket TIMESTAMPTZ NOT NULL,
    fingerprint TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    model TEXT NOT NULL,
    status INTEGER NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    repeat_count BIGINT NOT NULL DEFAULT 1,
    input_tokens BIGINT NOT NULL,
    output_tokens BIGINT NOT NULL,
    cache_read_tokens BIGINT NOT NULL,
    cache_creation_tokens BIGINT NOT NULL,
    actual_cost NUMERIC(20,8) NOT NULL,
    raw_bytes BIGINT NOT NULL,
    omitted_responses BIGINT NOT NULL DEFAULT 0,
    request_ids JSONB NOT NULL,
    request_headers_hash TEXT NOT NULL,
    request_body_hash TEXT NOT NULL,
    data JSONB NOT NULL,
    UNIQUE(bucket, fingerprint)
);
CREATE INDEX IF NOT EXISTS small_request_records_bucket ON small_request_records(bucket, last_seen DESC);
CREATE INDEX IF NOT EXISTS small_request_records_user ON small_request_records(user_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS small_request_records_key ON small_request_records(api_key_id, last_seen DESC);
CREATE INDEX IF NOT EXISTS small_request_records_model ON small_request_records(model, last_seen DESC);
CREATE INDEX IF NOT EXISTS small_request_records_ids ON small_request_records USING gin(request_ids);
CREATE TABLE IF NOT EXISTS small_request_blobs (
    bucket TIMESTAMPTZ NOT NULL,
    user_id BIGINT NOT NULL,
    hash TEXT NOT NULL,
    codec TEXT NOT NULL CHECK(codec IN ('identity','gzip')),
    raw_size INTEGER NOT NULL,
    stored_size INTEGER NOT NULL,
    ref_count BIGINT NOT NULL DEFAULT 0,
    payload BYTEA NOT NULL,
    PRIMARY KEY(bucket, user_id, hash)
);
CREATE TABLE IF NOT EXISTS small_request_responses (
    record_id BIGINT NOT NULL REFERENCES small_request_records(id) ON DELETE CASCADE,
    bucket TIMESTAMPTZ NOT NULL,
    fingerprint TEXT NOT NULL,
    headers_hash TEXT NOT NULL,
    body_hash TEXT NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen TIMESTAMPTZ NOT NULL,
    repeat_count BIGINT NOT NULL DEFAULT 1,
    data JSONB NOT NULL,
    PRIMARY KEY(record_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS small_request_responses_bucket ON small_request_responses(bucket);
CREATE INDEX IF NOT EXISTS small_request_responses_body ON small_request_responses(bucket,body_hash);
CREATE INDEX IF NOT EXISTS small_request_responses_headers ON small_request_responses(bucket,headers_hash);
CREATE TABLE IF NOT EXISTS small_request_skips (
    bucket TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL,
    count BIGINT NOT NULL,
    PRIMARY KEY(bucket,reason)
);
CREATE TABLE IF NOT EXISTS small_request_state (
    id INTEGER PRIMARY KEY CHECK(id=1),
    cleared_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity'
);
INSERT INTO small_request_state(id) VALUES(1) ON CONFLICT(id) DO NOTHING;
-- Rollback is binary-only: the old binary ignores these independent tables.
-- Physical rollback, only after capture is stopped: drop responses, records,
-- blobs and skips in that order. No existing table or billing row is changed.
