-- Append-only record of every change to groups.rate_multiplier. It only feeds
-- the "rate changed" and "new group" hints on the user-facing channel status
-- and API key pages; billing and request routing never read it.
--
-- Rows are written by a row trigger on groups, so every writer (admin save,
-- bulk statement, manual SQL) is captured in the same transaction as the
-- change itself, and a rolled-back edit leaves no row behind. Groups that
-- existed before this migration have no rows until their next rate change.
--
-- There is deliberately no foreign key: history must never block a group edit
-- or delete, and group ids are never reused.
CREATE TABLE IF NOT EXISTS group_rate_history (
    id         BIGSERIAL PRIMARY KEY,
    group_id   BIGINT NOT NULL,
    old_rate   DECIMAL(10, 4) NOT NULL,
    new_rate   DECIMAL(10, 4) NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_group_rate_history_group_changed
    ON group_rate_history (group_id, changed_at DESC);

CREATE OR REPLACE FUNCTION record_group_rate_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.rate_multiplier IS DISTINCT FROM OLD.rate_multiplier THEN
        INSERT INTO group_rate_history (group_id, old_rate, new_rate)
        VALUES (NEW.id, OLD.rate_multiplier, NEW.rate_multiplier);
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_rate_history ON groups;
CREATE TRIGGER trg_groups_rate_history
AFTER UPDATE OF rate_multiplier ON groups
FOR EACH ROW
WHEN (OLD.rate_multiplier IS DISTINCT FROM NEW.rate_multiplier)
EXECUTE FUNCTION record_group_rate_history();
