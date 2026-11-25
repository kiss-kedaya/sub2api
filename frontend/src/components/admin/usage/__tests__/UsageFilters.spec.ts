import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'

import UsageFilters from '../UsageFilters.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'

// --- i18n messages (only what UsageFilters needs) ---
const messages: Record<string, string> = {
  'admin.usage.userDeletedBadge': 'deleted',
  'admin.usage.userFilter': 'User',
  'admin.usage.searchUserPlaceholder': 'Search user...',
  'usage.apiKeyFilter': 'API Key',
  'admin.usage.searchApiKeyPlaceholder': 'Search API key...',
  'usage.model': 'Model',
  'admin.usage.allModels': 'All Models',
  'admin.usage.account': 'Account',
  'admin.usage.searchAccountPlaceholder': 'Search account...',
  'usage.type': 'Type',
  'admin.usage.allTypes': 'All Types',
  'usage.ws': 'WS',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'usage.claudeRefusal': 'Claude policy refusal',
  'usage.compactionFilter': 'Request Kind',
  'usage.allCompactionTypes': 'All Requests',
  'usage.compactionOnly': 'Compaction Only',
  'admin.usage.billingType': 'Billing Type',
  'admin.usage.allBillingTypes': 'All Billing Types',
  'admin.usage.billingTypeBalance': 'Balance',
  'admin.usage.billingTypeSubscription': 'Subscription',
  'admin.usage.billingMode': 'Billing Mode',
  'admin.usage.allBillingModes': 'All Billing Modes',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per Request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.upstreamModelAudit': 'Upstream model audit',
  'admin.usage.allUpstreamModelAudit': 'All response model states',
  'admin.usage.upstreamModelMismatchOnly': 'Mismatched only',
  'admin.usage.upstreamModelMatchedOnly': 'Matched only',
  'admin.usage.group': 'Group',
  'admin.usage.allGroups': 'All Groups',
  'admin.usage.requestId': 'Request ID',
  'usage.requestIdPlaceholder': 'Request/upstream ID',
  'admin.dashboard.timeRange': 'Time Range',
  'admin.usage.filters.title': 'Filters',
  'admin.usage.filters.activeCount': '{count} active',
  'admin.usage.filters.advanced': 'Advanced filters',
  'admin.usage.filters.search': 'Search',
  'admin.usage.filters.requestIdHint': 'Fragment ok, =exact ^prefix',
  'admin.usage.filters.requestIdPlaceholder': 'Request or upstream ID',
  'admin.usage.filters.noUser': 'No matching users. Try another email keyword.',
  'admin.usage.filters.noApiKey': 'No matching API keys. Try another name.',
  'admin.usage.filters.noAccount': 'No matching accounts. Try another name.',
  'admin.usage.filters.upstreamModelAuditHint': 'Requested vs upstream model',
  'admin.usage.filters.clearUser': 'Clear user filter',
  'admin.usage.filters.clearApiKey': 'Clear API key filter',
  'admin.usage.filters.clearAccount': 'Clear account filter',
  'admin.usage.filters.anonymousIdPlaceholder': 'ID',
  'common.refresh': 'Refresh',
  'common.reset': 'Reset',
  'common.cancel': 'Cancel',
  'common.more': 'More',
  'admin.usage.cleanup.button': 'Cleanup',
  'admin.usage.cleanup.confirmTitle': 'Confirm Cleanup',
  'admin.usage.cleanup.confirmMessage': 'Cleanup cannot be undone.',
  'admin.usage.cleanup.confirmSubmit': 'Confirm Cleanup',
  'admin.usage.cleanup.warning': 'Cleanup is irreversible.',
  'usage.exportExcel': 'Export Excel',
  'usage.exportCsv': 'Export CSV',
}

// Mock vue-i18n
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        const raw = messages[key] ?? key
        return params
          ? raw.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? ''))
          : raw
      },
      locale: { value: 'en' },
    }),
  }
})

// Mock the admin API module — we control searchUsers return value per test
const mockSearchUsers = vi.fn()
const mockSearchApiKeys = vi.fn().mockResolvedValue([])
const mockGroupsList = vi.fn().mockResolvedValue({ items: [] })
const mockGetModelStats = vi.fn().mockResolvedValue({ models: [] })
const mockAccountsList = vi.fn().mockResolvedValue({ items: [] })

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      searchUsers: (...args: any[]) => mockSearchUsers(...args),
      searchApiKeys: (...args: any[]) => mockSearchApiKeys(...args),
    },
    groups: { list: (...args: any[]) => mockGroupsList(...args) },
    dashboard: { getModelStats: (...args: any[]) => mockGetModelStats(...args) },
    accounts: { list: (...args: any[]) => mockAccountsList(...args) },
  },
}))

