-- Per-group OpenAI service-tier multipliers.
--
-- NULL keeps the official, model-owned tier pricing (Fast 2x / 2.5x, UltraFast 6x).
-- A value is the customer-facing multiplier applied on top of the group's rate
-- multiplier; 1 means no surcharge (free Fast / free UltraFast).
-- 下面的 ALTER 需要 groups / usage_logs 的 ACCESS EXCLUSIVE 锁，拿锁期间这两张表上的新查询会全部排队。
-- 限定最长等 5 秒：拿不到就让整个迁移失败回滚（发版在换二进制之前中止，线上实例不受影响），再重试。
SET LOCAL lock_timeout = '5s';

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS openai_fast_multiplier NUMERIC(10,4),
    ADD COLUMN IF NOT EXISTS openai_ultrafast_multiplier NUMERIC(10,4);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_openai_fast_multiplier_range' AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_openai_fast_multiplier_range
            CHECK (openai_fast_multiplier IS NULL OR (openai_fast_multiplier > 0 AND openai_fast_multiplier <= 100));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_openai_ultrafast_multiplier_range' AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_openai_ultrafast_multiplier_range
            CHECK (openai_ultrafast_multiplier IS NULL OR (openai_ultrafast_multiplier > 0 AND openai_ultrafast_multiplier <= 100));
    END IF;
END $$;

COMMENT ON COLUMN groups.openai_fast_multiplier IS
    'OpenAI Fast (priority) tier multiplier applied on top of the group rate multiplier; NULL keeps the official tier pricing, 1 = no surcharge';
COMMENT ON COLUMN groups.openai_ultrafast_multiplier IS
    'OpenAI UltraFast tier multiplier applied on top of the group rate multiplier; NULL keeps the official tier pricing, 1 = no surcharge';

-- The multiplier actually applied to the customer charge of a Fast/UltraFast
-- request (official tier ratio when the group has no override). NULL for rows
-- without a premium service tier and for rows written before this column existed.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS service_tier_multiplier NUMERIC(10,4);

COMMENT ON COLUMN usage_logs.service_tier_multiplier IS
    'Service-tier multiplier applied to the customer charge of a priority/ultrafast request; NULL when not recorded';
