-- Redeem code（卡密）凭据改为哈希存储（阶段 A：只加结构，不动存量数据）。
--
-- 背景：卡密是可兑现的价值载体，但库里长期整行保存明文 `code`。任何能读
-- redeem_codes 的路径（后台列表 / 详情 / 导出 CSV / 备份 / 审计 / 日志）都会
-- 直接拿到可兑换明文。本迁移让新卡密只落 sha256 哈希，明文只在「生成」的当次
-- 响应里出现一次：
--   * redeem_codes.code_hash   —— 校验/兑换快路径的索引列（sha256 hex，64 字符）
--   * redeem_codes.code_prefix —— 展示用前缀（前 8 位），列表/日志只靠它识别
--   * redeem_code_hashes       —— 独立哈希台账（redeem_code_id 一对一主键），
--                                与 api_key_hashes 同构：明确「凭据本体只以哈希
--                                形式存在」的数据边界，轮换/审计只看一张表。
--
-- 与 api_keys（迁移 327）保持同一套语义：
--   * code 列改为可空：新行写 NULL，绝不再写明文；
--   * 原唯一约束对 NULL 不生效，因此明文回退查询（过渡期）仍走原来的唯一索引；
--   * 部分唯一索引只约束已回填 / 已写入哈希的行，历史明文行在回填前不参与。
--
-- 阶段 B（生产侧人工执行，见 PR 说明里的回填 / 回滚 SQL，不在本迁移内）：
--   1. 备份 redeem_codes 与明细；
--   2. 分批回填 code_hash / code_prefix 与 redeem_code_hashes；
--   3. 校验「明文无哈希」「哈希冲突」「前缀不一致」三类计数全 0；
--   4. 最后 UPDATE redeem_codes SET code = NULL（写 NULL 而不是空串：
--      空串会撞原唯一约束，多行空串直接报错）；
--   5. 下一版再考虑 DROP COLUMN code。

ALTER TABLE redeem_codes ALTER COLUMN code DROP NOT NULL;

ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS code_hash CHAR(64);
ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS code_prefix VARCHAR(16);

COMMENT ON COLUMN redeem_codes.code IS 'Legacy plaintext redeem code; NULL for hash-only rows (set NULL in stage B backfill)';
COMMENT ON COLUMN redeem_codes.code_hash IS 'Lowercase hex SHA-256 of the redeem code; the only stored credential';
COMMENT ON COLUMN redeem_codes.code_prefix IS 'Display prefix (first 8 characters) of the redeem code';

-- 部分唯一索引：只约束已回填 / 已写入哈希的行，历史明文行在回填前不参与。
CREATE UNIQUE INDEX IF NOT EXISTS uq_redeem_codes_code_hash
    ON redeem_codes (code_hash)
    WHERE code_hash IS NOT NULL;

-- 后台「按前缀搜索」用：前缀不是唯一值，普通索引即可。
CREATE INDEX IF NOT EXISTS idx_redeem_codes_code_prefix
    ON redeem_codes (code_prefix)
    WHERE code_prefix IS NOT NULL;

CREATE TABLE IF NOT EXISTS redeem_code_hashes (
    redeem_code_id BIGINT PRIMARY KEY REFERENCES redeem_codes(id) ON DELETE CASCADE,
    code_hash      CHAR(64) NOT NULL UNIQUE,
    code_prefix    VARCHAR(16) NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
