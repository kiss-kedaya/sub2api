import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import type { UserDashboardStats } from '@/api/usage'
import type { PlatformQuotaItem, UsageLog } from '@/types'

const preferredMotion = ref('no-preference')
const canUseBatchImage = ref(false)
const push = vi.fn()
const refreshBatchImageAccess = vi.fn()
const refreshUser = vi.fn()
const getDashboardStats = vi.fn()
const getDashboardTrend = vi.fn()
const getDashboardModels = vi.fn()
const getDashboardSnapshotV2 = vi.fn()
const queryUsage = vi.fn()
const getMyPlatformQuotas = vi.fn()

vi.mock('vue-i18n', async (original) => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@vueuse/core', async (original) => ({ ...await original<typeof import('@vueuse/core')>(), usePreferredReducedMotion: () => preferredMotion }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage, refreshBatchImageAccess }),
}))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ user: { balance: 1234.567 }, isSimpleMode: false, refreshUser }),
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ siteName: 'kedaya.ai' }) }))
vi.mock('@/api/usage', () => ({
  usageAPI: { getDashboardStats: (...args: unknown[]) => getDashboardStats(...args),
    getDashboardTrend: (...args: unknown[]) => getDashboardTrend(...args),
    getDashboardModels: (...args: unknown[]) => getDashboardModels(...args),
    getDashboardSnapshotV2: (...args: unknown[]) => getDashboardSnapshotV2(...args),
    query: (...args: unknown[]) => queryUsage(...args) },
}))
vi.mock('@/api/user', () => ({ getMyPlatformQuotas: (...args: unknown[]) => getMyPlatformQuotas(...args) }))
vi.mock('vue-chartjs', () => ({
  Doughnut: defineComponent({
    name: 'Doughnut',
    props: ['data', 'options'],
    template: '<canvas />',
  }),
}))
vi.mock('@/components/charts/TokenUsageTrend.vue', () => ({
  default: defineComponent({
    name: 'TokenUsageTrend',
    props: ['trendData', 'loading', 'animationDuration', 'mode'],
    template: '<div />',
  }),
}))

import Stats from '../UserDashboardStats.vue'
import Charts from '../UserDashboardCharts.vue'
import Models from '../UserDashboardModels.vue'
import Recent from '../UserDashboardRecentUsage.vue'
import Actions from '../UserDashboardQuickActions.vue'
import Dashboard from '@/views/user/DashboardView.vue'

const stats: UserDashboardStats = {
  total_api_keys: 8, active_api_keys: 6,
  today_requests: 17, total_requests: 90,
  today_tokens: 100, today_input_tokens: 20, today_output_tokens: 30,
  today_cache_creation_tokens: 10, today_cache_read_tokens: 40,
  total_tokens: 500, total_input_tokens: 80, total_output_tokens: 90,
  total_cache_creation_tokens: 100, total_cache_read_tokens: 230,
  today_cost: 2.3456, today_actual_cost: 1.2345,
  total_cost: 10, total_actual_cost: 5,
  average_duration_ms: 1234, rpm: 7, tpm: 1500,
  by_platform: [{ platform: 'openai', total_actual_cost: 3, today_actual_cost: 1,
    total_requests: 60, total_tokens: 300, today_requests: 10, today_tokens: 50 }],
}
const quota = (overrides: Partial<PlatformQuotaItem>): PlatformQuotaItem => ({
  platform: 'gemini', daily_limit_usd: 0, weekly_limit_usd: null, monthly_limit_usd: 100,
  daily_usage_usd: 0, weekly_usage_usd: 0, monthly_usage_usd: 76,
  monthly_window_resets_at: '2026-10-01T00:00:00Z', ...overrides,
} as PlatformQuotaItem)
const log = (id = 1): UsageLog => ({
  id, model: 'gpt-test', created_at: '2026-09-17T08:00:00Z',
  input_tokens: 12, output_tokens: 34, cache_read_tokens: 99,
  actual_cost: 0.123456, total_cost: 0.234567,
} as UsageLog)
const chartStubs = {
  DateRangePicker: { props: ['startDate', 'endDate'], template: '<div />' },
  Select: { props: ['modelValue', 'options'], template: '<div />' },
}

