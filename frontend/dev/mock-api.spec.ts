import { describe, expect, it } from 'vitest'
import { createMockApi, PreviewError } from './mock-api'
import type { AccountListItem, AdminGroup, AdminUsageLog, AdminUser, ApiKey, DashboardStats, PaginatedResponse, UsageLog } from '../src/types'
import type { UserDashboardStats } from '../src/api/usage'
import type { MonitorMatrixResponse } from '../src/api/channelMonitorV2'
import type { UserMonitorListResponse } from '../src/api/channelMonitor'
import type { UserAvailableChannel } from '../src/api/channels'
import type { CheckoutInfoResponse, CreateOrderResult, PaymentOrder } from '../src/types/payment'

const date = new Date('2026-09-17T13:00:00Z')
const query = (value = '') => new URLSearchParams(value)

describe('local demo API contracts', () => {
  it('serves paginated redemption history while keeping legacy array reads', () => {
    const api = createMockApi(date)
    const legacy = api.handle('GET', '/api/v1/redeem/history', query()) as unknown[]
    expect(Array.isArray(legacy)).toBe(true)
    const result = api.handle('GET', '/api/v1/redeem/history', query('page=2&page_size=1')) as PaginatedResponse<unknown>
    expect(result).toMatchObject({ page: 2, page_size: 1, total: legacy.length, items: legacy.slice(1, 2) })
  })
  it('serves admin shell reads and large group selectors without enabling writes', () => {
    const api = createMockApi(date)
    expect(api.handle('GET', '/api/v1/admin/compliance', query())).toMatchObject({ required: false })
    expect(api.handle('GET', '/api/v1/admin/settings', query())).toMatchObject({ custom_menu_items: [] })
    expect(api.handle('GET', '/api/v1/admin/settings/web-search-emulation', query())).toEqual({ enabled: false, providers: [] })
    expect(api.handle('GET', '/api/v1/admin/system/check-updates', query())).toMatchObject({ has_update: false })
    expect(api.handle('GET', '/api/v1/admin/groups', query('page_size=1000'))).toMatchObject({ page_size: 1000 })
    expect(() => api.handle('PUT', '/api/v1/admin/settings', query(), {})).toThrow()
  })
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

  it('switches preview role explicitly while keeping user as the default', () => {
    const api = createMockApi(date)
    expect(api.user.role).toBe('user')
    expect(api.handle('POST', '/api/v1/__preview/role', query(), { role: 'admin' })).toMatchObject({
      role: 'admin', email: 'admin-preview@example.test', run_mode: 'standard',
    })
    expect(api.user.role).toBe('admin')
    expect(api.handle('GET', '/api/v1/auth/me', query())).toMatchObject({ role: 'admin', email: 'admin-preview@example.test' })
    expect(api.handle('POST', '/api/v1/__preview/role', query(), { role: 'user' })).toMatchObject({ role: 'user' })
    expect(() => api.handle('POST', '/api/v1/__preview/role', query(), { role: 'owner' })).toThrow('admin 或 user')
  })

  it('serves typed admin dashboard trends and rankings from consistent demo usage', () => {
    const api = createMockApi(date)
    const snapshot = api.handle('GET', '/api/v1/admin/dashboard/snapshot-v2', query('start_date=2026-09-11&end_date=2026-09-17&granularity=day')) as {
      stats: DashboardStats; trend: Array<{ requests: number }>; models: Array<{ model: string }>
    }
    expect(snapshot.stats.total_users).toBe(34)
    expect(snapshot.stats.total_accounts).toBe(24)
    expect(snapshot.trend.reduce((sum, item) => sum + item.requests, 0)).toBeGreaterThan(0)
    expect(snapshot.models).toEqual(expect.arrayContaining([expect.objectContaining({ model: 'gpt-5' })]))
    expect(api.handle('GET', '/api/v1/admin/dashboard/users-trend', query('limit=4'))).toMatchObject({ trend: expect.any(Array) })
    expect(api.handle('GET', '/api/v1/admin/dashboard/users-ranking', query('limit=5'))).toMatchObject({ ranking: expect.any(Array) })
  })

  it('paginates and filters admin accounts, groups, users, keys and usage', () => {
    const api = createMockApi(date)
    const accounts = api.handle('GET', '/api/v1/admin/accounts', query('page=2&page_size=5&platform=openai&sort_by=id&sort_order=asc')) as PaginatedResponse<AccountListItem>
    expect(accounts.page).toBe(2)
    expect(accounts.items.every(item => item.platform === 'openai' && item.credentials_status?.has_api_key === false)).toBe(true)

    const groups = api.handle('GET', '/api/v1/admin/groups', query('platform=gemini&page_size=10')) as PaginatedResponse<AdminGroup>
    expect(groups.items).toHaveLength(1)
    expect(groups.items[0]).toMatchObject({ platform: 'gemini', model_pricing: [], description: expect.stringContaining('演示数据') })

    const users = api.handle('GET', '/api/v1/admin/users', query('role=user&status=active&page_size=10')) as PaginatedResponse<AdminUser>
    expect(users.items.every(item => item.role === 'user' && item.status === 'active' && item.email.endsWith('.test'))).toBe(true)
    expect(api.handle('POST', '/api/v1/admin/dashboard/users-usage', query(), { user_ids: users.items.map(item => item.id) })).toMatchObject({ stats: expect.any(Object) })

    const keys = api.handle('GET', '/api/v1/admin/keys', query(`user_id=${users.items[0].id}&page_size=100`)) as PaginatedResponse<ApiKey>
    expect(keys.items.every(item => item.user_id === users.items[0].id && item.key.includes('not-valid'))).toBe(true)

    const usage = api.handle('GET', '/api/v1/admin/usage', query('model=claude&page=1&page_size=8')) as PaginatedResponse<AdminUsageLog>
    expect(usage.items).toHaveLength(8)
    expect(usage.items.every(item => item.model.includes('claude') && item.account?.name.includes('演示账号'))).toBe(true)
    expect(api.handle('GET', '/api/v1/admin/usage/stats', query('model=claude'))).toMatchObject({ total_requests: usage.total, total_account_cost: expect.any(Number) })
  })

  it('supports admin modal reads but rejects every admin write', () => {
    const api = createMockApi(date)
    expect(api.handle('GET', '/api/v1/admin/users/910002', query())).toMatchObject({ id: 910002, notes: expect.stringContaining('本地预览') })
    expect(api.handle('GET', '/api/v1/admin/users/910002/api-keys', query())).toMatchObject({ items: expect.any(Array) })
    expect(api.handle('GET', '/api/v1/admin/accounts/920001', query())).toMatchObject({ id: 920001, groups: expect.any(Array) })
    for (const [method, path] of [
      ['POST', '/api/v1/admin/users/910002/balance'],
      ['DELETE', '/api/v1/admin/users/910002'],
      ['PUT', '/api/v1/admin/groups/1'],
      ['POST', '/api/v1/admin/accounts/batch-delete'],
      ['POST', '/api/v1/admin/payment/orders/1/refund'],
    ]) expect(() => api.handle(method, path, query(), {})).toThrow('演示不支持')
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

  it('enables preview navigation, defaults monitor to V3 and keeps V1 switching local', () => {
    const api = createMockApi(date)
    expect(api.handle('GET', '/api/v1/settings/public', query())).toMatchObject({
      channel_monitor_enabled: true,
      channel_monitor_mode: 'v2',
      available_channels_enabled: true,
      payment_enabled: true,
      affiliate_enabled: true,
    })
    expect(api.handle('PUT', '/api/v1/settings/public', query(), { channel_monitor_mode: 'v1' })).toMatchObject({ channel_monitor_mode: 'v1' })
    expect(api.handle('GET', '/api/v1/settings/public', query())).toMatchObject({ channel_monitor_mode: 'v1' })
    expect(() => api.handle('PUT', '/api/v1/settings/public', query(), { channel_monitor_mode: 'v3' })).toThrow('v1 或 v2')
    api.handle('PUT', '/api/v1/settings/public', query(), { channel_monitor_mode: 'v2' })
  })

  it('supplies realistic V1 and V2/V3 monitor states with filterable matrix data', () => {
    const api = createMockApi(date)
    const v1 = api.handle('GET', '/api/v1/channel-monitors', query()) as UserMonitorListResponse
    expect(v1.items).toHaveLength(5)
    expect(new Set(v1.items.map(item => item.primary_status))).toEqual(new Set(['operational', 'degraded', 'failed', 'error']))
    expect(v1.items.every(item => item.name.includes('本地预览'))).toBe(true)
    expect(api.handle('GET', `/api/v1/channel-monitors/${v1.items[0].id}/status`, query())).toMatchObject({ id: v1.items[0].id })

    const matrix = api.handle('GET', '/api/v1/channel-monitor-v2/matrix', query('range=90m&group_by=platform_group')) as MonitorMatrixResponse
    expect(matrix.items).toHaveLength(5)
    expect(matrix.items.map(item => item.health.overall)).toEqual(['healthy', 'healthy', 'warning', 'critical', 'unknown'])
    expect(matrix.items.every(item => item.buckets.length === 30)).toBe(true)
    const filtered = api.handle('GET', '/api/v1/channel-monitor-v2/matrix', query('range=24h&group_by=platform_group_model&platform=gemini&model=gemini-2.5-pro')) as MonitorMatrixResponse
    expect(filtered.items).toHaveLength(1)
    expect(filtered.items[0]).toMatchObject({ platform: 'gemini', model: 'gemini-2.5-pro', health: { overall: 'warning' } })
    expect(api.handle('GET', '/api/v1/channel-monitor-v2/errors', query())).toMatchObject({
      items: expect.arrayContaining([expect.objectContaining({ category: 'rate_or_capacity' })]),
    })
  })

  it('returns channel pricing and subscription quota structures from source contracts', () => {
    const api = createMockApi(date)
    const channels = api.handle('GET', '/api/v1/channels/available', query()) as UserAvailableChannel[]
    expect(channels).toHaveLength(2)
    expect(channels.flatMap(channel => channel.platforms).flatMap(section => section.supported_models)).toEqual(expect.arrayContaining([
      expect.objectContaining({ name: 'gpt-5', pricing: expect.objectContaining({ billing_mode: 'token', input_price: 1.25 }) }),
      expect.objectContaining({ name: 'deepseek-chat', pricing: expect.objectContaining({ output_price: 1.1 }) }),
    ]))
    const subscriptions = api.handle('GET', '/api/v1/subscriptions', query()) as Array<{ status: string; group?: { daily_limit_usd: number | null } }>
    expect(subscriptions.map(item => item.status)).toEqual(['active', 'active', 'expired'])
    expect(subscriptions[0].group?.daily_limit_usd).toBe(12)
    expect(api.handle('GET', '/api/v1/subscriptions/summary', query())).toMatchObject({ active_count: 2 })
  })

  it('keeps payment orders local, currency-aware and free of outbound payment URLs', () => {
    const api = createMockApi(date)
    const checkout = api.handle('GET', '/api/v1/payment/checkout-info', query()) as CheckoutInfoResponse
    expect(checkout.methods.alipay.currency).toBe('CNY')
    expect(checkout.methods.epusdt.currency).toBe('USDT')
    expect(checkout.plans[0]).toMatchObject({ price: 29, currency: 'USD' })
    const created = api.handle('POST', '/api/v1/payment/orders', query(), { amount: 20, payment_type: 'alipay', order_type: 'balance' }) as CreateOrderResult
    expect(created).toMatchObject({ order_id: 51007, amount: 20, pay_amount: 20, currency: 'CNY', payment_mode: 'qrcode' })
    expect(created.qr_code).toBe('LOCAL-DEMO-PAYMENT:LOCAL-DEMO-20260917-51007')
    expect(created.pay_url).toBeUndefined()
    expect(created.client_secret).toBeUndefined()
    expect(api.handle('GET', '/api/v1/payment/orders/51007', query())).toMatchObject({ status: 'PENDING', currency: 'CNY' })
    api.handle('POST', '/api/v1/payment/orders/51007/cancel', query())
    expect(api.handle('GET', '/api/v1/payment/orders/51007', query())).toMatchObject({ status: 'CANCELLED' })

    const usdt = api.handle('POST', '/api/v1/payment/orders', query(), { amount: 12, payment_type: 'epusdt', order_type: 'balance' }) as CreateOrderResult
    expect(usdt).toMatchObject({ currency: 'USDT', pay_amount: 12 })
    const orders = api.handle('GET', '/api/v1/payment/orders/my', query('page=1&page_size=20')) as PaginatedResponse<PaymentOrder>
    expect(orders.total).toBe(8)
    expect(orders.items.some(order => order.currency === 'USDT')).toBe(true)
    api.handle('POST', '/api/v1/payment/orders/51005/refund-request', query(), { reason: '仅测试页面交互' })
    expect(api.handle('GET', '/api/v1/payment/orders/51005', query())).toMatchObject({ status: 'REFUND_REQUESTED', refund_request_reason: '仅测试页面交互' })
  })

  it('simulates redeem, affiliate, profile and IP allowlist actions only in memory', () => {
    const api = createMockApi(date)
    expect(api.handle('POST', '/api/v1/redeem', query(), { code: 'DEMO-BALANCE-10' })).toMatchObject({ type: 'balance', new_balance: 138.64 })
    expect(api.handle('GET', '/api/v1/auth/me', query())).toMatchObject({ balance: 138.64 })
    expect(() => api.handle('POST', '/api/v1/redeem', query(), { code: 'DEMO-BALANCE-10' })).toThrow('已在当前进程使用')
    expect(api.handle('POST', '/api/v1/user/aff/transfer', query())).toMatchObject({ transferred_quota: 18.64, balance: 157.28 })
    expect(api.handle('GET', '/api/v1/user/aff', query())).toMatchObject({ aff_quota: 0 })
    expect(api.handle('PUT', '/api/v1/user', query(), { username: '预览资料已修改' })).toMatchObject({ username: '预览资料已修改' })

    const added = api.handle('POST', '/api/v1/user/cf-allowlist', query(), { ip: '198.51.100.24' }) as { id: number }
    expect(api.handle('GET', '/api/v1/user/cf-allowlist', query())).toMatchObject({ used_slots: 3 })
    api.handle('DELETE', `/api/v1/user/cf-allowlist/${added.id}`, query())
    expect(api.handle('GET', '/api/v1/user/cf-allowlist', query())).toMatchObject({ used_slots: 2 })
    expect(() => api.handle('POST', '/api/v1/user/cf-allowlist', query(), { ip: 'not-an-ip' })).toThrow('有效的 IPv4 或 IPv6')
    expect(createMockApi(date).handle('GET', '/api/v1/auth/me', query())).toMatchObject({ balance: 128.64, username: '演示用户' })
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
