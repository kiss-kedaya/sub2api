-- ============================================================================
-- 卡密哈希化 · 阶段 B · 步骤 1/5：备份明文（**生产手工执行，不在迁移里自动跑**）
-- ============================================================================
--
-- 为什么单独放 backend/migrations/manual/：
--   backend/migrations 目录里的 *.sql 会被 go:embed 打进二进制，并在
--   DATABASE_MIGRATION_MODE=apply 时自动执行。回填 / 清空明文属于不可逆的数据
--   手术，必须由人工在备份与校验之后执行，绝不能随发版自动跑。
--
-- 执行方式（单条 ≤30s，逐条跑，不要一次性 -f 整个文件）：
--   scp backend/migrations/manual/*.sql kedaya-old:/tmp/
--   ssh kedaya-old "docker cp /tmp/redeem_code_hash_step1_backup.sql kedaya-sub2-postgres-1:/tmp/"
--   ssh kedaya-old "docker exec kedaya-sub2-postgres-1 psql -U sub2api -d sub2api -f /tmp/redeem_code_hash_step1_backup.sql"
--
-- 回滚入口：步骤 5 用这张备份表把 code 明文还原。
-- 备份表含明文卡密：只允许库机本地存在，校验窗口（建议 7 天）过后必须 DROP。

BEGIN;

-- 1) 全量明文备份（含已使用行：回滚需要完整原件）。
CREATE TABLE IF NOT EXISTS redeem_codes_plaintext_backup (
    id          BIGINT PRIMARY KEY,
    code        VARCHAR(32) NOT NULL,
    code_hash   CHAR(64),
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO redeem_codes_plaintext_backup (id, code, code_hash, backed_up_at)
SELECT id, code, NULL, NOW()
FROM redeem_codes
WHERE code IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM redeem_codes_plaintext_backup b WHERE b.id = redeem_codes.id);

-- 2) 记录基线规模，供后续步骤对比（写进一张一次性审计表，便于事后追账）。
CREATE TABLE IF NOT EXISTS redeem_code_hash_backfill_audit (
    step        TEXT PRIMARY KEY,
    row_count   BIGINT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO redeem_code_hash_backfill_audit (step, row_count, recorded_at)
VALUES (
    'baseline_rows_with_plaintext',
    (SELECT COUNT(*) FROM redeem_codes WHERE code IS NOT NULL),
    NOW()
)
ON CONFLICT (step) DO UPDATE
SET row_count = EXCLUDED.row_count, recorded_at = EXCLUDED.recorded_at;

COMMIT;
