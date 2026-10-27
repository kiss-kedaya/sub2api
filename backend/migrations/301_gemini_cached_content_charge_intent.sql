-- Cache visibility and its frozen charge must survive the same commit.
ALTER TABLE gemini_cached_contents
    ADD COLUMN IF NOT EXISTS billing_ready BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS charge_intent JSONB NULL,
    ADD COLUMN IF NOT EXISTS charge_target_expire_time TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_gemini_cached_contents_pending_charge
    ON gemini_cached_contents (id)
    WHERE deleted_at IS NULL AND charge_intent IS NOT NULL;
