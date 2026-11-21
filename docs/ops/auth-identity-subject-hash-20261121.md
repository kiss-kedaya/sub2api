# auth_identities / auth_identity_channels / pending_auth_sessions subject 哈希化（运维手册）

- 状态：**未执行**。本文件的 SQL 只随 PR 交付；生产上等新版本全量部署后按阶段人工执行。
- 关联迁移：`backend/migrations/328_auth_identity_subject_hash.sql`（只加结构、幂等）。
- 关联代码：`backend/internal/pkg/authsubject`、`backend/internal/service/auth_identity_subject.go`。
- 安全目标：数据库里**不再出现完整第三方 subject 明文（含 email 型 subject）**，
  前缀只用于后台/排障展示；登录匹配、唯一约束、去重全部走 keyed 盲索引。

## 0. 哈希算法（必须与代码完全一致）

```
hash = HMAC-SHA256(
          安装密钥,
          "identity" | "channel"  +  0x00  +  lower(trim(provider_type))  +  0x00  +  trim(subject)
       ) 的小写 hex
```

- 安装密钥就是 `security_secrets.key = 'totp_encryption_key'` 的 64 位 hex（AES-256 同一把
  installation key，跨实例持久、启动时由 `ensureBootstrapSecrets` 载入）。**不要轮换**：
  换 key 等于全部历史 subject 盲索引失效，所有第三方登录匹配失败（与 TOTP 密文同级）。
- `provider_key` 不参与 MAC：微信有 `wechat / wechat-main / 自定义 AppID` 三套兼容归一化，
  把 provider_key 放进输入会让「写入 key」与「查询兼容 key 集合」算出不同摘要。
- domain 隔离 identity / channel；provider_type 隔离不同 provider。
- 展示前缀 = `hash 前 8 个 hex 字符`（**不是 subject 原文前缀**），后台只能看到
  `a1b2c3d4***` 这类掩码，无法据此还原/猜测 subject。
- `pending_auth_sessions` 需要跨请求带回 subject 才能完成身份落库，因此新会话把
  subject 用安装密钥 AES-256-GCM 封存为 `enc:sub:v1:<base64>` 存在明文列位置；
  hash/prefix 列负责匹配。历史在途会话仍是明文，过期/清理后自然消失。
  claims 里的 `channel_subject` 同样封存为 `channel_subject_sealed`（另存 hash/前缀）。
- 与代码的对应：`authsubject.HashIdentity` / `authsubject.HashChannel`。

## 1. 阶段顺序（顺序错了会掉登录）

1. 应用 `328_auth_identity_subject_hash.sql`（只加列/索引，旧二进制完全兼容）。
2. 备份三张表（§2）。
3. 分批回填 `*_hash / *_prefix`（§3，pgcrypto HMAC）。
4. 验证（§4）：无 NULL 哈希、无重复、抽样重算一致、各 provider 登录冒烟。
5. 部署新版本二进制（写 hash、按 hash 查询、明文回退）。**滚动期间旧实例仍按明文查询，
   且旧实例会写入「只有明文、没有 hash」的新行。**
6. 部署完成后**再跑一次 §3 回填**，确认三张表 `*_hash IS NULL` 为 0
   （滚动窗口内旧实例写入的行必须补上 hash，否则清空明文后这些身份将无法匹配）。
7. 观察一个发布窗口，确认没有 `AUTH_IDENTITY_*` 报错、各 provider 登录/绑定正常。
8. 清空三处明文列 + scrub metadata / 迁移报告 / 遗留表（§5、§6）。
9. 下一版再 `DROP COLUMN`（不在本 PR 执行）。

> 第 1 步可以单独先上：迁移 328 与旧二进制兼容（新列可空、无 NOT NULL / DEFAULT）。

## 2. 备份

```sql
CREATE TABLE IF NOT EXISTS auth_identities_subject_backup AS
SELECT id, user_id, provider_type, provider_key, provider_subject, NOW() AS backed_up_at
FROM auth_identities;
ALTER TABLE auth_identities_subject_backup ADD PRIMARY KEY (id);

CREATE TABLE IF NOT EXISTS auth_identity_channels_subject_backup AS
SELECT id, identity_id, provider_type, provider_key, channel, channel_app_id, channel_subject, NOW() AS backed_up_at
FROM auth_identity_channels;
ALTER TABLE auth_identity_channels_subject_backup ADD PRIMARY KEY (id);

CREATE TABLE IF NOT EXISTS pending_auth_sessions_subject_backup AS
SELECT id, session_token, provider_type, provider_key, provider_subject, expires_at, NOW() AS backed_up_at
FROM pending_auth_sessions;
ALTER TABLE pending_auth_sessions_subject_backup ADD PRIMARY KEY (id);

SELECT
  (SELECT COUNT(*) FROM auth_identities)                 AS identities,
  (SELECT COUNT(*) FROM auth_identities_subject_backup)  AS identities_backup,
  (SELECT COUNT(*) FROM auth_identity_channels)                 AS channels,
  (SELECT COUNT(*) FROM auth_identity_channels_subject_backup)  AS channels_backup,
  (SELECT COUNT(*) FROM pending_auth_sessions)                  AS pending,
  (SELECT COUNT(*) FROM pending_auth_sessions_subject_backup)   AS pending_backup;
```

