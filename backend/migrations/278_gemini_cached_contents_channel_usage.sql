-- Adapted from ranxi2001/sub2api @ 9620d315 (LGPL-3.0-or-later).
-- Gemini 显式缓存创建时的渠道计费口径（渠道 ID / 请求模型 / 计费模型来源），
-- 延长有效期按同一口径计费。
ALTER TABLE gemini_cached_contents
    ADD COLUMN IF NOT EXISTS channel_usage JSONB NOT NULL DEFAULT '{}'::jsonb;
