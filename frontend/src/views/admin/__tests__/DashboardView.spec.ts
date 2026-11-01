import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking, push } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  push: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

// UButton / UPageCard links render through Nuxt UI's router-aware ULink (useRoute, RouterLink).
vi.mock('vue-router', () => ({
  useRouter: () => ({
    push
  }),
  useRoute: () => ({ path: '/admin/dashboard', query: {}, hash: '' }),
  RouterLink: { name: 'RouterLink', props: ['to', 'custom'], template: '<a><slot /></a>' }
}))

vi.mock('@/composables/useBatchImageAccess', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  const canUseBatchImage = ref(false)
  return { useBatchImageAccess: () => ({ canUseBatchImage, refreshBatchImageAccess: vi.fn() }) }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
})

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()
    push.mockReset()

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
  })

  it('uses last 24 hours as default dashboard range', async () => {
    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))
  })

  it('keeps every stat number in the eight tiles and the group pricing link', async () => {
    getSnapshotV2.mockResolvedValue({
      stats: {
        ...createDashboardStats(),
        total_api_keys: 26, active_api_keys: 23,
        total_accounts: 24, normal_accounts: 19, error_accounts: 2,
        today_requests: 49, total_requests: 1233,
        total_users: 34, today_new_users: 4, active_users: 26,
        today_tokens: 564180, today_actual_cost: 2.35, today_account_cost: 2.25, today_cost: 2.4,
        total_tokens: 2450000, total_actual_cost: 10.02, total_account_cost: 9.58, total_cost: 10.5,
        rpm: 7, tpm: 2620, average_duration_ms: 1340
      },
      trend: [],
      models: []
    })
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })
    await flushPromises()

    expect(wrapper.findAllComponents({ name: 'DashboardStat' })).toHaveLength(8)
    const text = wrapper.text()
    for (const value of [
      '26', '23 common.active', '24', '19 common.active', '2 common.error',
      '49', '1,233', '34', '+4', '26 admin.dashboard.activeUsers',
      '564.18K', '$2.35', '$2.25', '$2.40', '2.45M', '$10.02', '$9.58', '$10.50',
      '7', 'RPM', '2.62K', 'TPM', '1.34s'
    ]) expect(text).toContain(value)
    const links = wrapper.findAllComponents({ name: 'RouterLink' }).map((link) => link.props('to'))
    expect(links).toContain('/admin/groups')
    wrapper.unmount()
  })

  it('shows top-five model and spender lists that link to the full usage page', async () => {
    const models = Array.from({ length: 7 }, (_, i) => ({
      model: `model-${i}`, requests: 10 + i, input_tokens: 0, output_tokens: 0,
      cache_creation_tokens: 0, cache_read_tokens: 0, total_tokens: (i + 1) * 1000,
      cost: 1, actual_cost: i + 1, account_cost: 0
    }))
    getSnapshotV2.mockResolvedValue({ stats: createDashboardStats(), trend: [], models })
    getUserSpendingRanking.mockResolvedValue({
      ranking: Array.from({ length: 5 }, (_, i) => ({
        user_id: 100 + i, email: `u${i}@example.com`, username: i === 0 ? 'alice' : '',
        actual_cost: 50 - i, requests: 20 - i, tokens: 9000 - i
      })),
      total_actual_cost: 500, total_requests: 200, total_tokens: 90000, start_date: '', end_date: ''
    })
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })
    await flushPromises()

    // The ranking asks the server for five rows only; the full ranking is on /admin/usage.
    expect(getUserSpendingRanking).toHaveBeenCalledWith(expect.objectContaining({ limit: 5 }))

    // Models: the five with the most tokens, largest first.
    const modelRows = wrapper.findAll('.admin-top-models tbody tr')
    expect(modelRows).toHaveLength(5)
    expect(modelRows[0].text()).toContain('model-6')
    expect(modelRows[0].text()).toContain('7.00K')
    expect(modelRows[0].text()).toContain('$7.00')
    expect(wrapper.find('.admin-top-models').text()).not.toContain('model-1')

    // Spenders: rank, name (username, else email), and a row opens that user's usage.
    const spendRows = wrapper.findAll('.admin-top-spenders tbody tr')
    expect(spendRows).toHaveLength(5)
    expect(spendRows[0].text()).toContain('#1')
    expect(spendRows[0].text()).toContain('alice')
    expect(spendRows[1].text()).toContain('u1@example.com')
    await spendRows[1].trigger('click')
    expect(push).toHaveBeenCalledWith({
      path: '/admin/usage',
      query: expect.objectContaining({ user_id: '101' })
    })

    // Both lists end in "view all" on the usage page for the same date range.
    const usageLinks = wrapper.findAllComponents({ name: 'RouterLink' })
      .map((link) => link.props('to'))
      .filter((to) => typeof to === 'object' && to.path === '/admin/usage')
    expect(usageLinks).toHaveLength(2)
    expect(usageLinks[0].query).toEqual(expect.objectContaining({ start_date: expect.any(String), end_date: expect.any(String) }))
    wrapper.unmount()
  })
})
