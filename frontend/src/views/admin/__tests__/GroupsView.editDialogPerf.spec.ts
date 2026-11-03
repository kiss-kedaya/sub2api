import { defineComponent, h, ref, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup } from '@/types'
import GroupsView from '../GroupsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), duplicate: vi.fn(),
  candidates: vi.fn(), usage: vi.fn(), capacity: vi.fn(), live: vi.fn(),
  getAccountById: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ adminAPI: {
  groups: {
    list: mocks.list, create: mocks.create, update: mocks.update, duplicate: mocks.duplicate,
    getAll: vi.fn().mockResolvedValue([]), getModelAllowlistCandidates: mocks.candidates,
    getUsageSummary: mocks.usage, getCapacitySummary: mocks.capacity, getLiveCapability: mocks.live,
    delete: vi.fn(), updateSortOrder: vi.fn(),
  },
  accounts: { list: vi.fn().mockResolvedValue({ items: [] }), getById: mocks.getAccountById },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))

enableAutoUnmount(afterEach)

function group(patch: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 42, name: 'Primary', description: null, platform: 'openai', rate_multiplier: 1,
    is_exclusive: false, status: 'active', subscription_type: 'standard',
    daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
    long_context_pricing_enabled: true, force_openai_fast: false, free_openai_fast: false, model_pricing: [],
    allow_image_generation: false, allow_batch_image_generation: false, image_rate_independent: false,
    image_rate_multiplier: 1, batch_image_discount_multiplier: 0.5, batch_image_hold_multiplier: 0.6,
    image_price_1k: null, image_price_2k: null, image_price_4k: null,
    video_rate_independent: false, video_rate_multiplier: 1,
    video_price_480p: null, video_price_720p: null, video_price_1080p: null,
    web_search_price_per_call: null, search_price_per_1k: null, grok_moderation_refusal_price: null,
    audio_realtime_price_per_min: null, audio_tts_price_per_million_chars: null, audio_stt_price_per_hour: null,
    peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    profit_control_enabled: false, profit_min_margin: 0, profit_safety_buffer: 0,
    claude_code_only: false, fallback_group_id: null, fallback_group_id_on_invalid_request: null,
    allow_live: false, require_oauth_only: false, require_privacy_set: false,
    model_routing: null, model_routing_enabled: false, mcp_xml_inject: true, sort_order: 0,
    created_at: '2026-11-01T00:00:00Z', updated_at: '2026-11-01T00:00:00Z', ...patch,
  }
}

const SelectStub = defineComponent({
  props: { modelValue: [String, Number, Boolean], options: { type: Array as PropType<Array<{ value: string; label: string }>>, default: () => [] } },
  emits: ['update:modelValue', 'change'],
  setup(props, { emit }) {
    return () => h('select', {
      value: props.modelValue as string,
      onChange: (event: Event) => {
        const value = (event.target as HTMLSelectElement).value
        emit('update:modelValue', value)
        emit('change', value)
      },
    }, props.options.map(option => h('option', { value: option.value }, option.label)))
  },
})
const ValidatedFieldStub = defineComponent({
  setup(_props, { expose }) {
    expose({ validate: () => true, resetValidation: () => undefined })
    return () => h('div')
  },
})

const DataTableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: '<div><div v-for="row in data" :key="row.id" :data-row="row.id"><slot name="cell-actions" :row="row" /></div></div>',
})

async function render() {
  const wrapper = mount(GroupsView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
    DataTable: DataTableStub,
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    Select: SelectStub,
    ReasoningEffortPolicyFields: ValidatedFieldStub,
    CodexManifestAccountsField: ValidatedFieldStub,
    ConfirmDialog: true, Pagination: true, EmptyState: true, PlatformIcon: true, Icon: true,
    GroupCapacityBadge: true, GroupRateMultipliersModal: true, GroupRPMOverridesModal: true,
    PricingEntryCard: true, VueDraggable: true,
  } } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  mocks.list.mockResolvedValue({ items: [group()], total: 1, page: 1, page_size: 20, pages: 1 })
  mocks.create.mockResolvedValue(group({ id: 43 }))
  mocks.update.mockResolvedValue(group())
  mocks.candidates.mockResolvedValue([])
  mocks.usage.mockResolvedValue([])
  mocks.capacity.mockResolvedValue([])
  mocks.live.mockResolvedValue({ supported: false })
  mocks.getAccountById.mockReset()
})

describe('GroupsView edit dialog opening', () => {
  it('opens before the routing-rule account names resolve, then fills them in', async () => {
    let resolveDetail: ((value: { id: number; name: string }) => void) | undefined
    mocks.getAccountById.mockImplementation(() => new Promise((resolve) => {
      resolveDetail = resolve as (value: { id: number; name: string }) => void
    }))
    mocks.list.mockResolvedValue({
      items: [group({ platform: 'anthropic', model_routing: { 'gpt-*': [77] }, model_routing_enabled: true })],
      total: 1, page: 1, page_size: 20, pages: 1,
    })

    const wrapper = await render()
    const edit = wrapper.get('[data-row="42"]').findAll('button').find(button => button.text() === 'common.edit')!
    await edit.trigger('click')
    await flushPromises()

    // The dialog is already usable while the account detail request is still pending.
    expect(wrapper.find('#edit-group-form').exists()).toBe(true)
    expect(wrapper.text()).toContain('#77')
    expect(mocks.getAccountById).toHaveBeenCalledTimes(1)

    resolveDetail?.({ id: 77, name: 'Resolved account' })
    await flushPromises()

    expect(wrapper.find('#edit-group-form').exists()).toBe(true)
    expect(wrapper.text()).toContain('Resolved account')
    expect(mocks.getAccountById).toHaveBeenCalledTimes(1)
  })

  it('reuses a resolved account name the next time the same group is edited', async () => {
    mocks.getAccountById.mockResolvedValue({ id: 77, name: 'Cached account' })
    mocks.list.mockResolvedValue({
      items: [group({ platform: 'anthropic', model_routing: { 'gpt-*': [77] }, model_routing_enabled: true })],
      total: 1, page: 1, page_size: 20, pages: 1,
    })

    const wrapper = await render()
    const edit = () => wrapper.get('[data-row="42"]').findAll('button').find(button => button.text() === 'common.edit')!

    await edit().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Cached account')
    expect(mocks.getAccountById).toHaveBeenCalledTimes(1)

    await wrapper.get('#edit-group-form').exists()
    // Close and reopen: the name is cached, so no extra request is made.
    const cancel = wrapper.findAll('button').find(button => button.text() === 'common.cancel')
    if (cancel) await cancel.trigger('click')
    await flushPromises()

    await edit().trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Cached account')
    expect(mocks.getAccountById).toHaveBeenCalledTimes(1)
  })
})
