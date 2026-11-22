-- 333_crypto_deposit_instant_credit.sql
-- 即时入账（provisional credit）与撤回扣回。
--
-- 语义：
--   provisional    : 该笔充值在「首次观测」时按 0 确认先入账，尚未达到最终
--                    确认数；达到 confirmations 后同一条 UPDATE 把它置为
--                    false（转正，不再动余额）。
--   credited_at    : 实际给用户加余额的时刻，人工核对/对账与 ETA 展示用。
--   revoked_at     : 已因重组/交易失败被自动扣回。非空即永久拉黑该
--                    (chain, tx_hash, log_index)，之后任何扫描/人工确认都
--                    不能再次入账。
--   revoke_reason  : 扣回原因（机器码，管理端/用户端展示时翻译）。
--
-- 默认不加锁：ALTER TABLE ... ADD COLUMN 在 PG 11+ 只改元数据，不重写表。
ALTER TABLE deposit_transactions
    ADD COLUMN IF NOT EXISTS provisional BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS credited_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoke_reason TEXT;

-- 未转正的入账需要在风险窗口内持续复核（recheckCredited 按 last_attempt_at
-- 轮转），直到转正或撤回；部分索引只覆盖这一小撮行。
CREATE INDEX IF NOT EXISTS idx_deposit_transactions_provisional
    ON deposit_transactions (chain, last_attempt_at)
    WHERE provisional;

-- 历史已入账行补一个可解释的入账时间；幂等，重跑不改变已有值。
UPDATE deposit_transactions
   SET credited_at = created_at
 WHERE status = 'credited'
   AND credited_at IS NULL;

COMMENT ON COLUMN deposit_transactions.provisional IS
    '该笔充值按 0 确认先入账、尚未达到最终确认数；转正后为 false';
COMMENT ON COLUMN deposit_transactions.credited_at IS
    '实际加余额的时刻（即时入账可能早于最终确认）';
COMMENT ON COLUMN deposit_transactions.revoked_at IS
    '已因重组/交易失败自动扣回；非空即永久拉黑该 chain/tx_hash/log_index';
COMMENT ON COLUMN deposit_transactions.revoke_reason IS
    '自动扣回原因机器码，例如 reorg_noncanonical_block / reorg_receipt_missing';
