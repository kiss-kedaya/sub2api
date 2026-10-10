-- usage_logs 上原有两个 btree 索引（idx_usage_logs_request_id_api_key_unique / idx_usage_logs_upstream_request_id）
-- 在 '%...%' 前置通配下完全用不上，千万行表的请求 ID 片段搜索只能顺序扫。
-- 这里补 pg_trgm GIN 索引；扩展由 256a_ensure_usage_logs_pg_trgm_extension.sql 先行保证。
-- 非事务执行：CREATE INDEX CONCURRENTLY 不能包在事务里（文件名后缀 *_notx.sql），
-- 因此这里是纯 CREATE INDEX CONCURRENTLY 语句，不带 DO 块。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_request_id_trgm
    ON usage_logs USING gin (request_id gin_trgm_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_upstream_request_id_trgm
    ON usage_logs USING gin (upstream_request_id gin_trgm_ops);
