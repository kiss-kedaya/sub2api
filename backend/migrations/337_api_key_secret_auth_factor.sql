-- 浏览器端封存密文的「创建时身份校验因子」元数据。
--
-- 产品目标（老板 2026-11-23 反馈）：
--   1. 创建 API Key 时强制做一次身份校验（登录密码 / TOTP / Passkey 三选一）；
--   2. 以后再次显示这把 Key 时，必须用**同一因子**再校验一次（TOTP 创建的
--      就用 TOTP 查看，Passkey 创建的用 Passkey），而不是只校验登录密码。
--
-- 因此需要把「这把 Key 的密文是哪个因子授权封存的」落在密文元数据里。
-- 注意：本列只是授权方式的元数据，不是解密材料——解密仍由浏览器用登录密码
-- 走 PBKDF2-SHA256 + AES-256-GCM 完成，服务端没有密码、没有明文、也没有
-- 任何可解密材料。
--
-- 取值：password / totp / passkey。NULL 表示本次改造前封存的旧密文
--（前端旧版本没有上报 auth_factor），查看时按「任意可用因子」兼容处理。
ALTER TABLE api_key_secrets
    ADD COLUMN IF NOT EXISTS auth_factor VARCHAR(16) DEFAULT NULL;

COMMENT ON COLUMN api_key_secrets.auth_factor IS
    '创建时用于授权封存的三选一因子：password/totp/passkey；NULL=改造前旧密文';
