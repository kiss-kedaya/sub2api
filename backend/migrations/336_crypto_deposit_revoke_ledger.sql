-- 336_crypto_deposit_revoke_ledger.sql
-- 链上充值「临时入账」被重组/回滚后，在用户可见的余额流水（redeem_codes）
-- 补一条负向调整记录，让用户「余额明细 / 使用记录」能对得上余额变化。
--
-- 语义（不引入新表，复用现有余额流水）：
--   type       = 'crypto_revoke'（服务端专用来源枚举，用户不可兑换）
--   value      <  0             （扣回金额取负；SUM 类统计只取正值，不会被算成充值）
--   status     = 'used'
--   used_by    = 被扣回的用户
--   notes      = 「链上充值未最终确认已回滚」
--   source_ref = 'crypto_deposit:<deposit_id>:<chain>:<tx_hash>:<log_index>'
--
-- source_ref 是这条合成流水的结构化来源引用：既有 notes 给用户看，也留一条
-- 可索引的指针给财务/审计，从「余额明细」一行反查到 deposit_transactions 的
-- 同一笔链上入账。真实卡密行不写 source_ref（保持 NULL）。
ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS source_ref VARCHAR(160);

COMMENT ON COLUMN redeem_codes.source_ref IS
    'Synthetic ledger row source reference, e.g. crypto_deposit:<deposit_id>:<chain>:<tx_hash>:<log_index>';

COMMENT ON COLUMN redeem_codes.type IS
    'balance/payment_balance/admin_balance/affiliate_balance/concurrency/subscription/crypto_revoke; crypto_revoke = negative claw-back ledger row (not redeemable)';

COMMENT ON COLUMN redeem_codes.value IS
    'Amount in balance units; may be negative for crypto_revoke claw-back ledger rows';

-- 同一笔链上入账只允许一条扣回流水。与扣回事务里的条件 UPDATE 一起构成
-- 双重幂等：即使并发/重试绕过内存判断，第二条插入也会被唯一索引挡住。
CREATE UNIQUE INDEX IF NOT EXISTS uq_redeem_codes_crypto_revoke_source
    ON redeem_codes (source_ref)
    WHERE type = 'crypto_revoke' AND source_ref IS NOT NULL;

-- 用户「余额明细」按 used_by 过滤后按时间倒序；回滚行只占很小一部分，
-- 用部分索引让这条查询不依赖全表扫描。
CREATE INDEX IF NOT EXISTS idx_redeem_codes_crypto_revoke_user
    ON redeem_codes (used_by, used_at DESC)
    WHERE type = 'crypto_revoke';
