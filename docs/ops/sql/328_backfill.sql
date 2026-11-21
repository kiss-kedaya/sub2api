-- 一次性回填：auth_identities / auth_identity_channels / pending_auth_sessions 的
-- keyed 盲索引（HMAC-SHA256）与展示前缀。随 PR 交付，**不自动执行**。
--
-- 用法（key 为 security_secrets.totp_encryption_key 的 64 位 hex，不写进本文件）：
--   CREATE EXTENSION IF NOT EXISTS pgcrypto;
--   psql "$DSN" -v auth_subject_hash_key="$INSTALLATION_KEY_HEX" \
--        -f docs/ops/sql/328_backfill.sql
--
-- 算法必须与 Go 侧 authsubject.HashIdentity / HashChannel 完全一致：
--   HMAC-SHA256(key, domain || 0x00 || lower(trim(provider_type)) || 0x00 || trim(subject))
-- PostgreSQL text 不能存 NUL，所以用 bytea 拼接 decode('00','hex')。
-- 展示前缀取自 hash 前 8 个 hex 字符（不是 subject 原文）。
-- 幂等：只处理 *_hash IS NULL 的行，可重复执行。

-- ── auth_identities（domain=identity）──────────────────────────────────────
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('identity', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(provider_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM auth_identities
    WHERE provider_subject_hash IS NULL
      AND provider_subject IS NOT NULL
)
UPDATE auth_identities AS ai
SET provider_subject_hash = computed.hash_hex,
    provider_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ai.id = computed.id;

-- ── auth_identity_channels（domain=channel）────────────────────────────────
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('channel', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(channel_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM auth_identity_channels
    WHERE channel_subject_hash IS NULL
      AND channel_subject IS NOT NULL
)
UPDATE auth_identity_channels AS ac
SET channel_subject_hash = computed.hash_hex,
    channel_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ac.id = computed.id;

-- ── pending_auth_sessions（domain=identity）────────────────────────────────
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('identity', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(provider_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM pending_auth_sessions
    WHERE provider_subject_hash IS NULL
      AND provider_subject IS NOT NULL
)
UPDATE pending_auth_sessions AS ps
SET provider_subject_hash = computed.hash_hex,
    provider_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ps.id = computed.id;
