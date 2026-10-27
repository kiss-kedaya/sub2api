-- Judge configuration contains no API key, prompts, answers or provider bodies.
ALTER TABLE local_reasoning_plans ADD COLUMN IF NOT EXISTS judge_kind TEXT NOT NULL DEFAULT 'local';
ALTER TABLE local_reasoning_plans ADD COLUMN IF NOT EXISTS judge_group_id BIGINT;
ALTER TABLE local_reasoning_plans ADD COLUMN IF NOT EXISTS judge_model TEXT NOT NULL DEFAULT '';
