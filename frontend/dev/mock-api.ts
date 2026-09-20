import { isIP } from 'node:net'
import type {
  AdminUsageLog,
  DashboardStats,
  GroupStat,
  ModelStat,
  PaginatedResponse,
  TrendDataPoint,
  UsageLog,
  UserSpendingRankingResponse,
  UserUsageTrendPoint,
  ApiKey,
} from '../src/types'
import type { MonitorMatrixGroupBy, MonitorMatrixRow } from '../src/api/channelMonitorV2'
import type { PaymentOrder } from '../src/types/payment'
import { createFixtures, makeKey, settings } from './fixtures'

export class PreviewError extends Error {
  constructor(public status: number, message: string) { super(message) }
}

function integer(value: string | null, fallback: number, max: number) {
  if (value === null || value === '') return fallback
  const n = Number(value)
  if (!Number.isSafeInteger(n) || n < 1 || n > max) throw new PreviewError(400, '无效的分页参数')
  return n
}

function page<T>(items: T[], query: URLSearchParams, maxPageSize = 100): PaginatedResponse<T> {
  const current = integer(query.get('page'), 1, 1_000_000)
  const size = integer(query.get('page_size'), 20, maxPageSize)
  return { items: items.slice((current - 1) * size, current * size), total: items.length,
    page: current, page_size: size, pages: Math.ceil(items.length / size) }
}

function sort<T extends object>(items: T[], query: URLSearchParams, allowed: string[], fallback: string): T[] {
  const key = query.get('sort_by') || fallback
  if (!allowed.includes(key)) throw new PreviewError(400, '演示不支持此排序字段')
  const direction = query.get('sort_order') === 'asc' ? 1 : -1
  return [...items].sort((a, b) => {
    const left = a[key as keyof T] ?? ''
    const right = b[key as keyof T] ?? ''
    return (left < right ? -1 : left > right ? 1 : 0) * direction
  })
}

function dateLabel(timestamp: string, timezone: string, hourly = false) {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    ...(hourly ? { hour: '2-digit', hourCycle: 'h23' as const } : {}),
  }).formatToParts(new Date(timestamp))
  const value = (type: string) => parts.find(part => part.type === type)?.value
  return `${value('year')}-${value('month')}-${value('day')}${hourly ? ` ${value('hour')}:00` : ''}`
}

function summarize(rows: UsageLog[]) {
  const total = (field: 'input_tokens' | 'output_tokens' | 'cache_creation_tokens' | 'cache_read_tokens' | 'total_cost' | 'actual_cost') => rows.reduce((sum, row) => sum + row[field], 0)
  const input = total('input_tokens'), output = total('output_tokens')
  const creation = total('cache_creation_tokens'), read = total('cache_read_tokens')
  return {
    total_requests: rows.length, total_input_tokens: input, total_output_tokens: output,
    total_cache_creation_tokens: creation, total_cache_read_tokens: read,
    total_cache_tokens: creation + read, total_tokens: input + output + creation + read,
    total_cost: total('total_cost'), total_actual_cost: total('actual_cost'),
    average_duration_ms: rows.length ? rows.reduce((sum, row) => sum + (row.duration_ms ?? 0), 0) / rows.length : 0,
  }
}

function chartSummary(rows: UsageLog[]) {
  const s = summarize(rows)
  return { requests: s.total_requests, input_tokens: s.total_input_tokens, output_tokens: s.total_output_tokens,
    cache_creation_tokens: s.total_cache_creation_tokens, cache_read_tokens: s.total_cache_read_tokens,
    total_tokens: s.total_tokens, cost: s.total_cost, actual_cost: s.total_actual_cost }
}

function buckets<T extends UsageLog>(rows: T[], key: (row: T) => string) {
  const result = new Map<string, T[]>()
  for (const row of rows) {
    const label = key(row)
    const bucket = result.get(label) ?? []
    bucket.push(row)
    result.set(label, bucket)
  }
  return [...result]
}

