-- Deposit address rotation keeps one ACTIVE assignment per (user, chain family)
-- while retired addresses stay behind as disabled history. The scanner watches
-- active + disabled rows, so late deposits to a retired address are still
-- detected and credited to the original user.
--
-- 261 created uq_deposit_addr_user_chain as a plain unique index; later
-- installs upgraded it to a UNIQUE CONSTRAINT. Drop both shapes before adding
-- the partial index so the migration is idempotent on every deployment.
ALTER TABLE deposit_addresses DROP CONSTRAINT IF EXISTS uq_deposit_addr_user_chain;
DROP INDEX IF EXISTS uq_deposit_addr_user_chain;

CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_addr_user_chain_active
    ON deposit_addresses (user_id, chain)
    WHERE status = 'active';
