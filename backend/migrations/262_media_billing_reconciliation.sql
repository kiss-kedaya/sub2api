-- Unresolved video tasks stop automatic polling without releasing their holds.
-- Existing workers only lease submitted/ready/billed, so they leave this new
-- state alone during a rolling upgrade or an application-only rollback.
ALTER TABLE media_billing_jobs
    DROP CONSTRAINT IF EXISTS media_billing_jobs_status_valid;

ALTER TABLE media_billing_jobs
    ADD CONSTRAINT media_billing_jobs_status_valid
    CHECK (status IN ('creating', 'submitted', 'reconciliation', 'ready', 'billed', 'done', 'failed'));

-- Reconciliation is only an unresolved video state, never a frozen charge.
ALTER TABLE media_billing_jobs
    DROP CONSTRAINT IF EXISTS media_billing_jobs_reconciliation_valid;

ALTER TABLE media_billing_jobs
    ADD CONSTRAINT media_billing_jobs_reconciliation_valid
    CHECK (status <> 'reconciliation' OR (kind = 'grok_video' AND intent IS NULL));

-- Reservations still count against subscription limits while they await
-- reconciliation. Keep the existing index name and extend its predicate.
DROP INDEX IF EXISTS media_billing_jobs_subscription_pending_idx;
CREATE INDEX media_billing_jobs_subscription_pending_idx
    ON media_billing_jobs (subscription_id)
    WHERE subscription_id IS NOT NULL
      AND status IN ('creating', 'submitted', 'reconciliation', 'ready');
