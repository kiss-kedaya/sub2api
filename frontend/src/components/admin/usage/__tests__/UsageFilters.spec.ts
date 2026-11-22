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
  'common.refresh': 'Refresh',
  'common.reset': 'Reset',
  'common.query': 'Query',
  'admin.usage.cleanup.button': 'Cleanup',
  'usage.exportExcel': 'Export',
  'usage.exportCsv': 'Export CSV',
}

// Mock vue-i18n
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
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

function mountFilters(filters = defaultFilters()) {
  return mount(UsageFilters, {
    props: {
      modelValue: filters,
      exporting: false,
      startDate: '2026-05-01',
      endDate: '2026-05-28',
      showActions: false,
      modelOptions: [],
    },
    global: {
      stubs: {
        Select: true,
        Teleport: true,
      },
    },
  })
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

describe('UsageFilters date range', () => {
  it('lets observers filter anonymous IDs without loading identities or exposing cleanup/export', async () => {
    vi.clearAllMocks()
    const wrapper = mountFilters()
    wrapper.unmount()
    vi.clearAllMocks()
    const observer = mount(UsageFilters, {
      props: { modelValue: defaultFilters(), exporting: false, startDate: '2026-05-01', endDate: '2026-05-28', observer: true },
      global: { stubs: { Select: true, Teleport: true } },
    })
    await flushPromises()
    expect(mockGroupsList).not.toHaveBeenCalled()
    expect(mockSearchUsers).not.toHaveBeenCalled()
    expect(mockSearchApiKeys).not.toHaveBeenCalled()
    expect(mockAccountsList).not.toHaveBeenCalled()
    expect(observer.find('.usage-filter-dropdown').exists()).toBe(false)
    expect(observer.findAll('button').some(button => ['Cleanup', 'Export'].includes(button.text()))).toBe(false)
    await observer.get('#observer-user_id').setValue('42')
    await observer.get('#observer-user_id').trigger('change')
    // 手动查询：改条件只更新草稿，不发请求、也不触发查询
    expect(observer.props('modelValue').user_id).toBe(42)
    expect(observer.emitted('query')).toBeUndefined()
    observer.unmount()
  })
  it('reuses the shared picker and emits only the applied range', async () => {
    const wrapper = mountFilters()
    const picker = wrapper.findComponent(DateRangePicker)
    expect(wrapper.find('[data-testid="usage-date-range-filter"]').exists()).toBe(true)
    expect(picker.props()).toMatchObject({ startDate: '2026-05-01', endDate: '2026-05-28' })
    const range = { startDate: '2026-05-10', endDate: '2026-05-11', preset: null }
    picker.vm.$emit('change', range)
    await wrapper.vm.$nextTick()
    expect(wrapper.emitted('date-range-change')).toEqual([[range]])
    // 时间范围是即时控件，但也不应额外触发「查询」
    expect(wrapper.emitted('query')).toBeUndefined()
    await wrapper.setProps({ startDate: '2026-05-12', endDate: '2026-05-13' })
    expect(picker.props()).toMatchObject({ startDate: '2026-05-12', endDate: '2026-05-13' })
    wrapper.unmount()
  })
})

describe('UsageFilters — user search dropdown', () => {
  beforeEach(() => {
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

    const wrapper = mountFilters()

    // Trigger focus (sets showUserDropdown = true) then input (fires debounceUserSearch)
    const input = wrapper.find('.usage-filter-dropdown input[type="text"]')
    await input.trigger('focus')
    await input.setValue('test')
    await input.trigger('input')

    // Advance debounce timer (300ms) then flush the resolved promise
    vi.advanceTimersByTime(300)
    await flushPromises()

    // --- (b) Sort: active user should appear BEFORE deleted user ---
    // Check the underlying component state via rendered DOM order
    const buttons = wrapper.findAll('.usage-filter-dropdown button[type="button"]')
    const emailTexts = buttons.map((b) => b.text())

    // active@test.com should be listed first
    const activeIdx = emailTexts.findIndex((t) => t.includes('active@test.com'))
    const deletedIdx = emailTexts.findIndex((t) => t.includes('gone@test.com'))
    expect(activeIdx).toBeGreaterThanOrEqual(0)
    expect(deletedIdx).toBeGreaterThanOrEqual(0)
    expect(activeIdx).toBeLessThan(deletedIdx)

    // --- (a) Label: deleted user's button shows the badge text ---
    const deletedButton = buttons[deletedIdx]
    expect(deletedButton.text()).toContain('deleted')

    // active user's button does NOT show the badge text
    const activeButton = buttons[activeIdx]
    expect(activeButton.text()).not.toContain('deleted')

    // --- (c) Selection: clicking active user button sets the draft user_id ---
    await activeButton.trigger('click')
    await flushPromises()

    // selectUser 只写草稿（modelValue 原地更新），不发查询；用户需点「查询」才生效
    expect(wrapper.emitted('query')).toBeUndefined()
    expect(wrapper.props('modelValue').user_id).toBe(1)
  })

  it('keeps results from the latest user search when responses arrive out of order', async () => {
    const firstSearch = deferred<Array<{ id: number; email: string; deleted: boolean }>>()
    const secondSearch = deferred<Array<{ id: number; email: string; deleted: boolean }>>()
    mockSearchUsers
      .mockImplementationOnce(() => firstSearch.promise)
      .mockImplementationOnce(() => secondSearch.promise)

    const wrapper = mountFilters()
    const input = wrapper.find('.usage-filter-dropdown input[type="text"]')
    await input.trigger('focus')

    await input.setValue('a')
    vi.advanceTimersByTime(300)
    await flushPromises()

    await input.setValue('ab')
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

    const wrapper = mountFilters()
    const input = wrapper.find('.usage-filter-dropdown input[type="text"]')
    await input.trigger('focus')

    await input.setValue('stale')
    vi.advanceTimersByTime(300)
    await flushPromises()

    await input.setValue('')
    vi.advanceTimersByTime(300)
    await flushPromises()

    pendingSearch.resolve([{ id: 3, email: 'stale@test.com', deleted: false }])
    await flushPromises()
    expect(wrapper.text()).not.toContain('stale@test.com')
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
    const wrapper = mount(UsageFilters, {
      props: {
        modelValue: defaultFilters(),
        exporting: false,
        startDate: '2026-05-01',
        endDate: '2026-05-28',
        showActions: false,
        modelOptions: ['claude-3', 'gpt-4o'],
      },
      global: { stubs: { Select: true, Teleport: true } },
    })
    await flushPromises()

    expect(mockGetModelStats).not.toHaveBeenCalled()

    const opts = (wrapper.vm as any).modelOptions as Array<{ value: string | null; label: string }>
    expect(opts.map((o) => o.value)).toEqual([null, 'claude-3', 'gpt-4o'])
  })
})

describe('UsageFilters — native compaction filter', () => {
  it('offers only All/Compaction and emits the independent boolean filter', async () => {
    const SelectStub = {
      name: 'Select',
      props: ['modelValue', 'options'],
      emits: ['update:modelValue', 'change'],
      template: '<div />',
    }
    const filters = defaultFilters()
    const wrapper = mount(UsageFilters, {
      props: {
        modelValue: filters,
        exporting: false,
        startDate: '2026-05-01',
        endDate: '2026-05-28',
        showActions: false,
        modelOptions: [],
      },
      global: { stubs: { Select: SelectStub, Teleport: true } },
    })

    const compactionSelect = wrapper.findAllComponents(SelectStub).find((select: any) =>
      (select.props('options') as Array<{ value: unknown }>).some((option) => option.value === true)
    )
    expect(compactionSelect).toBeDefined()
    expect(compactionSelect!.props('options')).toEqual([
      { value: null, label: 'All Requests' },
      { value: true, label: 'Compaction Only' },
    ])
    expect(compactionSelect!.props('options')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ value: false })])
    )

    compactionSelect!.vm.$emit('update:modelValue', true)
    compactionSelect!.vm.$emit('change')
    await wrapper.vm.$nextTick()

    expect(filters.native_compaction_v2).toBe(true)
    expect(wrapper.emitted('query')).toBeUndefined()
  })
})

