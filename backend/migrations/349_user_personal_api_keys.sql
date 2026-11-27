-- 用户个人接口密钥（Personal API Key）：允许用户在个人资料页生成一把长期管理密钥，
-- 免浏览器 AT/RT 与网络指纹限制，直接调用全部用户权限范围内的管理接口（如查询账户/用量、创建与管理 API Key）。
--
-- 安全设计：
--   * 数据库只存不可逆 SHA-256 摘要（key_hash）与脱敏展示前缀（key_prefix），绝不落明文；
--   * 每位用户最多持有一把生效中的个人接口密钥（user_id 主键），重建即原子覆盖旧哈希使旧密钥立即失效；
--   * key_hash 建唯一索引，支撑鉴权热路径 O(1) 查找；
--   * 外键 ON DELETE CASCADE 在建表瞬间需要对 users 拿 SHARE ROW EXCLUSIVE 锁，故显式限定
--     SET LOCAL lock_timeout = '5s'；本迁移纯增量新增单表，不触碰任何既有热表，回滚无害。
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS user_personal_api_keys (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    key_prefix VARCHAR(32) NOT NULL,
    key_hash CHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    last_used_ip VARCHAR(64) NOT NULL DEFAULT ''
);
