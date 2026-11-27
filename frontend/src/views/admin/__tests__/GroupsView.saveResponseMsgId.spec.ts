import { defineComponent, h, ref, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup, GroupPlatform } from '@/types'
import { GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'
import GroupsView from '../GroupsView.vue'
import zh from '@/i18n/locales/zh/admin/overview'
import en from '@/i18n/locales/en/admin/overview'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), create: vi.fn(), update: vi.fn(), duplicate: vi.fn(),
  candidates: vi.fn(), usage: vi.fn(), capacity: vi.fn(), live: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: {
  groups: {
    list: mocks.list, create: mocks.create, update: mocks.update, duplicate: mocks.duplicate,
    getAll: vi.fn().mockResolvedValue([]), getModelAllowlistCandidates: mocks.candidates,
    getUsageSummary: mocks.usage, getCapacitySummary: mocks.capacity, getLiveCapability: mocks.live,
    delete: vi.fn(), updateSortOrder: vi.fn()
  },
  accounts: { list: vi.fn().mockResolvedValue({ items: [] }), getById: vi.fn() }
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: mocks.showSuccess }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('@/stores/onboarding', () => ({ useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
}))

enableAutoUnmount(afterEach)

function group(patch: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 42, name: 'Claude Group', description: null, platform: 'anthropic', rate_multiplier: 1,
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
    save_response_msg_id: false,
    model_routing: null, model_routing_enabled: false, mcp_xml_inject: true, sort_order: 0,
    created_at: '2026-10-26T00:00:00Z', updated_at: '2026-10-26T00:00:00Z', ...patch
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

async function openCreate(wrapper: Awaited<ReturnType<typeof render>>) {
  await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
  await flushPromises()
  await wrapper.get('#create-group-form input[type="text"]').setValue('Claude New')
}

async function selectCreatePlatform(wrapper: Awaited<ReturnType<typeof render>>, platform: GroupPlatform) {
  await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
  await flushPromises()
}

async function openEdit(wrapper: Awaited<ReturnType<typeof render>>, id = 42) {
  const edit = wrapper.get(`[data-row="${id}"]`).findAll('button').find(button => button.text() === 'common.edit')!
  await edit.trigger('click')
  await flushPromises()
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
})

describe('GroupsView Claude save_response_msg_id setting', () => {
  it('shows save_response_msg_id toggle only for anthropic platform on create', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    for (const { value: platform } of GROUP_PLATFORM_OPTIONS) {
      await selectCreatePlatform(wrapper, platform)
      expect(wrapper.find('[data-testid="create-save-response-msg-id"]').exists(), platform).toBe(platform === 'anthropic')
    }
  })

  it.each([false, true])('creates an anthropic group with save_response_msg_id=%s', async (enabled) => {
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'anthropic')
    const toggle = wrapper.get('[data-testid="create-save-response-msg-id"]')
    expect(toggle.attributes('role')).toBe('switch')
    expect(toggle.attributes('aria-checked')).toBe('false')
    if (enabled) await toggle.trigger('click')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledOnce()
    expect(mocks.create.mock.calls[0][0]).toHaveProperty('save_response_msg_id', enabled)
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('forces save_response_msg_id=false when switching away from anthropic before submit', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'anthropic')
    await wrapper.get('[data-testid="create-save-response-msg-id"]').trigger('click')
    await selectCreatePlatform(wrapper, 'openai')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ platform: 'openai', save_response_msg_id: false }))
  })

  it('resets an enabled create draft when reopening create dialog', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'anthropic')
    await wrapper.get('[data-testid="create-save-response-msg-id"]').trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'anthropic')
    expect(wrapper.get('[data-testid="create-save-response-msg-id"]').attributes('aria-checked')).toBe('false')
  })

  it.each([
    { stored: true, saved: true },
    { stored: true, saved: false },
    { stored: false, saved: true },
    { stored: false, saved: false },
    { stored: undefined, saved: false }
  ])('loads $stored and submits $saved when editing an anthropic group', async ({ stored, saved }) => {
    const patch = stored === undefined ? { save_response_msg_id: undefined } : { save_response_msg_id: stored }
    mocks.list.mockResolvedValue({ items: [group(patch)], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openEdit(wrapper)
    const toggle = wrapper.get('[data-testid="edit-save-response-msg-id"]')
    expect(toggle.attributes('aria-checked')).toBe(String(stored ?? false))
    if ((stored ?? false) !== saved) await toggle.trigger('click')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledOnce()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ save_response_msg_id: saved }))
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('hides save_response_msg_id toggle when editing a non-anthropic group', async () => {
    mocks.list.mockResolvedValue({ items: [group({ platform: 'openai', save_response_msg_id: false })], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(wrapper.find('[data-testid="edit-save-response-msg-id"]').exists()).toBe(false)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ platform: 'openai', save_response_msg_id: false }))
  })

  it('preserves save_response_msg_id returned by duplicate', async () => {
    const source = group({ save_response_msg_id: true })
    const copied = group({ id: 43, save_response_msg_id: true })
    mocks.list.mockResolvedValueOnce({ items: [source], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.list.mockResolvedValue({ items: [copied], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.duplicate.mockResolvedValue(copied)
    const wrapper = await render()
    await wrapper.get('[data-testid="group-duplicate"]').trigger('click')
    await flushPromises()
    expect(mocks.duplicate).toHaveBeenCalledWith(42)
    await openEdit(wrapper, 43)
    expect(wrapper.get('[data-testid="edit-save-response-msg-id"]').attributes('aria-checked')).toBe('true')
  })

  it('documents msg_id and always-saved response header IDs in both locales', () => {
    for (const messages of [zh, en]) {
      const cfg = messages.groups.saveResponseMsgId
      expect(cfg.title).toContain('msg_id')
      expect(cfg.tooltip).toContain('X-Request-Id')
      expect(cfg.tooltip).toContain('X-Client-Request-Id')
      expect(cfg.disabled).toContain('X-Request-Id')
    }
  })
})
