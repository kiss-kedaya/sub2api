import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const {
  getDashboardStats,
  getDashboardTrend,
  getDashboardModels,
  getByDateRange,
  refreshUser,
  getMyPlatformQuotas,
} = vi.hoisted(() => ({
  getDashboardStats: vi.fn(),
  getDashboardTrend: vi.fn(),
  getDashboardModels: vi.fn(),
  getByDateRange: vi.fn(),
  refreshUser: vi.fn(),
  getMyPlatformQuotas: vi.fn(),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats,
    getDashboardTrend,
    getDashboardModels,
    getByDateRange,
  },
}))

vi.mock('@/api/user', () => ({
  getMyPlatformQuotas,
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { balance: 1.25 },
    isSimpleMode: false,
    refreshUser,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: defineComponent({
    name: 'AppLayout',
    setup: (_, { slots }) => () => h('div', slots.default?.()),
  }),
}))

vi.mock('@/components/user/dashboard/UserDashboardStats.vue', () => ({
  default: defineComponent({
    name: 'UserDashboardStats',
    setup: () => () => h('div', { 'data-test': 'dashboard-stats' }, 'stats'),
  }),
}))

vi.mock('@/components/user/dashboard/UserDashboardCharts.vue', () => ({
  default: defineComponent({
    name: 'UserDashboardCharts',
    setup: () => () => h('div', { 'data-test': 'dashboard-charts' }, 'charts'),
  }),
}))

vi.mock('@/components/user/dashboard/UserDashboardRecentUsage.vue', () => ({
  default: defineComponent({
    name: 'UserDashboardRecentUsage',
    setup: () => () => h('div', { 'data-test': 'dashboard-recent' }, 'recent'),
  }),
}))

vi.mock('@/components/user/dashboard/UserDashboardQuickActions.vue', () => ({
  default: defineComponent({
    name: 'UserDashboardQuickActions',
    setup: () => () => h('div', { 'data-test': 'dashboard-actions' }, 'actions'),
  }),
}))

import DashboardView from '../DashboardView.vue'

describe('user DashboardView first paint', () => {
  beforeEach(() => {
    getDashboardStats.mockReset()
    getDashboardTrend.mockReset()
    getDashboardModels.mockReset()
    getByDateRange.mockReset()
    refreshUser.mockReset()
    getMyPlatformQuotas.mockReset()
    refreshUser.mockResolvedValue(undefined)
    getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
  })

  it('renders the dashboard shell before stats and charts resolve', async () => {
    getDashboardStats.mockImplementation(() => new Promise(() => {}))
    getDashboardTrend.mockImplementation(() => new Promise(() => {}))
    getDashboardModels.mockImplementation(() => new Promise(() => {}))
    getByDateRange.mockImplementation(() => new Promise(() => {}))

    const wrapper = mount(DashboardView)
    await flushPromises()

    expect(wrapper.get('[data-test="dashboard-stats"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="dashboard-charts"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="dashboard-recent"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="dashboard-actions"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
