-- API Key 凭据改为哈希存储（阶段 A：只加结构，不动存量数据）。
--
-- 背景：#464 之后明文 key 只在「创建 / 轮换」的当次响应里出现一次，但库里
-- 仍然整行保存明文。本迁移给新 key 只落 sha256 哈希留出结构：
--   * api_keys.key_hash   —— 鉴权快路径的索引列（sha256 hex，64 字符）
--   * api_keys.key_prefix —— 展示用前缀（前 8 位），避免为了显示再读明文
--   * api_key_hashes      —— 独立哈希表（api_key_id 一对一主键），轮换台账 /
--                            审计与未来「只留新表」的落点
--
-- 本迁移之外还有阶段 B（生产侧人工执行，见 PR 说明）：
--   1. 分批回填 api_keys.key_hash / key_prefix 与 api_key_hashes；
--   2. 校验无明文残留后 UPDATE api_keys SET key = NULL；
--   3. 下一版再 DROP COLUMN key。
--
-- key 列改为可空：新行写 NULL，绝不再写明文。原唯一约束对 NULL 不生效，
-- 因此不需要重建索引，明文回退查询（过渡期）仍走原来的唯一索引。
ALTER TABLE api_keys ALTER COLUMN key DROP NOT NULL;

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash CHAR(64);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix VARCHAR(16);

-- 部分唯一索引：只约束已回填 / 已写入哈希的行，历史明文行在回填前不参与。
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_key_hash
    ON api_keys (key_hash)
    WHERE key_hash IS NOT NULL;

CREATE TABLE IF NOT EXISTS api_key_hashes (
    api_key_id BIGINT PRIMARY KEY REFERENCES api_keys(id) ON DELETE CASCADE,
    key_hash   CHAR(64) NOT NULL UNIQUE,
    key_prefix VARCHAR(16) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 认证缓存失效触发器改为能用哈希定位：cache_key 本来就是 sha256(明文)，
-- 而新行的明文列是 NULL，旧触发器 OLD.key/NEW.key 取到 NULL 就不会入队，
-- 跨实例的 durable invalidation 会整体失效（改状态、轮换、删除都受影响）。
CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation_for(raw_hash TEXT, raw_key TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    cache_key_value TEXT;
BEGIN
    IF raw_hash IS NOT NULL AND raw_hash <> '' THEN
        cache_key_value := lower(raw_hash);
    ELSIF raw_key IS NOT NULL AND raw_key <> '' THEN
        cache_key_value := encode(sha256(convert_to(raw_key, 'UTF8')), 'hex');
    ELSE
        RETURN;
    END IF;
    INSERT INTO auth_cache_invalidation_outbox (cache_key) VALUES (cache_key_value);
END;
$$;

-- 与迁移 269 的版本保持一致，另外把「哈希变化」也纳入触发条件。
CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_api_key_auth_cache_invalidation_for(OLD.key_hash, OLD.key);
        RETURN OLD;
    END IF;

    IF OLD.key IS DISTINCT FROM NEW.key
       OR OLD.key_hash IS DISTINCT FROM NEW.key_hash
       OR OLD.status IS DISTINCT FROM NEW.status
       OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
       OR OLD.user_id IS DISTINCT FROM NEW.user_id
       OR OLD.group_id IS DISTINCT FROM NEW.group_id
       OR OLD.ip_whitelist IS DISTINCT FROM NEW.ip_whitelist
       OR OLD.ip_blacklist IS DISTINCT FROM NEW.ip_blacklist
       OR OLD.expires_at IS DISTINCT FROM NEW.expires_at
       OR OLD.deepseek_tool_injection_enabled IS DISTINCT FROM NEW.deepseek_tool_injection_enabled THEN
        PERFORM enqueue_api_key_auth_cache_invalidation_for(OLD.key_hash, OLD.key);
        IF NEW.deleted_at IS NULL
           AND (NEW.key_hash IS DISTINCT FROM OLD.key_hash OR NEW.key IS DISTINCT FROM OLD.key) THEN
            PERFORM enqueue_api_key_auth_cache_invalidation_for(NEW.key_hash, NEW.key);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

-- 用户 / 分组 / 白名单三处触发器都按「用户/分组下的 key」批量入队，
-- 取缓存键的地方统一改成 COALESCE(key_hash, sha256(明文))。
CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
BEGIN
    target_user_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT COALESCE(NULLIF(k.key_hash, ''), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'))
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND (k.key_hash IS NOT NULL OR k.key <> '');
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

-- 与迁移 268 的版本保持一致，仅改批量取缓存键的表达式。
CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.return_requested_model IS NOT DISTINCT FROM NEW.return_requested_model
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT COALESCE(NULLIF(k.key_hash, ''), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'))
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND (k.key_hash IS NOT NULL OR k.key <> '');
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_allowed_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
    target_group_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.user_id IS DISTINCT FROM NEW.user_id
            OR OLD.group_id IS DISTINCT FROM NEW.group_id) THEN
        IF EXISTS (
            SELECT 1 FROM groups g
            WHERE g.id = OLD.group_id AND g.is_exclusive = TRUE
        ) THEN
            INSERT INTO auth_cache_invalidation_outbox (cache_key)
            SELECT COALESCE(NULLIF(k.key_hash, ''), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'))
            FROM api_keys AS k
            WHERE k.user_id = OLD.user_id
              AND k.group_id = OLD.group_id
              AND k.deleted_at IS NULL
              AND (k.key_hash IS NOT NULL OR k.key <> '');
        END IF;
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSIF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    ELSIF TG_OP = 'INSERT' THEN
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSE
        target_user_id := OLD.user_id;
        target_group_id := OLD.group_id;
    END IF;

    IF EXISTS (
        SELECT 1 FROM groups g
        WHERE g.id = target_group_id AND g.is_exclusive = TRUE
    ) THEN
        INSERT INTO auth_cache_invalidation_outbox (cache_key)
        SELECT COALESCE(NULLIF(k.key_hash, ''), encode(sha256(convert_to(k.key, 'UTF8')), 'hex'))
        FROM api_keys AS k
        WHERE k.user_id = target_user_id
          AND k.group_id = target_group_id
          AND k.deleted_at IS NULL
          AND (k.key_hash IS NOT NULL OR k.key <> '');
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
