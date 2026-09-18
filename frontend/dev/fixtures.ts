import type {
  AccountListItem,
  AccountUsageInfo,
  AdminGroup,
  AdminUsageLog,
  AdminUser,
  ApiKey,
  Group,
  Proxy,
  PublicSettings,
  SubscriptionProgress,
  UsageLog,
  User,
  UserAffiliateDetail,
  UserSubscription,
  WindowStats,
} from '../src/types'
import type {
  MonitorConfig,
  MonitorCoverage,
  MonitorDimensions,
  MonitorHealth,
  MonitorMatrixRow,
  MonitorMetric,
  MonitorModelRow,
  MonitorSnapshot,
} from '../src/api/channelMonitorV2'
import type { UserMonitorDetail, UserMonitorView } from '../src/api/channelMonitor'
import type { UserAvailableChannel } from '../src/api/channels'
import type { CFAllowlistStatus } from '../src/api/cfAllowlist'
import type { RedeemHistoryItem } from '../src/api/redeem'
import type {
  CheckoutInfoResponse,
  PaymentConfig,
  PaymentOrder,
  SubscriptionPlan,
} from '../src/types/payment'

export const PREVIEW_LABEL = '本地预览 · 演示数据'
export const DEMO_TOKEN = 'local-console-demo-not-a-real-token'
export const DEMO_USER_ID = 900001
const DAY = 86_400_000

export function createFixtures(now = new Date()) {
  const timestamp = now.toISOString()
  const user: User = {
    id: DEMO_USER_ID, username: '演示用户', email: 'console-preview@example.test',
    role: 'user', status: 'active', balance: 128.64, concurrency: 8,
    allowed_groups: null, balance_notify_enabled: false, balance_notify_threshold: null,
    balance_notify_extra_emails: [], created_at: timestamp, updated_at: timestamp,
  }
  const groups: Group[] = [
    ['Claude · 演示', 'anthropic'], ['GPT · 演示', 'openai'],
    ['Gemini · 演示', 'gemini'], ['Grok · 演示', 'grok'],
    ['DeepSeek · 演示', 'deepseek'], ['Kimi · 演示', 'kimi'],
  ].map(([name, platform], i) => ({
    id: i + 1, name, platform: platform as Group['platform'], description: '本地生成的演示分组',
    rate_multiplier: 1, is_exclusive: false, status: 'active', subscription_type: i < 3 ? 'subscription' : 'standard',
    daily_limit_usd: i < 3 ? 12 + i * 4 : null, weekly_limit_usd: i < 3 ? 60 + i * 20 : null,
    monthly_limit_usd: i < 3 ? 180 + i * 60 : null,
    long_context_pricing_enabled: false, allow_image_generation: false,
    allow_batch_image_generation: false, image_rate_independent: false, image_rate_multiplier: 1,
    batch_image_discount_multiplier: 1, batch_image_hold_multiplier: 1,
    image_price_1k: null, image_price_2k: null, image_price_4k: null,
    video_rate_independent: false, video_rate_multiplier: 1,
    video_price_480p: null, video_price_720p: null, video_price_1080p: null,
    web_search_price_per_call: null, search_price_per_1k: null,
    audio_realtime_price_per_min: null, audio_tts_price_per_million_chars: null,
    audio_stt_price_per_hour: null, peak_rate_enabled: false, peak_start: '', peak_end: '',
    peak_rate_multiplier: 1, claude_code_only: false, fallback_group_id: null,
    fallback_group_id_on_invalid_request: null, allow_live: false,
    require_oauth_only: false, require_privacy_set: false, created_at: timestamp, updated_at: timestamp,
  }))
  const keys: ApiKey[] = Array.from({ length: 26 }, (_, i) => makeKey(i + 1, groups[i % groups.length], timestamp))
  const models = ['claude-sonnet-4-5', 'gpt-5', 'gemini-2.5-pro', 'grok-4', 'deepseek-chat', 'kimi-k2']
  const usage: UsageLog[] = []
  const midnight = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate())
  for (let day = 44; day >= 0; day--) {
    const count = 36 + Math.round(15 * (1 + Math.sin(day * 0.63))) + (44 - day)
    for (let j = 0; j < count; j++) {
      const created = day === 0
        ? now.getTime() - (count - 1 - j) * 5 * 60_000
        : midnight - day * DAY + Math.floor(j * DAY / count)
      if (created > now.getTime()) continue
      const seed = day * 173 + j * 29
      const key = keys[seed % keys.length]
      const group = key.group!
      const input = 2800 + seed % 17500
      const output = 500 + seed % 4300
      const cacheRead = 1600 + seed % 8200
      const cacheCreation = seed % 900
      const inputCost = input * 3 / 1_000_000
      const outputCost = output * 12 / 1_000_000
      const cacheReadCost = cacheRead * 0.3 / 1_000_000
      const cacheCreationCost = cacheCreation * 3.75 / 1_000_000
      const cost = inputCost + outputCost + cacheReadCost + cacheCreationCost
      const stream = j % 4 !== 0
      usage.push({
        id: usage.length + 1, user_id: user.id, api_key_id: key.id, account_id: null,
        request_id: `demo-request-${day}-${j}`, model: models[group.id - 1],
        inbound_endpoint: group.platform === 'anthropic' ? '/v1/messages' : '/v1/responses',
        upstream_endpoint: null, group_id: group.id, subscription_id: null,
        input_tokens: input, output_tokens: output, cache_creation_tokens: cacheCreation,
        cache_read_tokens: cacheRead, cache_creation_5m_tokens: cacheCreation, cache_creation_1h_tokens: 0,
        input_cost: inputCost, output_cost: outputCost, cache_creation_cost: cacheCreationCost,
        cache_read_cost: cacheReadCost, total_cost: cost, actual_cost: cost,
        rate_multiplier: 1, long_context_billing_applied: false, billing_type: 0,
        request_type: stream ? 'stream' : 'sync', stream, native_compaction_v2: false,
        duration_ms: 450 + seed % 2100, first_token_ms: stream ? 95 + seed % 380 : null,
        image_count: 0, image_size: null, image_input_size: null, image_output_size: null,
        image_size_source: null, image_size_breakdown: null, image_input_tokens: 0, image_input_cost: 0,
        image_output_tokens: 0, image_output_cost: 0, user_agent: 'ConsolePreview/DEMO',
        ip_address: '192.0.2.10', cache_ttl_overridden: false, billing_mode: 'token',
        created_at: new Date(created).toISOString(), api_key: key, group,
      })
    }
  }
  for (const key of keys) {
    const rows = usage.filter(row => row.api_key_id === key.id)
    key.quota_used = rows.reduce((sum, row) => sum + row.actual_cost, 0)
    key.last_used_at = rows.at(-1)?.created_at ?? null
  }
  return {
    user,
    groups,
    keys,
    usage,
    models,
    now: timestamp,
    ...createPageFixtures(now, user, groups),
    ...createAdminFixtures(now, groups, keys, usage),
  }
}

