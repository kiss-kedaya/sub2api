import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubscriptions, getAllGroups, listUsers } = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  getAllGroups: vi.fn(),
  listUsers: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: { list: listSubscriptions },
    groups: { getAll: getAllGroups },
    users: { list: listUsers },
    usage: { searchUsers: vi.fn().mockResolvedValue([]) }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (fn: () => Promise<unknown>) => fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-sub-id="row.id">
        <slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

describe('Admin SubscriptionsView daily reset display', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-07T02:00:00Z')) // Day 2 10:00 UTC+8
    vi.clearAllMocks()
    localStorage.clear()
    getAllGroups.mockResolvedValue([])
    listUsers.mockResolvedValue({ items: [], total: 0, pages: 1 })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows reset countdown in window 1 and quota-ends countdown in final anchored 24h window', async () => {
    listSubscriptions.mockResolvedValue({
      items: [
        {
          id: 101,
          user_id: 1,
          group_id: 10,
          status: 'active',
          starts_at: '2026-08-06T04:00:00Z',
          expires_at: '2026-08-08T04:00:00Z',
          daily_window_start: '2026-08-06T04:00:00Z',
          next_daily_reset_at: '2026-08-07T04:00:00Z',
          weekly_window_start: null,
          monthly_window_start: null,
          daily_usage_usd: 40,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          created_at: '2026-08-06T04:00:00Z',
          updated_at: '2026-08-06T04:00:00Z',
          group: { id: 10, name: '24h Group', daily_limit_usd: 100, daily_reset_mode: 'anchored_24h' }
        },
        {
          id: 102,
          user_id: 2,
          group_id: 10,
          status: 'active',
          starts_at: '2026-08-05T04:00:00Z',
          expires_at: '2026-08-07T04:00:00Z',
          daily_window_start: '2026-08-06T04:00:00Z',
          next_daily_reset_at: null,
          weekly_window_start: null,
          monthly_window_start: null,
          daily_usage_usd: 80,
          weekly_usage_usd: 0,
          monthly_usage_usd: 0,
          created_at: '2026-08-05T04:00:00Z',
          updated_at: '2026-08-06T04:00:00Z',
          group: { id: 10, name: '24h Group', daily_limit_usd: 100, daily_reset_mode: 'anchored_24h' }
        }
      ],
      total: 2,
      pages: 1
    })

    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: { template: '<div><slot name="table" /></div>' },
          DataTable: DataTableStub,
          UProgress: true,
          UIcon: true,
          UBadge: true,
          Pagination: true,
          BaseDialog: true,
          ConfirmDialog: true,
          EmptyState: true,
          Select: true,
          GroupBadge: true,
          GroupOptionItem: true,
          AdminPageHeader: true,
          AdminRowAction: true,
          PageToolbar: true,
          TotpStepUpDialog: true
        }
      }
    })
    await flushPromises()

    const row1 = wrapper.get('[data-sub-id="101"]').text()
    const row2 = wrapper.get('[data-sub-id="102"]').text()

    expect(row1).toContain('admin.subscriptions.resetInHoursMinutes:{"hours":2,"minutes":0}')
    expect(row2).toContain('admin.subscriptions.quotaEndsInHoursMinutes:{"hours":2,"minutes":0}')
    expect(row2).not.toContain('resetIn')
  })
})
