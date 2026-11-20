-- OpenAI 12h 缓存写入价：与通用的 cache_write_price（30m/5m 档）并列的一档独立单价。
-- NULL = 沿用通用缓存写入价（存量价卡语义不变，无需回填数据）。
ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE channel_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE channel_account_stats_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

COMMENT ON COLUMN channel_model_pricing.cache_write_12h_price IS
    'OpenAI 12h cache write price per token; NULL preserves the generic cache_write_price';
COMMENT ON COLUMN channel_pricing_intervals.cache_write_12h_price IS
    'Interval-specific OpenAI 12h cache write price per token; NULL inherits the generic price';
COMMENT ON COLUMN channel_account_stats_model_pricing.cache_write_12h_price IS
    'OpenAI 12h cache write price per token for account stats pricing';
COMMENT ON COLUMN channel_account_stats_pricing_intervals.cache_write_12h_price IS
    'Interval-specific OpenAI 12h cache write price per token for account stats pricing';