export function makeKey(id: number, group: Group | undefined, timestamp: string): ApiKey {
  return {
    id, user_id: DEMO_USER_ID, key: `sk-demo-not-valid-${String(id).padStart(6, '0')}`,
    name: `演示密钥 ${String(id).padStart(2, '0')}`, group_id: group?.id ?? null,
    group_ids: group ? [group.id] : [], group, status: id % 7 === 0 ? 'inactive' : 'active',
    ip_whitelist: [], ip_blacklist: [], last_used_at: null, last_used_ip: null,
    quota: 100, quota_used: 0, expires_at: null, created_at: timestamp, updated_at: timestamp,
    current_concurrency: 0, rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0,
    usage_5h: 0, usage_1d: 0, usage_7d: 0, window_5h_start: null, window_1d_start: null,
    window_7d_start: null, reset_5h_at: null, reset_1d_at: null, reset_7d_at: null,
  }
}

function createAdminFixtures(now: Date, groups: Group[], sourceKeys: ApiKey[], sourceUsage: UsageLog[]) {
  const at = (offsetMs: number) => new Date(now.getTime() + offsetMs).toISOString()
  const adminUsers: AdminUser[] = Array.from({ length: 34 }, (_, index) => {
    const id = 910001 + index
    const disabled = index % 11 === 8
    return {
      id,
      username: index === 0 ? '本地预览管理员' : `演示用户 ${String(index).padStart(2, '0')}`,
      email: index === 0 ? 'admin-preview@example.test' : `demo-user-${String(index).padStart(2, '0')}@example.test`,
      role: index === 0 || index === 17 ? 'admin' : 'user',
      status: disabled ? 'disabled' : 'active',
      balance: Math.round((24 + (index * 19.37) % 360) * 100) / 100,
      frozen_balance: index % 7 === 0 ? 3.2 : 0,
      concurrency: 2 + index % 10,
      rpm_limit: index % 5 === 0 ? 120 : 0,
      allowed_groups: index % 4 === 0 ? [groups[index % groups.length].id] : null,
      restrict_public_groups: index % 4 === 0,
      balance_notify_enabled: false,
      balance_notify_threshold: null,
      balance_notify_extra_emails: [],
      notes: `${PREVIEW_LABEL}，无真实账户`,
      current_concurrency: disabled ? 0 : index % 4,
      last_active_at: disabled ? at(-(index + 8) * DAY) : at(-(index % 18) * 60 * 60_000),
      last_used_at: disabled ? null : at(-(index % 20) * 45 * 60_000),
      created_at: at(-(index + 5) * DAY),
      updated_at: at(-(index % 6) * 60 * 60_000),
    }
  })

  const adminGroups: AdminGroup[] = groups.map((group, index) => ({
    ...group,
    name: `${group.name}（后台预览）`,
    description: `${PREVIEW_LABEL}，价格和容量均为虚构数据`,
    rpm_limit: 180 + index * 40,
    force_openai_fast: group.platform === 'openai',
    free_openai_fast: false,
    model_pricing: [],
    profit_control_enabled: false,
    profit_min_margin: 0.08,
    profit_safety_buffer: 0.03,
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    account_count: 3,
    active_account_count: index === 3 ? 2 : 3,
    rate_limited_account_count: index === 3 ? 1 : 0,
    model_allowlist: { enabled: true, models: [
      ['claude-sonnet-4-5'], ['gpt-5', 'gpt-5-mini'], ['gemini-2.5-pro'],
      ['grok-4'], ['deepseek-chat'], ['kimi-k2'],
    ][index] ?? [] },
    sort_order: (index + 1) * 10,
  }))

  const adminAccounts: AccountListItem[] = Array.from({ length: 24 }, (_, index) => {
    const group = adminGroups[index % adminGroups.length]
    const status: AccountListItem['status'] = index % 9 === 7 ? 'error' : index % 8 === 6 ? 'inactive' : 'active'
    return {
      id: 920001 + index,
      name: `${group.platform.toUpperCase()} 演示账号 ${String(index + 1).padStart(2, '0')}`,
      notes: `${PREVIEW_LABEL}，不含真实凭据`,
      platform: group.platform === 'composite' ? 'openai' : group.platform,
      type: index % 3 === 0 ? 'oauth' : index % 3 === 1 ? 'apikey' : 'upstream',
      credentials_status: { has_access_token: false, has_api_key: false },
      proxy_id: index % 3 === 0 ? 930001 : null,
      concurrency: 4 + index % 8,
      current_concurrency: status === 'active' ? index % 4 : 0,
      priority: 10 + index % 4,
      rate_multiplier: 0.82 + (index % 5) * 0.07,
      load_factor: 1,
      status,
      error_message: status === 'error' ? '本地演示：上游返回 429' : null,
      last_used_at: status === 'inactive' ? null : at(-(index % 16) * 20 * 60_000),
      expires_at: null,
      auto_pause_on_expired: true,
      created_at: at(-(index + 20) * DAY),
      updated_at: at(-(index % 9) * 30 * 60_000),
      group_ids: [group.id],
      schedulable: status === 'active',
      rate_limited_at: index % 10 === 5 ? at(-20 * 60_000) : null,
      rate_limit_reset_at: index % 10 === 5 ? at(40 * 60_000) : null,
      overload_until: null,
      temp_unschedulable_until: status === 'error' ? at(15 * 60_000) : null,
      temp_unschedulable_reason: status === 'error' ? '本地演示限流' : null,
      session_window_start: at(-2 * 60 * 60_000),
      session_window_end: at(3 * 60 * 60_000),
      session_window_status: status === 'active' ? 'allowed' : null,
      extra: { preview: true, plan_type: index % 2 === 0 ? 'team' : 'pro' },
      scheduler_score: { base_score: 0.91 - (index % 7) * 0.07, sticky_weighted_enabled: true },
    }
  })

  const adminKeys: ApiKey[] = sourceKeys.map((key, index) => {
    const owner = adminUsers[index % adminUsers.length]
    const group = adminGroups[(key.group_id ?? 1) - 1]
    return {
      ...key,
      id: 940001 + index,
      user_id: owner.id,
      name: `后台演示密钥 ${String(index + 1).padStart(2, '0')}`,
      key: `sk-admin-demo-not-valid-${String(index + 1).padStart(6, '0')}`,
      group,
    }
  })

  const adminUsage: AdminUsageLog[] = sourceUsage.map((row, index) => {
    const apiKey = adminKeys[index % adminKeys.length]
    const owner = adminUsers.find(item => item.id === apiKey.user_id) ?? adminUsers[0]
    const account = adminAccounts[index % adminAccounts.length]
    const group = adminGroups[(row.group_id ?? 1) - 1]
    return {
      ...row,
      id: 950001 + index,
      user_id: owner.id,
      api_key_id: apiKey.id,
      account_id: account.id,
      request_id: `admin-preview-request-${index + 1}`,
      upstream_request_id: `upstream-demo-${index + 1}`,
      upstream_model: row.model,
      upstream_response_model: row.model,
      upstream_model_mismatch: false,
      account_rate_multiplier: account.rate_multiplier ?? 1,
      account_stats_cost: row.total_cost * (account.rate_multiplier ?? 1),
      user: owner,
      api_key: apiKey,
      account: { id: account.id, name: account.name },
      group,
    }
  })

  const adminProxies: Proxy[] = [
    {
      id: 930001, name: `东京演示出口 · ${PREVIEW_LABEL}`, protocol: 'https', host: '192.0.2.20', port: 8443,
      username: null, status: 'active', account_count: 8, latency_ms: 68, latency_status: 'success',
      ip_address: '192.0.2.20', country: 'Japan', country_code: 'JP', region: 'Tokyo', city: 'Tokyo',
      expires_at: null, fallback_mode: 'none', expiry_warn_days: 7, created_at: at(-90 * DAY), updated_at: at(-10 * 60_000),
    },
  ]

  const usageByAccount: Record<string, AccountUsageInfo> = Object.fromEntries(adminAccounts.map((account, index) => [String(account.id), {
    source: 'passive',
    updated_at: at(-(index % 5) * 60_000),
    five_hour: { utilization: 18 + index % 65, resets_at: at((index % 5 + 1) * 60 * 60_000), remaining_seconds: (index % 5 + 1) * 3600 },
    seven_day: { utilization: 24 + index % 68, resets_at: at((index % 5 + 1) * DAY), remaining_seconds: (index % 5 + 1) * 86_400 },
    seven_day_sonnet: null,
  }]))
  const todayStatsByAccount: Record<string, WindowStats> = Object.fromEntries(adminAccounts.map((account, index) => [String(account.id), {
    requests: 42 + index * 7,
    tokens: 84_000 + index * 11_000,
    cost: Math.round((2.6 + index * 0.37) * 100) / 100,
  }]))

  return { adminUsers, adminGroups, adminAccounts, adminKeys, adminUsage, adminProxies, usageByAccount, todayStatsByAccount }
}