// 布局重排 + 手动查询语义
function mountWithActions(filters = defaultFilters(), extraProps: Record<string, unknown> = {}) {
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
        Select: true,
        Teleport: true,
      },
    },
  })
}

describe('UsageFilters — 多列网格布局', () => {
  it('lays fields out in a responsive grid: 1 column mobile / 2 tablet / 3 desktop', () => {
    const wrapper = mountWithActions()
    const grid = wrapper.get('[data-testid="usage-filter-grid"]')
    expect(grid.classes()).toContain('grid')
    expect(grid.classes()).toEqual(expect.arrayContaining([
      'grid-cols-1',
      'sm:grid-cols-2',
      'xl:grid-cols-3',
    ]))
    // 每个筛选项：label 在上、控件占满整格
    const field = wrapper.get('[data-testid="usage-date-range-filter"]')
    expect(field.classes()).toEqual(expect.arrayContaining(['flex', 'flex-col', 'min-w-0']))
    expect(field.find('label.input-label').exists()).toBe(true)
    wrapper.unmount()
  })

  it('renders one right-aligned action row with a primary query button and secondary actions', () => {
    const wrapper = mountWithActions()
    const actions = wrapper.get('[data-testid="usage-filter-actions"]')
    expect(actions.classes()).toEqual(expect.arrayContaining(['flex', 'flex-wrap', 'justify-end', 'border-t']))

    const query = wrapper.get('[data-testid="usage-filter-query"]')
    expect(query.classes()).toContain('btn-primary')

    for (const id of ['reset', 'refresh', 'export-excel', 'export-csv']) {
      expect(wrapper.get(`[data-testid="usage-filter-${id}"]`).classes()).toContain('btn-secondary')
    }
    expect(wrapper.get('[data-testid="usage-filter-cleanup"]').classes()).toContain('btn-danger')
    wrapper.unmount()
  })

  it('orders buttons: query → reset → refresh → column settings slot → cleanup → export excel → export csv', () => {
    const wrapper = mount(UsageFilters, {
      props: {
        modelValue: defaultFilters(),
        exporting: false,
        startDate: '2026-05-01',
        endDate: '2026-05-28',
        modelOptions: [],
      },
      slots: { 'after-reset': '<button data-testid="usage-column-settings">columns</button>' },
      global: { stubs: { Select: true, Teleport: true } },
    })
    const ids = Array.from(wrapper.get('[data-testid="usage-filter-actions"]').element.querySelectorAll('button'))
      .map((el) => el.getAttribute('data-testid'))
    expect(ids).toEqual([
      'usage-filter-query',
      'usage-filter-reset',
      'usage-filter-refresh',
      'usage-column-settings',
      'usage-filter-cleanup',
      'usage-filter-export-excel',
      'usage-filter-export-csv',
    ])
    wrapper.unmount()
  })

  it('hides the action row when embedded without actions (cleanup dialog)', () => {
    const wrapper = mountWithActions(defaultFilters(), { showActions: false })
    expect(wrapper.find('[data-testid="usage-filter-actions"]').exists()).toBe(false)
    wrapper.unmount()
  })
})

