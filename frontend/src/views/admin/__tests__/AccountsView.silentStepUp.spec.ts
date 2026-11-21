/**
 * 页面加载命中 403 STEP_UP_REQUIRED 的交互回归（2026-11 管理端 2FA 事故）。
 *
 * 事故：打开「账号管理」页，页面自动请求 proxies/all 与 accounts/today-stats/batch
 * 被后端误判为 step-up，全局门禁弹出 TOTP；用户取消后整页数据加载失败。
 *
 * 约定：后台/页面加载读取命中 step-up 403 时必须静默降级 —— 不弹 TOTP、
 * 不抛 StepUpCancelledError、页面照常渲染，列内显示「需二次验证」占位。
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import AccountsView from '../AccountsView.vue'
import { stepUpGate } from '@/composables/useStepUpGate'

const {
  listAccounts,
  listWithEtag,
  getById,
  refreshCredentials,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  showError,
  showWarning
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getById: vi.fn(),
  refreshCredentials: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getById,
      refreshCredentials,
      listWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
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
  useAppStore: () => ({ showError, showWarning, showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

/** 模拟共享客户端对静默读的 403 拒绝：不带 TOTP 弹窗，只把错误交回调用方。 */
function silentStepUpRejection() {
  return {
    status: 403,
    code: 'STEP_UP_REQUIRED',
    reason: 'STEP_UP_REQUIRED',
    message: 'stepUp.required',
    silentStepUp: true
  }
}

const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <span data-test="account-name">{{ row.name }}</span>
        <slot name="cell-today_stats" :row="row" />
      </div>
    </div>
  `
})

const AccountTodayStatsCellStub = defineComponent({
  props: { stats: { type: Object, default: null }, loading: Boolean, error: { type: String, default: null } },
  template: '<span data-test="today-stats">{{ error || "stats-ok" }}</span>'
})

function mountView() {
  return mount(AccountsView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: DataTableStub,
        AccountTableActions: { template: '<div><slot name="after" /></div>' },
        AccountTableFilters: true,
        AccountBulkActionsBar: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountBatchTestModal: true,
        AccountStatsModal: true,
        AccountDetailSlideover: true,
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
        AccountTodayStatsCell: AccountTodayStatsCellStub,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        UpstreamBillingRateCell: true,
        HelpTooltip: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

const listRow = {
  id: 30772,
  name: 'page-load account',
  platform: 'openai',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  concurrency: 2,
  priority: 1,
  group_ids: [7],
  extra: {},
  credentials: {}
}

describe('admin AccountsView page-load step-up degradation', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({ items: [listRow], total: 1, page: 1, page_size: 20, pages: 1 })
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getById.mockReset().mockResolvedValue(listRow)
    refreshCredentials.mockReset().mockResolvedValue(listRow)
    // 事故形态：页面加载的批量统计与代理下拉被要求 step-up。
    getBatchTodayStats.mockReset().mockRejectedValue(silentStepUpRejection())
    getAllProxies.mockReset().mockRejectedValue(silentStepUpRejection())
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: false })
    getAllGroups.mockReset().mockResolvedValue([{ id: 7, name: 'codex', platform: 'openai' }])
    showError.mockReset()
    showWarning.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('never prompts for TOTP and keeps rendering when page-load reads return 403', async () => {
    const promptSpy = vi.spyOn(stepUpGate, 'prompt')
    const wrapper = mountView()
    await flushPromises()

    // 页面照常渲染：列表行还在，加载失败不阻塞首屏。
    expect(wrapper.get('[data-test="account-name"]').text()).toBe('page-load account')
    // 后台读不弹 TOTP、不进可见状态。
    expect(promptSpy).not.toHaveBeenCalled()
    expect(stepUpGate.visible.value).toBe(false)
    // 统计列降级为「需二次验证后查看」占位，而不是空白/崩溃。
    expect(wrapper.get('[data-test="today-stats"]').text()).toBe('stepUp.silentReadBlocked')
    // 不把 403 当成页面错误弹窗（用户没有做任何敏感操作）。
    expect(showError).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('does not surface StepUpCancelledError when the background proxy load is rejected', async () => {
    const wrapper = mountView()
    await flushPromises()

    // 代理下拉失败只降级为空列表；页面其余部分可用。
    expect(wrapper.get('[data-test="account-name"]').text()).toBe('page-load account')
    expect(showWarning).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
