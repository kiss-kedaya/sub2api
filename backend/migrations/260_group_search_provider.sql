-- 分组级「搜索专用凭据」。
--
-- DeepSeek 的 /v1/tools/search 与 /v1/tools/fetch 是按次计费的独立接口，
-- 原先只能用账号自己的上游 key。现在允许分组单独配一套搜索服务地址与 key，
-- 这样即使上游账号是别家（或不想让搜索走模型账号的额度），也能统一由
-- 一个搜索专用的 API 提供搜索能力。
--
-- 两列都可为空：空表示回落到账号自带的 base_url / api_key（旧行为）。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS search_provider_base_url TEXT,
    ADD COLUMN IF NOT EXISTS search_provider_api_key TEXT;