describe('UsageFilters — 手动查询', () => {
  it('does not query when a filter is edited', async () => {
    const filters = defaultFilters()
    const wrapper = mountWithActions(filters)
    const input = wrapper.get('[data-testid="usage-request-id"]')
    await input.setValue('550e8400')
    await input.trigger('input')
    expect(filters.request_id).toBe('550e8400')
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })

  it('queries once with the draft when the query button is clicked', async () => {
    const filters = defaultFilters()
    const wrapper = mountWithActions(filters)
    await wrapper.get('[data-testid="usage-request-id"]').setValue('=abc123')
    await wrapper.get('[data-testid="usage-filter-query"]').trigger('click')
    expect(wrapper.emitted('query')).toHaveLength(1)
    expect(filters.request_id).toBe('=abc123')
    wrapper.unmount()
  })

  it('treats Enter in a filter input as clicking query', async () => {
    const wrapper = mountWithActions()
    const input = wrapper.get('[data-testid="usage-request-id"]')
    await input.setValue('^client:')
    await input.trigger('keyup.enter')
    expect(wrapper.emitted('query')).toHaveLength(1)
    wrapper.unmount()
  })

  it('treats Enter in an observer ID input as clicking query', async () => {
    const wrapper = mountWithActions(defaultFilters(), { observer: true })
    const input = wrapper.get('#observer-user_id')
    await input.setValue('42')
    await input.trigger('keyup.enter')
    expect(wrapper.emitted('query')).toHaveLength(1)
    wrapper.unmount()
  })

  it('emits reset / refresh / cleanup / export / export-csv from the action row', async () => {
    const wrapper = mountWithActions()
    await wrapper.get('[data-testid="usage-filter-reset"]').trigger('click')
    await wrapper.get('[data-testid="usage-filter-refresh"]').trigger('click')
    await wrapper.get('[data-testid="usage-filter-cleanup"]').trigger('click')
    await wrapper.get('[data-testid="usage-filter-export-excel"]').trigger('click')
    await wrapper.get('[data-testid="usage-filter-export-csv"]').trigger('click')
    expect(wrapper.emitted('reset')).toHaveLength(1)
    expect(wrapper.emitted('refresh')).toHaveLength(1)
    expect(wrapper.emitted('cleanup')).toHaveLength(1)
    expect(wrapper.emitted('export')).toHaveLength(1)
    expect(wrapper.emitted('export-csv')).toHaveLength(1)
    // 这些动作本身都不应额外触发查询，避免重复请求
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps observer ID edits as draft without emitting query', async () => {
    const filters = defaultFilters()
    const wrapper = mountWithActions(filters, { observer: true })
    await wrapper.get('#observer-account_id').setValue('7')
    expect(filters.account_id).toBe(7)
    expect(wrapper.emitted('query')).toBeUndefined()
    wrapper.unmount()
  })
})
