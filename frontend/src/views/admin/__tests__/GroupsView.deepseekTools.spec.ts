import { defineComponent, h, ref, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup } from '@/types'
import GroupsView from '../GroupsView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), duplicate: vi.fn(),
  candidates: vi.fn(), usage: vi.fn(), capacity: vi.fn(), live: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: {
  groups: { list: mocks.list, create: mocks.create, update: mocks.update, duplicate: mocks.duplicate,
    getAll: vi.fn().mockResolvedValue([]), getModelAllowlistCandidates: mocks.candidates,
    getUsageSummary: mocks.usage, getCapacitySummary: mocks.capacity, getLiveCapability: mocks.live,
    delete: vi.fn(), updateSortOrder: vi.fn() },
  accounts: { list: vi.fn().mockResolvedValue({ items: [] }), getById: vi.fn() }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key}${JSON.stringify(params)}` : key),
    locale: ref('en')
  })
}))

enableAutoUnmount(afterEach)

function makeGroup(patch: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 99, name: 'DeepSeek Official', description: null, platform: 'deepseek', rate_multiplier: 0.3,
    is_exclusive: false, status: 'active', subscription_type: 'standard',
    daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null,
    long_context_pricing_enabled: true, force_openai_fast: false, free_openai_fast: false, model_pricing: [],
    allow_image_generation: false, allow_batch_image_generation: false, image_rate_independent: false,
    image_rate_multiplier: 1, batch_image_discount_multiplier: 0.5, batch_image_hold_multiplier: 0.6,
    image_price_1k: null, image_price_2k: null, image_price_4k: null,
    video_rate_independent: false, video_rate_multiplier: 1,
    video_price_480p: null, video_price_720p: null, video_price_1080p: null,
    web_search_price_per_call: null, tool_rate_independent: false, tool_rate_multiplier: 1,
    search_price_per_1k: null, fetch_price_per_1k: null, ocr_price_per_1k_pages: null,
    grok_moderation_refusal_price: null,
    audio_realtime_price_per_min: null, audio_tts_price_per_million_chars: null, audio_stt_price_per_hour: null,
    peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1,
    profit_control_enabled: false, profit_min_margin: 0, profit_safety_buffer: 0,
    claude_code_only: false, fallback_group_id: null, fallback_group_id_on_invalid_request: null,
    allow_live: false, require_oauth_only: false, require_privacy_set: false,
    model_routing: null, model_routing_enabled: false, mcp_xml_inject: true, sort_order: 0,
    created_at: '2026-11-27T00:00:00Z', updated_at: '2026-11-27T00:00:00Z', ...patch
  }
}

const SelectStub = defineComponent({
  props: { modelValue: [String, Number, Boolean], options: { type: Array as PropType<Array<{ value: string; label: string }>>, default: () => [] } },
  emits: ['update:modelValue', 'change'],
  setup(props, { emit }) {
    return () => h('select', { value: props.modelValue as string, onChange: (event: Event) => {
      const value = (event.target as HTMLSelectElement).value
      emit('update:modelValue', value)
      emit('change', value)
    } }, props.options.map(option => h('option', { value: option.value }, option.label)))
  }
})
const ValidatedFieldStub = defineComponent({ setup(_props, { expose }) {
  expose({ validate: () => true, resetValidation: () => undefined })
  return () => h('div')
} })

async function render() {
  const wrapper = mount(GroupsView, { global: { stubs: {
    AppLayout: { template: '<main><slot /></main>' },
    TablePageLayout: { template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>' },
    DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.id" :data-row="row.id"><slot name="cell-actions" :row="row" /></div></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    Select: SelectStub, ReasoningEffortPolicyFields: ValidatedFieldStub, CodexManifestAccountsField: ValidatedFieldStub,
    ConfirmDialog: true, Pagination: true, EmptyState: true, PlatformIcon: true, Icon: true,
    GroupCapacityBadge: true, GroupRateMultipliersModal: true, GroupRPMOverridesModal: true,
    PricingEntryCard: true, VueDraggable: true
  } } })
  await flushPromises()
  return wrapper
}
type Wrapper = Awaited<ReturnType<typeof render>>

async function openCreate(wrapper: Wrapper, platform = 'deepseek') {
  await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
  await flushPromises()
  await wrapper.get('#create-group-form input[type="text"]').setValue('DS Tool Group')
  await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
  await flushPromises()
}

async function openEdit(wrapper: Wrapper, id: number) {
  const edit = wrapper.get(`[data-row="${id}"]`).findAll('button').find(button => button.text() === 'common.edit')!
  await edit.trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  mocks.list.mockResolvedValue({
    items: [
      makeGroup({
        id: 99,
        name: 'DS Custom',
        rate_multiplier: 0.3,
        tool_rate_independent: true,
        tool_rate_multiplier: 1,
        search_price_per_1k: 1.5,
        fetch_price_per_1k: 0,
        ocr_price_per_1k_pages: 0.45,
        audio_tts_price_per_million_chars: 100,
        audio_stt_price_per_hour: 0.25,
      }),
      makeGroup({
        id: 116,
        name: 'DS Default',
        rate_multiplier: 0.05,
        tool_rate_independent: false,
        tool_rate_multiplier: 1,
      }),
    ],
    total: 2, page: 1, page_size: 20, pages: 1
  })
  mocks.create.mockResolvedValue(makeGroup({ id: 117 }))
  mocks.update.mockResolvedValue(makeGroup({ id: 99 }))
  mocks.candidates.mockResolvedValue([])
  mocks.usage.mockResolvedValue([])
  mocks.capacity.mockResolvedValue([])
  mocks.live.mockResolvedValue({ supported: false })
})

describe('GroupsView DeepSeek tool pricing', () => {
  it('renders unified DeepSeek tool pricing with live effective preview and keeps Grok separate', async () => {
    const wrapper = await render()
    await openCreate(wrapper, 'deepseek')

    expect(wrapper.find('[data-testid="create-fetch-price"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="create-deepseek-tool-preview"]').exists()).toBe(true)

    await wrapper.get('#create-group-form input[data-tour="group-form-multiplier"]').setValue('0.05')
    await flushPromises()

    const preview = wrapper.get('[data-testid="create-deepseek-tool-preview"]').text()
    expect(preview).toContain('0.05')
    expect(preview).toContain('0.00025')
    expect(preview).toContain('0.000075')
    expect(preview).toContain('15')
    expect(preview).toContain('0.039')

    // Setting custom search price makes empty fetch show inherited tag & matching preview
    await wrapper.get('input[data-testid="create-search-price"]').setValue('4')
    await flushPromises()
    const fetchPreview = wrapper.get('[data-testid="create-deepseek-preview-deepseek-tools-fetch"]').text()
    expect(fetchPreview).toContain('deepseekToolPreviewInherited')
    expect(fetchPreview).toContain('0.0002')

    // Explicit 0 on fetch renders Free
    await wrapper.get('input[data-testid="create-fetch-price"]').setValue('0')
    await flushPromises()
    expect(wrapper.get('[data-testid="create-deepseek-preview-deepseek-tools-fetch"]').text()).toContain('deepseekToolPreviewFree')

    // Switching to Grok hides DeepSeek tool preview and fetch input
    await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue('grok')
    await flushPromises()
    expect(wrapper.find('[data-testid="create-deepseek-tool-preview"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-fetch-price"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="create-search-price"]').exists()).toBe(true)
  })

  it('submits tool_rate_independent, tool_rate_multiplier, and fetch_price_per_1k on create', async () => {
    const wrapper = await render()
    await openCreate(wrapper, 'deepseek')

    await wrapper.get('[data-testid="create-tool-rate-independent"]').trigger('click')
    await flushPromises()
    await wrapper.get('input[data-testid="create-tool-rate-multiplier"]').setValue('0.5')
    await wrapper.get('input[data-testid="create-search-price"]').setValue('4')
    await wrapper.get('input[data-testid="create-fetch-price"]').setValue('0')
    await wrapper.get('input[data-testid="create-ocr-price"]').setValue('1.2')

    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.showError).not.toHaveBeenCalled()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'deepseek',
      tool_rate_independent: true,
      tool_rate_multiplier: 0.5,
      search_price_per_1k: 4,
      fetch_price_per_1k: 0,
      ocr_price_per_1k_pages: 1.2,
    }))
  })

  it('hydrates edit form, sends -1 when clearing fetch/ocr while keeping 0 free, and resets ocr on close', async () => {
    const wrapper = await render()
    await openEdit(wrapper, 99)

    expect((wrapper.get('input[data-testid="edit-tool-rate-multiplier"]').element as HTMLInputElement).value).toBe('1')
    expect((wrapper.get('input[data-testid="edit-search-price"]').element as HTMLInputElement).value).toBe('1.5')
    expect((wrapper.get('input[data-testid="edit-fetch-price"]').element as HTMLInputElement).value).toBe('0')
    expect((wrapper.get('input[data-testid="edit-ocr-price"]').element as HTMLInputElement).value).toBe('0.45')
    expect(wrapper.get('[data-testid="edit-deepseek-preview-deepseek-tools-fetch"]').text()).toContain('deepseekToolPreviewFree')

    // Clear OCR, keep Fetch at 0, submit
    await wrapper.get('input[data-testid="edit-ocr-price"]').setValue('')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.update).toHaveBeenCalledWith(99, expect.objectContaining({
      tool_rate_independent: true,
      tool_rate_multiplier: 1,
      search_price_per_1k: 1.5,
      fetch_price_per_1k: 0,
      ocr_price_per_1k_pages: -1,
    }))

    // Re-open group 116 (null OCR/Fetch, independent false) — must not inherit stale OCR/Fetch from group 99
    await openEdit(wrapper, 116)
    expect((wrapper.get('input[data-testid="edit-ocr-price"]').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('input[data-testid="edit-fetch-price"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.find('input[data-testid="edit-tool-rate-multiplier"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="edit-deepseek-tool-preview"]').text()).toContain('0.000075')
  })
})
