-- Immutable transition history and a separately durable threshold baseline.
CREATE TABLE account_ops_threshold_monitors (
 account_id BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('balance_threshold','quota_threshold')),
 criteria TEXT NOT NULL DEFAULT '',
 identity TEXT NOT NULL DEFAULT '',
 episode_id TEXT NOT NULL DEFAULT '',
 adverse BOOLEAN NOT NULL DEFAULT FALSE,
 eligible BOOLEAN NOT NULL DEFAULT FALSE,
 healthy_since TIMESTAMPTZ,
 checked_at TIMESTAMPTZ,
 PRIMARY KEY(account_id,kind)
);
CREATE TABLE account_ops_threshold_events (
 id TEXT PRIMARY KEY,
 episode_id TEXT NOT NULL,
 phase TEXT NOT NULL CHECK(phase IN ('alert','recovery')),
 notification_enabled BOOLEAN NOT NULL,
 account_id BIGINT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('balance_threshold','quota_threshold')),
 account_name TEXT NOT NULL DEFAULT '',
 signal TEXT NOT NULL DEFAULT '',
 http_status INTEGER NOT NULL DEFAULT 0,
 first_seen TIMESTAMPTZ NOT NULL,
 last_seen TIMESTAMPTZ NOT NULL,
 occurrences BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL CHECK(state IN ('pending','sending','sent','failed','suppressed','resolved')),
 last_sent_at TIMESTAMPTZ,
 next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0,
 lease TEXT NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 details JSONB NOT NULL DEFAULT 'null'::jsonb,
 deliveries JSONB NOT NULL DEFAULT '{}'::jsonb,
 identity TEXT NOT NULL,
 criteria TEXT NOT NULL,
 UNIQUE(episode_id,phase)
);
CREATE INDEX account_ops_threshold_events_due ON account_ops_threshold_events(next_send_at) WHERE state IN ('pending','failed','sending');
CREATE INDEX account_ops_threshold_events_history ON account_ops_threshold_events(last_seen DESC,id);
-- Every old adverse row starts an existing episode, including partially delivered
-- warnings. First fresh scan adopts its criteria; no old warning is replayed.
INSERT INTO account_ops_threshold_monitors(account_id,kind,identity,episode_id,adverse)
SELECT account_id,kind,identity,'legacy:'||account_id||':'||kind,state<>'resolved'
FROM account_ops_alerts WHERE kind IN ('balance_threshold','quota_threshold');
-- Transfer incomplete delivery work exactly once, keeping receipt and retry
-- budgets. Blank criteria prevent claims until the first valid scan adopts the
-- live rule. Stable IDs make the transferred episode/event traceable to history.
INSERT INTO account_ops_threshold_events(
 id,episode_id,phase,notification_enabled,account_id,kind,account_name,signal,http_status,
 first_seen,last_seen,occurrences,state,last_sent_at,next_send_at,attempts,details,deliveries,identity,criteria)
SELECT 'legacy-transition:'||account_id||':'||kind,'legacy:'||account_id||':'||kind,
 'alert',TRUE,account_id,kind,account_name,signal,http_status,
 first_seen,last_seen,occurrences,CASE WHEN state='sending' THEN 'pending' ELSE state END,
 last_sent_at,next_send_at,attempts,details,deliveries,identity,''
FROM account_ops_alerts WHERE kind IN ('balance_threshold','quota_threshold')
 AND state IN ('pending','sending','failed')
 -- Old periodic rearming retained last_sent_at but cleared receipts. Such rows
 -- cannot be distinguished from a later unsent cycle; conservatively avoid
 -- replaying a warning already delivered under the legacy storage model.
 AND (last_sent_at IS NULL OR deliveries<>'{}'::jsonb);
-- Keep the original history/receipts. Cancel only its obsolete periodic queue.
UPDATE account_ops_alerts SET state='suppressed',lease='',lease_until=NULL
WHERE kind IN ('balance_threshold','quota_threshold') AND state IN ('pending','sending','failed');
