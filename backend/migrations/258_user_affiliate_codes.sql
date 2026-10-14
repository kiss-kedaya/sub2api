-- 用户自定义邀请（返利）码：一个用户可以拥有多个 aff_code。
--
-- 归属：`user_affiliates.aff_code` 保留为「默认码」（每用户一个，历史链接不失效）。
-- 本表只存用户/管理员额外创建的自定义码，因此：
--   * 返佣归因仍然落在 inviter_id（用户 ID）上，多码不会把返利拆散；
--   * 删除本表某行只影响「还能不能用这个码邀请」，不影响已产生的返佣；
--   * 原有的 6 处 user_affiliates JOIN（统计查询）一行都不用改。
--
-- code 全局唯一（跨用户），与 user_affiliates.aff_code 共享同一命名空间，
-- 因此应用层在写入前必须同时检查两张表，DB 层用独立唯一索引兜底。
CREATE TABLE IF NOT EXISTS user_affiliate_codes (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code VARCHAR(32) NOT NULL UNIQUE,
    is_custom BOOLEAN NOT NULL DEFAULT TRUE,
    created_by_admin BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 一个用户最多 9 个自定义码（默认码在 user_affiliates，不占这 9 个）：应用层校验，索引供列表与计数。
CREATE INDEX IF NOT EXISTS idx_user_affiliate_codes_user_id ON user_affiliate_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_user_affiliate_codes_code ON user_affiliate_codes(code);

COMMENT ON TABLE user_affiliate_codes IS '用户自定义邀请返利码（默认码仍在 user_affiliates.aff_code）';
COMMENT ON COLUMN user_affiliate_codes.code IS '邀请码，跨用户全局唯一';
COMMENT ON COLUMN user_affiliate_codes.is_custom IS '是否用户自定义（false 表示系统生成后加入本表）';
COMMENT ON COLUMN user_affiliate_codes.created_by_admin IS '是否由管理员代建';
