-- API 密钥级 Anthropic 缓存 TTL 注入覆盖。
--
-- 背景：管理员设置页已有全局开关「Anthropic 缓存 TTL 1h 注入」
-- (settings.enable_anthropic_cache_ttl_1h_injection)，但它是全站单值：
--   - 打开：所有 Anthropic 网关请求的 cache_control 断点都被改写成 1h
--   - 关闭：客户端自带什么就转发什么
-- 实际运营里同一个站上既有要 1h 的客户（长时间会话、Claude Code 长上下文），
-- 也有要 5m 的客户（短会话，1h 写入单价更高不划算）。全局单值满足不了。
--
-- 语义（每一把 API 密钥独立）：
--   inherit：跟随管理员全局设置（默认，保持存量行为）
--   off    ：本密钥不注入、也不改写 ttl
--   1h     ：把所有 ephemeral cache_control 断点强制改成 1h
--   5m     ：把所有 ephemeral cache_control 断点强制改成 5m
--
-- 生效条件：请求最终落在 Anthropic 平台分组，或智能路由解析出的目标平台是
-- Anthropic 时。非 Anthropic 目标的分组不做任何改写。
--
-- 与全局开关的冲突消解：密钥级设置优先于全局设置。密钥为 inherit 时才读全局值。
-- 密钥级只覆盖「注入与 ttl 取值」，不改变全局开关本身，也不会写回全局设置。
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS anthropic_cache_ttl_mode VARCHAR(16) NOT NULL DEFAULT 'inherit';

COMMENT ON COLUMN api_keys.anthropic_cache_ttl_mode IS
    'Per-key Anthropic cache_control TTL override: inherit|off|1h|5m. Applied only when the resolved target platform is anthropic.';
