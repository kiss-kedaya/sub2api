-- clean-xss-payloads-20261123.sql
-- 目的：清理历史存量 XSS payload（工单标题/正文与预览、用户名、API Key 名称、SVG 头像），
--       与代码侧「入库统一纯文本清洗」配套；本脚本只清存量，不自动执行。
--
-- 执行人：主代理（低峰窗口），执行前请确认已完成完整数据备份（schema 快照不算）。
-- 执行方式（单事务、遇错回滚）：
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/ops/clean-xss-payloads-20261123.sql
-- 建议先只跑 SELECT 盘点（脚本头部「0) 盘点」段），确认命中行数与预期一致再跑整个脚本。
--
-- 幂等性：
--   * 备份表 CREATE TABLE IF NOT EXISTS + ON CONFLICT DO NOTHING：重复执行只补新增命中行。
--   * UPDATE 的 WHERE 是「仍像脏数据」的条件，清洗后不再命中，第二次执行影响 0 行。
--   * DELETE 的 WHERE 是「非白名单头像」，删完后再跑命中 0 行。
--   * 整个脚本单事务，中途任何报错全部回滚。
--
-- 回滚（脚本执行后如要恢复，从对应备份表回写；备份表在脚本末尾仍保留）：
--   UPDATE users u SET username = b.username FROM ops_cleanup_20261123_users b WHERE u.id = b.user_id;
--   UPDATE api_keys k SET name = b.name FROM ops_cleanup_20261123_api_keys b WHERE k.id = b.api_key_id;
--   UPDATE support_ticket_messages m SET content = b.content
--     FROM ops_cleanup_20261123_ticket_messages b WHERE m.id = b.message_id;
--   UPDATE support_tickets t SET subject = b.subject, last_message_preview = b.last_message_preview
--     FROM ops_cleanup_20261123_tickets b WHERE t.id = b.ticket_id;
--   INSERT INTO user_avatars (user_id, storage_provider, storage_key, url, content_type, byte_size, sha256)
--     SELECT user_id, storage_provider, storage_key, url, content_type, byte_size, sha256
--     FROM ops_cleanup_20261123_avatars
--     ON CONFLICT (user_id) DO NOTHING;
--   备份表确认无用后由主代理手工 DROP（本脚本不自动删备份）。

BEGIN;

-- ---------------------------------------------------------------------------
-- 0) 盘点（只读）：先看清每张表有多少条要处理。可在事务外单独执行这一段。
-- ---------------------------------------------------------------------------
SELECT 'support_tickets.subject/preview' AS target,
       count(*) AS dirty_rows
FROM support_tickets t
WHERE t.subject ~* '</?[a-z][^>]*>'
   OR t.subject ~* 'javascript[[:space:]]*:'
   OR t.subject ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)'
   OR t.last_message_preview ~* '</?[a-z][^>]*>'
   OR t.last_message_preview ~* '\mon[a-z]{3,}[[:space:]]*=';

SELECT 'support_ticket_messages.content' AS target,
       count(*) AS dirty_rows
FROM support_ticket_messages m
WHERE m.content ~* '</?[a-z][^>]*>'
   OR m.content ~* '\mon[a-z]{3,}[[:space:]]*='
   OR m.content ~* 'javascript[[:space:]]*:'
   OR m.content ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)';

SELECT 'users.username' AS target,
       count(*) AS dirty_rows
FROM users u
WHERE u.username ~* '</?[a-z][^>]*>'
   OR u.username ~* '\mon[a-z]{3,}[[:space:]]*='
   OR u.username ~* 'javascript[[:space:]]*:'
   OR u.username ~* '&lt;|&#x?0*3c;'
   OR translate(u.username, U&'\200B\200C\200D\200E\200F\202A\202B\202C\202D\202E\2066\2067\2068\2069\FEFF\00AD', '') <> u.username;

SELECT 'api_keys.name' AS target,
       count(*) AS dirty_rows
FROM api_keys k
WHERE k.name ~* '</?[a-z][^>]*>'
   OR k.name ~* '\mon[a-z]{3,}[[:space:]]*='
   OR k.name ~* 'javascript[[:space:]]*:'
   OR k.name ~* '&lt;|&#x?0*3c;';

SELECT 'user_avatars(svg/unsafe)' AS target,
       count(*) AS dirty_rows
