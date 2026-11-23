-- Ops 5xx 规则口径拆分收尾：处置标准名存量规则（幂等迁移）。
--
-- 背景（发版审计发现）
--   337 只把别名（gateway5xx / gateway_5xx_count / *_total / *_spike /
--   error_5xx_count / total_5xx_count、upstream 系列）归一到规范 metric_type，
--   对「本来就叫 gateway_5xx」的存量规则完全没有动作。而拆分生效后
--   gateway_5xx 只统计「网关自产 5xx」（error_source<>upstream_http），
--   旧规则阈值是按「全部 5xx」设的（线上典型值 3000/5min），新口径下几乎打不到
--   → 网关故障告警静默，属于 P0 级监控失效。
--
-- 本迁移做的事（全部幂等，可重复执行）：
--   1) 会话级设置锁等待与语句超时（337 里的写法在事务外不生效，详见下方注释）；
--   2) 把仍未按新口径复核过的 gateway_5xx 存量规则（不论来自别名归一还是本来
--      就叫规范名）标记为「已按新口径待重设」，并 enabled=false；
--   3) 种子规则改成独立名字，避免与用户已存在的同名规则互相覆盖、也不再被
--      第 2 步误伤。
--
-- 明确不做的事情
--   - 不改任何规则的 threshold / operator / window_minutes / sustained_minutes /
--     severity / notify_email / cooldown_minutes / filters；运维自定的阈值原样保留。
--   - 不删除任何规则，不重命名任何规则。
--   - 不动 upstream_5xx* 规则（拆分后语义本来就对）。
--
-- 标记口径
--   描述行追加：口径已拆分：本条仅统计网关自产 5xx，阈值需按新量级重设
--   已带该标记的规则不再处理 → 重跑本迁移不改变结果（幂等）。
--   运维在后台按新口径改完阈值后重新启用即可，描述里的标记留着作为变更凭据。
--   若运维把该标记从描述里删掉，本迁移会再禁用一次（视为尚未复核）。
--
-- 种子规则命名
--   337 的种子名为「网关自身 5xx 激增」「上游 5xx 透传突增（观察）」
--   「上游 5xx 硬顶（观察）」。本迁移在 337 已插入且仍是种子默认内容时，
--   把它们改名为带「默认种子」后缀的版本，让默认种子与用户自建规则彻底分开：
--     - 已存在用户同名规则 → 337 的 DO NOTHING 保留了用户规则、种子未插入，
--       这里按「旧名 + metric_type + 默认阈值」匹配不到，不会误改用户规则；
--     - 用户改过种子内容（阈值等）→ 视为用户规则，不改名、不覆盖、不改开关。
--
-- 幂等性说明
--   每条语句都以「当前状态」为条件（描述是否已带标记 / 名字是否还是旧种子名），
--   重复执行时第 2、3 条不再命中任何行；第 1 条只是设置会话级超时，无数据影响。
--
-- 回滚
--   - 规则侧：无数据删除。需要恢复时把 description 里追加的说明行去掉并
--     enabled=true（运维当时的阈值一直在库里，没有被动过）。
--   - 代码侧：本迁移没有任何 DDL，不新增表 / 列 / 索引，回滚旧二进制无需数据库变更。
--   - 注意：回滚到旧版本（337 之后、本迁移之前）的二进制时，旧代码不认新种子名，
--     三条种子规则会落回「按名字找不到 → 等价于不存在」，不会重复插入。

-- 会话级超时：337 的写法只在当前事务内有效，而迁移执行器是自动提交，
-- 每条语句各成一个隐式事务，所以那两行什么都不做。这里改成会话级设置。
-- 迁移执行器（applyMigrationsFS）全程使用同一条持有 advisory lock 的连接
-- （lockConn）执行迁移 SQL 与事务，因此会话级设置对本迁移生效；
-- 连接在迁移结束后归还连接池，PostgreSQL 驱动 reset 时会把会话参数恢复默认，
-- 不会污染后续业务连接。
SET lock_timeout = '5s';
SET statement_timeout = '10min';

-- ============================================
-- 1) 标准名存量规则：只禁用 + 打标注，不动阈值
-- ============================================

-- 先把 337 因空描述补过文案的规则也视为「未复核」：
-- 它们的描述是 337 默认文案，但阈值仍是旧「全部 5xx」量级，同样会静默。
UPDATE ops_alert_rules
SET description = COALESCE(description, '') ||
        CASE WHEN COALESCE(description, '') = '' THEN '' ELSE E'\n' END ||
        '（口径已拆分：本条仅统计网关自产 5xx，阈值需按新量级重设）',
    enabled = false,
    updated_at = NOW()