function createPageFixtures(now: Date, user: User, groups: Group[]) {
  const at = (offsetMs: number) => new Date(now.getTime() + offsetMs).toISOString()
  const monitorThresholds = {
    minimum_sample: 20,
    warning_error_rate: 0.03,
    critical_error_rate: 0.15,
    target_ttft_ms: 800,
    warning_ttft_ms: 1800,
    critical_ttft_ms: 4000,
    warning_cache_rate: 0.2,
    critical_cache_rate: 0.05,
    error_weight: 0.5,
    ttft_weight: 0.35,
    cache_weight: 0.15,
  }
  const makeMetric = (requestCount: number, errorRate: number, ttft: number | null, cacheRate: number): MonitorMetric => {
    const errors = Math.round(requestCount * errorRate)
    const sampleCount = requestCount > 0 ? requestCount : 0
    return {
      success_requests: requestCount - errors,
      error_requests: errors,
      request_count: requestCount,
      token_count: requestCount * 3820,
      rpm: requestCount / 90,
      tpm: requestCount * 42,
      error_rate: errorRate,
      cache_rate: cacheRate,
      cache_rate_numerator: Math.round(requestCount * cacheRate),
      cache_rate_denominator: requestCount,
      ttft: { sample_count: sampleCount, p50_ms: ttft, p90_ms: ttft == null ? null : Math.round(ttft * 1.55), p95_ms: ttft == null ? null : Math.round(ttft * 1.8), avg_ms: ttft == null ? null : Math.round(ttft * 1.12) },
      duration: { sample_count: sampleCount, p50_ms: ttft == null ? null : ttft + 1380, p90_ms: ttft == null ? null : ttft + 2460, p95_ms: ttft == null ? null : ttft + 3180, avg_ms: ttft == null ? null : ttft + 1640 },
      upstream_affected_requests: errors,
      upstream_attempt_count: requestCount + Math.round(errors * 0.6),
    }
  }
  const makeHealth = (overall: MonitorHealth['overall'], score: number | null): MonitorHealth => ({
    overall,
    error_rate: overall,
    ttft: overall === 'unknown' ? 'unknown' : overall === 'critical' ? 'critical' : overall,
    cache: overall === 'unknown' ? 'unknown' : overall === 'critical' ? 'warning' : 'healthy',
    score,
    error_rate_score: score,
    ttft_score: score == null ? null : Math.max(0, score - 4),
    cache_score: score == null ? null : Math.min(100, score + 3),
    minimum_sample: monitorThresholds.minimum_sample,
    thresholds: monitorThresholds,
  })
  const monitorCases = [
    { platform: 'openai', group: groups[1], model: 'gpt-5', state: 'healthy', score: 98, count: 1260, error: 0.006, ttft: 540, cache: 0.46 },
    { platform: 'anthropic', group: groups[0], model: 'claude-sonnet-4-5', state: 'healthy', score: 94, count: 980, error: 0.012, ttft: 720, cache: 0.58 },
    { platform: 'gemini', group: groups[2], model: 'gemini-2.5-pro', state: 'warning', score: 67, count: 760, error: 0.064, ttft: 2350, cache: 0.18 },
    { platform: 'grok', group: groups[3], model: 'grok-4', state: 'critical', score: 18, count: 410, error: 0.73, ttft: 6250, cache: 0.04 },
    { platform: 'deepseek', group: groups[4], model: 'deepseek-chat', state: 'unknown', score: null, count: 0, error: 0, ttft: null, cache: 0 },
  ] as const
  const monitorMatrixRows: MonitorMatrixRow[] = monitorCases.map((entry, index) => {
    const metrics = makeMetric(entry.count, entry.error, entry.ttft, entry.cache)
    const health = makeHealth(entry.state, entry.score)
    const buckets = Array.from({ length: 30 }, (_, bucketIndex) => {
      const unavailable = entry.state === 'critical' && bucketIndex > 22
      const bucketError = unavailable ? Math.min(0.98, entry.error + (bucketIndex - 22) * 0.035) : entry.error
      const bucketMetrics = makeMetric(
        entry.count === 0 ? 0 : Math.max(8, Math.round(entry.count / 30 + ((bucketIndex + index) % 7) - 3)),
        bucketError,
        entry.ttft == null ? null : entry.ttft + ((bucketIndex % 5) - 2) * 55,
        entry.cache,
      )
      return {
        bucket_start: at(-(29 - bucketIndex) * 5 * 60_000),
        metrics: bucketMetrics,
        health: makeHealth(entry.state, entry.score),
      }
    })
    return {
      platform: entry.platform,
      group_id: entry.group?.id,
      group_name: entry.group?.name,
      metrics,
      health,
      buckets,
    }
  })
  const monitorCoverage: MonitorCoverage = {
    requested_start: at(-90 * 60_000),
    requested_end: now.toISOString(),
    coverage_start: at(-30 * DAY),
    data_through: at(-45_000),
    computed_at: now.toISOString(),
    aggregation_lag_seconds: 45,
    coverage_complete: true,
    bucket_seconds: 300,
    bootstrap: null,
  }
  const monitorConfig: MonitorConfig = {
    version: 2,
    enabled: true,
    refresh_interval_seconds: 60,
    platforms: monitorCases.map(entry => ({ platform: entry.platform, enabled: true, models: [entry.model] })),
    group_ids: monitorCases.flatMap(entry => entry.group ? [entry.group.id] : []),
    health_thresholds: monitorThresholds,
    ignored_error_categories: ['client_cancelled', 'content_policy'],
  }
  const monitorSnapshot: MonitorSnapshot = {
    config: monitorConfig,
    coverage: monitorCoverage,
    metrics: makeMetric(3410, 0.073, 880, 0.41),
    health: makeHealth('warning', 76),
    trend: Array.from({ length: 30 }, (_, index) => ({
      bucket_start: at(-(29 - index) * 5 * 60_000),
      metrics: makeMetric(105 + index % 13, index > 22 ? 0.11 : 0.035, 760 + (index % 6) * 35, 0.41),
      health: makeHealth(index > 22 ? 'warning' : 'healthy', index > 22 ? 72 : 91),
    })),
  }
  const monitorDimensions: MonitorDimensions = {
    platforms: monitorCases.map(entry => ({ value: entry.platform, label: entry.platform, request_count: entry.count })),
    groups: monitorCases.flatMap(entry => entry.group ? [{ id: entry.group.id, name: entry.group.name, platform: entry.platform, request_count: entry.count }] : []),
    models: monitorCases.map(entry => ({ value: entry.model, label: entry.model, platform: entry.platform, request_count: entry.count })),
  }
  const monitorModels: MonitorModelRow[] = monitorCases.map(entry => ({
    platform: entry.platform,
    model: entry.model,
    metrics: makeMetric(entry.count, entry.error, entry.ttft, entry.cache),
    health: makeHealth(entry.state, entry.score),
  }))
  const monitorViews: UserMonitorView[] = monitorCases.map((entry, index) => ({
    id: 7001 + index,
    name: `${entry.group?.name ?? entry.platform} · ${PREVIEW_LABEL}`,
    provider: entry.platform,
    group_name: entry.group?.name ?? '',
    primary_model: entry.model,
    primary_status: entry.state === 'healthy' ? 'operational' : entry.state === 'warning' ? 'degraded' : entry.state === 'unknown' ? 'error' : 'failed',
    primary_latency_ms: entry.ttft,
    primary_ping_latency_ms: entry.ttft == null ? null : Math.max(35, Math.round(entry.ttft * 0.16)),
    availability_7d: entry.state === 'healthy' ? 99.92 - index * 0.08 : entry.state === 'warning' ? 96.4 : entry.state === 'critical' ? 71.8 : 0,
    extra_models: [{ model: `${entry.model}-mini`, status: entry.state === 'healthy' ? 'operational' : 'degraded', latency_ms: entry.ttft == null ? null : Math.round(entry.ttft * 0.72) }],
    timeline: Array.from({ length: 30 }, (_, point) => ({
      status: entry.state === 'healthy' ? 'operational' : entry.state === 'warning' ? 'degraded' : entry.state === 'unknown' ? 'error' : point > 22 ? 'failed' : 'degraded',
      latency_ms: entry.ttft == null ? null : entry.ttft + (point % 4) * 40,
      ping_latency_ms: entry.ttft == null ? null : Math.round(entry.ttft * 0.16),
      checked_at: at(-(29 - point) * 60 * 60_000),
    })),
    latest_quota: index === 0 ? {
      source: 'usage', success: true, plan_level: 'demo', fetched_at: at(-60_000),
      tiers: [{ window: '5h', used_percent: 42, reset_at: at(2 * 60 * 60_000) }, { window: '7d', used_percent: 68, reset_at: at(4 * DAY) }],
    } : null,
  }))
  const monitorDetails: Record<number, UserMonitorDetail> = Object.fromEntries(monitorViews.map(item => [item.id, {
    id: item.id,
    name: item.name,
    provider: item.provider,
    group_name: item.group_name,
    models: [item.primary_model, ...item.extra_models.map(model => model.model)].map((model, index) => ({
      model,
      latest_status: index === 0 ? item.primary_status : item.extra_models[index - 1].status,
      latest_latency_ms: index === 0 ? item.primary_latency_ms : item.extra_models[index - 1].latency_ms,
      availability_7d: Math.max(0, item.availability_7d - index * 0.4),
      availability_15d: Math.max(0, item.availability_7d - index * 0.5 - 0.2),
      availability_30d: Math.max(0, item.availability_7d - index * 0.6 - 0.5),
      avg_latency_7d_ms: item.primary_latency_ms,
    })),
  }]))

  const tokenPricing = (input: number, output: number) => ({
    billing_mode: 'token' as const,
    input_price: input,
    output_price: output,
    cache_write_price: input * 1.25,
    cache_write_1h_price: input * 2,
    cache_read_price: input * 0.1,
    max_reasoning_effort_multiplier: 1.5,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
  })
  const availableChannels: UserAvailableChannel[] = [
    {
      name: `全球高速线路 · ${PREVIEW_LABEL}`,
      description: '多平台低延迟演示线路，价格仅用于本地页面预览。',
      platforms: [
        { platform: 'openai', groups: [groups[1]].map(group => ({ ...group, is_exclusive: false })), supported_models: [{ name: 'gpt-5', platform: 'openai', pricing: tokenPricing(1.25, 10) }, { name: 'gpt-5-mini', platform: 'openai', pricing: tokenPricing(0.25, 2) }] },
        { platform: 'anthropic', groups: [groups[0]].map(group => ({ ...group, is_exclusive: false })), supported_models: [{ name: 'claude-sonnet-4-5', platform: 'anthropic', pricing: tokenPricing(3, 15) }] },
      ],
    },
    {
      name: `亚太稳定线路 · ${PREVIEW_LABEL}`,
      description: '包含健康与慢响应案例，方便验收状态和价格展示。',
      platforms: [
        { platform: 'gemini', groups: [groups[2]].map(group => ({ ...group, is_exclusive: true })), supported_models: [{ name: 'gemini-2.5-pro', platform: 'gemini', pricing: tokenPricing(1.25, 10) }] },
        { platform: 'deepseek', groups: [groups[4]].map(group => ({ ...group, is_exclusive: false })), supported_models: [{ name: 'deepseek-chat', platform: 'deepseek', pricing: tokenPricing(0.27, 1.1) }] },
      ],
    },
  ]

  const paymentPlans: SubscriptionPlan[] = [
    { id: 8101, group_id: groups[0].id, group_platform: groups[0].platform, group_name: groups[0].name, rate_multiplier: 1, daily_limit_usd: 12, weekly_limit_usd: 60, monthly_limit_usd: 180, name: 'Claude 月度演示套餐', description: '本地预览套餐，不会产生真实扣款。', price: 29, original_price: 39, currency: 'USD', validity_days: 30, validity_unit: 'day', features: ['每日 $12 演示额度', '到期自动失效', '仅本地预览'], for_sale: true, sort_order: 1 },
    { id: 8102, group_id: groups[1].id, group_platform: groups[1].platform, group_name: groups[1].name, rate_multiplier: 1, daily_limit_usd: 16, weekly_limit_usd: 80, monthly_limit_usd: 240, name: 'GPT 季度演示套餐', description: '包含额度与有效期展示。', price: 79, original_price: 99, currency: 'USD', validity_days: 90, validity_unit: 'day', features: ['每日 $16 演示额度', '90 天有效', '仅本地预览'], for_sale: true, sort_order: 2 },
  ]
  const checkoutInfo: CheckoutInfoResponse = {
    methods: {
      alipay: { currency: 'CNY', display_name: '支付宝（本地演示）', daily_limit: 5000, daily_used: 286, daily_remaining: 4714, single_min: 10, single_max: 1000, fee_rate: 0, available: true, recharge_fee_rate: 0, balance_recharge_multiplier: 1 },
      wxpay: { currency: 'CNY', display_name: '微信支付（本地演示）', daily_limit: 3000, daily_used: 120, daily_remaining: 2880, single_min: 10, single_max: 800, fee_rate: 0.6, available: true, recharge_fee_rate: 0.6, balance_recharge_multiplier: 1 },
      epusdt: { currency: 'USDT', display_name: 'USDT（本地演示）', daily_limit: 2000, daily_used: 45, daily_remaining: 1955, single_min: 5, single_max: 500, fee_rate: 0, available: true, recharge_fee_rate: 0, balance_recharge_multiplier: 1 },
    },
    global_min: 5,
    global_max: 1000,
    plans: paymentPlans,
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 7.2,
    recharge_fee_rate: 0,
    help_text: `**${PREVIEW_LABEL}**：所有订单只保存在当前预览进程，不会跳转第三方或真实扣款。`,
    help_image_url: '',
    stripe_publishable_key: '',
    alipay_force_qrcode: true,
  }
  const paymentConfig: PaymentConfig = {
    payment_enabled: true,
    min_amount: checkoutInfo.global_min,
    max_amount: checkoutInfo.global_max,
    daily_limit: 5000,
    max_pending_orders: 5,
    order_timeout_minutes: 30,
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 7.2,
    enabled_payment_types: ['alipay', 'wxpay', 'epusdt'],
    help_image_url: '',
    help_text: checkoutInfo.help_text,
    stripe_publishable_key: '',
  }
  const paymentOrders: PaymentOrder[] = [
    { id: 51006, user_id: user.id, amount: 50, pay_amount: 50, currency: 'CNY', fee_rate: 0, payment_type: 'alipay', out_trade_no: 'LOCAL-DEMO-20260918-006', status: 'PENDING', order_type: 'balance', created_at: at(-8 * 60_000), expires_at: at(22 * 60_000), refund_amount: 0, provider_instance_id: 'local-demo-alipay' },
    { id: 51005, user_id: user.id, amount: 100, pay_amount: 100.6, currency: 'CNY', fee_rate: 0.6, payment_type: 'wxpay', out_trade_no: 'LOCAL-DEMO-20260918-005', status: 'COMPLETED', order_type: 'balance', created_at: at(-2 * DAY), expires_at: at(-2 * DAY + 30 * 60_000), paid_at: at(-2 * DAY + 3 * 60_000), completed_at: at(-2 * DAY + 4 * 60_000), refund_amount: 0, provider_instance_id: 'local-demo-wxpay' },
    { id: 51004, user_id: user.id, amount: 30, pay_amount: 30, currency: 'USDT', fee_rate: 0, payment_type: 'epusdt', out_trade_no: 'LOCAL-DEMO-20260918-004', status: 'COMPLETED', order_type: 'balance', created_at: at(-5 * DAY), expires_at: at(-5 * DAY + 30 * 60_000), paid_at: at(-5 * DAY + 6 * 60_000), completed_at: at(-5 * DAY + 7 * 60_000), refund_amount: 0, provider_instance_id: 'local-demo-epusdt' },
    { id: 51003, user_id: user.id, amount: 29, pay_amount: 208.8, currency: 'CNY', fee_rate: 0, payment_type: 'alipay', out_trade_no: 'LOCAL-DEMO-20260918-003', status: 'COMPLETED', order_type: 'subscription', plan_id: 8101, created_at: at(-12 * DAY), expires_at: at(-12 * DAY + 30 * 60_000), paid_at: at(-12 * DAY + 2 * 60_000), completed_at: at(-12 * DAY + 3 * 60_000), refund_amount: 0, provider_instance_id: 'local-demo-alipay' },
    { id: 51002, user_id: user.id, amount: 20, pay_amount: 20, currency: 'CNY', fee_rate: 0, payment_type: 'alipay', out_trade_no: 'LOCAL-DEMO-20260918-002', status: 'FAILED', order_type: 'balance', created_at: at(-18 * DAY), expires_at: at(-18 * DAY + 30 * 60_000), refund_amount: 0, provider_instance_id: 'local-demo-alipay' },
    { id: 51001, user_id: user.id, amount: 10, pay_amount: 10, currency: 'CNY', fee_rate: 0, payment_type: 'alipay', out_trade_no: 'LOCAL-DEMO-20260918-001', status: 'REFUNDED', order_type: 'balance', created_at: at(-26 * DAY), expires_at: at(-26 * DAY + 30 * 60_000), paid_at: at(-26 * DAY + 2 * 60_000), completed_at: at(-26 * DAY + 3 * 60_000), refund_amount: 10, refund_reason: '本地演示退款', provider_instance_id: 'local-demo-alipay' },
  ]

  const subscriptions: UserSubscription[] = [
    { id: 6101, user_id: user.id, group_id: groups[0].id, status: 'active', starts_at: at(-12 * DAY), daily_usage_usd: 4.86, weekly_usage_usd: 28.4, monthly_usage_usd: 72.16, daily_window_start: at(-8 * 60 * 60_000), weekly_window_start: at(-3 * DAY), monthly_window_start: at(-12 * DAY), created_at: at(-12 * DAY), updated_at: at(-20 * 60_000), expires_at: at(18 * DAY), group: groups[0] },
    { id: 6102, user_id: user.id, group_id: groups[1].id, status: 'active', starts_at: at(-38 * DAY), daily_usage_usd: 13.7, weekly_usage_usd: 41.2, monthly_usage_usd: 166.8, daily_window_start: at(-21 * 60 * 60_000), weekly_window_start: at(-5 * DAY), monthly_window_start: at(-8 * DAY), created_at: at(-38 * DAY), updated_at: at(-35 * 60_000), expires_at: at(52 * DAY), group: groups[1] },
    { id: 6103, user_id: user.id, group_id: groups[2].id, status: 'expired', starts_at: at(-70 * DAY), daily_usage_usd: 0, weekly_usage_usd: 0, monthly_usage_usd: 98.3, daily_window_start: null, weekly_window_start: null, monthly_window_start: at(-70 * DAY), created_at: at(-70 * DAY), updated_at: at(-10 * DAY), expires_at: at(-10 * DAY), group: groups[2] },
  ]
  const subscriptionProgress: SubscriptionProgress[] = subscriptions.filter(item => item.status === 'active').map(item => ({
    subscription_id: item.id,
    daily: { used: item.daily_usage_usd, limit: item.group?.daily_limit_usd ?? null, percentage: item.group?.daily_limit_usd ? item.daily_usage_usd / item.group.daily_limit_usd * 100 : 0, reset_in_seconds: 57_600 },
    weekly: { used: item.weekly_usage_usd, limit: item.group?.weekly_limit_usd ?? null, percentage: item.group?.weekly_limit_usd ? item.weekly_usage_usd / item.group.weekly_limit_usd * 100 : 0, reset_in_seconds: 345_600 },
    monthly: { used: item.monthly_usage_usd, limit: item.group?.monthly_limit_usd ?? null, percentage: item.group?.monthly_limit_usd ? item.monthly_usage_usd / item.group.monthly_limit_usd * 100 : 0, reset_in_seconds: 1_555_200 },
    expires_at: item.expires_at,
    days_remaining: item.expires_at ? Math.ceil((Date.parse(item.expires_at) - now.getTime()) / DAY) : null,
  }))
  const redeemHistory: RedeemHistoryItem[] = [
    { id: 9103, code: 'DEMO-SUB-USED', type: 'subscription', value: 30, status: 'used', used_at: at(-12 * DAY), created_at: at(-15 * DAY), group_id: groups[0].id, validity_days: 30, group: { id: groups[0].id, name: groups[0].name } },
    { id: 9102, code: 'DEMO-BALANCE-USED', type: 'balance', value: 25, status: 'used', used_at: at(-28 * DAY), created_at: at(-29 * DAY) },
    { id: 9101, code: 'DEMO-ADJUSTMENT', type: 'admin_balance', value: -3.5, status: 'used', used_at: at(-40 * DAY), created_at: at(-40 * DAY), notes: '本地演示调整记录' },
  ]
  const affiliateDetail: UserAffiliateDetail = {
    user_id: user.id,
    aff_code: 'LOCAL-DEMO-AFF',
    inviter_id: null,
    aff_count: 3,
    aff_quota: 18.64,
    aff_frozen_quota: 4.2,
    aff_history_quota: 86.5,
    effective_rebate_rate_percent: 20,
    invitees: [
      { user_id: 900101, email: 'invitee-one@example.test', username: '演示受邀用户一', created_at: at(-32 * DAY), total_rebate: 31.2 },
      { user_id: 900102, email: 'invitee-two@example.test', username: '演示受邀用户二', created_at: at(-18 * DAY), total_rebate: 22.8 },
      { user_id: 900103, email: 'invitee-three@example.test', username: '演示受邀用户三', created_at: at(-6 * DAY), total_rebate: 32.5 },
    ],
  }
  const cfAllowlist: CFAllowlistStatus = {
    eligible: true,
    threshold: 100,
    total_recharged: 286,
    max_slots: 3,
    used_slots: 2,
    detected_ip: '198.51.100.24',
    configured: true,
    items: [
      { id: 1, ip: '192.0.2.10', created_at: at(-24 * DAY) },
      { id: 2, ip: '203.0.113.18', created_at: at(-7 * DAY) },
    ],
  }
  return {
    monitorConfig,
    monitorCoverage,
    monitorSnapshot,
    monitorDimensions,
    monitorMatrixRows,
    monitorModels,
    monitorViews,
    monitorDetails,
    availableChannels,
    paymentPlans,
    checkoutInfo,
    paymentConfig,
    paymentOrders,
    subscriptions,
    subscriptionProgress,
    redeemHistory,
    affiliateDetail,
    cfAllowlist,
  }
}