// Default props helper
const defaultFilters = () => ({
  user_id: undefined,
  api_key_id: undefined,
  account_id: undefined,
  model: null,
  request_type: null,
  native_compaction_v2: null,
  billing_type: null,
  billing_mode: null,
  upstream_model_mismatch: null,
  group_id: null,
  start_date: '',
  end_date: '',
})

// 每个筛选项都是一个 UFormField；label / 描述都由它统一渲染，
// 所以用统一加在 label 上的 class 来数「字段」。
const BASE_FIELD_SELECTOR = '[data-testid="usage-filters-field"]'

// 只实现 v-model（update:modelValue），不实现 change：筛选卡自己不转发 change，
// 改动只落草稿，由「查询」提交。
const SelectStub = {
  name: 'Select',
  props: ['modelValue', 'options', 'searchable'],
  emits: ['update:modelValue'],
  template: '<div class="select-stub" />',
}

function mountFilters(filters = defaultFilters(), extraProps: Record<string, unknown> = {}) {
  return mount(UsageFilters, {
    props: {
      modelValue: filters,
      exporting: false,
      startDate: '2026-05-01',
      endDate: '2026-05-28',
      modelOptions: [],
      ...extraProps,
    },
    global: {
      stubs: {
        Select: SelectStub,
        Teleport: true,
      },
    },
  })
}

/**
 * 高级区默认收起（手机）、桌面展开；这一区用的是 Nuxt UI 折叠动画，在 jsdom 里
 * 收起/展开的 DOM 时序不稳定，所以需要高级区字段的用例统一在挂载时就撑开。
 */
function mountFiltersExpanded(filters = defaultFilters(), extraProps: Record<string, unknown> = {}) {
  window.innerWidth = 1440
  return mountFilters(filters, extraProps)
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })

  return { promise, resolve, reject }
}

/** Find the filter field (UFormField) whose label text matches, then return its wrapper. */
function fieldByLabel(wrapper: ReturnType<typeof mountFilters>, label: string) {
  const field = wrapper.findAll(BASE_FIELD_SELECTOR).find((node) => {
    const labelEl = node.find('label')
    return labelEl.exists() && labelEl.text() === label
  })
  if (!field) throw new Error(`field not found: ${label}`)
  return field
}

describe('UsageFilters date range', () => {
  it('lets observers filter anonymous IDs without loading identities or exposing cleanup/export', async () => {
    vi.clearAllMocks()
    const observer = mountFilters(defaultFilters(), { observer: true })
    await flushPromises()
    expect(mockGroupsList).not.toHaveBeenCalled()
    expect(mockSearchUsers).not.toHaveBeenCalled()
    expect(mockSearchApiKeys).not.toHaveBeenCalled()
    expect(mockAccountsList).not.toHaveBeenCalled()
    expect(observer.find('[data-testid="usage-filter-user-options"]').exists()).toBe(false)
    expect(observer.findAll('button').some(button => ['Cleanup', 'Export Excel', 'Export CSV'].includes(button.text()))).toBe(false)
    // 观察者只按匿名 ID 过滤：四个 ID 输入在高级区，改值是草稿，回车才是「查询」
    await observer.get('#observer-user_id').setValue('42')
    expect((observer.props('modelValue') as any).user_id).toBe(42)
    expect(observer.emitted('query')).toBeUndefined()
    await observer.get('#observer-user_id').trigger('keyup.enter')
    expect(observer.emitted('query')).toHaveLength(1)
    observer.unmount()
  })

  it('reuses the shared picker and emits only the applied range', async () => {
    const wrapper = mountFilters()
    const picker = wrapper.findComponent(DateRangePicker)
    expect(wrapper.get('[data-testid="usage-filters-field"][data-field="date"]').exists()).toBe(true)
    expect(picker.props()).toMatchObject({ startDate: '2026-05-01', endDate: '2026-05-28' })
    const range = { startDate: '2026-05-10', endDate: '2026-05-11', preset: null }
    picker.vm.$emit('change', range)
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('date-range-change')).toEqual([[range]])
    expect(wrapper.emitted('query')).toBeUndefined()
    await wrapper.setProps({ startDate: '2026-05-12', endDate: '2026-05-13' })
    expect(picker.props()).toMatchObject({ startDate: '2026-05-12', endDate: '2026-05-13' })
    wrapper.unmount()
  })
})

