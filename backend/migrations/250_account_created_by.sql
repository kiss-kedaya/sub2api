-- Admins only see accounts they uploaded. NULL created_by means a legacy
-- account from before this column existed and stays visible to every admin.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS created_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_accounts_created_by
    ON accounts (created_by)
    WHERE deleted_at IS NULL AND created_by IS NOT NULL;