-- Account credentials, proxy passwords and credential-bearing settings are now
-- encrypted at rest with AES-256-GCM. Proxy passwords previously fit
-- VARCHAR(255) only for the managed-proxy ciphertext format; the unified "enc:v1:"
-- format stores base64(nonce || ciphertext || tag) and a long upstream password
-- can exceed that limit. Widen the column to TEXT so no rewrite is blocked by
-- a truncation error.
ALTER TABLE proxies ALTER COLUMN password TYPE TEXT;
