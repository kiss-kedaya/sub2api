-- The multiplier actually applied to the customer charge of a Fast/UltraFast
-- request (official tier ratio when the group has no override). NULL for rows
-- without a premium service tier and for rows written before this column existed.
--
-- 本迁移只碰 usage_logs 一张表（groups 的两列在 340 里）：同一个事务里碰两张热表会和看板的
-- 关联查询互相等待而死锁。ADD COLUMN（可空、无默认值）只是元数据变更，不重写表，但要 ACCESS EXCLUSIVE 锁，
-- 所以限定最长等 5 秒：拿不到就整个迁移回滚、发版在换二进制之前中止，隔几秒重试。
SET LOCAL lock_timeout = '5s';

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS service_tier_multiplier NUMERIC(10,4);

COMMENT ON COLUMN usage_logs.service_tier_multiplier IS
    'Service-tier multiplier applied to the customer charge of a priority/ultrafast request; NULL when not recorded';
