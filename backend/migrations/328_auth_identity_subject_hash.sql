-- Auth identities：subject 明文哈希化（keyed HMAC-SHA256 + 盲索引 + 展示前缀）
-- 阶段 A：只加结构，不动存量数据。
--
-- 涉及三处 subject 明文：
--   * auth_identities.provider_subject          —— 第三方登录身份 ID；
--   * auth_identity_channels.channel_subject   —— 渠道级 openid / 渠道身份 ID；
--   * pending_auth_sessions.provider_subject   —— 登录/绑定中间态（分钟级会话）。
--
-- 每处都新增：
--   *_hash   CHAR(64)    HMAC-SHA256(安装密钥, domain|provider_type|subject) 小写 hex；
--   *_prefix VARCHAR(16) 后台/SRE 展示用短前缀；完整 subject 不再回显。
-- 明文列改为可空：新行过渡期仍双写（旧二进制混跑 / 回退查询），
-- 回填校验通过后由运维 SQL 清空，下一版再 DROP COLUMN。
--
-- 为什么不裸 SHA-256：email 会作为 email provider 的 subject 落库，邮箱可枚举，
-- 裸摘要可被字典/彩虹表反推。安装密钥复用 security_secrets.totp_encryption_key
-- （跨实例持久共享），没有 key 的库泄露无法离线验证候选 subject。
--
-- 本迁移之外还有阶段 B/C（生产侧人工执行，见
-- docs/ops/auth-identity-subject-hash-20261121.md）：
--   1. 备份三张表；
--   2. 用 pgcrypto hmac() 分批回填（key 通过 psql 变量传入，不写进本文件）；
--   3. 验证无 NULL 哈希、无重复哈希、抽样比对、各 provider 登录冒烟；
--   4. 全量部署新版本后清空三处明文列 + scrub metadata/迁移报告/遗留表；
--   5. 回滚：从备份表恢复明文，必要时删除新列。
--
-- 唯一约束挂在 hash 上但保留 provider_key：
--   provider_key 是「provider 实例」隔离维度（微信多 AppID、多租户 OIDC issuer），
--   历史唯一键语义必须原样保留。NULL 哈希（回填前的历史行）不参与唯一约束。

-- ── auth_identities ─────────────────────────────────────────────────────────
ALTER TABLE auth_identities ALTER COLUMN provider_subject DROP NOT NULL;

ALTER TABLE auth_identities ADD COLUMN IF NOT EXISTS provider_subject_hash CHAR(64);
ALTER TABLE auth_identities ADD COLUMN IF NOT EXISTS provider_subject_prefix VARCHAR(16);

CREATE UNIQUE INDEX IF NOT EXISTS auth_identities_provider_subject_hash_key
    ON auth_identities (provider_type, provider_key, provider_subject_hash);

CREATE INDEX IF NOT EXISTS auth_identities_provider_subject_hash_lookup_idx
    ON auth_identities (provider_type, provider_subject_hash);

-- ── auth_identity_channels ──────────────────────────────────────────────────
ALTER TABLE auth_identity_channels ALTER COLUMN channel_subject DROP NOT NULL;

ALTER TABLE auth_identity_channels ADD COLUMN IF NOT EXISTS channel_subject_hash CHAR(64);
ALTER TABLE auth_identity_channels ADD COLUMN IF NOT EXISTS channel_subject_prefix VARCHAR(16);

CREATE UNIQUE INDEX IF NOT EXISTS auth_identity_channels_channel_subject_hash_key
    ON auth_identity_channels (provider_type, provider_key, channel, channel_app_id, channel_subject_hash);

CREATE INDEX IF NOT EXISTS auth_identity_channels_channel_subject_hash_lookup_idx
    ON auth_identity_channels (provider_type, channel_subject_hash);

-- ── pending_auth_sessions ───────────────────────────────────────────────────
ALTER TABLE pending_auth_sessions ALTER COLUMN provider_subject DROP NOT NULL;

ALTER TABLE pending_auth_sessions ADD COLUMN IF NOT EXISTS provider_subject_hash CHAR(64);
ALTER TABLE pending_auth_sessions ADD COLUMN IF NOT EXISTS provider_subject_prefix VARCHAR(16);

CREATE INDEX IF NOT EXISTS pending_auth_sessions_provider_subject_hash_idx
    ON pending_auth_sessions (provider_type, provider_key, provider_subject_hash);
