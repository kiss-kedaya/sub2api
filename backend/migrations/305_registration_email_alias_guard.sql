-- Preserve existing registrations and alias deduplication; syntax rejection is opt-in.
INSERT INTO settings(key,value) VALUES ('registration_email_alias_block_enabled','false') ON CONFLICT(key) DO NOTHING;
