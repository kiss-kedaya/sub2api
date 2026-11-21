-- ============================================================================
-- 卡密哈希化 · 阶段 B · 步骤 2/5：分批回填 code_hash / code_prefix + 哈希台账
-- ============================================================================
--
-- 幂等：只处理 code_hash IS NULL AND code IS NOT NULL 的行，可重复执行。
-- 分批：单批 500 行，单条语句 ≤30s。重复执行本语句直到返回 UPDATE 0，
--       或者直接用同目录 step2_backfill_loop.sh 驱动循环。
--
-- 前缀规则必须和 Go 侧 RedeemCodePrefixFromPlaintext 一致：
--   * 明文长度 >= 8 → left(code, 8)
--   * 明文长度 <  8 → 空串（短自定义卡密不能把整段卡密当前缀暴露）
--
-- 哈希规则与 Go 侧 HashRedeemCode 一致：
--   sha256(UTF8(code)) 的小写 hex —— 即 encode(sha256(convert_to(code,'UTF8')),'hex')。

WITH batch AS (
    SELECT id
    FROM redeem_codes
    WHERE code_hash IS NULL
      AND code IS NOT NULL
    ORDER BY id
    LIMIT 500
), updated AS (
    UPDATE redeem_codes r
    SET code_hash = encode(sha256(convert_to(r.code, 'UTF8')), 'hex'),
        code_prefix = CASE WHEN length(r.code) >= 8 THEN left(r.code, 8) ELSE '' END
    FROM batch b
    WHERE r.id = b.id
    RETURNING r.id, r.code_hash, r.code_prefix
)
-- 台账与主表同事务写入（单条语句 = 单事务），避免「快路径命中、台账没有」的漂移。
INSERT INTO redeem_code_hashes (redeem_code_id, code_hash, code_prefix, created_at, updated_at)
SELECT id, code_hash, code_prefix, NOW(), NOW()
FROM updated
ON CONFLICT (redeem_code_id) DO UPDATE
SET code_hash = EXCLUDED.code_hash,
    code_prefix = EXCLUDED.code_prefix,
    updated_at = EXCLUDED.updated_at;

-- 观察回填进度（可单独跑）：
--   SELECT COUNT(*) FILTER (WHERE code_hash IS NULL AND code IS NOT NULL) AS pending,
--          COUNT(*) FILTER (WHERE code_hash IS NOT NULL)                 AS backfilled
--   FROM redeem_codes;
