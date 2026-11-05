import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UDropdownMenu from '@nuxt/ui/components/DropdownMenu.vue'
import AccountsView from '../AccountsView.vue'

const { listAccounts, listWithEtag, getBatchTodayStats, getAllProxies, getAllGroups } = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token' })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

// Renders the usage cell of every row; the cell stub reports whether it was handed today's numbers.
const DataTableStub = {
  props: ['columns', 'data'],
  template: `<div><div v-for="row in data" :key="row.id"><slot name="cell-usage" :row="row" /></div></div>`
}
const AccountUsageCellStub = {
  props: ['account', 'todayStats', 'todayStatsLoading'],
  template: `<span data-test="usage-cell" :data-today-stats="todayStats ? 'yes' : 'no'" />`
}

function mountView() {
  return mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: DataTableStub,
        HelpTooltip: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
        AccountTableFilters: { props: ['groups'], template: '<div><slot name="actions" /></div>' },
        AccountBulkActionsBar: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountBatchTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: AccountUsageCellStub,
        Icon: true
      }
    }
  })
}

const usageCellGetsStats = (wrapper: ReturnType<typeof mountView>) =>
  wrapper.get('[data-test="usage-cell"]').attributes('data-today-stats')

describe('admin AccountsView today stats are shown once', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({
      items: [{
        id: 11, name: 'grok-key', platform: 'grok', type: 'apikey', status: 'active', schedulable: true,
        created_at: '2026-11-05T00:00:00Z', updated_at: '2026-11-05T00:00:00Z'
      }],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockReset().mockResolvedValue({
      stats: { '11': { requests: 5, tokens: 10, cost: 1, user_cost: 1 } }
    })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
  })

  it('hands the numbers to the usage column while the dedicated column is hidden (the default layout)', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(usageCellGetsStats(wrapper)).toBe('yes')
  })

  it('holds them back when the dedicated column was already switched on', async () => {
    localStorage.setItem('account-hidden-columns', JSON.stringify([]))
    const wrapper = mountView()
    await flushPromises()
    expect(getBatchTodayStats).toHaveBeenCalled()
    expect(usageCellGetsStats(wrapper)).toBe('no')
  })

  it('moves the numbers out of the usage column the moment the dedicated column is switched on', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(usageCellGetsStats(wrapper)).toBe('yes')

    const menu = wrapper.findAllComponents(UDropdownMenu).find(m =>
      (m.props('items') as any[]).flat().some(item => item?.type === 'checkbox' && item?.label === 'admin.accounts.columns.todayStats'))
    expect(menu).toBeDefined()
    ;(menu!.props('items') as any[]).flat().find(item => item?.label === 'admin.accounts.columns.todayStats').onUpdateChecked(true)
    await flushPromises()

    expect(usageCellGetsStats(wrapper)).toBe('no')
  })
})
