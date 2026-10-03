-- 分组级跨协议转换开关。
--
-- 背景：网关历史上允许「入站协议 / 上游协议」任意交叉，例如客户端 /v1/responses
-- 打到只提供原生 Anthropic 端点的账号（上游 /v1/messages），中间要跑
-- Responses -> Anthropic 单向转换 + Anthropic 事件 -> Responses 事件回程转换。
-- 这类跨协议族的转换在工具调用、thinking 回传、usage 分桶上反复出问题，
-- 且上游官方实现（Wei-Shaw/sub2api）与本仓库的实现已经产生分歧。
--
-- 语义：
--   false：禁止 anthropic 协议族 <-> OpenAI 协议族 之间的互转。
--          入站 /v1/messages 只能落在 Anthropic 类上游；入站 /v1/responses、
--          /v1/chat/completions 只能落在 OpenAI 类上游。命中不到兼容账号时
--          返回明确的 4xx，而不是静默转换。
--   true ：恢复既有行为，允许两个协议族之间互转。
--
-- 注意：OpenAI 协议族内部的 /v1/chat/completions <-> /v1/responses 互转
-- （Codex OAuth 账号、国产 OpenAI 兼容上游）不受本开关影响，属上游既有设计。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS cross_protocol_conversion_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- 存量分组回填为 true（= 保持升级前的实际行为），新建分组才是 false。
--
-- 为什么不能直接让所有分组都为 false：按升级前 7 天的真实用量，跨协议族转换
-- 覆盖 kiro(组10) 约 7.1 万笔/天、Zhipu(组38) 2.9 万、Grok-25 2.6 万、
-- 国模(组52) 1.4 万、DeepSeek(组45) 0.9 万等，合计约 17 万笔/天。
-- 若升级瞬间这些分组一起变 false，这部分请求会立刻返回 400
-- cross_protocol_conversion_disabled，属于用户可见的故障。
-- 回填成 true 后，升级本身不改变任何分组的对外行为；要收紧就在后台
-- 「分组编辑」里逐个关掉，一次只影响一个分组。
--
-- 用 IS NOT NULL 之外的条件写法是为了让本迁移可重复执行（幂等）：
-- ADD COLUMN IF NOT EXISTS 已存在时不会改值，回填仅作用于本次新建的列。
-- 判定方式取 DEFAULT FALSE 之外的显式条件：只有当列刚刚被加出来（所有行都是
-- 默认值 false）时才整体回填，避免重复执行把管理员后来手动的改动覆盖掉。
DO $$
BEGIN
    -- 仅当整张表没有任何行为 true 时执行回填。管理员手工开启过任一分组后，
    -- 重跑本迁移不会再动数据。
    IF NOT EXISTS (SELECT 1 FROM groups WHERE cross_protocol_conversion_enabled) THEN
        UPDATE groups SET cross_protocol_conversion_enabled = TRUE;
    END IF;
END $$;

COMMENT ON COLUMN groups.cross_protocol_conversion_enabled IS
    'Allow Anthropic protocol family <-> OpenAI protocol family conversion for this group. Existing groups are backfilled to true (pre-upgrade behaviour); new groups default to false. Intra-family chat<->responses conversion is unaffected.';