## 3. 回填（pgcrypto HMAC，key 不进 SQL 文件）

先取安装密钥（只读；不要贴进聊天/日志/提交）：

```sql
SELECT value FROM security_secrets WHERE key = 'totp_encryption_key';
-- 或者：SELECT key, value FROM security_secrets WHERE key = 'totp_encryption_key';
```

`psql` 用变量传入（value 是 64 位 hex）：

```bash
psql "$DSN" -v auth_subject_hash_key="$INSTALLATION_KEY_HEX" -f docs/ops/sql/328_backfill.sql
```

> 生产环境如果 `CREATE EXTENSION pgcrypto` 不被允许，改用仓库里的
> `backend/cmd/auth-subject-backfill`（HMAC 在应用侧算，见 §9）。

### 3.1 auth_identities

```sql
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('identity', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(provider_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM auth_identities
    WHERE provider_subject_hash IS NULL AND provider_subject IS NOT NULL
)
UPDATE auth_identities AS ai
SET provider_subject_hash = computed.hash_hex,
    provider_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ai.id = computed.id;
```

### 3.2 auth_identity_channels（domain=channel）

```sql
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('channel', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(channel_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM auth_identity_channels
    WHERE channel_subject_hash IS NULL AND channel_subject IS NOT NULL
)
UPDATE auth_identity_channels AS ac
SET channel_subject_hash = computed.hash_hex,
    channel_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ac.id = computed.id;
```

### 3.3 pending_auth_sessions（domain=identity；会话分钟级，仍建议回填）

```sql
WITH computed AS (
    SELECT id,
           encode(
               hmac(
                   convert_to('identity', 'UTF8') || decode('00', 'hex') ||
                   convert_to(lower(btrim(provider_type)), 'UTF8') || decode('00', 'hex') ||
                   convert_to(btrim(provider_subject), 'UTF8'),
                   decode(:'auth_subject_hash_key', 'hex'),
                   'sha256'
               ), 'hex') AS hash_hex
    FROM pending_auth_sessions
    WHERE provider_subject_hash IS NULL
      AND provider_subject IS NOT NULL
      AND provider_subject NOT LIKE 'enc:sub:v1:%'   -- 新会话是封存值，不需要回填
)
UPDATE pending_auth_sessions AS ps
SET provider_subject_hash = computed.hash_hex,
    provider_subject_prefix = left(computed.hash_hex, 8)
FROM computed
WHERE ps.id = computed.id;
```

行数更大时把上面三条语句各自改成
`WHERE id IN (SELECT id FROM ... WHERE <hash> IS NULL AND <明文> IS NOT NULL ORDER BY id LIMIT 2000)`
循环执行到 0 行。当前生产量级（identity 约 1.4 万行）单事务直接跑也可以。

## 4. 验证（全部通过才允许清空明文）

```sql
-- 1) 必须为 0：仍未回填的行
SELECT 'identity' AS t, COUNT(*) FROM auth_identities WHERE provider_subject_hash IS NULL
UNION ALL
SELECT 'channel', COUNT(*) FROM auth_identity_channels WHERE channel_subject_hash IS NULL
UNION ALL
SELECT 'pending', COUNT(*) FROM pending_auth_sessions WHERE provider_subject_hash IS NULL;
```

```sql
-- 2) 必须为 0：SQL 重算与落库不一致（说明 subject 含 \t/\n 等非空格空白，需要人工处理）
SELECT COUNT(*) FROM auth_identities
WHERE provider_subject IS NOT NULL
  AND provider_subject_hash IS DISTINCT FROM encode(
        hmac(convert_to('identity','UTF8') || decode('00','hex') ||
             convert_to(lower(btrim(provider_type)),'UTF8') || decode('00','hex') ||
             convert_to(btrim(provider_subject),'UTF8'),
             decode(:'auth_subject_hash_key','hex'), 'sha256'), 'hex');
```

```sql
-- 3) 必须为 0：重复盲索引
SELECT provider_type, provider_key, provider_subject_hash, COUNT(*)
FROM auth_identities
WHERE provider_subject_hash IS NOT NULL
GROUP BY 1,2,3 HAVING COUNT(*) > 1;

SELECT provider_type, provider_key, channel, channel_app_id, channel_subject_hash, COUNT(*)
FROM auth_identity_channels
WHERE channel_subject_hash IS NOT NULL
GROUP BY 1,2,3,4,5 HAVING COUNT(*) > 1;
```

