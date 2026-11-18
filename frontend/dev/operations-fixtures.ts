import type { AccountOpsSettings, AccountOpsThresholdAccount, AccountOpsEvent } from '../src/api/admin/accountOps'
import type { OpsAutomationConfig } from '../src/api/admin/opsAutomation'
import type { Snapshot } from '../src/api/admin/channelOperations'
import type { RegionalStatus } from '../src/api/admin/serverless'

// Read-only, synthetic fixtures for the existing localhost-only preview. No upstream calls or writes.
export function operationsPreviewRead(path: string, query: URLSearchParams, timestamp: string): unknown {
  const config: AccountOpsSettings = {
    config: { enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60, webhooks: [], groups: [], balance_thresholds: [], quota_thresholds: [] },
    smtp_configured: false, dropped_signals: 0, storage_failures: 0, encryption_key_configured: false,
    admin_defaults: { email_available: false, email_masked: '', email_unavailable_reason: 'email_unavailable', bark_available: false, bark_configured: false, bark_enabled: false, bark_unavailable_reason: 'bark_unconfigured' },
  }
  const accounts: AccountOpsThresholdAccount[] = [
    { account_id: 901, account_name: '本地演示 · 美元资产', platform: 'openai', type: 'apikey', balance: 128.64, unit: 'USD', balance_status: 'ok', received_at: timestamp, usage_windows: [] },
    { account_id: 902, account_name: '本地演示 · 人民币资产', platform: 'openai', type: 'apikey', balance: 360, unit: 'CNY', balance_status: 'ok', received_at: timestamp, usage_windows: [] },
    { account_id: 903, account_name: '本地演示 · 待巡检账号', platform: 'anthropic', type: 'oauth', balance: null, unit: 'USD', balance_status: 'failed', received_at: timestamp, usage_windows: [{ window: '7d', label: '7d', used_percent: 92, status: 'ok', observed_at: timestamp }] },
  ]
  if (path === '/api/v1/admin/account-ops/config') return config
  if (path === '/api/v1/admin/account-ops/threshold-accounts') return { items: accounts }
  if (path === '/api/v1/admin/account-ops/groups') return { groups: [] }
  if (path === '/api/v1/admin/account-ops/alerts') return { items: [] as AccountOpsEvent[], has_more: false }
  if (path === '/api/v1/admin/credential-guard') return { items: [] }
  if (path === '/api/v1/admin/ops-automation') {
    const automation: OpsAutomationConfig = {
      revision: 'local-demo', updated_at: timestamp,
      oauth: ['openai', 'anthropic', 'gemini'].map(platform => ({ enabled: false, platform, concurrency: 3, priority: 50, load_factor: 1, group_ids: [] })),
      ramp: { enabled: false, group_ids: [], successes_per_step: 20, step: 1, maximum: 100, cooldown_seconds: 60 },
      ranking: { enabled: false, group_ids: [], mode: 'balanced', window_minutes: 60, minimum_samples: 10, quality_weight: 30, latency_weight: 45, cost_weight: 25 },
      oauth_fallback: { enabled: false, group_ids: [], minimum_quality: 1, minimum_quota_percent: 10, maximum_load_percent: 90 },
      admission: { enabled: false, platforms: ['openai'], model: '' },
    }
    return automation
  }
  if (path === '/api/v1/admin/ops-automation/reasoning') return []
  if (path === '/api/v1/admin/channels') return { items: [], total: 0, page: 1, page_size: 20, pages: 0 }
  if (path === '/api/v1/admin/channels/operations/snapshot') {
    const section = <T>(data: T) => ({ status: 'ready' as const, data, queried_at: timestamp })
    const metrics = { requests: 0, samples: 0, unknown_samples: 0, duration_samples: 0, p50_ms: null, p90_ms: null, p95_ms: null, duration_p90_ms: null, latest_sample_at: null }
    const snapshot: Snapshot = {
      scope: { channel_id: 0, upstream_id: 0, account_id: 0, group_id: 0, model: '', model_scope: 'public', dimension: 'channel' },
      from: query.get('from') || timestamp, until: query.get('until') || timestamp, model_scope: 'public',
      usage: section({ source: 'local-demo', attribution: 'current', latency_measurement: 'legacy_forward', summary: metrics, total: 0, rows: [], lineage: [], lineage_total: 0, error_rate: null, error_scope: 'unsupported' }),
      quality: section({ source: 'local-demo', scope: 'current accounts', summary: { rounds: 0, passed: 0, degraded: 0, inconclusive: 0, latest_sample_at: null }, rows: [], has_more: false }),
      errors: section({ source: 'local-demo', scope: 'current accounts', records: 0, upstream_5xx: 0, latest_sample_at: null, rows: [], bucket_total: 0, error_rate: null }),
      capacity: section({ source: 'local-demo', scope: 'current', accounts: 0, schedulable: 0, configured_concurrency: 0 }),
    }
    return snapshot
  }
  if (path === '/api/v1/admin/request-captures/config') return { enabled: false, id1_diagnostic_enabled: false, diagnostic_user_ids: [], quota_mib: 128, retention_days: 1, instance_id: 'local-demo', storage_ready: true }
  if (path === '/api/v1/admin/request-captures') return { items: [], has_more: false, stats: { instance_id: 'local-demo', used_bytes: 0, buffer_bytes: 0, peak_buffer_bytes: 0, active_requests: 0, admission_skipped: 0, storage_error: false } }
  if (path === '/api/v1/admin/serverless') {
    const status: RegionalStatus = {
      config: { enabled: false, established: false, revision: 'local-demo', pods: [], regions: [] },
      pods: [], stats: {}, runtime: { node_id: 'local-demo', gateway_node: false, secret_configured: false, redis_configured: false, private_network_configured: false },
    }
    return status
  }
}
