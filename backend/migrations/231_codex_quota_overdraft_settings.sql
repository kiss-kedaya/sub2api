-- Persist the guarded Codex quota-overdraft defaults for every installation.
--
-- The administrator settings are read from the settings table at runtime.  A
-- number of older deployments already have rows in this table, so this must
-- be additive: never replace an explicit operator choice during an upgrade.
INSERT INTO settings (key, value)
VALUES
    ('codex_quota_overdraft_enabled', 'true'),
    ('codex_quota_overdraft_business_injection_enabled', 'false')
ON CONFLICT (key) DO NOTHING;
