-- 非事务执行（*_notx.sql）：仅含纯 CREATE INDEX CONCURRENTLY IF NOT EXISTS，不阻塞热表写入。
-- 说明：migrations_runner.go 校验 *_notx.sql 仅允许并发索引语句并由执行器统一管理会话超时，故此处不混入 SET 语句。
-- 谓词携带 AND user_id > 0，防止只按 WHERE status = 3 查询的队列积压/出队语句被规划器误选为 BitmapAnd + Sort，
-- 仅服务于按 user_id 查询未结算行的 LoadLiveBalanceInitializationSnapshot。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_billing_balance_settlements_user_unsettled
    ON billing_balance_settlements (user_id)
    WHERE status IN (1, 2, 3) AND user_id > 0;
