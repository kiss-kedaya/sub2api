import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'
const confirmMock = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useConfirmation', () => ({ confirm: confirmMock }))


const {
  listGroups,
  batchDeleteGroups,
  deleteGroup,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  batchDeleteGroups: vi.fn(),
  deleteGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

const authState = vi.hoisted(() => ({ isSimpleMode: false }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      batchDelete: batchDeleteGroups,
      delete: deleteGroup,
      update: updateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getAll: vi.fn(),
      create: vi.fn(),
      duplicate: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: { list: vi.fn(), getById: vi.fn() }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

function makeGroup(id: number, name: string): AdminGroup {
  return {
    id,
    name,
    description: null,
    platform: 'openai',
    rate_multiplier: 1,
    rpm_limit: 0,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    allow_image_generation: false,
    allow_batch_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 0.5,
    batch_image_hold_multiplier: 0.6,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    video_price_480p: null,
    video_price_720p: null,
    video_price_1080p: null,
    web_search_price_per_call: null,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    allow_messages_dispatch: false,
    default_mapped_model: '',
    messages_dispatch_model_config: undefined,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-07-16T00:00:00Z',
    updated_at: '2026-07-16T00:00:00Z',
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: true,
    supported_model_scopes: [],
    account_count: 1,
    active_account_count: 1,
    rate_limited_account_count: 0,
    model_allowlist: undefined,
    sort_order: id * 10
  } as unknown as AdminGroup
}

const groups = [makeGroup(1, 'Primary'), makeGroup(2, 'Secondary'), makeGroup(3, 'Tertiary')]

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
    selectable: { type: Boolean, default: false },
    selectedKeys: { type: Array, default: () => [] }
  },
  emits: ['update:selectedKeys'],
  setup(props, { emit }) {
    const toggle = (id: number) => {
      const next = new Set(props.selectedKeys as Array<string | number>)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      emit('update:selectedKeys', Array.from(next))
    }
    return { toggle }
  },
  template: `<div>
    <div v-for="row in data" :key="row.id" :data-row="row.id">
      <span data-testid="row-name">{{ row.name }}</span>
      <button :data-testid="'toggle-' + row.id" @click="toggle(row.id)">toggle</button>
      <slot name="cell-actions" :row="row" />
    </div>
  </div>`
})

const ConfirmDialogStub = defineComponent({
  props: { show: { type: Boolean, default: false }, title: String, message: String },
  emits: ['confirm', 'cancel'],
  template: `<div v-if="show" data-testid="confirm-dialog">
    <button data-testid="confirm-delete" @click="$emit('confirm')">ok</button>
  </div>`
})

const AppLayoutStub = defineComponent({ template: '<main><slot /></main>' })
const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        ConfirmDialog: ConfirmDialogStub,
        Pagination: true,
        BaseDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true
      }
    }
  })
}

const rowNames = (wrapper: ReturnType<typeof mountView>) =>
  wrapper.findAll('[data-testid="row-name"]').map((node) => node.text())

async function openRowMenu(wrapper: ReturnType<typeof mountView>) {
  const trigger = wrapper.get('[data-testid="group-row-more"]')
  await trigger.trigger('click')
  await flushPromises()
  const menu = document.getElementById(trigger.attributes('aria-controls') ?? '')
  return Array.from(menu?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])
}

describe('GroupsView bulk selection and optimistic deletes', () => {
  beforeEach(() => {
    authState.isSimpleMode = false
    localStorage.clear()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    confirmMock.mockReset()
    confirmMock.mockResolvedValue(true)
    for (const fn of [
      listGroups,
      batchDeleteGroups,
      deleteGroup,
      updateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      showSuccess,
      showError
    ]) {
      fn.mockReset()
    }
    listGroups.mockResolvedValue({ items: groups.map((g) => ({ ...g })), total: 3, page: 1, page_size: 20, pages: 1 })
    getModelAllowlistCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('selects rows, removes them optimistically and issues one batch call', async () => {
    let resolveBatch!: (value: { success: number; failed: number; failed_ids: number[] }) => void
    batchDeleteGroups.mockImplementationOnce(
      () => new Promise((resolve) => { resolveBatch = resolve })
    )

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.find('[data-testid="groups-bulk-bar"]').exists()).toBe(false)
    await wrapper.get('[data-testid="toggle-1"]').trigger('click')
    await wrapper.get('[data-testid="toggle-2"]').trigger('click')
    expect(wrapper.find('[data-testid="groups-bulk-bar"]').exists()).toBe(true)

    await wrapper.get('[data-testid="groups-bulk-delete"]').trigger('click')

    // Rows disappear before the request settles, and the list is not re-fetched.
    expect(rowNames(wrapper)).toEqual(['Tertiary'])
    expect(listGroups).toHaveBeenCalledTimes(1)
    expect(batchDeleteGroups).toHaveBeenCalledTimes(1)
    expect(batchDeleteGroups).toHaveBeenCalledWith([1, 2])
    expect(wrapper.find('[data-testid="groups-bulk-bar"]').exists()).toBe(false)

    resolveBatch({ success: 2, failed: 0, failed_ids: [] })
    await flushPromises()

    expect(showSuccess).toHaveBeenCalledWith('admin.groups.groupDeleted')
    expect(rowNames(wrapper)).toEqual(['Tertiary'])
    expect(listGroups).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('keeps only the rows that failed to delete', async () => {
    batchDeleteGroups.mockResolvedValueOnce({ success: 1, failed: 1, failed_ids: [2] })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="toggle-1"]').trigger('click')
    await wrapper.get('[data-testid="toggle-2"]').trigger('click')
    await wrapper.get('[data-testid="groups-bulk-delete"]').trigger('click')
    await flushPromises()

    expect([...rowNames(wrapper)].sort()).toEqual(['Secondary', 'Tertiary'])
    expect(showError).toHaveBeenCalledWith('admin.groups.failedToDelete')
    expect(wrapper.find('[data-testid="groups-bulk-bar"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="toggle-2"]')).toHaveLength(1)
    expect(listGroups).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('rolls the rows back when the batch request rejects', async () => {
    batchDeleteGroups.mockRejectedValueOnce(new Error('boom'))

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="toggle-1"]').trigger('click')
    await wrapper.get('[data-testid="toggle-2"]').trigger('click')
    await wrapper.get('[data-testid="groups-bulk-delete"]').trigger('click')
    await flushPromises()

    expect([...rowNames(wrapper)].sort()).toEqual(['Primary', 'Secondary', 'Tertiary'])
    expect(showError).toHaveBeenCalled()
    expect(wrapper.find('[data-testid="groups-bulk-bar"]').exists()).toBe(true)
    expect(listGroups).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('removes a single row optimistically and restores it when the request fails', async () => {
    let rejectDelete!: (error: Error) => void
    deleteGroup.mockImplementationOnce(
      () => new Promise((_resolve, reject) => { rejectDelete = reject })
    )

    const wrapper = mountView()
    await flushPromises()

    const items = await openRowMenu(wrapper)
    const deleteItem = items.find((item) => item.textContent?.includes('common.delete'))
    expect(deleteItem).toBeTruthy()
    deleteItem!.click()
    await flushPromises()

    await wrapper.get('[data-testid="confirm-delete"]').trigger('click')
    expect(rowNames(wrapper)).not.toContain('Primary')
    expect(listGroups).toHaveBeenCalledTimes(1)

    rejectDelete(new Error('nope'))
    await flushPromises()
    expect(rowNames(wrapper)).toContain('Primary')
    expect(showError).toHaveBeenCalledWith('admin.groups.failedToDelete')
    expect(listGroups).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
