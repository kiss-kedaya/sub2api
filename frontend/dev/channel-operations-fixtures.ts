import type { Channel } from '../src/api/admin/channels'
import type { Catalog, Member, Metrics, Snapshot, Upstream } from '../src/api/admin/channelOperations'

// Synthetic, read-only channel operations. No credentials, network access or business writes.
export function channelOperationsPreviewRead(path: string, query: URLSearchParams, timestamp: string): unknown {
  if (!path.startsWith('/api/v1/admin/channels')) return
  const channels: Channel[] = ['演示 · 标准渠道', '演示 · 备用渠道', '演示 · 待配置渠道'].map((name, index) => ({
    id: index + 1, name, description: '仅供本地界面预览，不连接真实上游。', status: index === 2 ? 'disabled' : 'active',
    billing_model_source: 'requested', restrict_models: false, group_ids: index === 2 ? [] : [index + 11],
    model_pricing: [], model_mapping: {}, apply_pricing_to_account_stats: false, account_stats_pricing_rules: [],
    created_at: timestamp, updated_at: timestamp,
  }))
  const upstreams: Upstream[] = [
    { id: 101, name: '演示 · 北区供应商', members: 4 },
    { id: 102, name: '演示 · 南区供应商', members: 3 },
    { id: 201, name: '演示 · 备用供应商', members: 3 },
  ]
  const members: Array<Member & { channel: number }> = Array.from({ length: 12 }, (_, index) => ({
    id: 901 + index, name: `演示账号 ${String(index + 1).padStart(2, '0')}`, platform: index % 2 ? 'anthropic' : 'openai',
    status: index === 6 ? 'error' : 'active', schedulable: index !== 6, concurrency: index % 3 + 3,
    eligible: index !== 6, upstream_id: index === 7 || index === 11 ? 0 : index < 4 ? 101 : index < 8 ? 102 : 201,
    channel: index < 8 ? 1 : 2,
  }))
  const number = (key: string, fallback = 0) => Number(query.get(key)) || fallback
  const channelId = number('channel_id')
  const search = (key: string) => (query.get(key) || '').toLowerCase()
  const slice = <T>(rows: T[], page = number('page', 1), size = number('page_size', 20)) => rows.slice((page - 1) * size, page * size)
  const channelUpstreams = (id: number) => upstreams.filter(row => id === 1 ? row.id < 200 : id === 2 ? row.id >= 200 : false)
  const matchingMembers = (id: number) => members.filter(row => !id || row.channel === id)
  if (path === '/api/v1/admin/channels') {
    const rows = channels.filter(row => row.name.toLowerCase().includes(search('search')))
    return { items: slice(rows), total: rows.length, page: number('page', 1), page_size: number('page_size', 20), pages: Math.ceil(rows.length / number('page_size', 20)) }
  }
  const detail = path.match(/^\/api\/v1\/admin\/channels\/(\d+)$/)
  if (detail) return channels.find(row => row.id === Number(detail[1]))
  const catalog = path.match(/^\/api\/v1\/admin\/channels\/(\d+)\/operations$/)
  if (catalog) {
    const id = Number(catalog[1])
    const labels = channelUpstreams(id).filter(row => row.name.toLowerCase().includes(search('upstream_search')))
    const accounts = matchingMembers(id).filter(row => `${row.name} ${row.id}`.toLowerCase().includes(search('search')))
      .filter(row => query.get('membership') === 'assigned' ? !!row.upstream_id : query.get('membership') === 'unassigned' ? !row.upstream_id : true)
    return { revision: 42, upstreams: slice(labels, number('upstream_page', 1)), upstream_total: labels.length, accounts: slice(accounts), total: accounts.length } satisfies Catalog
  }
  if (path !== '/api/v1/admin/channels/operations/snapshot') return
  const upstreamId = number('upstream_id'), accountId = number('account_id')
  const model = query.get('model') || '', groupId = number('group_id')
  const modelScope = query.get('model_scope') || 'public', dimension = query.get('dimension') || 'channel'
  const selected = matchingMembers(channelId).filter(row => !accountId || row.id === accountId)
    .filter(row => upstreamId === -1 ? !row.upstream_id : !upstreamId || row.upstream_id === upstreamId)
    .filter(row => !groupId || row.channel + 10 === groupId)
  const hasUsage = !model || ['demo-chat', 'demo-recorded', 'demo-provider'].includes(model)
  const usageMembers = hasUsage ? selected : []
  const metrics = (accounts: typeof members): Metrics => ({
    requests: accounts.length * 120, samples: accounts.length * 108, unknown_samples: accounts.length * 12,
    duration_samples: accounts.length * 118, p50_ms: accounts.length ? 420 : null, p90_ms: accounts.length ? 780 : null,
    p95_ms: accounts.length ? 920 : null, duration_p90_ms: accounts.length ? 5840 : null, latest_sample_at: accounts.length ? timestamp : null,
  })
  const dimensions = dimension === 'channel' ? channels.filter(row => usageMembers.some(member => member.channel === row.id))
    : dimension === 'upstream' ? [...channelUpstreams(channelId), { id: 0, name: '未分配上游', members: 0 }].filter(row => usageMembers.some(member => member.upstream_id === row.id))
      : usageMembers
  const usageRows = dimensions.map(row => ({ ...metrics(usageMembers.filter(member => dimension === 'channel' ? member.channel === row.id : dimension === 'upstream' ? member.upstream_id === row.id : member.id === row.id)), dimension_id: row.id, name: row.name }))
  const qualityRows = usageMembers.slice(0, 6).map((member, index) => ({
    id: 600 - index, account_id: member.id, plan_id: 80 + index, measured_model: 'demo-chat',
    observed_at: new Date(Date.parse(timestamp) - index * 20 * 60_000).toISOString(), probes: index === 2 ? 2 : 4, passed_probes: index === 1 || index === 2 ? 2 : 4,
    expected: 4, outcome: index === 1 ? 'degraded' as const : index === 2 ? 'inconclusive' as const : 'passed' as const,
    round_complete: index !== 2, round_probes: index === 2 ? 2 : 4, round_expected: 4,
    actions: [index === 1 ? 'failure_counted:1/2' : null],
  }))
  const before = number('quality_before'), history = qualityRows.filter(row => !before || row.id < before)
  const section = <T>(data: T) => ({ status: 'ready' as const, data, queried_at: timestamp })
  return {
    scope: { channel_id: channelId, upstream_id: upstreamId, account_id: accountId, group_id: groupId, model, model_scope: modelScope, dimension },
    from: query.get('from') || timestamp, until: query.get('until') || timestamp, model_scope: modelScope,
    usage: section({ source: 'usage_logs', attribution: 'current', latency_measurement: 'legacy_forward', summary: metrics(usageMembers), total: usageRows.length, rows: slice(usageRows),
      lineage: usageMembers.length ? [{ public_model: 'demo-chat', recorded_model: 'demo-recorded', upstream_model: 'demo-provider', model_mapping_chain: 'demo-chat → demo-provider', samples: usageMembers.length * 120 }] : [], lineage_total: usageMembers.length ? 1 : 0, error_rate: null, error_scope: 'unsupported' }),
    quality: section({ source: 'scheduled_test_results', scope: 'current accounts', summary: { rounds: qualityRows.length, passed: qualityRows.filter(row => row.outcome === 'passed').length, degraded: qualityRows.filter(row => row.outcome === 'degraded').length, inconclusive: qualityRows.filter(row => row.outcome === 'inconclusive').length, latest_sample_at: qualityRows.length ? timestamp : null }, rows: history.slice(0, 3), has_more: history.length > 3 }),
    errors: section({ source: 'ops_error_logs', scope: 'current accounts', records: usageMembers.length ? 9 : 0, upstream_5xx: usageMembers.length ? 6 : 0, latest_sample_at: usageMembers.length ? timestamp : null,
      rows: usageMembers.length ? [{ error_type: 'upstream', error_phase: 'forward', status_code: 502, records: 6 }, { error_type: 'timeout', error_phase: 'forward', status_code: 504, records: 3 }] : [], bucket_total: usageMembers.length ? 2 : 0, error_rate: null }),
    capacity: section({ source: 'accounts', scope: 'current', accounts: selected.length, schedulable: selected.filter(row => row.status === 'active' && row.schedulable).length, configured_concurrency: selected.reduce((sum, row) => sum + row.concurrency, 0) }),
  } satisfies Snapshot
}
