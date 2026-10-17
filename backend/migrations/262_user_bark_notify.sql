-- Bark push notification settings. The device key is stored encrypted by the
-- application layer (SecretEncryptor); this column only holds the ciphertext.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS bark_device_key_encrypted TEXT NOT NULL DEFAULT '';

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS bark_notify_enabled BOOLEAN NOT NULL DEFAULT FALSE;
