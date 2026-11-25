-- Per-group OpenAI service-tier multipliers.
--
-- NULL keeps the official, model-owned tier pricing (Fast 2x / 2.5x, UltraFast 6x).
-- A value is the customer-facing multiplier applied on top of the group's rate
-- multiplier; 1 means no surcharge (free Fast / free UltraFast).
-- 本迁移只碰 groups 一张表。usage_logs 的新列在 342 里单独加：
-- 同一个事务里先锁 groups 再锁 usage_logs，会和「先读 usage_logs 再关联 groups」的看板查询互相等待，
-- 生产库实测直接 deadlock detected（2.3.165 发版时迁移被当成死锁牺牲品杀掉）。
-- 下面的 ALTER 需要 groups 的 ACCESS EXCLUSIVE 锁，拿锁期间这张表上的新查询会排队。
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
