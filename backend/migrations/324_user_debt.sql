-- 323_user_debt.sql
-- 历史负余额清零（2026-11-21）之后，欠款不再以负数挂在 users.balance 上，
-- 而是搬进独立台账，用户侧余额永远是「可用余额」：
--
--   negative_balance_writeoffs : 清零当次逐用户落账（清零前余额、冲销金额）
--   user_debt                  : 尚未收回的欠款，一人一行；首次由 writeoffs 回填
--   user_debt_clawbacks        : 每次充值抵扣欠款的流水，供对账/退款追溯
--
-- 充值到账（支付履约 / 兑换码）和管理员加余额都会在同一事务里先抵扣 user_debt，
-- 只有剩余部分进用户可用余额；用户接口不返回欠款字段。
-- 开关位于 settings 表：key = 'billing.debt_clawback_enabled'，值为 'false' 时关闭，
-- 缺失/无法解析按开启处理。每次充值都直接读库，改这一行即时生效（无需重启）。

CREATE TABLE IF NOT EXISTS negative_balance_writeoffs (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL UNIQUE,
    writeoff_amount  NUMERIC(20,8) NOT NULL CHECK (writeoff_amount > 0),
    balance_before   NUMERIC(20,8) NOT NULL CHECK (balance_before < 0),
    note             TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_negative_balance_writeoffs_user ON negative_balance_writeoffs (user_id);

COMMENT ON TABLE negative_balance_writeoffs IS
    '2026-11-21 负余额清零当次的逐用户落账（金额、清零前余额）；配合 users_negative_balance_backup_20261121 与 user_debt 对账，不得删除';

CREATE TABLE IF NOT EXISTS user_debt (
    user_id     BIGINT PRIMARY KEY,
    amount      NUMERIC(20,8) NOT NULL DEFAULT 0 CHECK (amount >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_debt_outstanding ON user_debt (updated_at) WHERE amount > 0;

COMMENT ON TABLE user_debt IS
    '未收回的历史欠款余额（来自 negative_balance_writeoffs）；充值到账先抵扣这里，不进入 users.balance';

CREATE TABLE IF NOT EXISTS user_debt_clawbacks (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL,
    amount         NUMERIC(20,8) NOT NULL CHECK (amount > 0),
    credit_amount  NUMERIC(20,8) NOT NULL CHECK (credit_amount > 0),
    source         VARCHAR(64) NOT NULL DEFAULT '',
    source_id      VARCHAR(128) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_debt_clawbacks_user ON user_debt_clawbacks (user_id, created_at DESC);

COMMENT ON TABLE user_debt_clawbacks IS
    '每次充值抵扣欠款的流水：amount=本次抵扣，credit_amount=本次到账总额，source=redeem_code/admin_adjust';

-- 把清零当次落账的欠款回填成待收欠款。生产上 writeoffs 已有数据；全新库为空，INSERT 0 行。
INSERT INTO user_debt (user_id, amount, created_at, updated_at)
SELECT user_id, SUM(writeoff_amount), MIN(created_at), NOW()
FROM negative_balance_writeoffs
GROUP BY user_id
HAVING SUM(writeoff_amount) > 0
ON CONFLICT (user_id) DO NOTHING;
