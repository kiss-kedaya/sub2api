-- Store time and numeric deltas for expiry at the edge of an hourly bucket.
-- Content stays in the existing deduplicated blobs. Record IDs are never reused.
-- Expire this table separately so older binaries can still truncate the original
-- capture tables during a rolling upgrade or rollback.
CREATE TABLE small_request_occurrences (
    record_id BIGINT NOT NULL,
    ordinal BIGINT NOT NULL,
    seen_at TIMESTAMPTZ NOT NULL,
    request_id TEXT NOT NULL,
    response_fingerprint TEXT NOT NULL,
    status INTEGER NOT NULL,
    input_tokens BIGINT NOT NULL,
    output_tokens BIGINT NOT NULL,
    cache_read_tokens BIGINT NOT NULL,
    cache_creation_tokens BIGINT NOT NULL,
    actual_cost NUMERIC(20,8) NOT NULL,
    raw_bytes BIGINT NOT NULL,
    PRIMARY KEY (record_id, ordinal)
);
CREATE INDEX small_request_occurrences_record_time
    ON small_request_occurrences(record_id, seen_at DESC, ordinal DESC);
CREATE INDEX small_request_occurrences_expiry ON small_request_occurrences(seen_at);