```sql
-- 3.5) pending 会话里不应再有明文：只允许空 / enc:sub:v1: 封存值
SELECT COUNT(*) FROM pending_auth_sessions
WHERE provider_subject IS NOT NULL
  AND provider_subject NOT LIKE 'enc:sub:v1:%';
```

```sql
-- 4) 覆盖率 + 前缀一致性（prefix 必须是 hash 前 8 位）
SELECT provider_type, COUNT(*) AS total,
       COUNT(provider_subject_hash) AS hashed,
       COUNT(provider_subject_prefix) AS prefixed,
       COUNT(*) FILTER (WHERE provider_subject_prefix IS DISTINCT FROM left(provider_subject_hash, 8)) AS bad_prefix
FROM auth_identities GROUP BY 1 ORDER BY 1;
```

应用侧冒烟（部署新版本后）：GitHub/Google、OIDC、LinuxDo、微信（公众号/开放平台）、
钉钉、邮箱验证码各登录一次；已绑定用户解绑/重绑一次；后台手动绑定一次。
全部应走 hash 命中，`auth_identities` / `auth_identity_channels` 不应新增重复行。

## 5. 清空明文（阶段 C，必须在新版本全量部署并观察后执行）

```sql
-- 前置：§4 全部通过；部署后再回填过一次且 `*_hash IS NULL` 为 0；
-- 备份表行数 = 主表行数。
SELECT
  (SELECT COUNT(*) FROM auth_identities) AS identities,
  (SELECT COUNT(*) FROM auth_identities_subject_backup) AS identities_backup;

BEGIN;

UPDATE auth_identities SET provider_subject = NULL WHERE provider_subject IS NOT NULL;
UPDATE auth_identity_channels SET channel_subject = NULL WHERE channel_subject IS NOT NULL;
-- pending 会话只清理历史明文；封存值留给会话自然过期（或确认全部过期后再清）。
UPDATE pending_auth_sessions
SET provider_subject = NULL
WHERE provider_subject IS NOT NULL
  AND provider_subject NOT LIKE 'enc:sub:v1:%';

-- 明文唯一索引的约束职责已由 hash 唯一索引承担。
DROP INDEX IF EXISTS auth_identities_provider_subject_key;
DROP INDEX IF EXISTS auth_identity_channels_channel_key;

COMMIT;
```

清空后验证：三张表明文列应为 0 行，哈希覆盖率 100%；再跑一轮登录冒烟（此时只可能命中 hash）。

## 6. 残留明文 scrub（同窗口执行）

### 6.1 identity / channel metadata

```sql
UPDATE auth_identities
SET metadata = metadata - ARRAY[
    'subject','provider_subject','union_id','unionid','openid',
    'channel_subject','corp_user_id','synthetic_email','email'
]
WHERE metadata ?| ARRAY[
    'subject','provider_subject','union_id','unionid','openid',
    'channel_subject','corp_user_id','synthetic_email','email'
];

UPDATE auth_identity_channels
SET metadata = metadata - ARRAY[
    'subject','provider_subject','union_id','unionid','openid',
    'channel_subject','corp_user_id','synthetic_email','email'
]
WHERE metadata ?| ARRAY[
    'subject','provider_subject','union_id','unionid','openid',
    'channel_subject','corp_user_id','synthetic_email','email'
];
```

> 新版本写路径已统一调用 `SanitizeAuthIdentityMetadata`，本 SQL 只清历史行。

### 6.2 pending_auth_sessions.upstream_identity_claims

新版本写入时已清洗（丢弃 subject/openid/unionid 明文；channel_subject 换成 hash/前缀，并保留
`channel_subject_sealed` 封存值）。
历史在途会话（≤15 分钟）也会过期，仍可顺手清洗：

```sql
UPDATE pending_auth_sessions
SET upstream_identity_claims = upstream_identity_claims - ARRAY[
    'subject','provider_subject','union_id','unionid','openid','channel_subject'
]
WHERE upstream_identity_claims ?| ARRAY[
    'subject','provider_subject','union_id','unionid','openid','channel_subject'
];
```

### 6.3 auth_identity_migration_reports.details

```sql
UPDATE auth_identity_migration_reports
SET details = (details - ARRAY[
        'provider_subject','provider_user_id','provider_union_id','openid','unionid',
        'channel_subject','provider_username','display_name','appid','app_id'
    ]) || jsonb_build_object('raw_metadata', '{}'::jsonb)
WHERE details ?| ARRAY[
        'provider_subject','provider_user_id','provider_union_id','openid','unionid',
        'channel_subject','provider_username','display_name','appid','app_id','raw_metadata'
    ];
```

