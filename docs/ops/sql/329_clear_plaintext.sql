-- 阶段 C 待执行：清空三处 subject 明文列 + metadata / 迁移报告 / 遗留表 scrub。
--
-- !!! 本文件不会自动执行 !!!
-- 执行前置（缺一不可）：
--   1. 阶段 B 新版本已全量部署并观察 ≥ 一个发布窗口（pending 会话 TTL 15 分钟已过）；
--   2. docs/ops/auth-identity-subject-hash-20261121.md §4 全部通过：
--      auth_identities / auth_identity_channels / pending_auth_sessions 的 *_hash IS NULL 均为 0；
--   3. 部署后回填再跑过一次（滚动窗口旧实例写入的行已补齐 hash）；
--   4. 备份表已建好且行数 = 主表行数：
--      auth_identities_bak_20261121_subject / auth_identity_channels_bak_20261121_subject /
--      pending_auth_sessions_bak_20261121_subject；
--   5. **执行前回报老板并取得确认。**
--
-- 生产约束：按 id 分批（≤2000 行/条）、statement_timeout=30s、不整表更新。
-- 每条语句输出本批更新行数；循环执行同一语句直到输出 0，再进入下一条。
-- 每条 UPDATE 都是幂等的（只处理明文 IS NOT NULL 的行）。
--
-- 用法示例（单条）：
--   psql "$DSN" -tA -v ON_ERROR_STOP=1 -f docs/ops/sql/329_clear_plaintext.sql

SET statement_timeout = '30s';

-- ── 1) auth_identities.provider_subject 清空（按 id 分批）──────────────────
WITH updated AS (
    UPDATE auth_identities
    SET provider_subject = NULL
    WHERE id IN (
        SELECT id FROM auth_identities
        WHERE provider_subject IS NOT NULL
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 2) auth_identity_channels.channel_subject 清空（按 id 分批）────────────
WITH updated AS (
    UPDATE auth_identity_channels
    SET channel_subject = NULL
    WHERE id IN (
        SELECT id FROM auth_identity_channels
        WHERE channel_subject IS NOT NULL
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 3) pending_auth_sessions.provider_subject 清空（按 id 分批）────────────
-- 新版本部署 + 观察窗口后，历史明文与阶段 A 的 enc:sub:v1: 封存值都应清掉，
-- 会话本身也已过期；这里不再保留任何可还原 subject 的材料。
WITH updated AS (
    UPDATE pending_auth_sessions
    SET provider_subject = NULL
    WHERE id IN (
        SELECT id FROM pending_auth_sessions
        WHERE provider_subject IS NOT NULL
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 4) auth_identities.metadata scrub（按 id 分批）─────────────────────────
WITH updated AS (
    UPDATE auth_identities
    SET metadata = metadata - ARRAY[
        'subject','provider_subject','union_id','unionid','openid',
        'channel_subject','corp_user_id','synthetic_email','email'
    ]
    WHERE id IN (
        SELECT id FROM auth_identities
        WHERE metadata ?| ARRAY[
            'subject','provider_subject','union_id','unionid','openid',
            'channel_subject','corp_user_id','synthetic_email','email'
        ]
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 5) auth_identity_channels.metadata scrub（按 id 分批）───────────────────
WITH updated AS (
    UPDATE auth_identity_channels
    SET metadata = metadata - ARRAY[
        'subject','provider_subject','union_id','unionid','openid',
        'channel_subject','corp_user_id','synthetic_email','email'
    ]
    WHERE id IN (
        SELECT id FROM auth_identity_channels
        WHERE metadata ?| ARRAY[
            'subject','provider_subject','union_id','unionid','openid',
            'channel_subject','corp_user_id','synthetic_email','email'
        ]
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 6) pending_auth_sessions.upstream_identity_claims scrub（按 id 分批）───
-- 新版本写入时已清洗；历史在途会话顺手清掉 subject 明文键。
WITH updated AS (
    UPDATE pending_auth_sessions
    SET upstream_identity_claims = upstream_identity_claims - ARRAY[
        'subject','provider_subject','union_id','unionid','openid','channel_subject'
    ]
    WHERE id IN (
        SELECT id FROM pending_auth_sessions
        WHERE upstream_identity_claims ?| ARRAY[
            'subject','provider_subject','union_id','unionid','openid','channel_subject'
        ]
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 7) auth_identity_migration_reports.details scrub（按 id 分批）──────────
-- 迁移报告是历史审计记录：保留 id / user_id / 冲突类型即可，subject 原文不再保留。
-- 先确认表存在：SELECT to_regclass('public.auth_identity_migration_reports');
-- 为空则跳过本节（该表只在做过 108~116 迁移的库存在）。
WITH updated AS (
    UPDATE auth_identity_migration_reports
    SET details = (details - ARRAY[
        'provider_subject','provider_user_id','provider_union_id','openid','unionid',
        'channel_subject','provider_username','display_name','appid','app_id'
    ]) || jsonb_build_object('raw_metadata', '{}'::jsonb)
    WHERE id IN (
        SELECT id FROM auth_identity_migration_reports
        WHERE details ?| ARRAY[
            'provider_subject','provider_user_id','provider_union_id','openid','unionid',
            'channel_subject','provider_username','display_name','appid','app_id','raw_metadata'
        ]
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 8) 遗留表 user_external_identities（108~116 迁移来源，代码已不读写）────
-- 先备份整表，再清空敏感列；确认无引用后再单独 DROP TABLE。
DO $$
BEGIN
    IF to_regclass('public.user_external_identities') IS NULL THEN
        RETURN;
    END IF;
    CREATE TABLE IF NOT EXISTS user_external_identities_bak_20261121_subject AS
    SELECT *, NOW() AS backed_up_at FROM user_external_identities;
END $$;

WITH updated AS (
    UPDATE user_external_identities
    SET provider_user_id = NULL,
        provider_union_id = NULL,
        metadata = '{}'::jsonb
    WHERE id IN (
        SELECT id FROM user_external_identities
        WHERE provider_user_id IS NOT NULL
           OR provider_union_id IS NOT NULL
           OR metadata <> '{}'::jsonb
        ORDER BY id
        LIMIT 2000
    )
    RETURNING 1
)
SELECT count(*) FROM updated;

-- ── 9) 明文唯一索引（约束职责已由 hash 唯一索引承担）──────────────────────
-- 确认上面所有批次都跑完、明文列 count=0 后再执行。
DROP INDEX IF EXISTS auth_identities_provider_subject_key;
DROP INDEX IF EXISTS auth_identity_channels_channel_key;

-- ── 10) 执行后验证（结果必须全 0；仅查询，不写库）──────────────────────────
SELECT 'auth_identities.provider_subject' AS check_name, count(*) AS remaining
FROM auth_identities WHERE provider_subject IS NOT NULL
UNION ALL
SELECT 'auth_identity_channels.channel_subject', count(*)
FROM auth_identity_channels WHERE channel_subject IS NOT NULL
UNION ALL
SELECT 'pending_auth_sessions.provider_subject', count(*)
FROM pending_auth_sessions WHERE provider_subject IS NOT NULL
UNION ALL
SELECT 'auth_identities hash NULL', count(*)
FROM auth_identities WHERE provider_subject_hash IS NULL
UNION ALL
SELECT 'auth_identity_channels hash NULL', count(*)
FROM auth_identity_channels WHERE channel_subject_hash IS NULL;
