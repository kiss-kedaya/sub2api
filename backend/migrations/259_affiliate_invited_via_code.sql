-- Record which affiliate code an invitee actually used.
-- Historical rows are backfilled with the inviter default code at migration time.
-- Later default-code changes do not rewrite these rows, so per-link stats stay stable.
ALTER TABLE user_affiliates
    ADD COLUMN IF NOT EXISTS invited_via_code VARCHAR(32);

UPDATE user_affiliates AS invitee
SET invited_via_code = inviter.aff_code
FROM user_affiliates AS inviter
WHERE invitee.inviter_id = inviter.user_id
  AND invitee.invited_via_code IS NULL
  AND invitee.inviter_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_user_affiliates_inviter_via_code
    ON user_affiliates (inviter_id, invited_via_code)
    WHERE inviter_id IS NOT NULL;

COMMENT ON COLUMN user_affiliates.invited_via_code IS 'affiliate code used when this user was invited';
