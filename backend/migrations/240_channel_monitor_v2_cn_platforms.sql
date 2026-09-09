-- Append kimi/zhipu/deepseek to channel_monitor_v2_config.platforms.
-- Factory seed (194/197) only listed the original six platforms, so CN
-- OpenAI-compatible providers never appeared in V2 settings or query scope.
-- Existing platform objects (enabled flags, model allow-lists) are left intact.

UPDATE channel_monitor_v2_config
SET
    platforms = (
        SELECT COALESCE(jsonb_agg(elem), '[]'::jsonb)
        FROM (
            SELECT elem
            FROM jsonb_array_elements(platforms) AS existing(elem)
            UNION ALL
            SELECT elem
            FROM jsonb_array_elements(
                '[{"platform":"kimi","enabled":true,"models":[]},{"platform":"zhipu","enabled":true,"models":[]},{"platform":"deepseek","enabled":true,"models":[]}]'::jsonb
            ) AS missing(elem)
            WHERE NOT EXISTS (
                SELECT 1
                FROM jsonb_array_elements(channel_monitor_v2_config.platforms) AS have(elem)
                WHERE lower(have.elem->>'platform') = lower(missing.elem->>'platform')
            )
        ) combined
    ),
    version = version + 1,
    updated_at = NOW()
WHERE id = 1
  AND (
      NOT (platforms @> '[{"platform":"kimi"}]'::jsonb)
      OR NOT (platforms @> '[{"platform":"zhipu"}]'::jsonb)
      OR NOT (platforms @> '[{"platform":"deepseek"}]'::jsonb)
  );