beforeEach(() => {
  vi.clearAllMocks()
  preferredMotion.value = 'no-preference'
  canUseBatchImage.value = false
  getDashboardStats.mockResolvedValue(stats)
  getDashboardTrend.mockResolvedValue({ trend: [] })
  getDashboardModels.mockResolvedValue({ models: [] })
  getDashboardSnapshotV2.mockResolvedValue({ trend: [], models: [] })
  queryUsage.mockResolvedValue({ items: Array.from({ length: 5 }, (_, i) => log(i)) })
  getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
})

describe('SIGNAL dashboard accounting and access contracts', () => {
  it('keeps balance rounding, actual/standard costs and both cache sums', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1234.567, isSimple: false } })
    expect(wrapper.find('.signal-metric--balance').text()).toContain('$1,234.57')
    const cost = wrapper.find('.signal-metric--cost').text()
    for (const value of ['$1.2345', '$2.3456']) expect(cost).toContain(value)
    expect(wrapper.get('.signal-metric--balance').text()).toContain('$5.0000')
    expect(wrapper.get('.signal-metric--balance').text()).toContain('$10.0000')
    expect(wrapper.get('.signal-metric--tokens').text()).toContain('dashboard.cache 50')
    expect(wrapper.get('.signal-token-split').text()).toContain('330')
    const readings = wrapper.findAll('.signal-reading')
    expect(readings[0].text()).toContain('7 RPM')
    expect(readings[0].text()).toContain('1.5K TPM')
    expect(readings[1].text()).toContain('1.23s')
    wrapper.unmount()
  })

  it('keeps quota-only platforms and avoids synthesizing spend across different windows', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1, isSimple: false, platformQuotas: [quota({})] } })
    const platforms = wrapper.findAll('.signal-platform')
    expect(platforms).toHaveLength(2)
    expect(platforms[1].text()).toContain('Gemini')
    expect(platforms[1].text()).toContain('dashboard.platformQuota.disabled')
    expect(platforms[1].text()).not.toContain('dashboard.platformQuota.weekly')
    expect(platforms[1].text()).toContain('$76.00 / $100.00')
    expect(platforms[1].find('.bg-amber-500').attributes('style')).toContain('76%')
    expect(platforms[1].text()).toContain('dashboard.platformQuota.resetsAt')
    expect(wrapper.find('.signal-platform--other').exists()).toBe(false)
    expect(wrapper.get('.signal-metric--total').text()).toContain('dashboard.recentTokens')
    expect(wrapper.get('.signal-metric--total').text()).not.toContain('dashboard.last30Days')
    wrapper.unmount()
  })

  it('keeps 75/95 percent quota warnings and clamps only the bar', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1, isSimple: false,
      platformQuotas: [quota({ daily_limit_usd: 100, daily_usage_usd: 74,
        weekly_limit_usd: 100, weekly_usage_usd: 95, monthly_usage_usd: 120 })] } })
    const platform = wrapper.findAll('.signal-platform')[1]
    expect(platform.find('.bg-green-500').attributes('style')).toContain('74%')
    expect(platform.findAll('.bg-red-500').map(bar => bar.attributes('style'))).toEqual(['width: 95%;', 'width: 100%;'])
    expect(platform.text()).toContain('$120.00 / $100.00')
    wrapper.unmount()
  })

  it('hides balance and platform data in simple mode but preserves the other metrics', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1, isSimple: true, platformQuotas: [quota({})] } })
    expect(wrapper.find('.signal-metric--balance').exists()).toBe(false)
    expect(wrapper.find('.signal-platforms').exists()).toBe(false)
    expect(wrapper.findAll('.signal-metric')).toHaveLength(5)
    expect(wrapper.findAll('.signal-reading')).toHaveLength(2)
    wrapper.unmount()
  })

  it('preserves timestamps, includes cache tokens and retains both four-place costs', () => {
    const wrapper = mount(Recent, { props: { data: [log()], loading: false }, global: { stubs: { RouterLink: true } } })
    expect(wrapper.find('time').attributes('datetime')).toBe(log().created_at)
    expect(wrapper.find('.signal-request-model').text()).toContain('gpt-test')
    expect(wrapper.find('.signal-request-tokens').text()).toContain('145')
    expect(wrapper.find('.signal-actual').text()).toBe('$0.1235')
    expect(wrapper.find('.signal-standard').text()).toBe('$0.2346')
    expect(wrapper.findComponent({ name: 'RouterLink' }).attributes('to')).toBe('/usage')
    wrapper.unmount()
  })

  it('preserves loading and empty states without showing fabricated records', async () => {
    const wrapper = mount(Recent, { props: { data: [], loading: true }, global: { stubs: { RouterLink: true } } })
    expect(wrapper.findComponent({ name: 'LoadingSpinner' }).exists()).toBe(true)
    await wrapper.setProps({ loading: false })
    expect(wrapper.findComponent({ name: 'EmptyState' }).exists()).toBe(true)
    expect(wrapper.find('.signal-request').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps all destinations and the batch-image permission gate', async () => {
    const wrapper = mount(Actions)
    expect(refreshBatchImageAccess).toHaveBeenCalledOnce()
    expect(wrapper.findAll('button')).toHaveLength(4)
    for (const button of wrapper.findAll('button')) await button.trigger('click')
    expect(push.mock.calls.map(call => call[0])).toEqual(['/keys', '/infinite-canvas', '/usage', '/redeem'])
    canUseBatchImage.value = true
    await flushPromises()
    await wrapper.findAll('button')[3].trigger('click')
    expect(push).toHaveBeenLastCalledWith('/batch-image')
    wrapper.unmount()
  })

  it('uses one total series by default and switches to token details without another request', async () => {
    const wrapper = mount(Charts, { props: { loading: false, startDate: '2026-09-11', endDate: '2026-09-17', granularity: 'day', trend: [] },
      global: { stubs: chartStubs } })
    const trend = wrapper.findComponent({ name: 'TokenUsageTrend' })
    expect(trend.props('mode')).toBe('total')
    expect(trend.props('animationDuration')).toBe(160)
    await wrapper.findAll('.signal-chart-mode button')[1].trigger('click')
    expect(trend.props('mode')).toBe('breakdown')
    expect(wrapper.emitted('dateRangeChange')).toBeUndefined()
    preferredMotion.value = 'reduce'
    await flushPromises()
    expect(trend.props('animationDuration')).toBe(0)
    await wrapper.setProps({ error: true })
    expect(wrapper.get('[role="alert"]').text()).toContain('dashboard.chartsFailed')
    expect(wrapper.findComponent({ name: 'TokenUsageTrend' }).exists()).toBe(false)
    await wrapper.get('[role="alert"] button').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    wrapper.unmount()
  })

  it('uses all returned model tokens for shares and distinguishes empty, loading and error states', async () => {
    const models = [
      { model: 'first', total_tokens: 40 }, { model: 'second', total_tokens: 30 },
      { model: 'third', total_tokens: 20 }, { model: 'fourth', total_tokens: 10 },
    ] as any
    const wrapper = mount(Models, { props: { models, startDate: '2026-09-11', endDate: '2026-09-17', loading: false } })
    const doughnut = wrapper.getComponent({ name: 'Doughnut' })
    expect(doughnut.props('data').datasets[0].data).toEqual([40, 30, 20, 10])
    expect(wrapper.findAll('.signal-model-legend li')).toHaveLength(3)
    expect(wrapper.findAll('.signal-model-legend li')[0].text()).toContain('40.0%')
    expect(wrapper.get('.signal-model-period').text()).toContain('2026-09-11')
    preferredMotion.value = 'reduce'
    await flushPromises()
    expect(doughnut.props('options').animation.duration).toBe(0)
    await wrapper.setProps({ loading: true })
    expect(wrapper.findComponent({ name: 'LoadingSpinner' }).exists()).toBe(true)
    await wrapper.setProps({ loading: false, error: true })
    expect(wrapper.text()).toContain('dashboard.chartsFailed')
    await wrapper.setProps({ error: false, models: [] })
    expect(wrapper.text()).toContain('dashboard.noDataAvailable')
    expect(wrapper.findComponent({ name: 'Doughnut' }).exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders an empty platform panel and keeps summary metrics out of platform mode', () => {
    const wrapper = mount(Stats, { props: { stats: { ...stats, total_actual_cost: 0, today_actual_cost: 0, by_platform: [] }, balance: 0, isSimple: false, section: 'platforms' } })
    expect(wrapper.findAll('.signal-metric')).toHaveLength(0)
    expect(wrapper.text()).toContain('dashboard.platformBreakdownEmpty')
    wrapper.unmount()
  })
})

