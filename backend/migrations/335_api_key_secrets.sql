-- 浏览器端封存的 API Key 密文（sealed secret）。
--
-- 产品目标：用户创建 Key 后，之后还能自己在浏览器里重新看到明文；但服务器
-- 既不保存明文，也不接收登录密码。做法是浏览器用「登录密码 + PBKDF2」派生
-- AES-256-GCM 密钥，把明文加密后只把密文与 KDF 参数上传；以后想看时用户再输
-- 密码，由浏览器解密。本表只存密文，拖库拖到的是无法解密的字节。
--
-- 数据边界：
--   * 一行对应一把活着的 api_keys（api_key_id UNIQUE）；api_keys 物理删除时级联，
--     软删除 / 轮换时由业务代码在同一事务里显式删除本行（见 api_key_repo.go）。
--   * kdf 存前端上报的派生参数：{"algo":"PBKDF2-SHA256","iterations":310000,"salt":"<base64>"}。
--     salt 不是秘密，但必须跟着密文走才能解出来。
--   * sealed 是 base64 的 AES-256-GCM 密文，服务端不解析、不解密。
--   * 本表没有明文列、没有 hash 列，也没有任何可反推 key 的字段。
CREATE TABLE IF NOT EXISTS api_key_secrets (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL UNIQUE REFERENCES api_keys(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL,
    version INT NOT NULL DEFAULT 1,
    kdf JSONB NOT NULL,
    sealed TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 列表页按 user_id 批量判断 has_sealed_secret，需要这条索引避免全表扫描。
CREATE INDEX IF NOT EXISTS idx_api_key_secrets_user ON api_key_secrets(user_id);
