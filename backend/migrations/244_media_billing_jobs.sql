CREATE TABLE IF NOT EXISTS media_billing_jobs (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    account_id BIGINT NOT NULL REFERENCES accounts(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    subscription_id BIGINT REFERENCES user_subscriptions(id),
    upstream_task_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    reserved_amount NUMERIC(20, 8) NOT NULL DEFAULT 0,
    snapshot JSONB,
    intent JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    available_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INTEGER NOT NULL DEFAULT 0,

    CONSTRAINT media_billing_jobs_id_nonempty CHECK (BTRIM(id) <> ''),
    CONSTRAINT media_billing_jobs_kind_valid CHECK (kind IN ('grok_video', 'image_usage')),
    CONSTRAINT media_billing_jobs_status_valid CHECK (status IN ('creating', 'submitted', 'ready', 'billed', 'done', 'failed')),
    CONSTRAINT media_billing_jobs_reserved_nonnegative CHECK (reserved_amount >= 0),
    CONSTRAINT media_billing_jobs_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT media_billing_jobs_snapshot_object CHECK (snapshot IS NULL OR jsonb_typeof(snapshot) = 'object'),
    CONSTRAINT media_billing_jobs_intent_object CHECK (intent IS NULL OR jsonb_typeof(intent) = 'object')
);

-- Snapshot and intent are private billing state. Callers must never put
-- credentials, request bodies, or media URLs in either JSON document.
COMMENT ON COLUMN media_billing_jobs.snapshot IS 'Private credential-free routing/pricing snapshot';
COMMENT ON COLUMN media_billing_jobs.intent IS 'Private credential-free frozen billing intent';

CREATE UNIQUE INDEX IF NOT EXISTS media_billing_jobs_upstream_owner_unique
    ON media_billing_jobs (kind, user_id, api_key_id, upstream_task_id)
    WHERE BTRIM(upstream_task_id) <> '';

CREATE INDEX IF NOT EXISTS media_billing_jobs_created_at_idx
    ON media_billing_jobs (created_at);
CREATE INDEX IF NOT EXISTS media_billing_jobs_updated_at_idx
    ON media_billing_jobs (updated_at);
CREATE INDEX IF NOT EXISTS media_billing_jobs_available_at_idx
    ON media_billing_jobs (available_at, created_at)
    WHERE status IN ('submitted', 'ready', 'billed');
CREATE INDEX IF NOT EXISTS media_billing_jobs_subscription_pending_idx
    ON media_billing_jobs (subscription_id)
    WHERE subscription_id IS NOT NULL AND status IN ('creating', 'submitted', 'ready');
