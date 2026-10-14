-- Deposit ledger: record why a transfer was not credited (below minimum,
-- above the per-deposit maximum, or an unassigned address) so the depositor
-- and the operator see the cause instead of a bare "ignored" badge.
ALTER TABLE deposit_transactions ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
