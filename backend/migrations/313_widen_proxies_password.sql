-- proxies.password must hold managed-proxy ciphertext, not just plaintext.
-- The managed import flow stores "enc:managed:v1:" + base64(AES-256-GCM), so a
-- generated 32-byte (64-character hex) password becomes 139 characters at rest
-- and was rejected by the previous VARCHAR(100) limit.
ALTER TABLE proxies ALTER COLUMN password TYPE VARCHAR(255);
