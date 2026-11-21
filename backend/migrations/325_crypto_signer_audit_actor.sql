-- Operator attribution for the signer audit trail. High-risk operations
-- (mnemonic / private-key export, seed import) must be traceable to an
-- authenticated admin and to the originating request, not just to the op.
--
-- Every column is nullable with no default and no backfill: pre-existing rows
-- stay NULL, and PostgreSQL applies this as a metadata-only change per column
-- (no table rewrite, no long lock). IF NOT EXISTS keeps a re-run harmless.
ALTER TABLE crypto_signer_audit
    ADD COLUMN IF NOT EXISTS actor_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS actor_email TEXT,
    ADD COLUMN IF NOT EXISTS client_ip TEXT,
    ADD COLUMN IF NOT EXISTS user_agent TEXT,
    ADD COLUMN IF NOT EXISTS request_id TEXT,
    ADD COLUMN IF NOT EXISTS auth_method TEXT;
