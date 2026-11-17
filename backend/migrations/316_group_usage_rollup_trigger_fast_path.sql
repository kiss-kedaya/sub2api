-- Migration 315 dropped the configured timezone and current-day insert bypass.
-- Restore them, replacing the manually installed lock-free insert function:
-- its unlocked read could miss a late row while a publisher closed that day.
-- Today's bucket is still open; the publisher re-aggregates it when closing it,
-- so inserts committed before that day's closing snapshot need no state lock.
-- ponytail: the bypass does not cover commits spanning midnight; durable
-- invalidations and publisher/reader changes are needed for that guarantee.
-- Keep the retention guard so trimming old rows cannot force a full rebuild.

CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    retained_before DATE;
    configured_timezone TEXT := current_setting('TimeZone');
BEGIN
    IF TG_OP = 'DELETE' THEN
        affected_date := (OLD.created_at AT TIME ZONE configured_timezone)::date;
    ELSE
        IF OLD.group_id IS NULL THEN
            affected_date := (NEW.created_at AT TIME ZONE configured_timezone)::date;
        ELSIF NEW.group_id IS NULL THEN
            affected_date := (OLD.created_at AT TIME ZONE configured_timezone)::date;
        ELSE
            affected_date := LEAST(
                (OLD.created_at AT TIME ZONE configured_timezone)::date,
                (NEW.created_at AT TIME ZONE configured_timezone)::date
            );
        END IF;
    END IF;

    IF affected_date IS NULL THEN
        IF TG_OP = 'DELETE' THEN
            RETURN OLD;
        END IF;
        RETURN NEW;
    END IF;

    SELECT closed_before, retained_from::date
    INTO published_before, retained_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR UPDATE;

    IF published_before > affected_date AND affected_date > retained_before THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_date DATE;
    published_before DATE;
    retained_before DATE;
    configured_timezone TEXT := current_setting('TimeZone');
    current_date_in_timezone DATE := (clock_timestamp() AT TIME ZONE configured_timezone)::date;
BEGIN
    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM inserted_usage_logs
    WHERE group_id IS NOT NULL;

    IF affected_date IS NULL OR affected_date >= current_date_in_timezone THEN
        RETURN NULL;
    END IF;

    -- Synchronize historical inserts with publication before testing the state.
    SELECT closed_before, retained_from::date
    INTO published_before, retained_before
    FROM usage_group_rollup_state
    WHERE id = 1
    FOR KEY SHARE;

    -- An expired row must not hide a retained historical row in the same batch.
    SELECT MIN((created_at AT TIME ZONE configured_timezone)::date)
    INTO affected_date
    FROM inserted_usage_logs
    WHERE group_id IS NOT NULL
      AND (created_at AT TIME ZONE configured_timezone)::date > retained_before;

    IF published_before > affected_date AND affected_date > retained_before THEN
        UPDATE usage_group_rollup_state
        SET closed_before = LEAST(closed_before, affected_date),
            updated_at = NOW()
        WHERE id = 1;
    END IF;

    RETURN NULL;
END;
$$;
