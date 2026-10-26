import { defineComponent, h, type PropType } from 'vue'
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
  useI18n: () => ({ t: (key: string) => key })
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
  await wrapper.get('#create-group-form input[type="text"]').setValue('New group')
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

describe('GroupsView requested response model setting', () => {
  it.each([false, true])('creates a group with explicit return_requested_model=%s', async (enabled) => {
    const wrapper = await render()
    await openCreate(wrapper)
    const toggle = wrapper.get('[data-testid="create-return-requested-model"]')
    expect(toggle.attributes('role')).toBe('switch')
    expect(toggle.attributes('aria-checked')).toBe('false')
    if (enabled) await toggle.trigger('click')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledOnce()
    expect(mocks.create.mock.calls[0][0]).toHaveProperty('return_requested_model', enabled)
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('keeps the create setting available and selected across every platform, including composite', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await wrapper.get('[data-testid="create-return-requested-model"]').trigger('click')
    for (const { value } of GROUP_PLATFORM_OPTIONS) {
      await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(value)
      await flushPromises()
      expect(wrapper.get('[data-testid="create-return-requested-model"]').attributes('aria-checked'), value).toBe('true')
    }
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ platform: 'composite', return_requested_model: true }))
  })

  it('resets an enabled create draft when the dialog is reopened', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await wrapper.get('[data-testid="create-return-requested-model"]').trigger('click')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await openCreate(wrapper)
    expect(wrapper.get('[data-testid="create-return-requested-model"]').attributes('aria-checked')).toBe('false')
  })

  it.each([
    { stored: true, saved: true }, { stored: true, saved: false },
    { stored: false, saved: true }, { stored: false, saved: false },
    { stored: undefined, saved: false }
  ])('loads $stored and submits explicit $saved when editing', async ({ stored, saved }) => {
    mocks.list.mockResolvedValue({ items: [group(stored === undefined ? {} : { return_requested_model: stored })], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openEdit(wrapper)
    const toggle = wrapper.get('[data-testid="edit-return-requested-model"]')
    expect(toggle.attributes('aria-checked')).toBe(String(stored ?? false))
    if ((stored ?? false) !== saved) await toggle.trigger('click')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledOnce()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ return_requested_model: saved }))
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it.each(GROUP_PLATFORM_OPTIONS.map(({ value }) => value))('preserves the setting while editing a %s group', async (platform: GroupPlatform) => {
    mocks.list.mockResolvedValue({ items: [group({ platform, return_requested_model: true })], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(wrapper.get('[data-testid="edit-return-requested-model"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ platform, return_requested_model: true }))
  })

  it('preserves the enabled setting returned by the backend duplicate operation', async () => {
    const source = group({ return_requested_model: true })
    const copied = group({ id: 43, return_requested_model: true })
    mocks.list.mockResolvedValueOnce({ items: [source], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.list.mockResolvedValue({ items: [copied], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.duplicate.mockResolvedValue(copied)
    const wrapper = await render()
    await wrapper.get('[data-testid="group-duplicate"]').trigger('click')
    await flushPromises()
    expect(mocks.duplicate).toHaveBeenCalledWith(42)
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.update).not.toHaveBeenCalled()
    await openEdit(wrapper, 43)
    expect(wrapper.get('[data-testid="edit-return-requested-model"]').attributes('aria-checked')).toBe('true')
  })

  it('describes JSON, streaming, Realtime, and unchanged content in both locales', () => {
    expect(zh.groups.returnRequestedModel.label).toBe('返回请求中的模型名')
    expect(zh.groups.returnRequestedModel.hint).toContain('即使未配置模型映射')
    expect(zh.groups.returnRequestedModel.hint).toContain('不修改正文内容')
    for (const messages of [zh, en]) {
      expect(messages.groups.returnRequestedModel.hint).toContain('JSON')
      expect(messages.groups.returnRequestedModel.hint).toContain('Realtime')
    }
  })
})
