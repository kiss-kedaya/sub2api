-- 定时测试默认保持官方行为（普通连通性测试）；打开 quality_check_enabled
-- 的计划才走降智检测（生成 + 视觉评估 + 连续退化暂停/恢复）。
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS quality_check_enabled BOOLEAN NOT NULL DEFAULT false;

-- 已存在的计划若保存的是官方降智检测题面快照（246 迁移里的旧题面或当前默认题面），
-- 视为已经在做降智检测，保持原有行为不变。
UPDATE scheduled_test_plans
SET quality_check_enabled = true
WHERE md5(prompt_text) IN (
    '15caede62ffea7c47ee2ccf2c8674e87',
    '297caf9418b914f791fe7df9ef44f58e'
);
