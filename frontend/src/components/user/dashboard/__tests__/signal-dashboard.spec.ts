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
const getByDateRange = vi.fn()
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
    getByDateRange: (...args: unknown[]) => getByDateRange(...args) },
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
    props: ['trendData', 'loading', 'animationDuration'],
    template: '<div />',
  }),
}))

import Stats from '../UserDashboardStats.vue'
import Charts from '../UserDashboardCharts.vue'
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
  getByDateRange.mockResolvedValue({ items: Array.from({ length: 7 }, (_, i) => log(i)) })
  getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
})

describe('SIGNAL dashboard accounting and access contracts', () => {
  it('keeps balance rounding, actual/standard costs and both cache sums', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1234.567, isSimple: false } })
    expect(wrapper.find('.signal-metric--balance').text()).toContain('$1,234.57')
    const cost = wrapper.find('.signal-metric--cost').text()
    for (const value of ['$1.2345', '$2.3456', '$5.0000', '$10.0000']) expect(cost).toContain(value)
    const readings = wrapper.findAll('.signal-reading')
    expect(readings[0].text()).toContain('dashboard.cache: 50')
    expect(readings[1].text()).toContain('dashboard.cache: 330')
    expect(readings[2].text()).toContain('7 RPM')
    expect(readings[2].text()).toContain('1.5K TPM')
    expect(readings[3].text()).toContain('1.23s')
    wrapper.unmount()
  })

  it('keeps quota-only platforms, disabled zero, null omission and other reconciliation', () => {
    const wrapper = mount(Stats, { props: { stats, balance: 1, isSimple: false, platformQuotas: [quota({})] } })
    const platforms = wrapper.findAll('.signal-platform')
    expect(platforms).toHaveLength(3)
    expect(platforms[1].text()).toContain('Gemini')
    expect(platforms[1].text()).toContain('dashboard.platformQuota.disabled')
    expect(platforms[1].text()).not.toContain('dashboard.platformQuota.weekly')
    expect(platforms[1].text()).toContain('$76.00 / $100.00')
    expect(platforms[1].find('.bg-amber-500').attributes('style')).toContain('76%')
    expect(platforms[1].text()).toContain('dashboard.platformQuota.resetsAt')
    expect(platforms[2].text()).toContain('$2.0000')
    expect(platforms[2].text()).toContain('$0.2345')
    expect(platforms[2].find('.signal-quota').exists()).toBe(false)
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
    expect(wrapper.findAll('.signal-metric')).toHaveLength(3)
    expect(wrapper.findAll('.signal-reading')).toHaveLength(4)
    wrapper.unmount()
  })

  it('preserves request timestamp, input plus output tokens and both four-place costs', () => {
    const wrapper = mount(Recent, { props: { data: [log()], loading: false }, global: { stubs: { RouterLink: true } } })
    expect(wrapper.find('time').attributes('datetime')).toBe(log().created_at)
    expect(wrapper.find('.signal-request-model').text()).toContain('gpt-test')
    expect(wrapper.find('.signal-request-tokens').text()).toContain('46')
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

  it('retains model token data and forwards reduced-motion to both chart presentations', async () => {
    const models = [{ model: 'test', requests: 3, total_tokens: 42, actual_cost: 0.5, cost: 1 }]
    const wrapper = mount(Charts, { props: { loading: false, startDate: '2026-09-11', endDate: '2026-09-17', granularity: 'day', trend: [], models } as any,
      global: { stubs: chartStubs } })
    const doughnut = wrapper.findComponent({ name: 'Doughnut' })
    const trend = wrapper.findComponent({ name: 'TokenUsageTrend' })
    expect(doughnut.props('data').datasets[0].data).toEqual([42])
    expect(doughnut.props('options').animation.duration).toBe(180)
    expect(trend.props('animationDuration')).toBe(180)
    preferredMotion.value = 'reduce'
    await flushPromises()
    expect(doughnut.props('options').animation.duration).toBe(0)
    expect(trend.props('animationDuration')).toBe(0)
    await wrapper.find('.signal-refresh').trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    wrapper.unmount()
  })

  it('switches overview and platforms without refetching or duplicating account totals', async () => {
    const wrapper = mount(Dashboard, { attachTo: document.body, global: { stubs: {
      AppLayout: { template: '<main><slot /></main>' },
      UserDashboardQuickActions: true, UserDashboardStats: true,
      UserDashboardCharts: true, UserDashboardRecentUsage: true,
    } } })
    await flushPromises()
    expect(wrapper.findComponent({ name: 'UserDashboardStats' }).props('section')).toBe('summary')
    await wrapper.get('#dashboard-view-tab-platforms').trigger('click')
    expect(wrapper.get('#dashboard-view-panel-overview').isVisible()).toBe(false)
    expect(wrapper.get('#dashboard-view-panel-platforms').isVisible()).toBe(true)
    await wrapper.get('#dashboard-view-tab-overview').trigger('click')
    expect(wrapper.get('#dashboard-view-panel-overview').isVisible()).toBe(true)
    expect(getDashboardStats).toHaveBeenCalledOnce()
    expect(getDashboardTrend).toHaveBeenCalledOnce()
    expect(getMyPlatformQuotas).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('renders an empty platform panel and keeps summary metrics out of platform mode', () => {
    const wrapper = mount(Stats, { props: { stats: { ...stats, total_actual_cost: 0, today_actual_cost: 0, by_platform: [] }, balance: 0, isSimple: false, section: 'platforms' } })
    expect(wrapper.findAll('.signal-metric')).toHaveLength(0)
    expect(wrapper.text()).toContain('dashboard.platformBreakdownEmpty')
    wrapper.unmount()
  })

  it('offers a refresh command when the initial overview request fails', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    getDashboardStats.mockRejectedValueOnce(new Error('Local test failure'))
    const wrapper = mount(Dashboard, { global: { stubs: {
      AppLayout: { template: '<main><slot /></main>' },
      UserDashboardQuickActions: true, UserDashboardStats: true,
      UserDashboardCharts: true, UserDashboardRecentUsage: true,
    } } })
    try {
      await flushPromises()
      expect(wrapper.get('[role="alert"]').text()).toContain('dashboard.loadFailed')
      await wrapper.get('[role="alert"] button').trigger('click')
      await flushPromises()
      expect(getDashboardStats).toHaveBeenCalledTimes(2)
      expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    } finally {
      wrapper.unmount()
      error.mockRestore()
    }
  })

  it('shows the configured brand and preserves date-change versus full-refresh fetches', async () => {
    const wrapper = mount(Dashboard, { global: { stubs: {
      AppLayout: { template: '<main><slot /></main>' },
      UserDashboardQuickActions: true, UserDashboardStats: true,
      UserDashboardCharts: true, UserDashboardRecentUsage: true,
    } } })
    await flushPromises()
    expect(wrapper.find('h1').text()).toBe('kedaya.ai')
    expect(getDashboardStats).toHaveBeenCalledOnce()
    expect(getMyPlatformQuotas).toHaveBeenCalledOnce()
    expect(wrapper.findComponent({ name: 'UserDashboardRecentUsage' }).props('data')).toHaveLength(5)
    const charts = wrapper.findComponent({ name: 'UserDashboardCharts' })
    charts.vm.$emit('dateRangeChange')
    await flushPromises()
    expect(getDashboardTrend).toHaveBeenCalledTimes(2)
    expect(getDashboardStats).toHaveBeenCalledOnce()
    expect(getByDateRange).toHaveBeenCalledOnce()
    charts.vm.$emit('refresh')
    await flushPromises()
    expect(getDashboardStats).toHaveBeenCalledTimes(2)
    expect(getByDateRange).toHaveBeenCalledTimes(2)
    expect(refreshUser).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
