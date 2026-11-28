SET LOCAL lock_timeout = '5s';

-- Convert the usage_logs AFTER DELETE rollup invalidation trigger from per-row
-- to statement-level using REFERENCING OLD TABLE AS deleted_usage_logs (same
-- pattern as migration 316's AFTER INSERT trigger). A 10,000-row retention
-- delete batch previously locked usage_group_rollup_state FOR UPDATE 10,000
-- times per statement; it now locks at most once per DELETE statement while
-- preserving session TimeZone handling and the retention trim guard.

CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_delete()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    retained_before DATE;
    configured_timezone TEXT := current_setting('TimeZone');
BEGIN
    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM deleted_usage_logs
    WHERE group_id IS NOT NULL;

    IF affected_date IS NULL THEN
        RETURN NULL;
    END IF;

    -- Serialize once per DELETE statement with rollup publication before
    -- evaluating closed_before and retained_from.
    SELECT closed_before, retained_from::date
    INTO published_before, retained_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR UPDATE;

    -- An expired row must not hide a retained historical row in the same batch.
    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM deleted_usage_logs
    WHERE group_id IS NOT NULL
      AND (created_at AT TIME ZONE configured_timezone)::date > retained_before;

    IF affected_date IS NOT NULL AND published_before > affected_date AND affected_date > retained_before THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS usage_logs_group_rollup_invalidate_delete ON usage_logs;
CREATE TRIGGER usage_logs_group_rollup_invalidate_delete
AFTER DELETE ON usage_logs
REFERENCING OLD TABLE AS deleted_usage_logs
FOR EACH STATEMENT
EXECUTE FUNCTION invalidate_group_usage_rollup_state_after_delete();
