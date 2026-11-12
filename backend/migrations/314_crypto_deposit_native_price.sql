-- Native-coin (ETH/BNB) deposits are settled at the market price observed at
-- credit time. Keep that settlement basis on the deposit row itself so the
-- question "which price, observed when" is answerable after the fact and
-- survives order retention changes.
--
-- Stablecoin rows keep these columns NULL: their token amount is already USD.
ALTER TABLE deposit_transactions
    ADD COLUMN IF NOT EXISTS native_price_usd NUMERIC(38,18),
    ADD COLUMN IF NOT EXISTS native_price_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS native_price_source TEXT NOT NULL DEFAULT '';
