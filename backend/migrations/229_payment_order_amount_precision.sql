-- Payment supports ISO currencies with three fractional digits (for example
-- KWD and BHD). Keep persisted plan, order, and refund amounts at that
-- precision so provider callbacks compare against the same value charged.
ALTER TABLE payment_orders
    ALTER COLUMN amount TYPE DECIMAL(20,3),
    ALTER COLUMN pay_amount TYPE DECIMAL(20,3),
    ALTER COLUMN refund_amount TYPE DECIMAL(20,3);

ALTER TABLE subscription_plans
    ALTER COLUMN price TYPE DECIMAL(20,3),
    ALTER COLUMN original_price TYPE DECIMAL(20,3);
