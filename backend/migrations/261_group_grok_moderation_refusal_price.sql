-- Grok 内容策略拒绝（SAFETY_CHECK_TYPE）单次扣费，分组级自助配置。
--
-- NULL 表示未配置：运行时回落到默认 0.1 USD/次。
-- 0 表示显式关闭扣费。
-- 该费用不乘分组 RateMultiplier：运营按「每次多少钱」直接定价。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS grok_moderation_refusal_price DECIMAL(20, 8);

COMMENT ON COLUMN groups.grok_moderation_refusal_price IS
    'Grok 安全拒绝单次扣费（USD/次）；NULL 使用默认 0.1，0 表示不扣费';
