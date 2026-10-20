-- Durable sweep jobs. Existing successful rows retain their accounting meaning.
-- Legacy uncertain hashes are evidence, never permission to send again.
ALTER TABLE sweep_records
    ADD COLUMN IF NOT EXISTS job_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS raw_amount TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS token_decimals INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS derivation_index BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS confirmations_required NUMERIC(20,0) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS gas_payer TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gas_amount TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gas_nonce TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gas_raw_tx TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS gas_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS sweep_nonce TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sweep_raw_tx TEXT NOT NULL DEFAULT '';

ALTER TABLE sweep_records DROP CONSTRAINT IF EXISTS sweep_records_status_check;
ALTER TABLE sweep_records ADD CONSTRAINT sweep_records_status_check CHECK
    (status IN ('planned','gas_sent','gas_pending','gas_confirmed','sweep_pending',
                'swept','failed','manual_review'));

UPDATE sweep_records SET status='manual_review',
    error=CASE WHEN error='' THEN 'Legacy uncertain transaction; reconcile hashes and amount manually'
               ELSE error END
WHERE job_version=0 AND status<>'swept'
  AND (gas_tx_hash<>'' OR sweep_tx_hash<>'' OR status='gas_sent');

-- Old attempts can have duplicates; only the new durable jobs are unique.
CREATE UNIQUE INDEX IF NOT EXISTS uq_sweep_active_job
    ON sweep_records (chain, lower(from_address), lower(token))
    WHERE job_version=1 AND status NOT IN ('swept','failed');

ALTER TABLE sweep_records DROP CONSTRAINT IF EXISTS sweep_job_amount_check;
ALTER TABLE sweep_records ADD CONSTRAINT sweep_job_amount_check CHECK
    (job_version=0 OR (raw_amount ~ '^[1-9][0-9]*$'
      AND token_decimals BETWEEN 0 AND 18 AND derivation_index BETWEEN 0 AND 4294967295
      AND confirmations_required>0));

ALTER TABLE sweep_records DROP CONSTRAINT IF EXISTS sweep_job_payload_check;
ALTER TABLE sweep_records ADD CONSTRAINT sweep_job_payload_check CHECK
    (job_version=0 OR (
      ((gas_raw_tx='' AND gas_nonce='' AND gas_tx_hash='' AND gas_payer=''
        AND gas_amount='' AND NOT gas_confirmed)
       OR (gas_raw_tx<>'' AND gas_tx_hash<>'' AND gas_nonce ~ '^[0-9]+$'
           AND gas_payer<>'' AND gas_amount ~ '^[1-9][0-9]*$'))
      AND ((sweep_raw_tx='' AND sweep_nonce='' AND sweep_tx_hash='')
       OR (sweep_raw_tx<>'' AND sweep_tx_hash<>'' AND sweep_nonce ~ '^[0-9]+$'))
      AND (status IN ('manual_review','failed')
       OR (status='planned' AND gas_raw_tx='' AND sweep_raw_tx='')
       OR (status='gas_pending' AND gas_raw_tx<>'' AND NOT gas_confirmed AND sweep_raw_tx='')
       OR (status='gas_confirmed' AND gas_confirmed AND gas_raw_tx<>'' AND sweep_raw_tx='')
       OR (status IN ('sweep_pending','swept') AND sweep_raw_tx<>''))));

CREATE INDEX IF NOT EXISTS idx_sweep_unfinished_jobs ON sweep_records (id)
    WHERE (job_version=1 AND status NOT IN ('swept','failed'))
       OR (job_version=0 AND status<>'swept'
           AND (status IN ('manual_review','gas_sent') OR gas_tx_hash<>'' OR sweep_tx_hash<>''));
