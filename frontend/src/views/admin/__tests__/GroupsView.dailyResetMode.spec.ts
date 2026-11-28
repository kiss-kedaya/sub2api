import { defineComponent, h, ref, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup, DailyResetMode } from '@/types'
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
    id: 42, name: 'Sub Group', description: null, platform: 'anthropic', rate_multiplier: 1,
    is_exclusive: true, status: 'active', subscription_type: 'subscription',
    daily_reset_mode: 'calendar',
    daily_limit_usd: 100, weekly_limit_usd: null, monthly_limit_usd: null,
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
  await wrapper.get('#create-group-form input[type="text"]').setValue('Sub New')
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

describe('GroupsView subscription daily_reset_mode setting', () => {
  it('shows daily_reset_mode selector only when subscription_type is subscription on create', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    expect(wrapper.find('[data-testid="create-daily-reset-mode"]').exists()).toBe(false)

    await wrapper.get('[data-testid="create-subscription-type"]').setValue('subscription')
    await flushPromises()
    expect(wrapper.find('[data-testid="create-daily-reset-mode"]').exists()).toBe(true)

    await wrapper.get('[data-testid="create-subscription-type"]').setValue('standard')
    await flushPromises()
    expect(wrapper.find('[data-testid="create-daily-reset-mode"]').exists()).toBe(false)
  })

  it.each<DailyResetMode>(['calendar', 'anchored_24h'])('creates subscription group with daily_reset_mode=%s', async (mode) => {
    const wrapper = await render()
    await openCreate(wrapper)
    await wrapper.get('[data-testid="create-subscription-type"]').setValue('subscription')
    await flushPromises()
    await wrapper.get('[data-testid="create-daily-reset-mode"]').setValue(mode)
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.create).toHaveBeenCalledOnce()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      subscription_type: 'subscription',
      daily_reset_mode: mode
    }))
  })

  it('resets daily_reset_mode to calendar when switching back to standard before submit', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await wrapper.get('[data-testid="create-subscription-type"]').setValue('subscription')
    await flushPromises()
    await wrapper.get('[data-testid="create-daily-reset-mode"]').setValue('anchored_24h')
    await wrapper.get('[data-testid="create-subscription-type"]').setValue('standard')
    await flushPromises()
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      subscription_type: 'standard',
      daily_reset_mode: 'calendar'
    }))
  })

  it.each([
    { stored: 'calendar' as const, saved: 'anchored_24h' as const },
    { stored: 'anchored_24h' as const, saved: 'calendar' as const },
    { stored: undefined, saved: 'calendar' as const }
  ])('loads $stored and submits $saved when editing a subscription group', async ({ stored, saved }) => {
    mocks.list.mockResolvedValue({
      items: [group({ daily_reset_mode: stored })],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = await render()
    await openEdit(wrapper)
    const select = wrapper.get('[data-testid="edit-daily-reset-mode"]')
    expect((select.element as HTMLSelectElement).value).toBe(stored ?? 'calendar')
    if ((stored ?? 'calendar') !== saved) {
      await select.setValue(saved)
    }
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(mocks.update).toHaveBeenCalledOnce()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({
      subscription_type: 'subscription',
      daily_reset_mode: saved
    }))
  })

  it('hides daily_reset_mode selector when editing a standard group', async () => {
    mocks.list.mockResolvedValue({
      items: [group({ subscription_type: 'standard', daily_reset_mode: 'calendar' })],
      total: 1, page: 1, page_size: 20, pages: 1
    })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(wrapper.find('[data-testid="edit-daily-reset-mode"]').exists()).toBe(false)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({
      subscription_type: 'standard',
      daily_reset_mode: 'calendar'
    }))
  })

  it('preserves daily_reset_mode returned by duplicate', async () => {
    const source = group({ daily_reset_mode: 'anchored_24h' })
    const copied = group({ id: 43, daily_reset_mode: 'anchored_24h' })
    mocks.list.mockResolvedValueOnce({ items: [source], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.list.mockResolvedValue({ items: [copied], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.duplicate.mockResolvedValue(copied)
    const wrapper = await render()
    await wrapper.get('[data-testid="group-duplicate"]').trigger('click')
    await flushPromises()
    await openEdit(wrapper, 43)
    expect((wrapper.get('[data-testid="edit-daily-reset-mode"]').element as HTMLSelectElement).value).toBe('anchored_24h')
  })

  it('documents daily_reset_mode options in both locales', () => {
    for (const messages of [zh, en]) {
      const sub = messages.groups.subscription
      expect(sub.dailyResetMode).toBeTruthy()
      expect(sub.dailyResetModeHint).toBeTruthy()
      expect(sub.dailyResetCalendar).toBeTruthy()
      expect(sub.dailyResetAnchored24h).toBeTruthy()
    }
  })
})
