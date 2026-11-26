-- 用户安全偏好：目前只有「创建 / 批量轮换 / 明文编辑 API Key 前要不要先验证身份」这一项开关。
--
-- 取值语义（见 service/api_key_create_verification.go）：
--   * 没有行 = auto：沿用旧行为（开了 TOTP / 绑了 Passkey 的账号强制校验，只有登录密码的账号不强制）。
--     所有存量用户都没有行，所以本迁移不回填，上线前后行为一字不差。
--   * on  = 账号有任意可用因子（登录密码 / TOTP / Passkey）就必须校验。
--   * off = 不要求校验（即使开了 2FA）；请求里主动带了 verification 时仍然照常真验证。
-- 「恢复默认」= 删掉这一行，所以列上只允许 'on' / 'off'。
--
-- 发版安全性：
--   * 纯增量新表：不改 users、不回填、不写默认行。
--   * 旧二进制不读这张表：可以先迁移后换二进制；回滚 = 换回旧二进制，这张表留着无害。
--   * 外键（ON DELETE CASCADE）要对 users 拿一瞬间的 SHARE ROW EXCLUSIVE 锁（建级联触发器）。
--     users 是热表，所以限定最长等 5 秒：拿不到就让整个迁移回滚、发版在换二进制之前中止，
--     线上实例不受影响，隔几秒重试。本文件只新建这一张表，不碰别的表。
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS user_security_preferences (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    api_key_create_verification TEXT NOT NULL CHECK (api_key_create_verification IN ('on', 'off')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
