-- /admin/usage 的请求 ID 搜索是前置通配 ILIKE '%...%'，需要 pg_trgm GIN 索引才能走索引
-- （见 256b_add_usage_logs_request_id_trgm_indexes_notx.sql）。
-- 这里只确保扩展可用，best effort：权限不足或包缺失不阻塞迁移，与 033/065 口径一致。
DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION
        WHEN OTHERS THEN
            RAISE NOTICE 'pg_trgm extension not created: %', SQLERRM;
    END;
END
$$;