export function createMockApi(now = new Date()) {
  const data = createFixtures(now)
  const adminSettings = () => ({
    ...settings, ops_monitoring_enabled: false, ops_realtime_monitoring_enabled: false,
    ops_query_mode_default: 'auto', custom_menu_items: [],
    payment_recharge_center_enabled: data.checkoutInfo.recharge_center_enabled === true,
    payment_enabled_types: data.paymentConfig.enabled_payment_types,
  })
  let nextId = data.keys.length + 1
  let nextOrderId = Math.max(...data.paymentOrders.map(order => order.id)) + 1
  let nextAllowlistId = Math.max(...data.cfAllowlist.items.map(item => item.id)) + 1
  let nextRedeemId = Math.max(...data.redeemHistory.map(item => item.id)) + 1
  const usedDemoCodes = new Set<string>()
  const timezoneOf = (query: URLSearchParams) => {
    const timezone = query.get('timezone') || 'UTC'
    try { new Intl.DateTimeFormat('en', { timeZone: timezone }) } catch { throw new PreviewError(400, '无效的时区') }
    return timezone
  }
  function filteredUsage(query: URLSearchParams) {
    const timezone = timezoneOf(query)
    const start = query.get('start_date'), end = query.get('end_date')
    for (const date of [start, end]) {
      if (date && (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(Date.parse(date)))) {
        throw new PreviewError(400, '无效的日期')
      }
    }
    if (start && end && start > end) throw new PreviewError(400, '开始日期不能晚于结束日期')
    return data.usage.filter(row => {
      for (const field of ['api_key_id', 'group_id', 'billing_type', 'billing_mode', 'request_type', 'stream', 'native_compaction_v2'] as const) {
        const filter = query.get(field)
        if (filter !== null && filter !== '' && String(row[field]) !== filter) return false
      }
      const model = query.get('model')?.toLowerCase()
      if (model && !row.model.toLowerCase().includes(model)) return false
      if (start || end) {
        const date = dateLabel(row.created_at, timezone)
        if ((start && date < start) || (end && date > end)) return false
      }
      return true
    })
  }
  function charts(query: URLSearchParams) {
    const rows = filteredUsage(query)
    const timezone = timezoneOf(query)
    const granularity = query.get('granularity') || 'day'
    if (!['day', 'hour'].includes(granularity)) throw new PreviewError(400, '无效的时间粒度')
    return {
      generated_at: data.now, start_date: query.get('start_date') || dateLabel(data.usage[0].created_at, timezone),
      end_date: query.get('end_date') || dateLabel(data.now, timezone), granularity,
      trend: buckets(rows, row => dateLabel(row.created_at, timezone, granularity === 'hour'))
        .map(([date, items]) => ({ date, ...chartSummary(items) })).sort((a, b) => a.date.localeCompare(b.date)),
      models: buckets(rows, row => row.model).map(([model, items]) => ({ model, ...chartSummary(items) }))
        .sort((a, b) => b.total_tokens - a.total_tokens),
      groups: buckets(rows, row => String(row.group_id)).map(([id, items]) => ({ group_id: Number(id),
        group_name: data.groups.find(group => group.id === Number(id))?.name ?? '演示', ...chartSummary(items) })),
    }
  }
  function filteredAdminUsage(query: URLSearchParams): AdminUsageLog[] {
    const timezone = timezoneOf(query)
    const start = query.get('start_date'), end = query.get('end_date')
    for (const date of [start, end]) {
      if (date && (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !Number.isFinite(Date.parse(date)))) {
        throw new PreviewError(400, '无效的日期')
      }
    }
    if (start && end && start > end) throw new PreviewError(400, '开始日期不能晚于结束日期')
    return data.adminUsage.filter(row => {
      for (const field of ['user_id', 'api_key_id', 'account_id', 'group_id', 'billing_type', 'billing_mode', 'request_type', 'stream', 'native_compaction_v2'] as const) {
        const filter = query.get(field)
        if (filter !== null && filter !== '' && String(row[field]) !== filter) return false
      }
      const model = query.get('model')?.toLowerCase()
      if (model && !row.model.toLowerCase().includes(model)) return false
      if (start || end) {
        const date = dateLabel(row.created_at, timezone)
        if ((start && date < start) || (end && date > end)) return false
      }
      return true
    })
  }
  function adminCharts(query: URLSearchParams) {
    const rows = filteredAdminUsage(query)
    const timezone = timezoneOf(query)
    const granularity = query.get('granularity') || 'day'
    if (!['day', 'hour'].includes(granularity)) throw new PreviewError(400, '无效的时间粒度')
    const trend: TrendDataPoint[] = buckets(rows, row => dateLabel(row.created_at, timezone, granularity === 'hour'))
      .map(([date, items]) => ({ date, ...chartSummary(items) })).sort((a, b) => a.date.localeCompare(b.date))
    const models: ModelStat[] = buckets(rows, row => row.model).map(([model, items]) => ({
      model,
      ...chartSummary(items),
      account_cost: items.reduce((sum, row) => sum + (row.account_stats_cost ?? row.total_cost), 0),
    })).sort((a, b) => b.total_tokens - a.total_tokens)
    const groups: GroupStat[] = buckets(rows, row => String(row.group_id)).map(([id, items]) => ({
      group_id: Number(id),
      group_name: data.adminGroups.find(group => group.id === Number(id))?.name ?? '本地演示分组',
      ...chartSummary(items),
      account_cost: items.reduce((sum, row) => sum + (row.account_stats_cost ?? row.total_cost), 0),
    }))
    return {
      generated_at: data.now,
      start_date: query.get('start_date') || dateLabel(data.adminUsage[0].created_at, timezone),
      end_date: query.get('end_date') || dateLabel(data.now, timezone),
      granularity,
      trend,
      models,
      groups,
    }
  }
  function adminDashboardStats(rows = data.adminUsage): DashboardStats {
    const all = summarize(rows)
    const todayLabel = dateLabel(data.now, 'UTC')
    const todayRows = rows.filter(row => dateLabel(row.created_at, 'UTC') === todayLabel)
    const today = summarize(todayRows)
    const recent = summarize(rows.filter(row => Date.parse(row.created_at) > now.getTime() - 5 * 60_000))
    return {
      total_users: data.adminUsers.length,
      today_new_users: data.adminUsers.filter(user => dateLabel(user.created_at, 'UTC') === todayLabel).length,
      active_users: new Set(todayRows.map(row => row.user_id)).size,
      hourly_active_users: new Set(todayRows.filter(row => Date.parse(row.created_at) > now.getTime() - 60 * 60_000).map(row => row.user_id)).size,
      stats_updated_at: data.now,
      stats_stale: false,
      total_api_keys: data.adminKeys.length,
      active_api_keys: data.adminKeys.filter(key => key.status === 'active').length,
      total_accounts: data.adminAccounts.length,
      normal_accounts: data.adminAccounts.filter(account => account.status === 'active').length,
      error_accounts: data.adminAccounts.filter(account => account.status === 'error').length,
      ratelimit_accounts: data.adminAccounts.filter(account => account.rate_limit_reset_at && Date.parse(account.rate_limit_reset_at) > now.getTime()).length,
      overload_accounts: data.adminAccounts.filter(account => account.overload_until && Date.parse(account.overload_until) > now.getTime()).length,
      total_requests: all.total_requests,
      total_input_tokens: all.total_input_tokens,
      total_output_tokens: all.total_output_tokens,
      total_cache_creation_tokens: all.total_cache_creation_tokens,
      total_cache_read_tokens: all.total_cache_read_tokens,
      total_tokens: all.total_tokens,
      total_cost: all.total_cost,
      total_actual_cost: all.total_actual_cost,
      total_account_cost: rows.reduce((sum, row) => sum + (row.account_stats_cost ?? row.total_cost), 0),
      today_requests: today.total_requests,
      today_input_tokens: today.total_input_tokens,
      today_output_tokens: today.total_output_tokens,
      today_cache_creation_tokens: today.total_cache_creation_tokens,
      today_cache_read_tokens: today.total_cache_read_tokens,
      today_tokens: today.total_tokens,
      today_cost: today.total_cost,
      today_actual_cost: today.total_actual_cost,
      today_account_cost: todayRows.reduce((sum, row) => sum + (row.account_stats_cost ?? row.total_cost), 0),
      average_duration_ms: all.average_duration_ms,
      uptime: 18 * 86_400 + 13_260,
      rpm: recent.total_requests / 5,
      tpm: recent.total_tokens / 5,
    }
  }
  function adminUserTrend(query: URLSearchParams): UserUsageTrendPoint[] {
    const timezone = timezoneOf(query)
    const granularity = query.get('granularity') || 'day'
    const limit = integer(query.get('limit'), 12, 100)
    const selectedUsers = [...buckets(filteredAdminUsage(query), row => String(row.user_id))]
      .sort((a, b) => summarize(b[1]).total_tokens - summarize(a[1]).total_tokens).slice(0, limit)
    return selectedUsers.flatMap(([id, userRows]) => {
      const user = data.adminUsers.find(item => item.id === Number(id))
      return buckets(userRows, row => dateLabel(row.created_at, timezone, granularity === 'hour')).map(([date, items]) => {
        const summary = summarize(items)
        return { date, user_id: Number(id), email: user?.email ?? '', username: user?.username ?? '',
          requests: summary.total_requests, tokens: summary.total_tokens, cost: summary.total_cost, actual_cost: summary.total_actual_cost }
      })
    }).sort((a, b) => a.date.localeCompare(b.date) || a.user_id - b.user_id)
  }
  function adminRanking(query: URLSearchParams): UserSpendingRankingResponse {
    const limit = integer(query.get('limit'), 10, 100)
    const ranking = buckets(filteredAdminUsage(query), row => String(row.user_id)).map(([id, rows]) => {
      const user = data.adminUsers.find(item => item.id === Number(id))
      const summary = summarize(rows)
      return { user_id: Number(id), email: user?.email ?? '', username: user?.username ?? '',
        actual_cost: summary.total_actual_cost, requests: summary.total_requests, tokens: summary.total_tokens }
    }).sort((a, b) => b.actual_cost - a.actual_cost).slice(0, limit)
    return {
      ranking,
      total_actual_cost: ranking.reduce((sum, item) => sum + item.actual_cost, 0),
      total_requests: ranking.reduce((sum, item) => sum + item.requests, 0),
      total_tokens: ranking.reduce((sum, item) => sum + item.tokens, 0),
      start_date: query.get('start_date') || dateLabel(data.adminUsage[0].created_at, 'UTC'),
      end_date: query.get('end_date') || dateLabel(data.now, 'UTC'),
    }
  }
  function editKey(key: ApiKey, body: Record<string, unknown>) {
    const next = { ...key }
    if ('custom_key' in body) throw new PreviewError(422, '演示不支持自定义密钥，只生成无效的演示密钥')
    if ('name' in body) {
      if (typeof body.name !== 'string' || !body.name.trim() || body.name.length > 100) throw new PreviewError(422, '名称须为 1 至 100 个字符')
      next.name = body.name.trim()
    }
    if ('status' in body) {
      if (body.status !== 'active' && body.status !== 'inactive') throw new PreviewError(422, '无效的密钥状态')
      next.status = body.status
    }
    if ('group_id' in body || 'group_ids' in body) {
      const ids = body.group_ids ?? (body.group_id === null ? [] : [body.group_id])
      if (!Array.isArray(ids) || ids.some(id => !data.groups.some(group => group.id === id))) throw new PreviewError(422, '无效的演示分组')
      next.group_ids = [...new Set(ids)]
      next.group_id = next.group_ids[0] ?? null
      next.group = data.groups.find(group => group.id === next.group_id)
    }
    for (const field of ['quota', 'rate_limit_5h', 'rate_limit_1d', 'rate_limit_7d'] as const) {
      if (!(field in body)) continue
      const value = body[field]
      if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new PreviewError(422, '额度须为非负数字')
      next[field] = value
    }
    for (const field of ['ip_whitelist', 'ip_blacklist'] as const) {
      if (!(field in body)) continue
      const value = body[field]
      if (!Array.isArray(value) || value.length > 100 || value.some(item => typeof item !== 'string' || item.length > 100)) throw new PreviewError(422, '无效的 IP 列表')
      next[field] = [...value]
    }
    if ('expires_in_days' in body) {
      const days = body.expires_in_days
      if (typeof days !== 'number' || !Number.isInteger(days) || days < 1 || days > 3650) throw new PreviewError(422, '有效期须为 1 至 3650 天')
      next.expires_at = new Date(now.getTime() + days * 86_400_000).toISOString()
    }
    if ('expires_at' in body) {
      const expiration = body.expires_at === '' ? null : body.expires_at
      if (expiration !== null && (typeof expiration !== 'string' || !Number.isFinite(Date.parse(expiration)))) throw new PreviewError(422, '无效的过期时间')
      next.expires_at = expiration as string | null
    }
    if (body.reset_quota === true) next.quota_used = 0
    if (body.reset_rate_limit_usage === true) next.usage_5h = next.usage_1d = next.usage_7d = 0
    next.updated_at = new Date().toISOString()
    return next
  }
  function selectedMonitorRows(query: URLSearchParams): MonitorMatrixRow[] {
    const platforms = query.getAll('platform')
    const groupIds = query.getAll('group_id').map(Number)
    const models = query.getAll('model')
    const groupBy = (query.get('group_by') || 'platform_group') as MonitorMatrixGroupBy
    if (!['platform', 'platform_group', 'platform_model', 'platform_group_model'].includes(groupBy)) {
      throw new PreviewError(400, '无效的监控分组方式')
    }
    return data.monitorMatrixRows.flatMap((row, index) => {
      const model = data.monitorModels[index]?.model
      if (platforms.length && !platforms.includes(row.platform)) return []
      if (groupIds.length && (!row.group_id || !groupIds.includes(row.group_id))) return []
      if (models.length && (!model || !models.includes(model))) return []
      return [{
        ...row,
        group_id: groupBy.includes('group') ? row.group_id : undefined,
        group_name: groupBy.includes('group') ? row.group_name : undefined,
        model: groupBy.includes('model') ? model : undefined,
      }]
    })
  }
  function publicOrder(order: PaymentOrder) {
    return {
      out_trade_no: order.out_trade_no,
      status: order.status,
      paid: ['PAID', 'RECHARGING', 'COMPLETED'].includes(order.status),
      created_at: order.created_at,
      expires_at: order.expires_at,
    }
  }
  function findOrder(id: number) {
    const order = data.paymentOrders.find(item => item.id === id)
    if (!order) throw new PreviewError(404, '本地演示订单不存在')
    return order
  }
  function orderByTradeNo(value: unknown) {
    if (typeof value !== 'string' || !value) throw new PreviewError(422, '缺少演示订单号')
    const order = data.paymentOrders.find(item => item.out_trade_no === value)
    if (!order) throw new PreviewError(404, '本地演示订单不存在')
    return order
  }
  return {
    user: data.user,
    handle(method: string, path: string, query: URLSearchParams, body: Record<string, unknown> = {}): unknown {
      if (method === 'GET') {
        if (path === '/setup/status') return { needs_setup: false, step: 'complete' }
        if (path === '/api/v1/settings/public') return settings
        if (['/api/v1/auth/me', '/api/v1/user/profile'].includes(path)) return { ...data.user, run_mode: 'standard' }
        if (path === '/api/v1/admin/compliance') return {
          required: false, version: 'local-preview', document_path_zh: '', document_path_en: '',
          document_url_zh: '', document_url_en: '', ack_phrase_zh: '', ack_phrase_en: '',
        }
        if (path === '/api/v1/admin/settings') return adminSettings()
        if (path === '/api/v1/admin/payment/config') return { enabled: true }
        if (path === '/api/v1/admin/settings/web-search-emulation') return { enabled: false, providers: [] }
        if (path === '/api/v1/admin/system/check-updates') return {
          current_version: 'local-preview', latest_version: 'local-preview', has_update: false,
          cached: true, build_type: 'source',
        }
        if (path === '/api/v1/admin/dashboard/stats') return adminDashboardStats()
        if (path === '/api/v1/admin/dashboard/realtime') return {
          active_requests: 7, requests_per_minute: 42, average_response_time: 1380, error_rate: 0.018,
        }
        if (path === '/api/v1/admin/dashboard/snapshot-v2') {
          const snapshot = adminCharts(query)
          return {
            ...snapshot,
            stats: query.get('include_stats') === 'false' ? undefined : adminDashboardStats(filteredAdminUsage(query)),
            trend: query.get('include_trend') === 'false' ? undefined : snapshot.trend,
            models: query.get('include_model_stats') === 'false' ? undefined : snapshot.models,
            groups: query.get('include_group_stats') === 'false' ? undefined : snapshot.groups,
            users_trend: query.get('include_users_trend') === 'true' ? adminUserTrend(query) : undefined,
          }
        }
        if (path === '/api/v1/admin/dashboard/trend') {
          const snapshot = adminCharts(query)
          return { trend: snapshot.trend, start_date: snapshot.start_date, end_date: snapshot.end_date, granularity: snapshot.granularity }
        }
        if (path === '/api/v1/admin/dashboard/models') {
          const snapshot = adminCharts(query)
          return { models: snapshot.models, start_date: snapshot.start_date, end_date: snapshot.end_date }
        }
        if (path === '/api/v1/admin/dashboard/groups') {
          const snapshot = adminCharts(query)
          return { groups: snapshot.groups, start_date: snapshot.start_date, end_date: snapshot.end_date }
        }
        if (path === '/api/v1/admin/dashboard/users-trend') {
          const snapshot = adminCharts(query)
          return { trend: adminUserTrend(query), start_date: snapshot.start_date, end_date: snapshot.end_date, granularity: snapshot.granularity }
        }
        if (path === '/api/v1/admin/dashboard/users-ranking') return adminRanking(query)
        if (path === '/api/v1/admin/dashboard/user-breakdown') {
          const ranking = adminRanking(query)
          return { users: ranking.ranking.map(item => ({
            user_id: item.user_id, email: item.email, requests: item.requests,
            input_tokens: Math.round(item.tokens * 0.48), output_tokens: Math.round(item.tokens * 0.19),
            cache_tokens: Math.round(item.tokens * 0.33), total_tokens: item.tokens,
            cost: item.actual_cost, actual_cost: item.actual_cost, account_cost: item.actual_cost * 0.82,
          })), start_date: ranking.start_date, end_date: ranking.end_date }
        }
        if (path === '/api/v1/admin/accounts') {
          const search = query.get('search')?.toLowerCase()
          const items = data.adminAccounts.filter(account =>
            (!search || `${account.name} ${account.notes ?? ''}`.toLowerCase().includes(search)) &&
            (!query.get('platform') || account.platform === query.get('platform')) &&
            (!query.get('type') || account.type === query.get('type')) &&
            (!query.get('status') || account.status === query.get('status')) &&
            (!query.get('group') || account.group_ids?.includes(Number(query.get('group')))))
          return page(sort(items, query, ['id', 'name', 'platform', 'type', 'status', 'priority', 'created_at', 'last_used_at'], 'id'), query)
        }
        if (path === '/api/v1/admin/accounts/upstream-billing-rates') {
          const result = page(data.adminAccounts.map(account => ({
            account_id: account.id, enabled: false, rate: account.rate_multiplier ?? 1, observed_at: data.now,
          })), query)
          return result
        }
        if (path === '/api/v1/admin/accounts/upstream-billing-probe/settings') return { enabled: false, interval_minutes: 60 }
        const adminAccount = path.match(/^\/api\/v1\/admin\/accounts\/(\d+)$/)
        if (adminAccount) {
          const account = data.adminAccounts.find(item => item.id === Number(adminAccount[1]))
          if (!account) throw new PreviewError(404, '本地演示账号不存在')
          return { ...account, groups: data.adminGroups.filter(group => account.group_ids?.includes(group.id)) }
        }
        const accountUsage = path.match(/^\/api\/v1\/admin\/accounts\/(\d+)\/usage$/)
        if (accountUsage) return data.usageByAccount[accountUsage[1]] ?? { updated_at: null, five_hour: null, seven_day: null, seven_day_sonnet: null }
        const accountToday = path.match(/^\/api\/v1\/admin\/accounts\/(\d+)\/today-stats$/)
        if (accountToday) return data.todayStatsByAccount[accountToday[1]] ?? { requests: 0, tokens: 0, cost: 0 }
        if (path === '/api/v1/admin/groups') {
          const search = query.get('search')?.toLowerCase()
          const items = data.adminGroups.filter(group =>
            (!search || `${group.name} ${group.description ?? ''}`.toLowerCase().includes(search)) &&
            (!query.get('platform') || group.platform === query.get('platform')) &&
            (!query.get('status') || group.status === query.get('status')) &&
            (!query.get('is_exclusive') || String(group.is_exclusive) === query.get('is_exclusive')))
          return page(sort(items, query, ['id', 'name', 'platform', 'status', 'rate_multiplier', 'sort_order', 'created_at'], 'sort_order'), query, 1000)
        }
        if (path === '/api/v1/admin/groups/all') return data.adminGroups.filter(group =>
          (query.get('include_inactive') === 'true' || group.status === 'active') &&
          (!query.get('platform') || group.platform === query.get('platform')))
        if (path === '/api/v1/admin/groups/live-capability') return { supported: true }
        if (path === '/api/v1/admin/groups/usage-summary') return data.adminGroups.map((group, index) => ({
          group_id: group.id, today_cost: 3.4 + index * 1.7, yesterday_cost: 4.1 + index * 1.5, total_cost: 420 + index * 187,
        }))
        if (path === '/api/v1/admin/groups/capacity-summary') return data.adminGroups.map((group, index) => ({
          group_id: group.id, concurrency_used: 4 + index, concurrency_max: 24 + index * 4,
          sessions_used: 9 + index * 2, sessions_max: 60, rpm_used: 42 + index * 11, rpm_max: group.rpm_limit ?? 0,
        }))
        const modelCandidates = path.match(/^\/api\/v1\/admin\/groups\/(\d+)\/model-allowlist-candidates$/)
        if (modelCandidates) {
          const group = data.adminGroups.find(item => item.id === Number(modelCandidates[1]))
          return { models: group?.model_allowlist?.models ?? data.models }
        }
        const adminGroup = path.match(/^\/api\/v1\/admin\/groups\/(\d+)$/)
        if (adminGroup) {
          const group = data.adminGroups.find(item => item.id === Number(adminGroup[1]))
          if (!group) throw new PreviewError(404, '本地演示分组不存在')
          return group
        }
        if (path === '/api/v1/admin/users') {
          const search = query.get('search')?.toLowerCase()
          const groupName = query.get('group_name')?.toLowerCase()
          const keyGroup = Number(query.get('api_key_group_id') || 0)
          const items = data.adminUsers.filter(user =>
            (!search || `${user.email} ${user.username} ${user.notes}`.toLowerCase().includes(search)) &&
            (!query.get('status') || user.status === query.get('status')) &&
            (!query.get('role') || user.role === query.get('role')) &&
            (!groupName || data.adminGroups.some(group => user.allowed_groups?.includes(group.id) && group.name.toLowerCase().includes(groupName))) &&
            (!keyGroup || data.adminKeys.some(key => key.user_id === user.id && key.group_ids?.includes(keyGroup))))
          return page(sort(items, query, ['id', 'email', 'username', 'balance', 'status', 'role', 'created_at', 'last_active_at'], 'id'), query)
        }
        const userApiKeys = path.match(/^\/api\/v1\/admin\/users\/(\d+)\/api-keys$/)
        if (userApiKeys) return page(data.adminKeys.filter(key => key.user_id === Number(userApiKeys[1])), query)
        const userUsage = path.match(/^\/api\/v1\/admin\/users\/(\d+)\/usage$/)
        if (userUsage) {
          const summary = summarize(data.adminUsage.filter(row => row.user_id === Number(userUsage[1])))
          return { total_requests: summary.total_requests, total_cost: summary.total_actual_cost, total_tokens: summary.total_tokens }
        }
        const platformQuotas = path.match(/^\/api\/v1\/admin\/users\/(\d+)\/platform-quotas$/)
        if (platformQuotas) return { platform_quotas: data.adminGroups.slice(0, 5).map((group, index) => ({
          platform: group.platform, daily_limit_usd: 20, weekly_limit_usd: 100, monthly_limit_usd: 320,
          daily_usage_usd: 2 + index, weekly_usage_usd: 18 + index * 2, monthly_usage_usd: 74 + index * 6,
        })) }
        const adminUser = path.match(/^\/api\/v1\/admin\/users\/(\d+)$/)
        if (adminUser) {
          const user = data.adminUsers.find(item => item.id === Number(adminUser[1]))
          if (!user) throw new PreviewError(404, '本地演示用户不存在')
          return user
        }
        if (path === '/api/v1/admin/user-attributes') return []
        if (path === '/api/v1/admin/proxies/all') return data.adminProxies
        if (path === '/api/v1/admin/keys' || path === '/api/v1/admin/api-keys') {
          const search = query.get('search')?.toLowerCase()
          return page(data.adminKeys.filter(key =>
            (!search || `${key.name} ${key.key}`.toLowerCase().includes(search)) &&
            (!query.get('status') || key.status === query.get('status')) &&
            (!query.get('user_id') || key.user_id === Number(query.get('user_id'))) &&
            (!query.get('group_id') || key.group_ids?.includes(Number(query.get('group_id'))))), query)
        }
        if (path === '/api/v1/admin/usage') return page(sort(filteredAdminUsage(query), query,
          ['id', 'created_at', 'model', 'input_tokens', 'output_tokens', 'actual_cost', 'total_cost', 'duration_ms', 'first_token_ms'], 'created_at'), query)
        if (path === '/api/v1/admin/usage/stats') {
          const rows = filteredAdminUsage(query)
          const endpoints = buckets(rows, row => row.inbound_endpoint || '/v1/responses').map(([endpoint, items]) => ({ endpoint, ...chartSummary(items) }))
          return { ...summarize(rows), total_account_cost: rows.reduce((sum, row) => sum + (row.account_stats_cost ?? row.total_cost), 0), endpoints, upstream_endpoints: [], endpoint_paths: [] }
        }
        if (path === '/api/v1/admin/usage/search-users') {
          const keyword = query.get('q')?.toLowerCase() ?? ''
          return data.adminUsers.filter(user => !keyword || user.email.toLowerCase().includes(keyword)).slice(0, 30)
            .map(user => ({ id: user.id, email: user.email, deleted: Boolean(user.deleted_at) }))
        }
        if (path === '/api/v1/admin/usage/search-api-keys') {
          const keyword = query.get('q')?.toLowerCase() ?? ''
          return data.adminKeys.filter(key =>
            (!query.get('user_id') || key.user_id === Number(query.get('user_id'))) &&
            (!keyword || key.name.toLowerCase().includes(keyword))).slice(0, 30)
            .map(key => ({ id: key.id, name: key.name, user_id: key.user_id }))
        }
        if (path === '/api/v1/admin/usage/cleanup-tasks') return page([], query)
        if (path === '/api/v1/admin/ops/errors') return page([], query)
        if (path === '/api/v1/groups/available') return data.groups
        if (path === '/api/v1/groups/rates') return {}
        if (path === '/api/v1/user/platform-quotas') return { platform_quotas: [] }
        if (['/api/v1/announcements', '/api/v1/user/custom-endpoints'].includes(path)) return []
        if (path === '/api/v1/channel-monitors') return { items: data.monitorViews }
        const monitorDetail = path.match(/^\/api\/v1\/channel-monitors\/(\d+)\/status$/)
        if (monitorDetail) {
          const detail = data.monitorDetails[Number(monitorDetail[1])]
          if (!detail) throw new PreviewError(404, '本地演示监控项不存在')
          return detail
        }
        if (path === '/api/v1/channel-monitor-v2/dimensions') return data.monitorDimensions
        if (path === '/api/v1/channel-monitor-v2/snapshot') {
          const rows = selectedMonitorRows(query)
          if (rows.length === 1) return { ...data.monitorSnapshot, metrics: rows[0].metrics, health: rows[0].health, trend: rows[0].buckets }
          return data.monitorSnapshot
        }
        if (path === '/api/v1/channel-monitor-v2/matrix') {
          return { coverage: data.monitorCoverage, group_by: query.get('group_by') || 'platform_group', items: selectedMonitorRows(query) }
        }
        if (path === '/api/v1/channel-monitor-v2/models') {
          const platforms = query.getAll('platform'), models = query.getAll('model')
          return { coverage: data.monitorCoverage, items: data.monitorModels.filter(item =>
            (!platforms.length || platforms.includes(item.platform)) && (!models.length || models.includes(item.model))) }
        }
        if (path === '/api/v1/channel-monitor-v2/errors') return { coverage: data.monitorCoverage, items: [
          { category: 'rate_or_capacity', count: 184, rate: 0.054, details: [{ platform: 'grok', model: 'grok-4', status_code: 429, message: '本地演示：上游容量不足', count: 151 }] },
          { category: 'timeout', count: 42, rate: 0.012, details: [{ platform: 'gemini', model: 'gemini-2.5-pro', status_code: 504, message: '本地演示：响应较慢', count: 42 }] },
          { category: 'client_cancelled', count: 18, rate: 0.005, ignored: true },
        ] }
        if (path === '/api/v1/channel-monitor-v2/users') return { coverage: data.monitorCoverage, items: [
          { user_id: data.user.id, rank: 1, email: data.user.email, username: data.user.username, display_label: '我（本地演示）', is_self: true, can_drilldown: true, metrics: data.monitorSnapshot.metrics },
          { rank: 2, display_label: '匿名演示用户', is_self: false, can_drilldown: false, metrics: { ...data.monitorSnapshot.metrics, request_count: 680, success_requests: 648, error_requests: 32 } },
        ] }
        if (path === '/api/v1/channels/available') return data.availableChannels
        if (path === '/api/v1/model-plaza') return data.modelPlaza
        if (path === '/api/v1/payment/config') return data.paymentConfig
        if (path === '/api/v1/payment/plans') return data.paymentPlans
        if (path === '/api/v1/payment/checkout-info') return data.checkoutInfo
        if (path === '/api/v1/payment/limits') return { methods: data.checkoutInfo.methods, global_min: data.checkoutInfo.global_min, global_max: data.checkoutInfo.global_max }
        if (path === '/api/v1/payment/orders/my') {
          const status = query.get('status')
          const orders = data.paymentOrders.filter(order => !status || order.status === status).sort((a, b) => b.id - a.id)
          return page(orders, query)
        }
        if (path === '/api/v1/payment/orders/refund-eligible-providers') return { provider_instance_ids: ['local-demo-alipay', 'local-demo-wxpay'] }
        const paymentOrder = path.match(/^\/api\/v1\/payment\/orders\/(\d+)$/)
        if (paymentOrder) return findOrder(Number(paymentOrder[1]))
        if (path === '/api/v1/subscriptions') return data.subscriptions
        if (path === '/api/v1/subscriptions/active') return data.subscriptions.filter(item => item.status === 'active')
        if (path === '/api/v1/subscriptions/progress') return data.subscriptionProgress
        const subscriptionProgress = path.match(/^\/api\/v1\/subscriptions\/(\d+)\/progress$/)
        if (subscriptionProgress) {
          const progress = data.subscriptionProgress.find(item => item.subscription_id === Number(subscriptionProgress[1]))
          if (!progress) throw new PreviewError(404, '本地演示订阅不存在')
          return progress
        }
        if (path === '/api/v1/subscriptions/summary') {
          const active = data.subscriptions.filter(item => item.status === 'active')
          return { active_count: active.length, subscriptions: active.map(item => ({
            id: item.id, group_name: item.group?.name ?? `#${item.group_id}`, status: item.status,
            daily_progress: item.group?.daily_limit_usd ? item.daily_usage_usd / item.group.daily_limit_usd * 100 : null,
            weekly_progress: item.group?.weekly_limit_usd ? item.weekly_usage_usd / item.group.weekly_limit_usd * 100 : null,
            monthly_progress: item.group?.monthly_limit_usd ? item.monthly_usage_usd / item.group.monthly_limit_usd * 100 : null,
            expires_at: item.expires_at, days_remaining: item.expires_at ? Math.ceil((Date.parse(item.expires_at) - now.getTime()) / 86_400_000) : null,
          })) }
        }
        if (path === '/api/v1/redeem/history') {
          return query.has('page') || query.has('page_size') ? page(data.redeemHistory, query) : data.redeemHistory
        }
        if (path === '/api/v1/user/aff') return data.affiliateDetail
        if (path === '/api/v1/user/cf-allowlist') return { ...data.cfAllowlist, used_slots: data.cfAllowlist.items.length }
        if (path === '/api/v1/user/totp/status') return { enabled: false, enabled_at: null, feature_enabled: false }
        if (path === '/api/v1/user/totp/verification-method') return { method: 'password' }
        if (path === '/v1/models') return { object: 'list', data: data.models.map(id => ({ id, object: 'model', owned_by: 'local-demo' })) }
        if (path === '/api/v1/keys') {
          const items = data.keys.filter(key => {
            const search = query.get('search')?.toLowerCase()
            return (!search || `${key.name} ${key.key}`.toLowerCase().includes(search)) &&
              (!query.get('status') || key.status === query.get('status')) &&
              (!query.get('group_id') || key.group_ids?.includes(Number(query.get('group_id'))))
          })
          return page(sort(items, query, ['id', 'name', 'created_at', 'last_used_at', 'quota_used', 'status', 'group_id', 'current_concurrency', 'expires_at'], 'id'), query)
        }
        if (/^\/api\/v1\/keys\/\d+$/.test(path)) {
          const key = data.keys.find(item => item.id === Number(path.split('/').pop()))
          if (!key) throw new PreviewError(404, '演示密钥不存在')
          return key
        }
        if (path === '/api/v1/usage/errors') return page([], query)
        if (path === '/api/v1/usage') return page(sort(filteredUsage(query), query,
          ['id', 'created_at', 'model', 'input_tokens', 'output_tokens', 'actual_cost', 'total_cost', 'duration_ms', 'first_token_ms'], 'created_at'), query)
        if (/^\/api\/v1\/usage\/\d+$/.test(path)) {
          const row = data.usage.find(item => item.id === Number(path.split('/').pop()))
          if (!row) throw new PreviewError(404, '演示记录不存在')
          return row
        }
        if (path === '/api/v1/usage/stats') {
          const rows = filteredUsage(query)
          const endpoints = buckets(rows, row => row.inbound_endpoint || '/v1/responses')
            .map(([endpoint, items]) => ({ endpoint, ...chartSummary(items) }))
          return { ...summarize(rows), endpoints, upstream_endpoints: [], endpoint_paths: [] }
        }
        if (path === '/api/v1/usage/dashboard/stats') {
          const timezone = timezoneOf(query)
          const month = data.usage.filter(row => new Date(row.created_at).getTime() >= now.getTime() - 30 * 86_400_000)
          const today = month.filter(row => dateLabel(row.created_at, timezone) === dateLabel(data.now, timezone))
          const todayStats = Object.fromEntries(Object.entries(summarize(today)).filter(([key]) => key.startsWith('total_')).map(([key, value]) => [key.replace('total_', 'today_'), value]))
          const recent = summarize(month.filter(row => new Date(row.created_at).getTime() > now.getTime() - 5 * 60_000))
          const byPlatform = data.groups.map(group => {
            const total = summarize(month.filter(row => row.group_id === group.id))
            const daily = summarize(today.filter(row => row.group_id === group.id))
            return { platform: group.platform, total_requests: total.total_requests,
              total_tokens: total.total_tokens, total_actual_cost: total.total_actual_cost,
              today_requests: daily.total_requests, today_tokens: daily.total_tokens,
              today_actual_cost: daily.total_actual_cost }
          })
          return { ...summarize(month), ...todayStats, total_api_keys: data.keys.length,
            active_api_keys: data.keys.filter(key => key.status === 'active').length,
            rpm: recent.total_requests / 5, tpm: recent.total_tokens / 5, by_platform: byPlatform }
        }
        if (['/api/v1/usage/dashboard/trend', '/api/v1/usage/dashboard/models', '/api/v1/usage/dashboard/snapshot-v2'].includes(path)) return charts(query)
        const daily = path.match(/^\/api\/v1\/user\/api-keys\/(\d+)\/usage\/daily$/)
        if (daily) {
          const days = integer(query.get('days'), 30, 90)
          const dailyQuery = new URLSearchParams(query)
          dailyQuery.set('api_key_id', daily[1])
          dailyQuery.set('start_date', new Date(now.getTime() - (days - 1) * 86_400_000).toISOString().slice(0, 10))
          const result = charts(dailyQuery)
          return { ...result, days, items: result.trend.map(row => ({ ...row, cache_write_tokens: row.cache_creation_tokens })) }
        }
        throw new PreviewError(404, '此接口未提供演示数据，不会连接真实后端')
      }
      if (method === 'POST' && path === '/api/v1/__preview/role') {
        if (body.role !== 'admin' && body.role !== 'user') throw new PreviewError(422, '预览身份仅支持 admin 或 user')
        data.user.role = body.role
        data.user.username = body.role === 'admin' ? '本地预览管理员' : '演示用户'
        data.user.email = body.role === 'admin' ? 'admin-preview@example.test' : 'console-preview@example.test'
        return { ...data.user, run_mode: 'standard' }
      }
      if (method === 'POST' && path === '/api/v1/admin/dashboard/users-usage') {
        if (!Array.isArray(body.user_ids) || body.user_ids.length > 100 || body.user_ids.some(id => !Number.isSafeInteger(id))) {
          throw new PreviewError(422, '无效的用户列表')
        }
        const today = dateLabel(data.now, 'UTC')
        return { stats: Object.fromEntries(body.user_ids.map(id => {
          const rows = data.adminUsage.filter(row => row.user_id === id)
          const todayRows = rows.filter(row => dateLabel(row.created_at, 'UTC') === today)
          return [String(id), {
            user_id: id,
            total_actual_cost: summarize(rows).total_actual_cost,
            today_actual_cost: summarize(todayRows).total_actual_cost,
            by_platform: data.adminGroups.map(group => ({
              platform: group.platform,
              total_actual_cost: summarize(rows.filter(row => row.group_id === group.id)).total_actual_cost,
              today_actual_cost: summarize(todayRows.filter(row => row.group_id === group.id)).total_actual_cost,
            })),
          }]
        })) }
      }
      if (method === 'POST' && path === '/api/v1/admin/dashboard/api-keys-usage') {
        if (!Array.isArray(body.api_key_ids) || body.api_key_ids.length > 100 || body.api_key_ids.some(id => !Number.isSafeInteger(id))) {
          throw new PreviewError(422, '无效的密钥列表')
        }
        const today = dateLabel(data.now, 'UTC')
        return { stats: Object.fromEntries(body.api_key_ids.map(id => {
          const rows = data.adminUsage.filter(row => row.api_key_id === id)
          return [String(id), { api_key_id: id, total_actual_cost: summarize(rows).total_actual_cost,
            today_actual_cost: summarize(rows.filter(row => dateLabel(row.created_at, 'UTC') === today)).total_actual_cost }]
        })) }
      }
      if (method === 'POST' && path === '/api/v1/admin/accounts/usage/batch') {
        if (!Array.isArray(body.account_ids) || body.account_ids.some(id => !Number.isSafeInteger(id))) throw new PreviewError(422, '无效的账号列表')
        return { usage: Object.fromEntries(body.account_ids.map(id => [String(id), data.usageByAccount[String(id)] ?? null])), errors: {} }
      }
      if (method === 'POST' && path === '/api/v1/admin/accounts/today-stats/batch') {
        if (!Array.isArray(body.account_ids) || body.account_ids.some(id => !Number.isSafeInteger(id))) throw new PreviewError(422, '无效的账号列表')
        return { stats: Object.fromEntries(body.account_ids.map(id => [String(id), data.todayStatsByAccount[String(id)] ?? { requests: 0, tokens: 0, cost: 0 }])) }
      }
      if (method === 'POST' && path === '/api/v1/admin/user-attributes/batch') {
        if (!Array.isArray(body.user_ids) || body.user_ids.some(id => !Number.isSafeInteger(id))) throw new PreviewError(422, '无效的用户列表')
        return { attributes: Object.fromEntries(body.user_ids.map(id => [String(id), {}])) }
      }
      if (method === 'POST' && path === '/api/v1/usage/dashboard/api-keys-usage') {
        if (!Array.isArray(body.api_key_ids) || body.api_key_ids.length > 100 || body.api_key_ids.some(id => !Number.isSafeInteger(id))) throw new PreviewError(422, '无效的密钥列表')
        const today = dateLabel(data.now, 'UTC')
        return { stats: Object.fromEntries(body.api_key_ids.map(id => {
          const rows = data.usage.filter(row => row.api_key_id === id)
          return [String(id), { api_key_id: id, total_actual_cost: summarize(rows).total_actual_cost,
            today_actual_cost: summarize(rows.filter(row => dateLabel(row.created_at, 'UTC') === today)).total_actual_cost }]
        })) }
      }
      if (method === 'POST' && path === '/api/v1/payment/orders') {
        const amount = body.amount
        const paymentType = body.payment_type
        const orderType = body.order_type
        if (typeof amount !== 'number' || !Number.isFinite(amount) || amount <= 0) throw new PreviewError(422, '演示订单金额必须大于 0')
        if (typeof paymentType !== 'string' || !data.checkoutInfo.methods[paymentType]?.available) throw new PreviewError(422, '演示支付方式不可用')
        if (orderType !== 'balance' && orderType !== 'subscription') throw new PreviewError(422, '无效的演示订单类型')
        const plan = orderType === 'subscription' ? data.paymentPlans.find(item => item.id === body.plan_id) : undefined
        if (orderType === 'subscription' && !plan) throw new PreviewError(422, '本地演示套餐不存在')
        const limits = data.checkoutInfo.methods[paymentType]
        if (amount < limits.single_min || amount > limits.single_max) throw new PreviewError(422, '演示订单金额超出该方式限额')
        const currency = limits.currency || 'CNY'
        const converted = orderType === 'subscription' && currency === 'CNY' ? amount * data.checkoutInfo.subscription_usd_to_cny_rate : amount
        const feeRate = limits.recharge_fee_rate ?? data.checkoutInfo.recharge_fee_rate
        const payAmount = Math.round(converted * (1 + feeRate / 100) * 100) / 100
        const outTradeNo = `LOCAL-DEMO-${now.toISOString().slice(0, 10).replaceAll('-', '')}-${String(nextOrderId)}`
        const order: PaymentOrder = {
          id: nextOrderId++, user_id: data.user.id, amount, pay_amount: payAmount, currency,
          fee_rate: feeRate, payment_type: paymentType, out_trade_no: outTradeNo, status: 'PENDING',
          order_type: orderType, plan_id: plan?.id, created_at: now.toISOString(),
          expires_at: new Date(now.getTime() + 30 * 60_000).toISOString(), refund_amount: 0,
          provider_instance_id: `local-demo-${paymentType}`,
        }
        data.paymentOrders.unshift(order)
        return {
          order_id: order.id,
          amount: order.amount,
          pay_amount: order.pay_amount,
          fee_rate: order.fee_rate,
          currency: order.currency,
          expires_at: order.expires_at,
          payment_type: order.payment_type,
          out_trade_no: order.out_trade_no,
          payment_mode: 'qrcode',
          result_type: 'order_created',
          qr_code: `LOCAL-DEMO-PAYMENT:${order.out_trade_no}`,
          resume_token: `local-demo-resume-${order.id}`,
        }
      }
      const cancelOrder = path.match(/^\/api\/v1\/payment\/orders\/(\d+)\/cancel$/)
      if (method === 'POST' && cancelOrder) {
        const order = findOrder(Number(cancelOrder[1]))
        if (order.status !== 'PENDING') throw new PreviewError(409, '只有待支付的本地演示订单可以取消')
        order.status = 'CANCELLED'
        return { message: '本地演示订单已取消' }
      }
      const refundOrder = path.match(/^\/api\/v1\/payment\/orders\/(\d+)\/refund-request$/)
      if (method === 'POST' && refundOrder) {
        const order = findOrder(Number(refundOrder[1]))
        if (order.status !== 'COMPLETED') throw new PreviewError(409, '只有已完成的本地演示订单可以申请退款')
        if (typeof body.reason !== 'string' || !body.reason.trim()) throw new PreviewError(422, '请输入本地演示退款原因')
        order.status = 'REFUND_REQUESTED'
        order.refund_request_reason = body.reason.trim()
        order.refund_requested_at = now.toISOString()
        return order
      }
      if (method === 'POST' && path === '/api/v1/payment/orders/verify') return orderByTradeNo(body.out_trade_no)
      if (method === 'POST' && path === '/api/v1/payment/public/orders/verify') return publicOrder(orderByTradeNo(body.out_trade_no))
      if (method === 'POST' && path === '/api/v1/payment/public/orders/resolve') {
        if (typeof body.resume_token !== 'string' || !/^local-demo-resume-\d+$/.test(body.resume_token)) throw new PreviewError(404, '本地演示恢复令牌不存在')
        return publicOrder(findOrder(Number(body.resume_token.split('-').pop())))
      }
      if (method === 'POST' && path === '/api/v1/redeem') {
        const code = typeof body.code === 'string' ? body.code.trim().toUpperCase() : ''
        if (!['DEMO-BALANCE-10', 'DEMO-SUB-30'].includes(code)) throw new PreviewError(422, '请输入本地演示兑换码 DEMO-BALANCE-10 或 DEMO-SUB-30')
        if (usedDemoCodes.has(code)) throw new PreviewError(409, '该本地演示兑换码已在当前进程使用')
        usedDemoCodes.add(code)
        if (code === 'DEMO-BALANCE-10') {
          data.user.balance += 10
          data.redeemHistory.unshift({ id: nextRedeemId++, code, type: 'balance', value: 10, status: 'used', used_at: now.toISOString(), created_at: now.toISOString() })
          return { message: '本地演示余额已增加', type: 'balance', value: 10, new_balance: data.user.balance }
        }
        const group = data.groups[2]
        if (!group) throw new PreviewError(500, '本地演示分组缺失')
        const subscription = {
          id: Math.max(...data.subscriptions.map(item => item.id)) + 1,
          user_id: data.user.id,
          group_id: group.id,
          status: 'active' as const,
          starts_at: now.toISOString(),
          daily_usage_usd: 0,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          daily_window_start: now.toISOString(),
          weekly_window_start: now.toISOString(),
          monthly_window_start: now.toISOString(),
          created_at: now.toISOString(),
          updated_at: now.toISOString(),
          expires_at: new Date(now.getTime() + 30 * 86_400_000).toISOString(),
          group,
        }
        data.subscriptions.unshift(subscription)
        data.redeemHistory.unshift({ id: nextRedeemId++, code, type: 'subscription', value: 30, status: 'used', used_at: now.toISOString(), created_at: now.toISOString(), group_id: group.id, validity_days: 30, group: { id: group.id, name: group.name } })
        return { message: '本地演示订阅已添加', type: 'subscription', value: 30, group_name: group.name, validity_days: 30 }
      }
      if (method === 'POST' && path === '/api/v1/user/aff/transfer') {
        const amount = data.affiliateDetail.aff_quota
        if (amount <= 0) throw new PreviewError(409, '当前没有可转入的本地演示额度')
        data.affiliateDetail.aff_quota = 0
        data.affiliateDetail.aff_history_quota += amount
        data.user.balance = Math.round((data.user.balance + amount) * 100) / 100
        return { transferred_quota: amount, balance: data.user.balance }
      }
      if (method === 'POST' && path === '/api/v1/user/cf-allowlist') {
        const ip = typeof body.ip === 'string' ? body.ip.trim() : ''
        if (!isIP(ip)) throw new PreviewError(422, '请输入有效的 IPv4 或 IPv6 地址')
        if (data.cfAllowlist.items.some(item => item.ip === ip)) throw new PreviewError(409, '该 IP 已在本地演示白名单中')
        if (data.cfAllowlist.items.length >= data.cfAllowlist.max_slots) throw new PreviewError(409, '本地演示白名单名额已满')
        const item = { id: nextAllowlistId++, ip, created_at: now.toISOString() }
        data.cfAllowlist.items.push(item)
        return item
      }
      if (method === 'PUT' && path === '/api/v1/admin/settings') {
        if (!('payment_recharge_center_enabled' in body)) throw new PreviewError(405, '演示不支持修改其他系统设置')
        if ('payment_recharge_center_enabled' in body) {
          if (typeof body.payment_recharge_center_enabled !== 'boolean') throw new PreviewError(422, '页面开关必须是布尔值')
          data.checkoutInfo.recharge_center_enabled = body.payment_recharge_center_enabled
        }
        return adminSettings()
      }
      if (method === 'PUT' && path === '/api/v1/settings/public') {
        if (body.channel_monitor_mode !== 'v1' && body.channel_monitor_mode !== 'v2') throw new PreviewError(422, '本地预览仅支持 v1 或 v2 监控模式')
        settings.channel_monitor_mode = body.channel_monitor_mode
        return settings
      }
      if (method === 'PUT' && path === '/api/v1/user') {
        if ('username' in body) {
          if (typeof body.username !== 'string' || !body.username.trim() || body.username.length > 100) throw new PreviewError(422, '演示用户名须为 1 至 100 个字符')
          data.user.username = body.username.trim()
        }
        if ('avatar_url' in body) data.user.avatar_url = typeof body.avatar_url === 'string' && body.avatar_url ? body.avatar_url : null
        if ('balance_notify_enabled' in body) data.user.balance_notify_enabled = body.balance_notify_enabled === true
        if ('balance_notify_threshold' in body) {
          const threshold = body.balance_notify_threshold
          if (threshold !== null && (typeof threshold !== 'number' || threshold < 0)) throw new PreviewError(422, '演示提醒阈值无效')
          data.user.balance_notify_threshold = threshold as number | null
        }
        data.user.updated_at = now.toISOString()
        return data.user
      }
      if (method === 'PUT' && path === '/api/v1/user/password') return { message: '本地演示密码未写入任何账户' }
      if (method === 'POST' && path === '/api/v1/keys') {
        if (!('name' in body)) throw new PreviewError(422, '请输入演示密钥名称')
        const key = editKey({ ...makeKey(nextId, undefined, new Date().toISOString()), status: 'active', quota: 0 }, body)
        data.keys.unshift(key)
        nextId++
        return key
      }
      if (['PUT', 'DELETE'].includes(method) && /^\/api\/v1\/keys\/\d+$/.test(path)) {
        const index = data.keys.findIndex(key => key.id === Number(path.split('/').pop()))
        if (index < 0) throw new PreviewError(404, '演示密钥不存在')
        if (method === 'DELETE') {
          data.keys.splice(index, 1)
          return { message: '已删除本地演示密钥' }
        }
        data.keys[index] = editKey(data.keys[index], body)
        return data.keys[index]
      }
      const allowlistDelete = path.match(/^\/api\/v1\/user\/cf-allowlist\/(\d+)$/)
      if (method === 'DELETE' && allowlistDelete) {
        const index = data.cfAllowlist.items.findIndex(item => item.id === Number(allowlistDelete[1]))
        if (index < 0) throw new PreviewError(404, '本地演示 IP 不存在')
        data.cfAllowlist.items.splice(index, 1)
        return { message: '本地演示 IP 已移除' }
      }
      throw new PreviewError(501, '演示不支持此操作，不会写入或转发到真实后端')
    },
  }
}
