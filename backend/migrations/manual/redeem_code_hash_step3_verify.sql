-- ============================================================================
-- 卡密哈希化 · 阶段 B · 步骤 3/5：验证（每一项都必须返回 0 才能进步骤 4）
-- ============================================================================
--
-- 只读。任何一项非 0 都不许执行 step4_null_plaintext.sql。

-- ① 还有没有「有明文但无哈希」的行（回填没跑完 / 中途失败）
SELECT COUNT(*) AS pending_plaintext_without_hash
FROM redeem_codes
WHERE code IS NOT NULL AND code_hash IS NULL;

-- ② 有没有哈希重复（唯一索引理论上挡住，这里兜一层确认没有脏数据绕过）
SELECT COUNT(*) AS duplicate_code_hash_groups
FROM (
    SELECT code_hash
    FROM redeem_codes
    WHERE code_hash IS NOT NULL
    GROUP BY code_hash
    HAVING COUNT(*) > 1
) dup;

-- ③ 主表与台账是否一一对应（少台账行 = 双写漂移）
SELECT COUNT(*) AS missing_ledger_rows
FROM redeem_codes r
LEFT JOIN redeem_code_hashes h ON h.redeem_code_id = r.id
WHERE r.code_hash IS NOT NULL AND h.redeem_code_id IS NULL;

-- ④ 主表与台账的哈希/前缀是否一致
SELECT COUNT(*) AS mismatched_ledger_rows
FROM redeem_codes r
JOIN redeem_code_hashes h ON h.redeem_code_id = r.id
WHERE r.code_hash IS DISTINCT FROM h.code_hash
   OR r.code_prefix IS DISTINCT FROM h.code_prefix;

-- ⑤ 抽样自检：明文仍在的行，哈希必须等于 sha256(明文)，前缀规则必须一致
SELECT COUNT(*) AS wrong_hash_samples
FROM redeem_codes
WHERE code IS NOT NULL
  AND code_hash IS DISTINCT FROM encode(sha256(convert_to(code, 'UTF8')), 'hex');

SELECT COUNT(*) AS wrong_prefix_samples
FROM redeem_codes
WHERE code IS NOT NULL
  AND code_prefix IS DISTINCT FROM (CASE WHEN length(code) >= 8 THEN left(code, 8) ELSE '' END);

-- ⑥ 记录回填完成时的规模，供步骤 4 之后对比
INSERT INTO redeem_code_hash_backfill_audit (step, row_count, recorded_at)
VALUES ('verified_rows_with_hash',
        (SELECT COUNT(*) FROM redeem_codes WHERE code_hash IS NOT NULL),
        NOW())
ON CONFLICT (step) DO UPDATE
SET row_count = EXCLUDED.row_count, recorded_at = EXCLUDED.recorded_at;
