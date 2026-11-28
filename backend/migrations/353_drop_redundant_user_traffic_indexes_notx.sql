-- Drop the three redundant user_traffic_events indexes from 272_user_traffic.sql.
-- Every query against user_traffic_events (details keyset pagination, hourly
-- boundary fragments, MIN(started_at), and 7-day retention pruning) is fully
-- covered by the matching descending cursor indexes added in
-- 273_user_traffic_cursor_indexes_notx.sql:
--   user_traffic_events_user_cursor_idx (user_id, started_at DESC, event_id DESC)
--   user_traffic_events_key_cursor_idx  (api_key_id, started_at DESC, event_id DESC)
--   user_traffic_events_cursor_idx      (started_at DESC, event_id DESC)
-- Non-transactional (*_notx.sql): only pure DROP INDEX CONCURRENTLY IF EXISTS
-- statements are permitted, and timeout control is managed by the migration runner.

DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_user_time_idx;
DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_key_time_idx;
DROP INDEX CONCURRENTLY IF EXISTS user_traffic_events_time_idx;
