-- 非事务执行（*_notx.sql）：仅含纯 CREATE INDEX CONCURRENTLY IF NOT EXISTS，不阻塞热表写入。
-- 部分索引（WHERE col IS NOT NULL）跳过历史 NULL 行，降低建索引耗时与磁盘占用；
-- btree 支持精确（=）查询，pg_trgm GIN 支持片段/前缀（ILIKE）查询（扩展由 256a 保障）。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_request_id
    ON usage_logs (response_request_id)
    WHERE response_request_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_client_request_id
    ON usage_logs (client_request_id)
    WHERE client_request_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_msg_id
    ON usage_logs (response_msg_id)
    WHERE response_msg_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_request_id_trgm
    ON usage_logs USING gin (response_request_id gin_trgm_ops)
    WHERE response_request_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_client_request_id_trgm
    ON usage_logs USING gin (client_request_id gin_trgm_ops)
    WHERE client_request_id IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_response_msg_id_trgm
    ON usage_logs USING gin (response_msg_id gin_trgm_ops)
    WHERE response_msg_id IS NOT NULL;