> 迁移报告是历史审计记录，保留 id / user_id / 冲突类型即可，subject 原文不再保留。

### 6.4 遗留表 user_external_identities

该表是 108~116 迁移的来源表，代码已不再读写。确认无引用后先备份、再清空敏感列、最后删表：

```sql
DO $$
BEGIN
    IF to_regclass('public.user_external_identities') IS NULL THEN
        RETURN;
    END IF;
    EXECUTE 'CREATE TABLE IF NOT EXISTS user_external_identities_backup AS SELECT * FROM user_external_identities';
END $$;

-- 清空敏感列（provider_user_id / provider_union_id / metadata 含 subject 原文）
UPDATE user_external_identities
SET provider_user_id = NULL,
    provider_union_id = NULL,
    metadata = '{}'::jsonb
WHERE provider_user_id IS NOT NULL OR provider_union_id IS NOT NULL OR metadata <> '{}'::jsonb;

-- 确认 auth_identities 回填/验证完成、且没有任何代码引用后：
DROP TABLE IF EXISTS user_external_identities;
```

## 7. 回滚

### 7.1 清空明文之前（推荐窗口）

```sql
UPDATE auth_identities AS ai
SET provider_subject = b.provider_subject
FROM auth_identities_subject_backup AS b
WHERE ai.id = b.id AND ai.provider_subject IS NULL;

UPDATE auth_identity_channels AS ac
SET channel_subject = b.channel_subject
FROM auth_identity_channels_subject_backup AS b
WHERE ac.id = b.id AND ac.channel_subject IS NULL;

UPDATE pending_auth_sessions AS ps
SET provider_subject = b.provider_subject
FROM pending_auth_sessions_subject_backup AS b
WHERE ps.id = b.id AND ps.provider_subject IS NULL;
```

### 7.2 清空明文之后

- 备份里的行：按 §7.1 恢复明文，旧版本二进制可正常登录。
- 备份之后新增的行：过渡期新版本仍双写明文所以能被备份覆盖；但如果清空后才新增，
  这些行没有备份明文，回滚到旧版本前需要用户重新发起一次 OAuth 登录/绑定。
- 代码整体回滚时（回到只读明文列的旧版本）再执行结构回退：

```sql
DROP INDEX IF EXISTS auth_identities_provider_subject_hash_key;
DROP INDEX IF EXISTS auth_identities_provider_subject_hash_lookup_idx;
ALTER TABLE auth_identities DROP COLUMN IF EXISTS provider_subject_hash;
ALTER TABLE auth_identities DROP COLUMN IF EXISTS provider_subject_prefix;

DROP INDEX IF EXISTS auth_identity_channels_channel_subject_hash_key;
DROP INDEX IF EXISTS auth_identity_channels_channel_subject_hash_lookup_idx;
ALTER TABLE auth_identity_channels DROP COLUMN IF EXISTS channel_subject_hash;
ALTER TABLE auth_identity_channels DROP COLUMN IF EXISTS channel_subject_prefix;

DROP INDEX IF EXISTS pending_auth_sessions_provider_subject_hash_idx;
ALTER TABLE pending_auth_sessions DROP COLUMN IF EXISTS provider_subject_hash;
ALTER TABLE pending_auth_sessions DROP COLUMN IF EXISTS provider_subject_prefix;
```

> 回滚后备份表（含明文 subject）按敏感数据留存策略处理。

## 8. 残留风险 / 未在本次范围

- `pending_auth_sessions.session_token` 仍是明文 bearer 会话令牌（审计建议后续哈希）。
  它与 subject 无关，单独一批处理。
- OAuth upstream claims 中的 `email` 仍会写入 pending 会话（钉钉同步等功能依赖），
  会话分钟级过期；identity metadata 已不再保留 email。
- 安装密钥丢失 = 盲索引无法再计算，等价于 TOTP 密钥丢失；必须从数据库备份恢复同一把 key。

## 9. 无 pgcrypto 时的回填方式（备选）

仓库提供一次性回填命令（不进部署镜像，只本地/跳板机执行）：

```bash
# 只打印 SQL，不写库（默认 dry-run）
go run ./backend/cmd/auth-subject-backfill \
  --dsn "$DSN" --installation-key-hex "$INSTALLATION_KEY_HEX"

# 确认后加 --apply 分批写入
go run ./backend/cmd/auth-subject-backfill \
  --dsn "$DSN" --installation-key-hex "$INSTALLATION_KEY_HEX" --apply
```

该命令与迁移 328 的回填 SQL 产出完全一致（同一 HMAC 输入格式），二选一即可。
