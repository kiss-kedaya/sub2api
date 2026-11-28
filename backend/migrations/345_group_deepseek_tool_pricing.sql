-- Per-group DeepSeek tool pricing controls:
--   1. fetch_price_per_1k: dedicated price per 1,000 calls for /v1/tools/fetch and
--      hosted web_fetch (NULL inherits search_price_per_1k then official default;
--      0 = explicit free).
--   2. tool_rate_independent / tool_rate_multiplier: align tool endpoints with
--      image/video billing so tool calls follow group/user rate_multiplier by
--      default, with optional independent fixed/override multiplier.
--
-- Single hot table (groups) with bounded lock_timeout to prevent blocking or
-- deadlocking live queries during blue-green rollout.
SET LOCAL lock_timeout = '5s';

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS fetch_price_per_1k DECIMAL(20,8),
    ADD COLUMN IF NOT EXISTS tool_rate_independent BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS tool_rate_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0;

-- Preserve effective customer prices for existing DeepSeek groups that already
-- configured custom tool prices under the previous fixed-1x behavior (where
-- operators pre-multiplied prices by rate_multiplier). Groups with all tool
-- prices NULL stay tool_rate_independent=false so they immediately inherit
-- group/user rate_multiplier without overcharging.
UPDATE groups
   SET tool_rate_independent = true,
       tool_rate_multiplier = 1.0
 WHERE platform = 'deepseek'
   AND deleted_at IS NULL
   AND tool_rate_independent = false
   AND (
       search_price_per_1k IS NOT NULL
       OR fetch_price_per_1k IS NOT NULL
       OR ocr_price_per_1k_pages IS NOT NULL
       OR audio_tts_price_per_million_chars IS NOT NULL
       OR audio_stt_price_per_hour IS NOT NULL
   );

COMMENT ON COLUMN groups.fetch_price_per_1k IS '网页抓取工具价格 per 1,000 calls（DeepSeek /v1/tools/fetch 与托管 web_fetch）；NULL 回落 search_price_per_1k 再回落默认价，0 免费';
COMMENT ON COLUMN groups.tool_rate_independent IS '工具端点是否使用独立倍率；false 表示共享分组/用户有效倍率（不含高峰倍率）';
COMMENT ON COLUMN groups.tool_rate_multiplier IS '工具端点独立倍率，仅 tool_rate_independent=true 时生效';