function renderDashboard() {
  return mount(Dashboard, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    UserDashboardQuickActions: true, UserDashboardStats: true,
    UserDashboardCharts: true, UserDashboardModels: true, UserDashboardRecentUsage: true,
  } } })
}

async function toggleDetail(wrapper: ReturnType<typeof renderDashboard>, name: string, open: boolean) {
  const element = wrapper.get(`[data-detail="${name}"]`).element as HTMLDetailsElement
  await new Promise<void>(resolve => {
    element.addEventListener('toggle', () => resolve(), { once: true })
    element.open = open
  })
  await flushPromises()
}

describe('User dashboard data loading', () => {
  it('loads one chart snapshot and defers quota and recent-log queries until expanded', async () => {
    const wrapper = renderDashboard()
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('dashboard.overview')
    expect(getDashboardStats).toHaveBeenCalledOnce()
    expect(getDashboardSnapshotV2).toHaveBeenCalledOnce()
    expect(getDashboardSnapshotV2.mock.calls[0][0]).toMatchObject({ include_trend: true, include_model_stats: true, include_group_stats: false })
    expect(getDashboardTrend).not.toHaveBeenCalled()
    expect(getDashboardModels).not.toHaveBeenCalled()
    expect(queryUsage).not.toHaveBeenCalled()
    expect(getMyPlatformQuotas).not.toHaveBeenCalled()
    await toggleDetail(wrapper, 'recent', true)
    expect(queryUsage).toHaveBeenCalledOnce()
    expect(queryUsage.mock.calls[0][0]).toMatchObject({ page: 1, page_size: 5, sort_by: 'created_at', sort_order: 'desc' })
    expect(wrapper.getComponent({ name: 'UserDashboardRecentUsage' }).props('data')).toHaveLength(5)
    await toggleDetail(wrapper, 'recent', false)
    await toggleDetail(wrapper, 'recent', true)
    expect(queryUsage).toHaveBeenCalledOnce()
    await toggleDetail(wrapper, 'platforms', true)
    expect(getMyPlatformQuotas).toHaveBeenCalledOnce()
    await toggleDetail(wrapper, 'platforms', false)
    await toggleDetail(wrapper, 'platforms', true)
    expect(getMyPlatformQuotas).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('retries failed stats without displaying fake zero totals', async () => {
    getDashboardStats.mockRejectedValueOnce(new Error('Local test failure'))
    const wrapper = renderDashboard()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('dashboard.loadFailed')
    expect(wrapper.findComponent({ name: 'UserDashboardStats' }).exists()).toBe(false)
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(getDashboardStats).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('treats chart failures as errors, not empty successful snapshots', async () => {
    getDashboardSnapshotV2.mockRejectedValueOnce(new Error('Snapshot failed'))
    const wrapper = renderDashboard()
    await flushPromises()
    const charts = wrapper.getComponent({ name: 'UserDashboardCharts' })
    expect(charts.props('error')).toBe(true)
    charts.vm.$emit('refresh')
    await flushPromises()
    expect(charts.props('error')).toBe(false)
    expect(getDashboardSnapshotV2).toHaveBeenCalledTimes(2)
    expect(getDashboardStats).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('rejects stale chart responses after the date range changes and aborts on unmount', async () => {
    let resolveOld!: (value: unknown) => void
    getDashboardSnapshotV2.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = renderDashboard()
    await flushPromises()
    const charts = wrapper.getComponent({ name: 'UserDashboardCharts' })
    getDashboardSnapshotV2.mockResolvedValueOnce({ trend: [{ date: 'new' }], models: [] })
    charts.vm.$emit('update:startDate', '2026-09-16')
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    expect(getDashboardSnapshotV2.mock.calls[0][1].signal.aborted).toBe(true)
    resolveOld({ trend: [{ date: 'old' }], models: [] })
    await flushPromises()
    expect(charts.props('trend')).toEqual([{ date: 'new' }])
    const signal = getDashboardSnapshotV2.mock.calls[1][1].signal
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
  })

  it('refreshes recent logs only when open and follows the chosen dates', async () => {
    const wrapper = renderDashboard()
    await flushPromises()
    const charts = wrapper.getComponent({ name: 'UserDashboardCharts' })
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    expect(queryUsage).not.toHaveBeenCalled()
    await toggleDetail(wrapper, 'recent', true)
    charts.vm.$emit('update:startDate', '2026-09-15')
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    expect(queryUsage.mock.calls.at(-1)?.[0].start_date).toBe('2026-09-15')
    await wrapper.get('.signal-overview-refresh').trigger('click')
    await flushPromises()
    expect(getDashboardStats).toHaveBeenCalledTimes(2)
    expect(queryUsage).toHaveBeenCalledTimes(3)
    expect(getMyPlatformQuotas).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows disclosure failures explicitly and retries on reopening', async () => {
    queryUsage.mockRejectedValueOnce(new Error('Logs failed'))
    getMyPlatformQuotas.mockRejectedValueOnce(new Error('Quotas failed'))
    const wrapper = renderDashboard()
    await flushPromises()
    await toggleDetail(wrapper, 'recent', true)
    expect(wrapper.get('[data-detail="recent"] [role="alert"]').exists()).toBe(true)
    await toggleDetail(wrapper, 'recent', false)
    await toggleDetail(wrapper, 'recent', true)
    expect(wrapper.find('[data-detail="recent"] [role="alert"]').exists()).toBe(false)
    await toggleDetail(wrapper, 'platforms', true)
    expect(wrapper.get('[data-detail="platforms"] [role="alert"]').exists()).toBe(true)
    await toggleDetail(wrapper, 'platforms', false)
    await toggleDetail(wrapper, 'platforms', true)
    expect(wrapper.find('[data-detail="platforms"] [role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('invalidates an in-flight closed log panel when the dates change', async () => {
    let resolveOld!: (value: unknown) => void
    queryUsage.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = renderDashboard()
    await flushPromises()
    await toggleDetail(wrapper, 'recent', true)
    await toggleDetail(wrapper, 'recent', false)
    const charts = wrapper.getComponent({ name: 'UserDashboardCharts' })
    charts.vm.$emit('update:startDate', '2026-09-15')
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    expect(queryUsage.mock.calls[0][1].signal.aborted).toBe(true)
    resolveOld({ items: [log(99)] })
    await flushPromises()
    await toggleDetail(wrapper, 'recent', true)
    expect(queryUsage).toHaveBeenCalledTimes(2)
    expect(queryUsage.mock.calls[1][0].start_date).toBe('2026-09-15')
    expect(wrapper.getComponent({ name: 'UserDashboardRecentUsage' }).props('data')[0].id).not.toBe(99)
    wrapper.unmount()
  })
})
