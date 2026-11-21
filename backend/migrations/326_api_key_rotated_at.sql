-- Only successful in-place rotations write this timestamp. Do not infer
-- historical rotations from updated_at, which also tracks ordinary edits.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_rotated_at TIMESTAMPTZ;
