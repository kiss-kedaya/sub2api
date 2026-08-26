-- Keep current-day usage inserts off the single rollup watermark lock.
--
-- Group rollups only publish buckets strictly before the configured current
-- date. A usage row from the current or a future date therefore cannot change
-- the range being published and does not need to serialize with the rollup.
-- Late historical rows still take KEY SHARE so a concurrent publisher either
-- sees them or commits first and lets their trigger invalidate the watermark.
CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    configured_timezone TEXT := current_setting('TimeZone');
    current_date_in_timezone DATE := (CURRENT_TIMESTAMP AT TIME ZONE configured_timezone)::date;
BEGIN
    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM inserted_usage_logs
    WHERE group_id IS NOT NULL;

    IF affected_date IS NULL OR affected_date >= current_date_in_timezone THEN
        RETURN NULL;
    END IF;

    SELECT closed_before
    INTO published_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR KEY SHARE;

    IF published_before > affected_date THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    RETURN NULL;
END;
$$;
