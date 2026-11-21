-- ============================================================================
-- 卡密哈希化 · 阶段 B · 步骤 4/5：清空明文列（不可逆，务必在步骤 3 全 0 之后）
-- ============================================================================
--
-- 关键约束：
--   * 必须写 NULL，不能写空串：redeem_codes.code 上还留着原来的唯一约束，
--     多行空串会直接撞唯一索引（duplicate key），整条语句失败；
--   * PostgreSQL 的唯一索引对 NULL 不生效，所以任意多行 code = NULL 都合法；
--   * 本步骤之后，任何读路径都拿不回明文，只剩 sha256 哈希与前缀。
--
-- 分批执行（每批 5000 行，单条 ≤30s），重复跑到 UPDATE 0：
--   UPDATE redeem_codes SET code = NULL
--   WHERE id IN (SELECT id FROM redeem_codes WHERE code IS NOT NULL ORDER BY id LIMIT 5000);

WITH batch AS (
    SELECT id
    FROM redeem_codes
    WHERE code IS NOT NULL
    ORDER BY id
    LIMIT 5000
)
UPDATE redeem_codes r
SET code = NULL
FROM batch b
WHERE r.id = b.id
  AND r.code_hash IS NOT NULL;  -- 双保险：绝不把「有明文但还没哈希」的行抹掉

-- 收尾验证：以下三项必须是 0 / 0 / 不等于 0 才说明清干净了。
--   SELECT COUNT(*) FROM redeem_codes WHERE code IS NOT NULL;          -- 必须 0
--   SELECT COUNT(*) FROM redeem_codes WHERE code_hash IS NULL;         -- 必须 0
--   SELECT COUNT(*) FROM redeem_code_hashes;                           -- 应等于卡密总数
INSERT INTO redeem_code_hash_backfill_audit (step, row_count, recorded_at)
VALUES ('rows_still_holding_plaintext_after_null',
        (SELECT COUNT(*) FROM redeem_codes WHERE code IS NOT NULL),
        NOW())
ON CONFLICT (step) DO UPDATE
SET row_count = EXCLUDED.row_count, recorded_at = EXCLUDED.recorded_at;