export const settings: PublicSettings = {
  registration_enabled: false, email_verify_enabled: false, force_email_on_third_party_signup: false,
  registration_email_suffix_whitelist: [], promo_code_enabled: false, password_reset_enabled: false,
  invitation_code_enabled: false, turnstile_enabled: false, turnstile_site_key: '',
  site_name: 'kedaya.ai', site_logo: '', site_subtitle: PREVIEW_LABEL, api_base_url: '',
  contact_info: '', doc_url: '', home_content: '', compact_home_enabled: true,
  hide_ccs_import_button: true, payment_enabled: true, risk_control_enabled: false,
  table_default_page_size: 10, table_page_size_options: [10, 20, 50, 100],
  custom_menu_items: [], custom_endpoints: [], linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false, oidc_oauth_enabled: false, oidc_oauth_provider_name: '',
  github_oauth_enabled: false, google_oauth_enabled: false, backend_mode_enabled: false,
  version: PREVIEW_LABEL, balance_low_notify_enabled: false, account_quota_notify_enabled: false,
  balance_low_notify_threshold: 20, channel_monitor_enabled: true, channel_monitor_mode: 'v2',
  channel_monitor_default_interval_seconds: 60, channel_monitor_show_quota: true,
  available_channels_enabled: true,
  model_plaza_enabled: false, model_plaza_require_auth: false, plugin_management_enabled: false,
  service_quota_enabled: false, affiliate_enabled: true, allow_user_view_error_requests: true,
}
