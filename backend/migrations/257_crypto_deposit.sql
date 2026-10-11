-- Crypto deposit & sweep: address pool, deposit records, sweep records, scan cursors.
-- All tables are additive; nothing existing is modified.

-- Address pool: HD-derived addresses, assigned lazily to users.
CREATE TABLE IF NOT EXISTS crypto_address_pool (
    id               BIGSERIAL PRIMARY KEY,
    chain_family     TEXT NOT NULL,
    derivation_index INT  NOT NULL,
    address          TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'free',
    assigned_user_id BIGINT,
    assigned_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT crypto_address_pool_family_check CHECK (chain_family IN ('evm', 'solana')),
    CONSTRAINT crypto_address_pool_status_check CHECK (status IN ('free', 'assigned', 'retired'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_crypto_pool_family_index
    ON crypto_address_pool (chain_family, derivation_index);
CREATE INDEX IF NOT EXISTS idx_crypto_pool_status
    ON crypto_address_pool (chain_family, status);
CREATE INDEX IF NOT EXISTS idx_crypto_pool_user
    ON crypto_address_pool (assigned_user_id);

-- Per-user deposit addresses (assignment result, includes chain).
CREATE TABLE IF NOT EXISTS deposit_addresses (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL,
    chain            TEXT NOT NULL,
    address          TEXT NOT NULL,
    derivation_index INT  NOT NULL,
    status           TEXT NOT NULL DEFAULT 'active',
    last_seen_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT deposit_addresses_status_check CHECK (status IN ('active', 'disabled'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_addr_chain_address
    ON deposit_addresses (chain, address);
CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_addr_user_chain
    ON deposit_addresses (user_id, chain);
CREATE INDEX IF NOT EXISTS idx_deposit_addr_user
    ON deposit_addresses (user_id);

-- On-chain deposits observed by the scanners.
CREATE TABLE IF NOT EXISTS deposit_transactions (
    id            BIGSERIAL PRIMARY KEY,
    chain         TEXT NOT NULL,
    tx_hash       TEXT NOT NULL,
    log_index     INT  NOT NULL DEFAULT 0,
    from_address  TEXT NOT NULL,
    to_address    TEXT NOT NULL,
    token         TEXT NOT NULL,
    amount        NUMERIC(38,18) NOT NULL,
    block_number  BIGINT NOT NULL,
    block_hash    TEXT NOT NULL DEFAULT '',
    confirmations INT  NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'pending',
    order_id      BIGINT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT deposit_transactions_status_check
        CHECK (status IN ('pending', 'credited', 'ignored', 'reverted'))
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_deposit_tx_chain_hash_log
    ON deposit_transactions (chain, tx_hash, log_index);
CREATE INDEX IF NOT EXISTS idx_deposit_tx_to
    ON deposit_transactions (to_address, chain);
CREATE INDEX IF NOT EXISTS idx_deposit_tx_status
    ON deposit_transactions (status, chain);

-- Sweep records: one row per attempted address sweep.
CREATE TABLE IF NOT EXISTS sweep_records (
    id            BIGSERIAL PRIMARY KEY,
    chain         TEXT NOT NULL,
    from_address  TEXT NOT NULL,
    to_address    TEXT NOT NULL,
    token         TEXT NOT NULL,
    amount        NUMERIC(38,18) NOT NULL,
    gas_tx_hash   TEXT NOT NULL DEFAULT '',
    sweep_tx_hash TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'planned',
    error         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT sweep_records_status_check
        CHECK (status IN ('planned', 'gas_sent', 'swept', 'failed'))
);
CREATE INDEX IF NOT EXISTS idx_sweep_status
    ON sweep_records (status, chain);
CREATE INDEX IF NOT EXISTS idx_sweep_from
    ON sweep_records (from_address, chain);

-- Scan cursors per chain.
CREATE TABLE IF NOT EXISTS crypto_scan_cursors (
    chain          TEXT PRIMARY KEY,
    last_block     BIGINT NOT NULL DEFAULT 0,
    last_signature TEXT NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
