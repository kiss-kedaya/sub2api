-- Pending rows need a retry clock: without it the oldest 50 permanent
-- failures are retried forever and newer deposits never get a turn.
ALTER TABLE deposit_transactions ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_deposit_pending_retry
    ON deposit_transactions (status, last_attempt_at);