WHERE metric_type = 'gateway_5xx'
  AND enabled IS DISTINCT FROM false
  AND COALESCE(description, '') NOT LIKE '%口径已拆分%';

-- 兼容已经手工 disable 但没打标注的规则：只补标注，不改开关状态。
UPDATE ops_alert_rules
SET description = COALESCE(description, '') ||
        CASE WHEN COALESCE(description, '') = '' THEN '' ELSE E'\n' END ||
        '（口径已拆分：本条仅统计网关自产 5xx，阈值需按新量级重设）',
    updated_at = NOW()
WHERE metric_type = 'gateway_5xx'
  AND enabled = false
  AND COALESCE(description, '') NOT LIKE '%口径已拆分%';

-- ============================================
-- 2) 种子规则改用独立名字，避免与用户规则同名冲突
-- ============================================

-- 2.1 337 已按旧名插入过种子 → 改名为带「默认种子」后缀的独立名字。
--     条件：旧名 + metric_type + 默认阈值三者都匹配，且目标新名未被占用。
--     注意不改 enabled：新建库上 337 刚插的种子是启用的，而用户改过阈值的同名
--     规则会因为阈值不匹配直接跳过，不会被这里打开的。
UPDATE ops_alert_rules
SET name = '网关自身 5xx 激增（默认种子）',
    updated_at = NOW()
WHERE name = '网关自身 5xx 激增'
  AND metric_type = 'gateway_5xx'
  AND threshold = 500.0
  AND NOT EXISTS (
      SELECT 1 FROM ops_alert_rules AS taken WHERE taken.name = '网关自身 5xx 激增（默认种子）'
  );

UPDATE ops_alert_rules
SET name = '上游 5xx 透传突增（观察·默认种子）',
    updated_at = NOW()
WHERE name = '上游 5xx 透传突增（观察）'
  AND metric_type = 'upstream_5xx_spike_ratio'
  AND threshold = 3.0
  AND NOT EXISTS (
      SELECT 1 FROM ops_alert_rules AS taken WHERE taken.name = '上游 5xx 透传突增（观察·默认种子）'
  );

UPDATE ops_alert_rules
SET name = '上游 5xx 硬顶（观察·默认种子）',
    updated_at = NOW()
WHERE name = '上游 5xx 硬顶（观察）'
  AND metric_type = 'upstream_5xx'
  AND threshold = 30000.0
  AND NOT EXISTS (
      SELECT 1 FROM ops_alert_rules AS taken WHERE taken.name = '上游 5xx 硬顶（观察·默认种子）'
  );

-- 2.2 种子规则幂等落位（独立名字 + ON CONFLICT (name) DO NOTHING）。
--     新库直接命中这里；老库若 2.1 已改名也跳过；用户自建规则永不覆盖。
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '网关自身 5xx 激增（默认种子）',
    '【网关自身 5xx】5 分钟内 status_code>=500 且 error_source<>upstream_http 的条数超过阈值时告警；上游透传 5xx 不计入本指标（见上游 5xx 观察规则）。',
    true, 'gateway_5xx', '>', 500.0, 5, 5, 'P1', true, 20, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '上游 5xx 透传突增（观察·默认种子）',
    '【上游 503 透传】当前 5 分钟 upstream_5xx 超过前 30 分钟中位数的 3 倍时记录观察事件；渠道健康指标，error_source=upstream_http，默认不发邮件，不代表网关自身故障。',
    true, 'upstream_5xx_spike_ratio', '>', 3.0, 5, 10, 'P3', false, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '上游 5xx 硬顶（观察·默认种子）',
    '【上游 503 透传】5 分钟内 upstream_5xx 超过 30000 条（极端上游故障）时记录观察事件；默认不发邮件，仅用于渠道健康趋势。',
    true, 'upstream_5xx', '>', 30000.0, 5, 5, 'P2', false, 30, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

COMMENT ON COLUMN ops_alert_rules.metric_type IS
    '告警指标类型。5xx 拆分后：gateway_5xx=status_code>=500 且 error_source<>upstream_http；upstream_5xx=status_code>=500 且 error_source=upstream_http；upstream_5xx_spike_ratio=上游 5xx 相对 30 分钟中位数的倍数。存量 gateway_5xx 规则升级时被禁用并在描述里追加「口径已拆分」标记，需管理员按新量级重设阈值后重新启用。';