// ---------------------------------------------------------------------------
// 重构后的信息层级：常用 4 项 + 高级筛选折叠 + 分组 + 操作条主次
// ---------------------------------------------------------------------------
describe('UsageFilters — 常用筛选与高级筛选', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockSearchUsers.mockReset()
    mockSearchApiKeys.mockResolvedValue([])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps only the 4 common filters in the first row', () => {
    const wrapper = mountFilters()
    const common = wrapper.get('[data-testid="usage-filters-common"]')
    // 常用区恰好 4 个 UFormField：时间范围 / 用户 / API 密钥 / 模型
    expect(common.findAll(BASE_FIELD_SELECTOR)).toHaveLength(4)
    expect(common.text()).toContain('Time Range')
    expect(common.text()).toContain('User')
    expect(common.text()).toContain('API Key')
    expect(common.text()).toContain('Model')
    // 桌面 4 列、平板 2 列、手机 1 列
    expect(common.classes()).toEqual(expect.arrayContaining(['grid', 'gap-x-4', 'sm:grid-cols-2', 'xl:grid-cols-4']))
    wrapper.unmount()
  })

  it('lays the remaining filters out in one flat grid, without group legends', async () => {
    const wrapper = mountFilters()
    // 桌面端高级区默认展开（窄屏默认收起，见移动端用例）
    // 展开状态由组件状态驱动（jsdom 不计算 display，所以断言状态而不是 isVisible）
    expect((wrapper.vm as any).advancedOpen).toBe(true)
    expect(wrapper.get('[data-testid="usage-filters-advanced-toggle"]').attributes('aria-expanded')).toBe('true')
    const advanced = wrapper.get('[data-testid="usage-filters-advanced"]')
    // 和常用区同一套网格：平板 2 列、桌面 4 列，列对得上
    expect(advanced.classes()).toEqual(expect.arrayContaining(['grid', 'sm:grid-cols-2', 'xl:grid-cols-4']))
    // 字段名本身就是标签：整个筛选卡里都不再有「请求 / 计费 / 范围」分组小标题
    expect(wrapper.find('legend').exists()).toBe(false)
    expect(wrapper.find('fieldset').exists()).toBe(false)
    // 用量明细：8 个高级字段正好铺满两行（4 + 4）
    expect(advanced.findAll(BASE_FIELD_SELECTOR)).toHaveLength(8)
    const advancedText = advanced.text()
    for (const label of ['Request ID', 'Type', 'Request Kind', 'Billing Type', 'Billing Mode', 'Upstream model audit', 'Account', 'Group']) {
      expect(advancedText).toContain(label)
    }
    wrapper.unmount()
  })

  it('shows 6 advanced fields in the errors tab and none of the billing ones', () => {
    const wrapper = mountFilters(defaultFilters(), { mode: 'errors' })
    const advanced = wrapper.get('[data-testid="usage-filters-advanced"]')
    expect(advanced.findAll(BASE_FIELD_SELECTOR)).toHaveLength(6)
    expect(advanced.text()).not.toContain('Billing')
    wrapper.unmount()
  })

  it('does not clip the search result lists: the card is overflow-visible', () => {
    const wrapper = mountFilters()
    const card = wrapper.get('[data-testid="usage-filters-card"]')
    // 高级区收起时卡片很矮；overflow-hidden 会把下拉裁成一行半
    expect(card.classes()).toContain('overflow-visible')
    expect(card.classes()).not.toContain('overflow-hidden')
    wrapper.unmount()
  })

  it('puts syntax help on the label row instead of a second line under the label', () => {
    const wrapper = mountFilters()
    const requestId = fieldByLabel(wrapper, 'Request ID')
    expect(requestId.text()).toContain('Fragment ok, =exact ^prefix')
    // 提示和标签是同一行里的兄弟节点（UFormField 的 hint），不是标签下面单起一行的 description
    const hint = requestId.findAll('*').find((node) => node.text() === 'Fragment ok, =exact ^prefix' && node.element.children.length === 0)!
    expect(Array.from(hint.element.parentElement!.children).some((child) => child.tagName === 'LABEL')).toBe(true)
    // 提示、占位符各管一件事：占位符不再重复语法
    expect(requestId.get('input').attributes('placeholder')).toBe('Request or upstream ID')
    wrapper.unmount()
  })

  it('toggles the advanced area from the header button', async () => {
    const wrapper = mountFilters()
    const toggle = wrapper.get('[data-testid="usage-filters-advanced-toggle"]')
    expect((wrapper.vm as any).advancedOpen).toBe(true)
    expect(toggle.attributes('aria-expanded')).toBe('true')
    await toggle.trigger('click')
    expect((wrapper.vm as any).advancedOpen).toBe(false)
    await toggle.trigger('click')
    expect((wrapper.vm as any).advancedOpen).toBe(true)
    wrapper.unmount()
  })

  it('uses UFormField for labels instead of hand-rolled label.input-label', () => {
    const wrapper = mountFilters()
    // 改前是 <label class="input-label">，现在标签与描述由 UFormField 统一渲染
    expect(wrapper.find('label.input-label').exists()).toBe(false)
    expect(wrapper.findAll(BASE_FIELD_SELECTOR).length).toBeGreaterThan(10)
    // 搜索类输入统一前置图标
    expect(wrapper.get('[data-testid="usage-filters-common"] input[type="text"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('shows an active-filter count in the header', async () => {
    const filters = defaultFilters()
    filters.billing_type = 1
    const wrapper = mountFilters(filters)
    expect(wrapper.get('[data-testid="usage-filters-active-count"]').text()).toContain('1 active')
    wrapper.unmount()
  })
})

