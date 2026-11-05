-- 309_upstream_monitor_usage.sql
-- Third batch for the operations-center upstream monitor: plan, quota and
-- subscription state. The probe already parses GET /v1/usage into
-- accounts.extra.upstream_billing_probe.balance; this migration only adds the
-- two alert thresholds those numbers need. No upstream request, no gateway,
-- billing or credential change.

ALTER TABLE upstream_monitor_alert_rules
    ADD COLUMN IF NOT EXISTS usage_min_pct NUMERIC(10, 2) NOT NULL DEFAULT 20;

ALTER TABLE upstream_monitor_alert_rules
    ADD COLUMN IF NOT EXISTS expire_days INTEGER NOT NULL DEFAULT 7;
