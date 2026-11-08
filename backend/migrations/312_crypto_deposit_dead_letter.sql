-- A pending deposit whose chain or token was removed from the configuration can
-- never be verified again. Rotating its check clock forever hides a real
-- deposit from operators and keeps the retry queue busy. dead_letter is a
-- terminal status with a durable reason, so the observation leaves the retry
-- rotation and stays visible for manual reconciliation.
ALTER TABLE deposit_transactions DROP CONSTRAINT IF EXISTS deposit_transactions_status_check;
ALTER TABLE deposit_transactions ADD CONSTRAINT deposit_transactions_status_check
    CHECK (status IN ('pending', 'credited', 'ignored', 'reverted', 'dead_letter'));
