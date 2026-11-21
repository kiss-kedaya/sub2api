-- 一次性回填（分批版）：auth_identities / auth_identity_channels / pending_auth_sessions
-- 的 keyed 盲索引（HMAC-SHA256）与展示前缀。随阶段 B PR 交付，**不自动执行**。
--
-- 与 329_backfill.sql 算法完全一致，区别是每条只处理 ≤2000 行，适合生产按 id 分批、
-- 单条语句 ≤30s 的约束。幂等：只处理 *_hash IS NULL 且明文非空的行。
--
-- 用法（key 为 security_secrets.totp_encryption_key 的 64 位 hex；只经 psql -v 传入，
-- 不写文件、不回显）：
--
--   psql "$DSN" -v ON_ERROR_STOP=1 -v auth_subject_hash_key="$INSTALLATION_KEY_HEX" \
--        -f docs/ops/sql/329_backfill_batch.sql
--
--   # 循环跑，直到三个输出计数全部为 0：
--   while :; do
--     out=$(psql "$DSN" -tA -v ON_ERROR_STOP=1 \
--             -v auth_subject_hash_key="$INSTALLATION_KEY_HEX" \
--             -f docs/ops/sql/329_backfill_batch.sql)
--     echo "$out"
--     [ "$(echo "$out" | awk '{s+=$1} END {print s+0}')" = "0" ] && break
--   done
--
-- 依赖 pgcrypto 的 hmac()；生产不允许 CREATE EXTENSION 时改用
-- backend/cmd/auth-subject-backfill（同样按 id 分批）。

SET statement_timeout = '30s';

-- ── auth_identities（domain=identity）：返回本批更新行数 ────────────────────
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
    ORDER BY id
    LIMIT 2000
), updated AS (
    UPDATE auth_identities AS ai
    SET provider_subject_hash = computed.hash_hex,
        provider_subject_prefix = left(computed.hash_hex, 8)
    FROM computed
    WHERE ai.id = computed.id
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── auth_identity_channels（domain=channel）：返回本批更新行数 ──────────────
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
    ORDER BY id
    LIMIT 2000
), updated AS (
    UPDATE auth_identity_channels AS ac
    SET channel_subject_hash = computed.hash_hex,
        channel_subject_prefix = left(computed.hash_hex, 8)
    FROM computed
    WHERE ac.id = computed.id
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── pending_auth_sessions（domain=identity）：返回本批更新行数 ──────────────
-- 封存值（enc:sub:v1:）不是明文，创建时已有 hash，跳过；旧二进制留下的明文行才回填。
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
      AND provider_subject NOT LIKE 'enc:sub:v1:%'
    ORDER BY id
    LIMIT 2000
), updated AS (
    UPDATE pending_auth_sessions AS ps
    SET provider_subject_hash = computed.hash_hex,
        provider_subject_prefix = left(computed.hash_hex, 8)
    FROM computed
    WHERE ps.id = computed.id
    RETURNING 1
)
SELECT count(*) FROM updated;
