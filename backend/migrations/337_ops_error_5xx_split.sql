-- Ops 告警口径拆分：上游透传 5xx 与网关自产 5xx 分开统计（幂等迁移）。
--
-- 背景
--   旧口径把 ops_error_logs 里所有 status_code>=500 都算成「网关 5xx」，
--   上游返回并透传的 503/502/504 会让上游抖动触发网关故障告警。
--
-- 本迁移只改三类东西，全部可重复执行：
--   1) 把 DB 里历史/别名 metric_type 归一到 gateway_5xx / upstream_5xx；
--   2) 给已存在但描述为空的 5xx 规则补口径文案（不动 operator / threshold /
--      severity / enabled / filters，阈值仍由运维决定）；
--   3) 种子三条 5xx 告警规则（网关自身 P1 + 上游突增 P3 + 上游硬顶 P2 观察）。
--      已存在同名规则时 DO NOTHING，不覆盖运维改过的阈值。
--
-- 指标口径（数据源 ops_error_logs，只读聚合，不写任何表）
--   gateway_5xx              = status_code>=500 AND error_source<>'upstream_http'
--   upstream_5xx             = status_code>=500 AND error_source ='upstream_http'
--   upstream_5xx_spike_ratio = 当前 5 分钟桶 upstream_5xx / 前 30 分钟中位数
--                              （中位数为 0 时按 1 条平滑，避免基线稀疏不可观察）
--   两者都排除 is_count_tokens=true，与 ops 其它错误统计口径一致。
--
-- 存量规则处理说明
--   - metric_type='gateway_5xx' 的规则保留，语义自动收紧为「只算网关自产」；
--     阈值/严重级/开关一律不动，需要按新量级调整时由运维在后台改。
--   - 旧别名（*_count/*_total/*_spike/gateway5xx/upstream5xx 等）改名为规范
--     metric_type，其余字段不动。
--   - 规则名只做展示，不参与指标计算；本迁移不重命名规则，避免唯一索引冲突。
--
-- 回滚
--   本迁移不删除任何规则、不改阈值。回滚只需删除 3 条种子规则
--   （按 name 识别），并把需要恢复的规则 metric_type 改回原值；
--   新代码回滚到旧版本后这些 metric_type 会落回「不支持 → 不触发」，
--   不会产生新类型告警。

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

-- ============================================
-- 1) metric_type 归一：历史别名 → 规范指标名
-- ============================================

-- 旧的「网关 5xx」别名统一归到 gateway_5xx。
-- 语义变化：以前若有人把这些别名理解成「全部 5xx」，升级后只统计网关自产
-- （上游透传部分会进 upstream_5xx），这是本次拆分的预期效果。
UPDATE ops_alert_rules
SET metric_type = 'gateway_5xx',
    updated_at = NOW()
WHERE LOWER(TRIM(metric_type)) IN (
    'gateway5xx',
    'gateway_5xx_count',
    'gateway_5xx_total',
    'gateway_5xx_spike',
    'error_5xx_count',
    'total_5xx_count'
);

UPDATE ops_alert_rules
SET metric_type = 'upstream_5xx',
    updated_at = NOW()
WHERE LOWER(TRIM(metric_type)) IN (
    'upstream5xx',
    'upstream_5xx_count',
    'upstream_5xx_total'
);

-- ============================================
-- 2) 存量 5xx 规则补口径文案（只补空描述）
-- ============================================

UPDATE ops_alert_rules
SET description = '【网关自身 5xx】窗口内 status_code>=500 且 error_source<>upstream_http 的条数；上游透传 5xx 不计入本指标（见 upstream_5xx）。',
    updated_at = NOW()
WHERE metric_type = 'gateway_5xx'
  AND COALESCE(TRIM(description), '') = '';

UPDATE ops_alert_rules
SET description = '【上游 503 透传】窗口内 status_code>=500 且 error_source=upstream_http 的条数；渠道健康趋势指标，不代表网关自身故障，默认不触发紧急告警。',
    updated_at = NOW()
WHERE metric_type = 'upstream_5xx'
  AND COALESCE(TRIM(description), '') = '';

-- ============================================
-- 3) 种子默认规则（ON CONFLICT (name) DO NOTHING，已存在不覆盖）
-- ============================================

-- 3.1 网关自身 5xx：真故障指标，默认 P1 发邮件。
-- 旧默认硬阈值 3000/5min 是「全部 5xx」，拆分后只算网关自产，收紧到 500/5min。
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '网关自身 5xx 激增',
    '【网关自身 5xx】5 分钟内 status_code>=500 且 error_source<>upstream_http 的条数超过阈值时告警；上游透传 5xx 不计入本指标（见上游 5xx 观察规则）。',
    true, 'gateway_5xx', '>', 500.0, 5, 5, 'P1', true, 20, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

-- 3.2 上游 5xx 突增（观察）：当前 5 分钟条数 / 前 30 分钟中位数的倍数，
-- 降级为 P3 且默认不发邮件，只在 Ops 告警面板可见，避免上游抖动打爆值班邮箱。
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '上游 5xx 透传突增（观察）',
    '【上游 503 透传】当前 5 分钟 upstream_5xx 超过前 30 分钟中位数的 3 倍时记录观察事件；渠道健康指标，error_source=upstream_http，默认不发邮件，不代表网关自身故障。',
    true, 'upstream_5xx_spike_ratio', '>', 3.0, 5, 10, 'P3', false, 60, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

-- 3.3 上游 5xx 硬顶（观察）：极端上游故障（如整条上游链路 503）才触发，
-- 阈值明显高于正常流量下的上游透传量，仍不发紧急邮件。
INSERT INTO ops_alert_rules (
    name, description, enabled, metric_type, operator, threshold,
    window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes,
    created_at, updated_at
) VALUES (
    '上游 5xx 硬顶（观察）',
    '【上游 503 透传】5 分钟内 upstream_5xx 超过 30000 条（极端上游故障）时记录观察事件；默认不发邮件，仅用于渠道健康趋势。',
    true, 'upstream_5xx', '>', 30000.0, 5, 5, 'P2', false, 30, NOW(), NOW()
) ON CONFLICT (name) DO NOTHING;

COMMENT ON COLUMN ops_alert_rules.metric_type IS
    '告警指标类型。5xx 拆分后：gateway_5xx=status_code>=500 且 error_source<>upstream_http；upstream_5xx=status_code>=500 且 error_source=upstream_http；upstream_5xx_spike_ratio=上游 5xx 相对 30 分钟中位数的倍数。';
