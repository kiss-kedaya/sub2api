-- Migration: 228_add_balance_notify_primary_email_disabled
-- Persist only the disabled state; the primary recipient is derived from users.email.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS balance_notify_primary_email_disabled BOOLEAN NOT NULL DEFAULT FALSE;
