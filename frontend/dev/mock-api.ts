import type { ApiKey, PaginatedResponse, UsageLog } from '../src/types'
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

function page<T>(items: T[], query: URLSearchParams): PaginatedResponse<T> {
  const current = integer(query.get('page'), 1, 1_000_000)
  const size = integer(query.get('page_size'), 20, 100)
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

function buckets(rows: UsageLog[], key: (row: UsageLog) => string) {
  const result = new Map<string, UsageLog[]>()
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
  let nextId = data.keys.length + 1
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
      if (body.expires_at !== null && (typeof body.expires_at !== 'string' || !Number.isFinite(Date.parse(body.expires_at)))) throw new PreviewError(422, '无效的过期时间')
      next.expires_at = body.expires_at as string | null
    }
    if (body.reset_quota === true) next.quota_used = 0
    if (body.reset_rate_limit_usage === true) next.usage_5h = next.usage_1d = next.usage_7d = 0
    next.updated_at = new Date().toISOString()
    return next
  }
  return {
    user: data.user,
    handle(method: string, path: string, query: URLSearchParams, body: Record<string, unknown> = {}): unknown {
      if (method === 'GET') {
        if (path === '/setup/status') return { needs_setup: false, step: 'complete' }
        if (path === '/api/v1/settings/public') return settings
        if (['/api/v1/auth/me', '/api/v1/user/profile'].includes(path)) return { ...data.user, run_mode: 'standard' }
        if (path === '/api/v1/groups/available') return data.groups
        if (path === '/api/v1/groups/rates') return {}
        if (path === '/api/v1/user/platform-quotas') return { platform_quotas: [] }
        if (['/api/v1/announcements', '/api/v1/user/custom-endpoints', '/api/v1/subscriptions',
          '/api/v1/subscriptions/active', '/api/v1/subscriptions/progress'].includes(path)) return []
        if (path === '/api/v1/subscriptions/summary') return { active_count: 0, subscriptions: [] }
        if (path === '/v1/models') return { object: 'list', data: data.models.map(id => ({ id, object: 'model', owned_by: 'local-demo' })) }
        if (path === '/api/v1/keys') {
          const items = data.keys.filter(key => {
            const search = query.get('search')?.toLowerCase()
            return (!search || `${key.name} ${key.key}`.toLowerCase().includes(search)) &&
              (!query.get('status') || key.status === query.get('status')) &&
              (!query.get('group_id') || key.group_ids?.includes(Number(query.get('group_id'))))
          })
          return page(sort(items, query, ['id', 'name', 'created_at', 'last_used_at', 'quota_used', 'status', 'group_id'], 'id'), query)
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
          const today = data.usage.filter(row => dateLabel(row.created_at, timezone) === dateLabel(data.now, timezone))
          const todayStats = Object.fromEntries(Object.entries(summarize(today)).filter(([key]) => key.startsWith('total_')).map(([key, value]) => [key.replace('total_', 'today_'), value]))
          return { ...summarize(data.usage), ...todayStats, total_api_keys: data.keys.length,
            active_api_keys: data.keys.filter(key => key.status === 'active').length, rpm: 0, tpm: 0 }
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
      if (method === 'POST' && path === '/api/v1/usage/dashboard/api-keys-usage') {
        if (!Array.isArray(body.api_key_ids) || body.api_key_ids.length > 100 || body.api_key_ids.some(id => !Number.isSafeInteger(id))) throw new PreviewError(422, '无效的密钥列表')
        const today = dateLabel(data.now, 'UTC')
        return { stats: Object.fromEntries(body.api_key_ids.map(id => {
          const rows = data.usage.filter(row => row.api_key_id === id)
          return [String(id), { api_key_id: id, total_actual_cost: summarize(rows).total_actual_cost,
            today_actual_cost: summarize(rows.filter(row => dateLabel(row.created_at, 'UTC') === today)).total_actual_cost }]
        })) }
      }
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
      throw new PreviewError(501, '演示不支持此操作，不会写入或转发到真实后端')
    },
  }
}
