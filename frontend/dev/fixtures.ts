import type { ApiKey, Group, PublicSettings, UsageLog, User } from '../src/types'

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
    rate_multiplier: 1, is_exclusive: false, status: 'active', subscription_type: 'standard',
    daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
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
      const created = midnight - day * DAY + Math.floor(j * DAY / count)
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
  return { user, groups, keys, usage, models, now: timestamp }
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

export const settings: PublicSettings = {
  registration_enabled: false, email_verify_enabled: false, force_email_on_third_party_signup: false,
  registration_email_suffix_whitelist: [], promo_code_enabled: false, password_reset_enabled: false,
  invitation_code_enabled: false, turnstile_enabled: false, turnstile_site_key: '',
  site_name: 'kedaya.ai', site_logo: '', site_subtitle: PREVIEW_LABEL, api_base_url: '',
  contact_info: '', doc_url: '', home_content: '', compact_home_enabled: true,
  hide_ccs_import_button: true, payment_enabled: false, risk_control_enabled: false,
  table_default_page_size: 10, table_page_size_options: [10, 20, 50, 100],
  custom_menu_items: [], custom_endpoints: [], linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false, oidc_oauth_enabled: false, oidc_oauth_provider_name: '',
  github_oauth_enabled: false, google_oauth_enabled: false, backend_mode_enabled: false,
  version: PREVIEW_LABEL, balance_low_notify_enabled: false, account_quota_notify_enabled: false,
  balance_low_notify_threshold: 0, channel_monitor_enabled: false,
  channel_monitor_default_interval_seconds: 60, available_channels_enabled: false,
  model_plaza_enabled: false, model_plaza_require_auth: false, plugin_management_enabled: false,
  service_quota_enabled: false, affiliate_enabled: false, allow_user_view_error_requests: true,
}
