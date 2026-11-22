-- at-rest 加密（阶段一，加列，不动数据）：
--   * accounts.credentials_cipher: 整段 credentials 的 enc:v2: 密文；
--   * proxies.password_cipher:   代理密码的 enc:v2: 密文。
--
-- 为什么只加列、不在这里做数据迁移：
--   1) AES-256-GCM 无法在 SQL 内安全完成，密钥只允许存在于部署 env；
--      数据迁移必须由新版二进制的 `-atrest-migrate` 分批执行（可 dry-run、可校验、可中断续跑）。
--   2) 加列本身向后兼容：旧二进制不读新列，新二进制在密文为空时回退读明文，
--      所以在「先发版、再迁移」的顺序下，两端都能工作，回滚窗口安全。
--
-- 幂等：IF NOT EXISTS，可重复执行。
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS credentials_cipher TEXT;

ALTER TABLE proxies
    ADD COLUMN IF NOT EXISTS password_cipher TEXT;

COMMENT ON COLUMN accounts.credentials_cipher IS
    'enc:v2: at-rest ciphertext of the full credentials JSON object; empty for legacy plaintext rows';

COMMENT ON COLUMN proxies.password_cipher IS
    'enc:v2: at-rest ciphertext of the proxy password; empty for legacy plaintext rows';
