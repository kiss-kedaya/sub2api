CREATE TABLE local_reasoning_plans (
 id BIGSERIAL PRIMARY KEY, account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 model VARCHAR(200) NOT NULL, enabled BOOLEAN NOT NULL DEFAULT FALSE,
 interval_minutes INTEGER NOT NULL CHECK(interval_minutes BETWEEN 5 AND 10080),
 action TEXT NOT NULL CHECK(action IN ('observe','pause')), auto_restore BOOLEAN NOT NULL DEFAULT FALSE,
 revision BIGINT NOT NULL DEFAULT 1, last_outcome TEXT NOT NULL DEFAULT '',
 next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), lease TEXT NOT NULL DEFAULT '',lease_until TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_local_reasoning_due ON local_reasoning_plans(next_run_at) WHERE enabled;
CREATE TABLE local_reasoning_results (
 id BIGSERIAL PRIMARY KEY,plan_id BIGINT NOT NULL REFERENCES local_reasoning_plans(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,model VARCHAR(200) NOT NULL,
 question_id TEXT NOT NULL,family TEXT NOT NULL,outcome TEXT NOT NULL CHECK(outcome IN ('passed','failed','inconclusive')),
 duration_ms BIGINT NOT NULL,action TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_local_reasoning_history ON local_reasoning_results(account_id,model,created_at DESC);
CREATE TABLE local_reasoning_owners (
 plan_id BIGINT PRIMARY KEY REFERENCES local_reasoning_plans(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 token TEXT NOT NULL,account_updated_at TIMESTAMPTZ NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
COMMENT ON TABLE local_reasoning_results IS 'Safe local question metadata and verdict only; no prompts, answers, credentials or upstream body';
