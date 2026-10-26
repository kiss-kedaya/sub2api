-- Gasless orders retain signed authorization across crashes and timeouts.
CREATE TABLE IF NOT EXISTS crypto_bitget_sweep_jobs (
    job_id BIGINT PRIMARY KEY REFERENCES sweep_records(id),
    order_id VARCHAR(128) NOT NULL UNIQUE,
    intent JSONB NOT NULL,
    prepared JSONB NOT NULL,
    signed_messages TEXT NOT NULL DEFAULT '',
    authorization_mac TEXT NOT NULL DEFAULT '',
    bound_tx_hash VARCHAR(66) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE sweep_records DROP CONSTRAINT IF EXISTS sweep_job_amount_check;
ALTER TABLE sweep_records ADD CONSTRAINT sweep_job_amount_check CHECK
    (job_version=0 OR (job_version IN (1,2) AND raw_amount ~ '^[1-9][0-9]*$'
      AND token_decimals BETWEEN 0 AND 18 AND derivation_index BETWEEN 0 AND 4294967295
      AND confirmations_required>0));

ALTER TABLE sweep_records DROP CONSTRAINT IF EXISTS sweep_job_payload_check;
ALTER TABLE sweep_records ADD CONSTRAINT sweep_job_payload_check CHECK
    (job_version=0 OR (job_version=2 AND gas_raw_tx='' AND gas_nonce=''
       AND gas_tx_hash='' AND gas_payer='' AND gas_amount='' AND NOT gas_confirmed
       AND sweep_raw_tx='' AND sweep_nonce=''
       AND status IN ('planned','sweep_pending','swept','manual_review','failed')
       AND (status<>'swept' OR sweep_tx_hash<>'')) OR (job_version=1 AND (
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
       OR (status IN ('sweep_pending','swept') AND sweep_raw_tx<>'')))));

CREATE UNIQUE INDEX IF NOT EXISTS uq_sweep_active_provider_job
    ON sweep_records (chain, lower(from_address), lower(token))
    WHERE job_version IN (1,2) AND status NOT IN ('swept','failed');