describe('UsageFilters — 操作条主次', () => {
  it('orders actions: search (primary) → reset → refresh → more → cleanup on the right', () => {
    const wrapper = mountFilters()
    const actions = wrapper.get('[data-testid="usage-filters-actions"]')
    const order = Array.from(actions.element.querySelectorAll('[data-testid]'))
      .map((el) => el.getAttribute('data-testid'))
      .filter((id): id is string => !!id && id.startsWith('usage-filters-'))
    const searchIdx = order.indexOf('usage-filters-search')
    const resetIdx = order.indexOf('usage-filters-reset')
    const refreshIdx = order.indexOf('usage-filters-refresh')
    const moreIdx = order.indexOf('usage-filters-more')
    const cleanupIdx = order.indexOf('usage-filters-cleanup')
    expect(searchIdx).toBeGreaterThanOrEqual(0)
    expect(searchIdx).toBeLessThan(resetIdx)
    expect(resetIdx).toBeLessThan(refreshIdx)
    expect(refreshIdx).toBeLessThan(moreIdx)
    expect(moreIdx).toBeLessThan(cleanupIdx)
    // 查询是主操作（UButton 默认 primary），重置/刷新是次级 outline
    expect(wrapper.get('[data-testid="usage-filters-search"]').classes()).toContain('bg-primary')
    expect(wrapper.get('[data-testid="usage-filters-reset"]').classes()).toContain('ring-accented')
    // 清理是危险操作，单独放在右侧
    expect(wrapper.get('[data-testid="usage-filters-cleanup"]').classes().join(' ')).toContain('text-error')
    expect(cleanupIdx).toBeGreaterThan(0)
  })

  it('keeps export actions in the more menu, not in the top-level action row', () => {
    const wrapper = mountFilters()
    const actions = wrapper.get('[data-testid="usage-filters-actions"]')
    expect(actions.text()).not.toContain('Export Excel')
    expect(actions.text()).not.toContain('Export CSV')
    // 更多菜单项由页面注入的列设置 + 两个导出组成
    const items = (wrapper.vm as any).moreMenuItems as Array<Array<{ label: string }>>
    const labels = items.flat().map((item) => item.label)
    expect(labels).toEqual(['Export Excel', 'Export CSV'])
    wrapper.unmount()
  })

  it('hides the action row when embedded without actions (cleanup dialog)', () => {
    const wrapper = mountFilters(defaultFilters(), { showActions: false })
    expect(wrapper.find('[data-testid="usage-filters-actions"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides cleanup and exports for the ranking tab and for observers', () => {
    const ranking = mountFilters(defaultFilters(), { mode: 'ranking' })
    expect(ranking.find('[data-testid="usage-filters-cleanup"]').exists()).toBe(false)
    expect((ranking.vm as any).moreMenuItems.flat().map((item: { label: string }) => item.label))
      .not.toContain('Export Excel')
    ranking.unmount()

    const observer = mountFilters(defaultFilters(), { observer: true })
    expect(observer.find('[data-testid="usage-filters-cleanup"]').exists()).toBe(false)
    observer.unmount()
  })
})

describe('UsageFilters — 清理二次确认', () => {
  it('asks for confirmation before emitting cleanup', async () => {
    const wrapper = mountFilters()
    await wrapper.get('[data-testid="usage-filters-cleanup"]').trigger('click')
    // 第一次点只开确认弹窗，不触发清理
    expect(wrapper.emitted('cleanup')).toBeUndefined()
    await flushPromises()
    const confirm = wrapper.get('[data-testid="usage-filters-cleanup-confirm-button"]')
    await confirm.trigger('click')
    expect(wrapper.emitted('cleanup')).toHaveLength(1)
    wrapper.unmount()
  })

  it('does not emit cleanup when the confirmation is cancelled', async () => {
    const wrapper = mountFilters()
    await wrapper.get('[data-testid="usage-filters-cleanup"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="usage-filters-cleanup-cancel"]').trigger('click')
    expect(wrapper.emitted('cleanup')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('UsageFilters — 移动端折叠默认收起', () => {
  const originalWidth = window.innerWidth
  afterEach(() => {
    window.innerWidth = originalWidth
  })

  it('keeps the advanced area collapsed by default on a phone-width viewport', () => {
    window.innerWidth = 390
    const wrapper = mountFilters()
    expect((wrapper.vm as any).advancedOpen).toBe(false)
    wrapper.unmount()
  })

  it('expands the advanced area by default on a desktop viewport', () => {
    const wrapper = mountFilters()
    expect((wrapper.vm as any).advancedOpen).toBe(true)
    wrapper.unmount()
  })
})

describe('UsageFilters — user search dropdown', () => {
  beforeEach(() => {
    // UFormField 对第一个有错误的字段会 scrollIntoView，jsdom 没实现
    Element.prototype.scrollIntoView = Element.prototype.scrollIntoView ?? (() => {})
    vi.useFakeTimers()
    mockSearchUsers.mockReset()
    mockSearchApiKeys.mockResolvedValue([])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('(a) labels deleted users with the i18n badge and (b) sorts active users before deleted ones, (c) selection sets user_id', async () => {
    // Arrange: mock returns deleted FIRST (proves sorting re-orders to active-first)
    mockSearchUsers.mockResolvedValue([
      { id: 2, email: 'gone@test.com', deleted: true },
      { id: 1, email: 'active@test.com', deleted: false },
    ])

    const wrapper = mountFiltersExpanded()

    // Trigger focus (sets showUserDropdown = true) then input (fires debounceUserSearch)
    const userInput = fieldByLabel(wrapper, 'User').get('input[type="text"]')
    await userInput.trigger('focus')
    await userInput.setValue('test')
    await userInput.trigger('input')

    // Advance debounce timer (300ms) then flush the resolved promise
    vi.advanceTimersByTime(300)
    await flushPromises()

    // --- (b) Sort: active user should appear BEFORE deleted user ---
    const buttons = wrapper.findAll('[data-testid="usage-filter-user-options"] button')
    const emailTexts = buttons.map((b) => b.text())

    const activeIdx = emailTexts.findIndex((t) => t.includes('active@test.com'))
    const deletedIdx = emailTexts.findIndex((t) => t.includes('gone@test.com'))
    expect(activeIdx).toBeGreaterThanOrEqual(0)
    expect(deletedIdx).toBeGreaterThanOrEqual(0)
    expect(activeIdx).toBeLessThan(deletedIdx)

    // --- (a) Label: deleted user's button shows the badge text ---
    expect(buttons[deletedIdx].text()).toContain('deleted')
    expect(buttons[activeIdx].text()).not.toContain('deleted')

    // --- (c) Selection: clicking active user button sets filters.user_id ---
    await buttons[activeIdx].trigger('click')
    await flushPromises()
    expect(wrapper.props('modelValue').user_id).toBe(1)
    // 选人后自动带出该用户的密钥候选，且不自动查询（手动点「查询」才生效）
    expect(mockSearchApiKeys).toHaveBeenCalledWith(1, '')
    expect(wrapper.emitted('query')).toBeUndefined()
  })

  it('keeps results from the latest user search when responses arrive out of order', async () => {
    const firstSearch = deferred<Array<{ id: number; email: string; deleted: boolean }>>()
    const secondSearch = deferred<Array<{ id: number; email: string; deleted: boolean }>>()
    mockSearchUsers
      .mockImplementationOnce(() => firstSearch.promise)
      .mockImplementationOnce(() => secondSearch.promise)

    const wrapper = mountFiltersExpanded()
    const userInput = fieldByLabel(wrapper, 'User').get('input')
    await userInput.trigger('focus')

    await userInput.setValue('a')
    vi.advanceTimersByTime(300)
    await flushPromises()

    await userInput.setValue('ab')
    vi.advanceTimersByTime(300)
    await flushPromises()

    secondSearch.resolve([{ id: 2, email: 'ab@test.com', deleted: false }])
    await flushPromises()
    expect(wrapper.text()).toContain('ab@test.com')

    firstSearch.resolve([{ id: 1, email: 'a@test.com', deleted: false }])
    await flushPromises()
    expect(wrapper.text()).toContain('ab@test.com')
    expect(wrapper.text()).not.toContain('a@test.com')
  })

  it('does not restore stale user results after the search is cleared', async () => {
    const pendingSearch = deferred<Array<{ id: number; email: string; deleted: boolean }>>()
    mockSearchUsers.mockImplementationOnce(() => pendingSearch.promise)

    const wrapper = mountFiltersExpanded()
    const userInput = fieldByLabel(wrapper, 'User').get('input')
    await userInput.trigger('focus')

    await userInput.setValue('stale')
    vi.advanceTimersByTime(300)
    await flushPromises()

    await userInput.setValue('')
    vi.advanceTimersByTime(300)
    await flushPromises()

    pendingSearch.resolve([{ id: 3, email: 'stale@test.com', deleted: false }])
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale@test.com')
  })

  it('clears the selected user and its API key from the trailing clear button', async () => {
    const filters = defaultFilters()
    filters.user_id = 1
    filters.api_key_id = 9
    const wrapper = mountFilters(filters)
    const clear = wrapper.get('[data-testid="usage-filter-clear-user"]')
    await clear.trigger('click')
    expect(filters.user_id).toBeUndefined()
    expect(filters.api_key_id).toBeUndefined()
    wrapper.unmount()
  })
})

describe('UsageFilters — model options come from prop (no dup request)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    mockGetModelStats.mockClear()
    mockGroupsList.mockClear()
  })
  afterEach(() => { vi.useRealTimers() })

  it('does not call dashboard.getModelStats on mount and renders model options from prop', async () => {
    const wrapper = mountFilters(defaultFilters(), { modelOptions: ['claude-3', 'gpt-4o'] })
    await flushPromises()

    expect(mockGetModelStats).not.toHaveBeenCalled()

    const opts = (wrapper.vm as any).modelOptions as Array<{ value: string | null; label: string }>
    expect(opts.map((o) => o.value)).toEqual([null, 'claude-3', 'gpt-4o'])
    wrapper.unmount()
  })
})

describe('UsageFilters — native compaction filter', () => {
  it('offers only All/Compaction and emits the independent boolean filter', async () => {
    const filters = defaultFilters()
    const wrapper = mountFilters(filters)

    const compactionSelect = wrapper.findAllComponents(SelectStub).find((select: any) =>
      (select.props('options') as Array<{ value: unknown }>).some((option) => option.value === true)
    )
    expect(compactionSelect).toBeDefined()
    expect(compactionSelect!.props('options')).toEqual([
      { value: null, label: 'All Requests' },
      { value: true, label: 'Compaction Only' },
    ])
    // 刻意不提供 false：「全部」本身就是“不过滤”，false 没有意义
    expect(compactionSelect!.props('options')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ value: false })])
    )

    compactionSelect!.vm.$emit('update:modelValue', true)
    await wrapper.vm.$nextTick()

    // 改值只落草稿；查询由「查询」按钮 / 回车触发
    expect(filters.native_compaction_v2).toBe(true)
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('UsageFilters — request type filter', () => {
  it('offers Claude policy refusal as a request type and writes it to the draft', async () => {
    const filters = defaultFilters()
    const wrapper = mountFilters(filters)

    const typeSelect = wrapper.findAllComponents(SelectStub).find((select: any) =>
      (select.props('options') as Array<{ value: unknown }>).some((option) => option.value === 'stream')
    )
    expect(typeSelect).toBeDefined()
    expect(typeSelect!.props('options')).toEqual(
      expect.arrayContaining([{ value: 'claude_refusal', label: 'Claude policy refusal' }])
    )

    typeSelect!.vm.$emit('update:modelValue', 'claude_refusal')
    await wrapper.vm.$nextTick()
    expect(filters.request_type).toBe('claude_refusal')
    wrapper.unmount()
  })
})

// ---------------------------------------------------------------------------
// 手动查询：所有筛选参数的组装与改前完全一致，只是提交时机从「改即查」变成「点查询」
// ---------------------------------------------------------------------------
describe('UsageFilters — 手动查询', () => {
  // 高级区（含请求 ID）在测试里以桌面视口挂载，挂载即展开
  const mountExpanded = async (filters = defaultFilters(), extraProps: Record<string, unknown> = {}) =>
    mountFiltersExpanded(filters, extraProps)

  it('does not query when a filter is edited', async () => {
    const filters = defaultFilters()
    const wrapper = await mountExpanded(filters)
    const input = fieldByLabel(wrapper, 'Request ID').get('input')
    await input.setValue('550e8400')
    await input.trigger('input')
    expect(filters.request_id).toBe('550e8400')
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })

  it('queries once with the draft when the search button is clicked', async () => {
    const filters = defaultFilters()
    const wrapper = await mountExpanded(filters)
    await fieldByLabel(wrapper, 'Request ID').get('input').setValue('=abc123')
    await wrapper.get('[data-testid="usage-filters-search"]').trigger('click')
    expect(wrapper.emitted('query')).toHaveLength(1)
    expect(filters.request_id).toBe('=abc123')
    wrapper.unmount()
  })

  it('treats Enter in a filter input as clicking search', async () => {
    const wrapper = await mountExpanded()
    const input = fieldByLabel(wrapper, 'Request ID').get('input')
    await input.setValue('^client:')
    await input.trigger('keyup.enter')
    expect(wrapper.emitted('query')).toHaveLength(1)
    wrapper.unmount()
  })

  it('keeps every filter parameter on the draft object (parameter set unchanged)', async () => {
    const filters = defaultFilters()
    const wrapper = await mountExpanded(filters)

    // select 类筛选：改值即写草稿
    const selects = wrapper.findAllComponents(SelectStub)
    // 按序号显式指定下拉：0=模型 1=类型 2=请求类别 3=计费类型 4=计费模式 5=上游模型审计 6=分组
    // （不要按 option 值反查：0/false、true 会在多个下拉里同时出现）
    const selectAt = (index: number) => selects[index]
    selectAt(1).vm.$emit('update:modelValue', 'stream')
    selectAt(3).vm.$emit('update:modelValue', 0)
    selectAt(4).vm.$emit('update:modelValue', 'token')
    selectAt(5).vm.$emit('update:modelValue', true)
    // 模型下拉是 searchable 的，可直接落任意模型名（选项来自 prop）
    selectAt(0).vm.$emit('update:modelValue', 'claude-3')
    await wrapper.vm.$nextTick()

    expect(filters).toMatchObject({
      request_type: 'stream',
      billing_type: 0,
      billing_mode: 'token',
      upstream_model_mismatch: true,
      model: 'claude-3',
    })
    // 未改动的参数保持默认值，不被新组件擅自赋值
    expect(filters.group_id).toBeNull()
    expect(filters.account_id).toBeUndefined()
    wrapper.unmount()
  })

  it('emits reset / refresh from the action row without triggering a query', async () => {
    const wrapper = mountFilters()
    await wrapper.get('[data-testid="usage-filters-reset"]').trigger('click')
    await wrapper.get('[data-testid="usage-filters-refresh"]').trigger('click')
    expect(wrapper.emitted('reset')).toHaveLength(1)
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps observer ID edits as draft without emitting query', async () => {
    const filters = defaultFilters()
    const wrapper = await mountExpanded(filters, { observer: true })
    await wrapper.get('#observer-account_id').setValue('7')
    expect(filters.account_id).toBe(7)
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })

  it('forwards the page column menu into the more menu', () => {
    const columnMenuItems = [{ label: 'Request ID', type: 'checkbox' as const, checked: false }]
    const wrapper = mountFilters(defaultFilters(), { columnMenuItems })
    const groups = (wrapper.vm as any).moreMenuItems as Array<Array<{ label: string }>>
    expect(groups[0].map((item) => item.label)).toEqual(['Request ID'])
    // 导出仍在第二组
    expect(groups[1].map((item) => item.label)).toEqual(['Export Excel', 'Export CSV'])
    wrapper.unmount()
  })
})

describe('UsageFilters — 「不限」的显示', () => {
  it('reads unset filters as the "All …" placeholder, whether the value is undefined or null', () => {
    // 页面里 model / group_id / request_type 初始是 undefined，billing_type 初始是 null：界面上应一致
    const filters = { ...defaultFilters(), model: undefined, group_id: undefined, request_type: undefined } as any
    const wrapper = mountFilters(filters)
    const selects = wrapper.findAllComponents({ name: 'Select' })
    expect(selects).toHaveLength(7)
    for (const select of selects) {
      expect(select.props('modelValue')).toBeUndefined()
      expect(select.attributes('placeholder')).toMatch(/^All /)
    }
    wrapper.unmount()
  })

  it('keeps real values visible and writes the picked option back as before', () => {
    const filters = defaultFilters() as any
    filters.model = 'claude-opus-4-5'
    const wrapper = mountFilters(filters)
    const model = wrapper.findAllComponents({ name: 'Select' }).find((s) => s.attributes('placeholder') === 'All Models')!
    expect(model.props('modelValue')).toBe('claude-opus-4-5')
    model.vm.$emit('update:modelValue', 'gpt-5.1')
    expect((wrapper.props('modelValue') as any).model).toBe('gpt-5.1')
    // 选回「全部」仍然写 null，页面拼查询参数的口径不变
    model.vm.$emit('update:modelValue', null)
    expect((wrapper.props('modelValue') as any).model).toBeNull()
    wrapper.unmount()
  })

  it('keeps the "Advanced filters" text next to the count badge', () => {
    const filters = defaultFilters() as any
    filters.billing_type = 1
    const wrapper = mountFilters(filters)
    const toggle = wrapper.get('[data-testid="usage-filters-advanced-toggle"]')
    // UButton 有默认插槽时不渲染 label：改前有高级条件时按钮上只剩一个数字
    expect(toggle.text()).toContain('Advanced filters')
    expect(wrapper.get('[data-testid="usage-filters-advanced-count"]').text()).toBe('1')
    wrapper.unmount()
  })
})

describe('UsageFilters — 搜索下拉的输入与收起', () => {
  beforeEach(() => {
    Element.prototype.scrollIntoView = Element.prototype.scrollIntoView ?? (() => {})
    vi.useFakeTimers()
    mockSearchUsers.mockReset()
    mockSearchApiKeys.mockResolvedValue([])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  async function typeIntoUserSearch(value: string) {
    mockSearchUsers.mockResolvedValue([{ id: 1, email: 'a@test.com', deleted: false }])
    const wrapper = mountFiltersExpanded()
    const input = fieldByLabel(wrapper, 'User').get('input')
    await input.trigger('focus')
    // 只发 input，不发 change：真实键入时 change 要等失焦 / 回车才来
    ;(input.element as HTMLInputElement).value = value
    await input.trigger('input')
    vi.advanceTimersByTime(300)
    await flushPromises()
    return { wrapper, input }
  }

  it('searches while typing, without waiting for blur or Enter', async () => {
    const { wrapper } = await typeIntoUserSearch('a')
    // 拿的是刚键入的值，而不是 v-model 还没写入时的旧值
    expect(mockSearchUsers).toHaveBeenCalledWith('a')
    expect(wrapper.get('[data-testid="usage-filter-user-options"]').text()).toContain('a@test.com')
    wrapper.unmount()
  })

  it('closes the list on a press outside the field, and reopens it on focus', async () => {
    const { wrapper, input } = await typeIntoUserSearch('a')
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await flushPromises()
    expect(wrapper.find('[data-testid="usage-filter-user-options"]').exists()).toBe(false)
    await input.trigger('focus')
    expect(wrapper.find('[data-testid="usage-filter-user-options"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('closes the list on Escape', async () => {
    const { wrapper, input } = await typeIntoUserSearch('a')
    await input.trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[data-testid="usage-filter-user-options"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('says so when a search finds nothing, instead of letting the list vanish', async () => {
    mockSearchUsers.mockResolvedValue([])
    const wrapper = mountFiltersExpanded()
    const input = fieldByLabel(wrapper, 'User').get('input')
    await input.trigger('focus')
    ;(input.element as HTMLInputElement).value = 'zzz'
    await input.trigger('input')
    // 搜索还在防抖 / 进行中：不能先闪一下「无匹配」
    expect(wrapper.find('[data-testid="usage-filter-user-empty"]').exists()).toBe(false)
    vi.advanceTimersByTime(300)
    await flushPromises()
    expect(wrapper.get('[data-testid="usage-filter-user-empty"]').text()).toBe('No matching users. Try another email keyword.')
    wrapper.unmount()
  })

  it('does not call a selected user a failed search when the list is empty', async () => {
    const filters = defaultFilters() as any
    filters.user_id = 5
    const wrapper = mountFiltersExpanded(filters)
    ;(wrapper.vm as any).setUserKeyword('picked@test.com')
    await fieldByLabel(wrapper, 'User').get('input').trigger('focus')
    // 关键字是选中项的名字，不是一次落空的搜索
    expect(wrapper.find('[data-testid="usage-filter-user-empty"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('lets a truncated result be read in full on hover', async () => {
    const { wrapper } = await typeIntoUserSearch('a')
    const label = wrapper.get('[data-testid="usage-filter-user-options"] span.truncate')
    expect(label.attributes('title')).toBe('a@test.com')
    wrapper.unmount()
  })

  it('stops listening for outside presses once unmounted', async () => {
    const removeSpy = vi.spyOn(document, 'removeEventListener')
    const { wrapper } = await typeIntoUserSearch('a')
    wrapper.unmount()
    expect(removeSpy).toHaveBeenCalledWith('pointerdown', expect.any(Function))
    removeSpy.mockRestore()
  })
})
