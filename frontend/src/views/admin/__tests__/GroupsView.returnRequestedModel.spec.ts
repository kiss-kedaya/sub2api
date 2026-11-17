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
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') })
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

const toolPriceFields = ['ocr-price', 'deepseek-tts-price', 'deepseek-asr-price']
const toolPricePayload = { ocr_price_per_1k_pages: 0.45, audio_tts_price_per_million_chars: 45, audio_stt_price_per_hour: 0.117 }

async function selectCreatePlatform(wrapper: Awaited<ReturnType<typeof render>>, platform: GroupPlatform) {
  await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
  await flushPromises()
}

// UInput keeps data-testid on its root; address the native input for actual v-model events.
function toolInput(wrapper: Awaited<ReturnType<typeof render>>, id: string) {
  const field = wrapper.get(`[data-testid="${id}"]`)
  return field.element.tagName === 'INPUT' ? field : field.get('input')
}

describe('GroupsView DeepSeek fixed tool pricing', () => {
  it('shows all three prices only for DeepSeek, without changing Grok voice controls', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    for (const { value: platform } of GROUP_PLATFORM_OPTIONS) {
      await selectCreatePlatform(wrapper, platform)
      for (const field of toolPriceFields) {
        expect(wrapper.find(`[data-testid="create-${field}"]`).exists(), `${platform}:${field}`).toBe(platform === 'deepseek')
      }
      expect(wrapper.find('[data-testid="create-audio-tts-price"]').exists(), platform).toBe(platform === 'grok')
      expect(wrapper.find('[data-testid="create-audio-stt-price"]').exists(), platform).toBe(platform === 'grok')
    }
  })

  it.each([null, 0, 'custom'] as const)('creates with %s prices, preserving blank defaults and explicit free', async (setting) => {
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'deepseek')
    const values = setting === 'custom' ? Object.values(toolPricePayload) : [setting, setting, setting]
    for (const [index, field] of toolPriceFields.entries()) {
      const input = toolInput(wrapper, `create-${field}`)
      expect(input.attributes('min')).toBe('0')
      await input.setValue('1')
      await input.setValue(values[index] === null ? '' : String(values[index]))
    }
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining(Object.fromEntries(Object.keys(toolPricePayload).map((key, index) => [key, values[index]]))))
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('resets a discarded tool pricing draft when reopening create', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'deepseek')
    for (const field of toolPriceFields) await toolInput(wrapper, `create-${field}`).setValue('1')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'deepseek')
    for (const field of toolPriceFields) expect((toolInput(wrapper, `create-${field}`).element as HTMLInputElement).value).toBe('')
  })

  it.each([null, 0, 'custom'] as const)('loads saved %s prices and submits empty edits as -1 (clear)', async (setting) => {
    const prices = setting === 'custom' ? toolPricePayload : { ocr_price_per_1k_pages: setting, audio_tts_price_per_million_chars: setting, audio_stt_price_per_hour: setting }
    mocks.list.mockResolvedValue({ items: [group({ platform: 'deepseek', ...prices })], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.update.mockImplementation(async (_id: number, payload: Partial<AdminGroup>) => group(payload))
    const wrapper = await render()
    await openEdit(wrapper)
    for (const [index, field] of toolPriceFields.entries()) {
      expect((toolInput(wrapper, `edit-${field}`).element as HTMLInputElement).value).toBe(String(Object.values(prices)[index] ?? ''))
    }
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    // Same convention as every other group price: an empty field is sent as -1,
    // which the backend normalizes to NULL (default price). 0 stays "free".
    const submitted = Object.fromEntries(Object.entries(prices).map(([key, value]) => [key, value ?? -1]))
    expect(mocks.update).toHaveBeenLastCalledWith(42, expect.objectContaining(submitted))

    await openEdit(wrapper)
    for (const field of toolPriceFields) {
      await toolInput(wrapper, `edit-${field}`).setValue('1')
      await toolInput(wrapper, `edit-${field}`).setValue('')
    }
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith(42, expect.objectContaining({ ocr_price_per_1k_pages: -1, audio_tts_price_per_million_chars: -1, audio_stt_price_per_hour: -1 }))
  })

  it.each(toolPriceFields)('rejects a negative %s on create and edit', async (field) => {
    mocks.list.mockResolvedValue({ items: [group({ platform: 'deepseek' })], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openCreate(wrapper)
    await selectCreatePlatform(wrapper, 'deepseek')
    await toolInput(wrapper, `create-${field}`).setValue('-0.1')
    await wrapper.get('#create-group-form').trigger('submit')
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledWith('admin.groups.explicitPricing.invalidDeepseekToolPrice')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await openEdit(wrapper)
    await toolInput(wrapper, `edit-${field}`).setValue('-0.1')
    await wrapper.get('#edit-group-form').trigger('submit')
    expect(mocks.update).not.toHaveBeenCalled()
  })

  it('reopens prices returned by the server-side duplicate operation', async () => {
    mocks.list.mockResolvedValueOnce({ items: [group({ platform: 'deepseek', ...toolPricePayload })], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.list.mockResolvedValue({ items: [group({ id: 43, platform: 'deepseek', ...toolPricePayload })], total: 1, page: 1, page_size: 20, pages: 1 })
    mocks.duplicate.mockResolvedValue(group({ id: 43, platform: 'deepseek', ...toolPricePayload }))
    const wrapper = await render()
    await wrapper.get('[data-testid="group-duplicate"]').trigger('click')
    await flushPromises()
    expect(mocks.duplicate).toHaveBeenCalledWith(42)
    await openEdit(wrapper, 43)
    for (const [index, field] of toolPriceFields.entries()) expect((toolInput(wrapper, `edit-${field}`).element as HTMLInputElement).value).toBe(String(Object.values(toolPricePayload)[index]))
  })

  it('keeps Grok editing controls and its existing clear-price payload unchanged', async () => {
    mocks.list.mockResolvedValue({ items: [group({ platform: 'grok', audio_tts_price_per_million_chars: 15, audio_stt_price_per_hour: 0.10 })], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(wrapper.find('[data-testid="edit-ocr-price"]').exists()).toBe(false)
    for (const [field, value] of [['tts', '15'], ['stt', '0.1']]) {
      const input = toolInput(wrapper, `edit-audio-${field}-price`)
      expect((input.element as HTMLInputElement).value).toBe(value)
      expect(input.attributes('placeholder')).toBe('admin.groups.voicePricing.pricePlaceholder')
      await input.setValue('')
    }
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({ audio_tts_price_per_million_chars: -1, audio_stt_price_per_hour: -1 }))
  })

  it('states the official list-price defaults and the zero semantics in both locales', () => {
    for (const messages of [zh, en]) {
      const text = messages.groups.explicitPricing
      expect(text.ocrPricePlaceholder).toContain('1.5')
      expect(text.deepseekTtsPlaceholder).toContain('300')
      expect(text.deepseekAsrPlaceholder).toContain('0.78')
      expect(text.deepseekToolsHint).toContain('0')
      // This group is priced in the same unit as its search price, not in USD.
      for (const label of [text.ocrPricePer1kPages, text.deepseekTtsPerMillionChars, text.deepseekAsrPerHour]) expect(label).not.toContain('USD')
    }
  })
})
