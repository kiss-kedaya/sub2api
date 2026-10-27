-- Existing and newly-created keys keep automatic DeepSeek tools enabled.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS deepseek_tool_injection_enabled BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN api_keys.deepseek_tool_injection_enabled IS
    'Enable automatic DeepSeek search/fetch tool injection for this API key';

-- Keep the existing transactionally-enqueued invalidation conditions and add
-- the tool switch so a missed immediate pub/sub notification is recoverable.
CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        RETURN OLD;
    END IF;

    IF OLD.key IS DISTINCT FROM NEW.key
       OR OLD.status IS DISTINCT FROM NEW.status
       OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
       OR OLD.user_id IS DISTINCT FROM NEW.user_id
       OR OLD.group_id IS DISTINCT FROM NEW.group_id
       OR OLD.ip_whitelist IS DISTINCT FROM NEW.ip_whitelist
       OR OLD.ip_blacklist IS DISTINCT FROM NEW.ip_blacklist
       OR OLD.expires_at IS DISTINCT FROM NEW.expires_at
       OR OLD.deepseek_tool_injection_enabled IS DISTINCT FROM NEW.deepseek_tool_injection_enabled THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        IF NEW.deleted_at IS NULL AND NEW.key IS DISTINCT FROM OLD.key THEN
            PERFORM enqueue_auth_cache_invalidation(NEW.key);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
