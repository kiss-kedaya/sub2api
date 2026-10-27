const zh = {
  fallback: 'OAuth 额度优先与 API 后备', fallbackHint: '只在相同分组、优先级与协议层内优先使用质量合格、额度新鲜且有容量的 OAuth。缺失证据时沿用原序；API 账号继续作为后备。', minimumQuality: '最低质量通过率（0～1）', minimumQuota: '最低剩余额度（%）', maximumLoad: '最大负载（%）', admission: '新 OAuth 先检测再放行', admissionHint: '仅影响之后新建的 OAuth 账号。创建时先保持不可调度，随机题检测通过后才解除自己的准入暂停。失败或不确定不放行。', admissionPlatforms: '平台（英文逗号分隔）', admissionModel: '准入检测模型', scope: '隔离范围', scopeModel: '仅失败模型', scopeGroup: '指定组内模型，全部失败后摘组', scopeAccount: '整个账号', targetGroup: '目标分组',
  title: '账号自动化', description: '按需启用新账号模板、成功升并发和组内排序。', refresh: '刷新', enabled: '启用',
  oauth: 'OAuth 新账号默认模板', oauthHint: '只影响新建的 OAuth 账号。倍率、模型映射和凭据沿用原设置。',
  platform: '平台', concurrency: '初始并发', priority: '优先级', loadFactor: '负载因子', groups: '分组',
  ramp: '真实成功后逐步升并发', rampHint: '仅完整成功的对话累计。取消、断流、错误会进入冷却；人工修改并发会重置累计。',
  successes: '每步成功次数', step: '每步增加并发', maximum: '并发上限', cooldown: '冷却时间（秒）', advanced: '高级设置',
  ranking: '组内候选排序', rankingHint: '保留当前分组顺序、会话归属、协议优先和利润控制。样本不足沿用原顺序；请求链路不查询数据库。',
  mode: '偏好', experience: '体验优先', balanced: '均衡', cost: '成本优先', custom: '自定义', window: '统计窗口（分钟）',
  samples: '最少样本', quality_weight: '质量权重', latency_weight: '延迟权重', cost_weight: '成本权重', save: '保存',
  reasoning: '本地随机题质检', reasoningHint: '每次生成可验证答案的新题。只有启用计划或点击执行时才请求上游并产生费用。',
  judge: '判题方式', localJudge: '本地确定性比对', llmJudge: '独立 LLM 判题', jevJudge: 'Jev 原生选择判题', judgeGroup: '判题分组', judgeModel: '判题模型', judgeHint: 'LLM 使用指定分组并排除受测账号。Jev 复用已启用的内容审计凭据。判题不确定、限流或失败时只记录，不暂停或恢复账号。',
  newPlan: '新建计划', account: '账号', model: '模型', outcome: '最近结果', actions: '操作', yes: '是', no: '否', edit: '编辑',
  run: '执行一次', ownership: '暂停归属', interval: '间隔（分钟）', action: '不通过时', observe: '仅记录', pause: '暂停此账号',
  autoRestore: '通过后只解除本计划的暂停', owned: '本计划持有暂停', unowned: '没有持有暂停',
  ownershipHint: '解除前复核预览版本。人工改动、其他计划或掺水检测的暂停不会被覆盖。无法恢复时删除只放弃本计划归属，账号仍保持暂停。', restore: '解除本计划暂停', remove: '删除并放弃归属',
  rampSuspended: '升并发因队列或存储异常已安全暂停。排除异常后重新保存设置才会恢复。',
  passed: '通过', failed: '未通过', inconclusive: '不确定', saved: '已完成', failedMessage: '操作失败，请刷新后重试。'
}
const en: typeof zh = {
  fallback: 'OAuth quota preference and API fallback', fallbackHint: 'Prefer quality-verified OAuth accounts with fresh quota and available capacity only within the existing group, priority and protocol stratum. Missing evidence preserves the original order. API accounts remain fallback candidates.', minimumQuality: 'Minimum quality pass ratio (0–1)', minimumQuota: 'Minimum remaining quota (%)', maximumLoad: 'Maximum load (%)', admission: 'Test new OAuth accounts before admission', admissionHint: 'Applies only to subsequently created OAuth accounts. New accounts stay unschedulable until a randomized check passes. Failed or inconclusive checks never release the admission hold.', admissionPlatforms: 'Platforms (comma separated)', admissionModel: 'Admission test model', scope: 'Isolation scope', scopeModel: 'Failed model only', scopeGroup: 'Models in one group; detach only when all fail', scopeAccount: 'Entire account', targetGroup: 'Target group ID',
  title: 'Account automation', description: 'Opt in to new-account defaults, success ramping and within-group ranking.', refresh: 'Refresh', enabled: 'Enabled',
  oauth: 'New OAuth account defaults', oauthHint: 'Applies only to new OAuth accounts. Existing rates, mappings and credentials are preserved.',
  platform: 'Platform', concurrency: 'Initial concurrency', priority: 'Priority', loadFactor: 'Load factor', groups: 'Groups',
  ramp: 'Raise concurrency after genuine success', rampHint: 'Only complete successful conversations count. Cancellations, stream failures and errors trigger cooldown. Manual changes reset progress.',
  successes: 'Successes per step', step: 'Concurrency step', maximum: 'Maximum concurrency', cooldown: 'Cooldown (seconds)', advanced: 'Advanced settings',
  ranking: 'Within-group candidate ranking', rankingHint: 'Preserves group order, continuity, protocol preference and profit gates. Insufficient samples keep the existing order. Requests never query the database.',
  mode: 'Preference', experience: 'Experience', balanced: 'Balanced', cost: 'Cost', custom: 'Custom', window: 'Window (minutes)',
  samples: 'Minimum samples', quality_weight: 'Quality weight', latency_weight: 'Latency weight', cost_weight: 'Cost weight', save: 'Save',
  reasoning: 'Local randomized reasoning checks', reasoningHint: 'Each run generates a new question with a verified answer. Enabled plans and manual runs send paid upstream requests.',
  judge: 'Judge method', localJudge: 'Local deterministic comparison', llmJudge: 'Independent LLM judge', jevJudge: 'Native Jev choice judge', judgeGroup: 'Judge group ID', judgeModel: 'Judge model', judgeHint: 'LLM uses the specified group and excludes the tested account. Jev reuses enabled audit credentials. Uncertain, rate-limited or failed grading neither pauses nor restores an account.',
  newPlan: 'New plan', account: 'Account', model: 'Model', outcome: 'Latest outcome', actions: 'Actions', yes: 'Yes', no: 'No', edit: 'Edit',
  run: 'Run once', ownership: 'Pause ownership', interval: 'Interval (minutes)', action: 'On failure', observe: 'Observe only', pause: 'Pause account',
  autoRestore: 'Passing clears only this plan’s pause', owned: 'This plan owns a pause', unowned: 'No owned pause',
  ownershipHint: 'Release rechecks the versioned preview. Manual changes and other owners are retained. Deleting an unrestorable plan relinquishes only its marker and leaves the account paused.', restore: 'Release this pause', remove: 'Delete and relinquish',
  rampSuspended: 'Concurrency promotion paused after a queue or storage failure. Resolve the failure and explicitly save settings to resume.',
  passed: 'Passed', failed: 'Failed', inconclusive: 'Inconclusive', saved: 'Completed', failedMessage: 'Operation failed. Refresh and retry.'
}
export const opsAutomationMessages = { zh: { opsAutomation: zh }, en: { opsAutomation: en } }
