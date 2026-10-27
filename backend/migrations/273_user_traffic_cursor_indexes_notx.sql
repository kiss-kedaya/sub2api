-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
-- Match descending keyset pagination, including tied start timestamps.
-- Retain existing indexes for older binaries. Remove redundant ones only after
-- deployment-specific EXPLAIN/usage measurements and the compatibility window.
CREATE INDEX CONCURRENTLY IF NOT EXISTS user_traffic_events_user_cursor_idx
    ON user_traffic_events (user_id, started_at DESC, event_id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS user_traffic_events_key_cursor_idx
    ON user_traffic_events (api_key_id, started_at DESC, event_id DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS user_traffic_events_cursor_idx
    ON user_traffic_events (started_at DESC, event_id DESC);
