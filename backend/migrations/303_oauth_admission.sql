CREATE TABLE oauth_admission_leases(
 account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 token TEXT NOT NULL,lease TEXT NOT NULL DEFAULT '',lease_until TIMESTAMPTZ,next_run_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_outcome TEXT NOT NULL DEFAULT '',account_updated_at TIMESTAMPTZ NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_oauth_admission_due ON oauth_admission_leases(next_run_at);
CREATE FUNCTION oauth_admission_track() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' AND NEW.type='oauth' AND COALESCE(NEW.extra->'oauth_admission_pause'->>'token','')<>'' THEN
  INSERT INTO oauth_admission_leases(account_id,token,account_updated_at) VALUES(NEW.id,NEW.extra->'oauth_admission_pause'->>'token',NEW.updated_at);
 ELSIF TG_OP='UPDATE' AND NEW.extra->'oauth_admission_pause' IS NOT DISTINCT FROM OLD.extra->'oauth_admission_pause'
  AND to_jsonb(NEW)-'extra'-'updated_at' = to_jsonb(OLD)-'extra'-'updated_at' THEN
  UPDATE oauth_admission_leases SET account_updated_at=NEW.updated_at WHERE account_id=NEW.id AND account_updated_at=OLD.updated_at AND lease='';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER oauth_admission_new_account AFTER INSERT OR UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION oauth_admission_track();
