-- 仅触碰 groups 一张热表；控制锁等待时间，避免与看板或网关并发查询死锁。
SET LOCAL lock_timeout = '5s';

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS daily_reset_mode VARCHAR(20) NOT NULL DEFAULT 'calendar';

COMMENT ON COLUMN groups.daily_reset_mode IS
    'Subscription daily quota reset mode: calendar (midnight in configured timezone) or anchored_24h (fixed 24h cadence anchored at subscription starts_at)';

-- 与迁移 346 的版本保持一致，补充 daily_reset_mode 变更时的认证缓存失效监听。
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
       AND OLD.daily_reset_mode IS NOT DISTINCT FROM NEW.daily_reset_mode
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.return_requested_model IS NOT DISTINCT FROM NEW.return_requested_model
       AND OLD.save_response_msg_id IS NOT DISTINCT FROM NEW.save_response_msg_id
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
