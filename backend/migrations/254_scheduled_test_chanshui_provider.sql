-- Existing plans retain the Pelican evaluator. No credentials are stored here.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS quality_provider VARCHAR(24) NOT NULL DEFAULT 'pelican',
    ADD COLUMN IF NOT EXISTS quality_config JSONB NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS quality_audit_state JSONB NOT NULL DEFAULT '{}';

-- Conservative shared POST gate across all runner instances/providers. Polls do
-- not consume this gate. The service still honors the provider's Retry-After.
CREATE TABLE IF NOT EXISTS scheduled_test_audit_gate (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    next_allowed_at TIMESTAMPTZ NOT NULL
);
INSERT INTO scheduled_test_audit_gate(id, next_allowed_at)
VALUES (1, TIMESTAMPTZ 'epoch') ON CONFLICT (id) DO NOTHING;
