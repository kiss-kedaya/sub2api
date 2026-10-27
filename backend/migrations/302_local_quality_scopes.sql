ALTER TABLE local_reasoning_plans ADD COLUMN pause_scope TEXT NOT NULL DEFAULT 'account' CHECK(pause_scope IN ('account','model','group'));
ALTER TABLE local_reasoning_plans ADD COLUMN target_group_id BIGINT;
ALTER TABLE local_reasoning_owners ADD COLUMN pause_scope TEXT NOT NULL DEFAULT 'account';
ALTER TABLE local_reasoning_owners ADD COLUMN binding_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE local_quality_group_versions(account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,revision BIGINT NOT NULL DEFAULT 0);
CREATE FUNCTION local_quality_group_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM accounts WHERE id=COALESCE(NEW.account_id,OLD.account_id)) THEN
  INSERT INTO local_quality_group_versions(account_id,revision) VALUES(COALESCE(NEW.account_id,OLD.account_id),1)
  ON CONFLICT(account_id) DO UPDATE SET revision=local_quality_group_versions.revision+1;
 END IF;
 RETURN COALESCE(NEW,OLD);
END $$;
CREATE TRIGGER local_quality_group_revision_change AFTER INSERT OR UPDATE OR DELETE ON account_groups FOR EACH ROW EXECUTE FUNCTION local_quality_group_revision();
CREATE TABLE local_quality_group_owners(
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
 token TEXT NOT NULL,binding_priority INTEGER NOT NULL,binding_created_at TIMESTAMPTZ NOT NULL,
 binding_revision BIGINT NOT NULL,account_updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(account_id,group_id)
);