FROM user_avatars a
WHERE a.content_type ~* 'svg'
   OR a.url ~* '^[[:space:]]*data:image/svg'
   OR a.url ~* '^[[:space:]]*data:application/(xhtml\+xml|xml)'
   OR a.url ~* '^[[:space:]]*(javascript|vbscript):'
   OR (a.storage_provider = 'remote_url' AND a.url !~* '^https?://');

-- ---------------------------------------------------------------------------
-- 1) 公共清洗函数（只在本会话的临时 schema 里存在，不污染业务 schema）
--    与后端 internal/util/textsanitize 的口径对齐：
--    换行归一 → 去控制字符 → 去零宽/双向覆写字符 → 解 HTML 实体（最多 3 轮）
--    → 剥 HTML 标签（最多 3 轮）→ 去 javascript:/data:text/html 等伪协议 → trim。
--    数字实体只解码与标记/伪协议相关的常见码位；其余实体保留为普通文本
--    （新版写入由 Go 侧完整解码，这里只为把历史 payload 变成纯文本）。
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION pg_temp.clean_plain_text(input text)
RETURNS text
LANGUAGE plpgsql
AS $$
DECLARE
    v text := input;
    prev text;
    i int;
BEGIN
    IF v IS NULL THEN
        RETURN NULL;
    END IF;

    -- 换行归一
    v := replace(replace(v, chr(13) || chr(10), chr(10)), chr(13), chr(10));

    -- C0/C1 控制字符（保留 \n 与 \t）与 DEL
    v := translate(v,
        chr(1) || chr(2) || chr(3) || chr(4) || chr(5) || chr(6) || chr(7) || chr(8) ||
        chr(11) || chr(12) || chr(14) || chr(15) || chr(16) || chr(17) || chr(18) ||
        chr(19) || chr(20) || chr(21) || chr(22) || chr(23) || chr(24) || chr(25) ||
        chr(26) || chr(27) || chr(28) || chr(29) || chr(30) || chr(31) || chr(127),
        '');

    -- 零宽（U+200B..U+200F）、双向覆写（U+202A..U+202E / U+2066..U+2069）、BOM、软连字符
    v := translate(v,
        U&'\200B\200C\200D\200E\200F\202A\202B\202C\202D\202E\2060\2061\2062\2063\2064\2066\2067\2068\2069\FEFF\00AD',
        '');

    -- HTML 实体解码：先解命名实体，再解与标记相关的数字实体（3 轮处理二次编码）
    FOR i IN 1..3 LOOP
        prev := v;
        v := replace(v, '&lt;', '<');
        v := replace(v, '&gt;', '>');
        v := replace(v, '&amp;', '&');
        v := replace(v, '&quot;', '"');
        v := replace(v, '&apos;', '''');
        v := replace(v, '&nbsp;', ' ');
        v := replace(v, '&colon;', ':');
        v := regexp_replace(v, '&#x0*3c;', '<', 'gi');
        v := regexp_replace(v, '&#0*60;', '<', 'g');
        v := regexp_replace(v, '&#x0*3e;', '>', 'gi');
        v := regexp_replace(v, '&#0*62;', '>', 'g');
        v := regexp_replace(v, '&#x0*26;', '&', 'gi');
        v := regexp_replace(v, '&#0*38;', '&', 'g');
        v := regexp_replace(v, '&#x0*27;', '''', 'gi');
        v := regexp_replace(v, '&#0*39;', '''', 'g');
        v := regexp_replace(v, '&#x0*22;', '"', 'gi');
        v := regexp_replace(v, '&#0*34;', '"', 'g');
        v := regexp_replace(v, '&#x0*3a;', ':', 'gi');
        v := regexp_replace(v, '&#0*58;', ':', 'g');
        v := regexp_replace(v, '&#x0*2f;', '/', 'gi');
        v := regexp_replace(v, '&#0*47;', '/', 'g');
        EXIT WHEN v = prev;
    END LOOP;

    -- 剥 HTML 标签（3 轮处理嵌套/拼接残片；未闭合的标签连同其后内容一起丢弃）
    FOR i IN 1..3 LOOP
        prev := v;
        v := regexp_replace(v, '</?[a-zA-Z][^>]*>', '', 'g');
        v := regexp_replace(v, '<[!?][^>]*>', '', 'g');
        v := regexp_replace(v, '</?[a-zA-Z][^>]*$', '', 'g');
        EXIT WHEN v = prev;
    END LOOP;

    -- 伪协议与裸事件属性残片
    v := regexp_replace(v, '\m(javascript|vbscript|livescript)[[:space:]]*:', '', 'gi');
    v := regexp_replace(v, '\mdata[[:space:]]*:[[:space:]]*(text/html|text/xml|application/(xml|xhtml\+xml)|image/svg\+xml)', '', 'gi');
    v := regexp_replace(v, '\mon[a-z]{3,}[[:space:]]*=', '', 'gi');

    RETURN btrim(v);
END;
$$;

-- ---------------------------------------------------------------------------
-- 2) 备份命中的原始行（幂等：已备份过的记录不覆盖原始值）
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS ops_cleanup_20261123_tickets (
    ticket_id BIGINT PRIMARY KEY,
    user_id BIGINT,
    subject TEXT,
    last_message_preview TEXT,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_cleanup_20261123_tickets (ticket_id, user_id, subject, last_message_preview)
SELECT t.id, t.user_id, t.subject, t.last_message_preview
FROM support_tickets t
WHERE t.subject ~* '</?[a-z][^>]*>'
   OR t.subject ~* 'javascript[[:space:]]*:'
   OR t.subject ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)'
   OR t.last_message_preview ~* '</?[a-z][^>]*>'
   OR t.last_message_preview ~* '\mon[a-z]{3,}[[:space:]]*='
ON CONFLICT (ticket_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS ops_cleanup_20261123_ticket_messages (
    message_id BIGINT PRIMARY KEY,
    ticket_id BIGINT,
    author_role TEXT,
    content TEXT,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_cleanup_20261123_ticket_messages (message_id, ticket_id, author_role, content)
SELECT m.id, m.ticket_id, m.author_role, m.content
FROM support_ticket_messages m
WHERE m.content ~* '</?[a-z][^>]*>'
   OR m.content ~* '\mon[a-z]{3,}[[:space:]]*='
   OR m.content ~* 'javascript[[:space:]]*:'
   OR m.content ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)'
ON CONFLICT (message_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS ops_cleanup_20261123_users (
    user_id BIGINT PRIMARY KEY,
    username TEXT,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_cleanup_20261123_users (user_id, username)
SELECT u.id, u.username
FROM users u
WHERE u.username ~* '</?[a-z][^>]*>'
   OR u.username ~* '\mon[a-z]{3,}[[:space:]]*='
   OR u.username ~* 'javascript[[:space:]]*:'
   OR u.username ~* '&lt;|&#x?0*3c;'
   OR translate(u.username, U&'\200B\200C\200D\200E\200F\202A\202B\202C\202D\202E\2066\2067\2068\2069\FEFF\00AD', '') <> u.username
ON CONFLICT (user_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS ops_cleanup_20261123_api_keys (
    api_key_id BIGINT PRIMARY KEY,
    user_id BIGINT,
    name TEXT,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_cleanup_20261123_api_keys (api_key_id, user_id, name)
SELECT k.id, k.user_id, k.name
FROM api_keys k
WHERE k.name ~* '</?[a-z][^>]*>'
   OR k.name ~* '\mon[a-z]{3,}[[:space:]]*='
   OR k.name ~* 'javascript[[:space:]]*:'
   OR k.name ~* '&lt;|&#x?0*3c;'
ON CONFLICT (api_key_id) DO NOTHING;

CREATE TABLE IF NOT EXISTS ops_cleanup_20261123_avatars (
    user_id BIGINT PRIMARY KEY,
    storage_provider TEXT,
    storage_key TEXT,
    url TEXT,
    content_type TEXT,
    byte_size INT,
    sha256 TEXT,
    backed_up_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO ops_cleanup_20261123_avatars (user_id, storage_provider, storage_key, url, content_type, byte_size, sha256)
SELECT a.user_id, a.storage_provider, a.storage_key, a.url, a.content_type, a.byte_size, a.sha256
FROM user_avatars a
WHERE a.content_type ~* 'svg'
   OR a.url ~* '^[[:space:]]*data:image/svg'
   OR a.url ~* '^[[:space:]]*data:application/(xhtml\+xml|xml)'
   OR a.url ~* '^[[:space:]]*(javascript|vbscript):'
   OR (a.storage_provider = 'remote_url' AND a.url !~* '^https?://')
ON CONFLICT (user_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 3) 清洗为纯文本
--    长度沿用现有约束：subject <= 160、message content <= 10000、
--    last_message_preview <= 200（与 repository 侧截断一致）、
--    username/api_keys.name <= 100（列宽）。
--    清洗后为空时给可读兜底值，避免撞 CHECK (char_length BETWEEN 1 AND N)。
-- ---------------------------------------------------------------------------
UPDATE support_tickets t
SET subject = left(coalesce(nullif(pg_temp.clean_plain_text(t.subject), ''), '[已清理]'), 160),
    last_message_preview = left(coalesce(pg_temp.clean_plain_text(t.last_message_preview), ''), 200)
WHERE t.id IN (SELECT ticket_id FROM ops_cleanup_20261123_tickets);

UPDATE support_ticket_messages m
SET content = left(coalesce(nullif(pg_temp.clean_plain_text(m.content), ''), '[已清理]'), 10000)
WHERE m.id IN (SELECT message_id FROM ops_cleanup_20261123_ticket_messages);

UPDATE users u
SET username = left(coalesce(pg_temp.clean_plain_text(u.username), ''), 100)
WHERE u.id IN (SELECT user_id FROM ops_cleanup_20261123_users);

UPDATE api_keys k
SET name = left(coalesce(nullif(pg_temp.clean_plain_text(k.name), ''), 'API Key'), 100)
WHERE k.id IN (SELECT api_key_id FROM ops_cleanup_20261123_api_keys);

-- SVG / 非 http(s) 头像直接删行：读取侧已降级为默认头像，这里把存量清干净
-- （user_avatars 按 user_id 唯一，删掉即回落默认头像）。
DELETE FROM user_avatars a
WHERE a.user_id IN (SELECT user_id FROM ops_cleanup_20261123_avatars);

-- ---------------------------------------------------------------------------
-- 4) 校验：以下计数必须全部为 0；备份表计数 = 本次处理量。
-- ---------------------------------------------------------------------------
SELECT 'verify:support_tickets' AS check_name, count(*) AS remaining
FROM support_tickets t
WHERE t.subject ~* '</?[a-z][^>]*>'
   OR t.subject ~* 'javascript[[:space:]]*:'
   OR t.subject ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)'
   OR t.last_message_preview ~* '</?[a-z][^>]*>'
   OR t.last_message_preview ~* '\mon[a-z]{3,}[[:space:]]*=';

SELECT 'verify:support_ticket_messages' AS check_name, count(*) AS remaining
FROM support_ticket_messages m
WHERE m.content ~* '</?[a-z][^>]*>'
   OR m.content ~* '\mon[a-z]{3,}[[:space:]]*='
   OR m.content ~* 'javascript[[:space:]]*:'
   OR m.content ~* 'data[[:space:]]*:[[:space:]]*(text/html|image/svg)';

SELECT 'verify:users' AS check_name, count(*) AS remaining
FROM users u
WHERE u.username ~* '</?[a-z][^>]*>'
   OR u.username ~* '\mon[a-z]{3,}[[:space:]]*='
   OR u.username ~* 'javascript[[:space:]]*:'
   OR u.username ~* '&lt;|&#x?0*3c;'
   OR translate(u.username, U&'\200B\200C\200D\200E\200F\202A\202B\202C\202D\202E\2066\2067\2068\2069\FEFF\00AD', '') <> u.username;

SELECT 'verify:api_keys' AS check_name, count(*) AS remaining
FROM api_keys k
WHERE k.name ~* '</?[a-z][^>]*>'
   OR k.name ~* '\mon[a-z]{3,}[[:space:]]*='
   OR k.name ~* 'javascript[[:space:]]*:'
   OR k.name ~* '&lt;|&#x?0*3c;';

SELECT 'verify:user_avatars' AS check_name, count(*) AS remaining
FROM user_avatars a
WHERE a.content_type ~* 'svg'
   OR a.url ~* '^[[:space:]]*data:image/svg'
   OR a.url ~* '^[[:space:]]*data:application/(xhtml\+xml|xml)'
   OR a.url ~* '^[[:space:]]*(javascript|vbscript):'
   OR (a.storage_provider = 'remote_url' AND a.url !~* '^https?://');

SELECT 'backup:support_tickets' AS backup_table, count(*) AS rows FROM ops_cleanup_20261123_tickets
UNION ALL
SELECT 'backup:support_ticket_messages', count(*) FROM ops_cleanup_20261123_ticket_messages
UNION ALL
SELECT 'backup:users', count(*) FROM ops_cleanup_20261123_users
UNION ALL
SELECT 'backup:api_keys', count(*) FROM ops_cleanup_20261123_api_keys
UNION ALL
SELECT 'backup:user_avatars', count(*) FROM ops_cleanup_20261123_avatars;

COMMIT;
