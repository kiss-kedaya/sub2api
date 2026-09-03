ALTER TABLE payment_provider_instances
    ADD COLUMN IF NOT EXISTS recharge_fee_rate DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS balance_recharge_multiplier DOUBLE PRECISION;
