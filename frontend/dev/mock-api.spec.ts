import { describe, expect, it } from 'vitest'
import { createMockApi, PreviewError } from './mock-api'
import type { ApiKey, PaginatedResponse, UsageLog } from '../src/types'
import type { UserDashboardStats } from '../src/api/usage'

const date = new Date('2026-09-17T13:00:00Z')
const query = (value = '') => new URLSearchParams(value)

describe('local demo API contracts', () => {
  it('supplies consistent platform totals and recent activity even immediately after midnight', () => {
    const api = createMockApi(new Date('2026-09-17T16:01:00Z'))
    const stats = api.handle('GET', '/api/v1/usage/dashboard/stats', query('timezone=Asia%2FShanghai')) as UserDashboardStats
    expect(stats.today_requests).toBeGreaterThan(0)
    expect(stats.rpm).toBeGreaterThan(0)
    expect(stats.by_platform).toHaveLength(6)
    expect(stats.by_platform!.reduce((sum, item) => sum + item.total_requests, 0)).toBe(stats.total_requests)
    expect(stats.by_platform!.reduce((sum, item) => sum + item.total_actual_cost, 0)).toBeCloseTo(stats.total_actual_cost, 8)
  })

  it('provides fake identities, real pagination, filtering and sorting', () => {
    const api = createMockApi(date)
    expect(api.user.email).toMatch(/\.test$/)
    const page = api.handle('GET', '/api/v1/keys', query('page=2&page_size=10&sort_by=id&sort_order=asc')) as PaginatedResponse<ApiKey>
    expect(page).toMatchObject({ total: 26, page: 2, pages: 3, page_size: 10 })
    expect(page.items.map(key => key.id)).toEqual([11, 12, 13, 14, 15, 16, 17, 18, 19, 20])
    expect(page.items.every(key => key.key.startsWith('sk-demo-not-valid-'))).toBe(true)
    const filtered = api.handle('GET', '/api/v1/keys', query('group_id=1&status=active&search=000001')) as PaginatedResponse<ApiKey>
    expect(filtered.items.map(key => key.id)).toEqual([1])
    expect(() => api.handle('GET', '/api/v1/keys', query('page_size=999999'))).toThrow(PreviewError)
  })

  it('keeps CRUD across reads, uses monotonic IDs and resets on a new process', () => {
    const api = createMockApi(date)
    const created = api.handle('POST', '/api/v1/keys', query(), { name: '浏览器演示', group_ids: [1, 2], quota: 8, expires_in_days: 7 }) as ApiKey
    expect(created).toMatchObject({ id: 27, group_id: 1, group_ids: [1, 2], quota: 8, status: 'active' })
    expect(created.expires_at).toBe('2026-09-24T13:00:00.000Z')
    api.handle('PUT', '/api/v1/keys/27', query(), { expires_at: '' })
    expect(api.handle('GET', '/api/v1/keys/27', query())).toMatchObject({ expires_at: null })
    api.handle('PUT', '/api/v1/keys/27', query(), { name: '已修改', status: 'inactive', group_id: 3 })
    expect(api.handle('GET', '/api/v1/keys/27', query())).toMatchObject({ name: '已修改', status: 'inactive', group_id: 3, group_ids: [3] })
    expect(() => api.handle('PUT', '/api/v1/keys/27', query(), { name: '', group_id: 999 })).toThrow()
    expect(api.handle('GET', '/api/v1/keys/27', query())).toMatchObject({ name: '已修改' })
    api.handle('DELETE', '/api/v1/keys/27', query())
    expect(() => api.handle('GET', '/api/v1/keys/27', query())).toThrow('不存在')
    expect(api.handle('POST', '/api/v1/keys', query(), { name: '新的演示' })).toMatchObject({ id: 28 })
    expect(() => createMockApi(date).handle('GET', '/api/v1/keys/28', query())).toThrow('不存在')
  })

  it('keeps usage filters, totals, model/group/trend charts consistent', () => {
    const api = createMockApi(date)
    const filter = query('group_id=1&model=claude&request_type=stream&start_date=2026-09-11&end_date=2026-09-17&timezone=Asia%2FShanghai')
    const list = api.handle('GET', '/api/v1/usage', filter) as PaginatedResponse<UsageLog>
    expect(list.total).toBeGreaterThan(20)
    expect(list.items.every(row => row.group_id === 1 && row.stream && row.model.includes('claude'))).toBe(true)
    const stats = api.handle('GET', '/api/v1/usage/stats', filter) as { total_requests: number; total_actual_cost: number }
    const snapshot = api.handle('GET', '/api/v1/usage/dashboard/snapshot-v2', filter) as { trend: { requests: number; actual_cost: number }[]; models: { model: string }[]; groups: { group_id: number }[] }
    expect(stats.total_requests).toBe(list.total)
    expect(snapshot.trend).toHaveLength(7)
    expect(snapshot.trend.reduce((sum, point) => sum + point.requests, 0)).toBe(stats.total_requests)
    expect(snapshot.trend.reduce((sum, point) => sum + point.actual_cost, 0)).toBeCloseTo(stats.total_actual_cost, 8)
    expect(snapshot.models.map(model => model.model)).toEqual(['claude-sonnet-4-5'])
    expect(snapshot.groups.map(group => group.group_id)).toEqual([1])
    filter.set('granularity', 'hour')
    const hourly = api.handle('GET', '/api/v1/usage/dashboard/trend', filter) as { trend: unknown[] }
    expect(hourly.trend.length).toBeGreaterThan(7)
  })

  it('supports empty states and key usage statistics without invented rows', () => {
    const api = createMockApi(date)
    expect(api.handle('GET', '/api/v1/usage', query('model=not-a-demo-model'))).toMatchObject({ items: [], total: 0, pages: 0 })
    expect(api.handle('GET', '/api/v1/usage/stats', query('api_key_id=999'))).toMatchObject({ total_requests: 0, total_tokens: 0, total_actual_cost: 0 })
    expect(api.handle('POST', '/api/v1/usage/dashboard/api-keys-usage', query(), { api_key_ids: [1, 999] })).toMatchObject({ stats: { '1': { api_key_id: 1 }, '999': { total_actual_cost: 0 } } })
    expect(api.handle('GET', '/api/v1/user/api-keys/1/usage/daily', query('days=7'))).toMatchObject({ days: 7 })
  })

  it('rejects unsupported operations and malformed input explicitly', () => {
    const api = createMockApi(date)
    for (const path of ['/api/v1/user/password', '/api/v1/admin/users', '/v1/responses', '/setup/install']) {
      expect(() => api.handle('POST', path, query())).toThrow('演示不支持')
    }
    expect(() => api.handle('GET', '/api/new-endpoint', query())).toThrow('不会连接真实后端')
    expect(() => api.handle('POST', '/api/v1/keys', query(), { name: 'demo', custom_key: 'supplied' })).toThrow('自定义密钥')
    expect(() => api.handle('POST', '/api/v1/keys', query(), { name: 'demo', quota: -1 })).toThrow('额度')
    expect(() => api.handle('GET', '/api/v1/usage', query('timezone=invalid'))).toThrow('时区')
    expect(() => api.handle('GET', '/api/v1/usage', query('start_date=2026-10-01&end_date=2026-09-01'))).toThrow('开始日期')
  })
})
