import { defineComponent, h, ref, type PropType } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdminGroup } from '@/types'
import { GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'
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
// key + 参数的 JSON：既能按 key 找文案，也能断言预览里的数字。
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key}${JSON.stringify(params)}` : key),
    locale: ref('en')
  })
}))

enableAutoUnmount(afterEach)

const K = 'admin.groups.openaiFast.tierMultiplier'

function group(patch: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 42, name: 'Primary', description: null, platform: 'openai', rate_multiplier: 0.16,
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
    created_at: '2026-11-25T00:00:00Z', updated_at: '2026-11-25T00:00:00Z', ...patch
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

async function openCreate(wrapper: Wrapper, platform = 'openai') {
  await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
  await flushPromises()
  await wrapper.get('#create-group-form input[type="text"]').setValue('New group')
  await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
  await flushPromises()
}

async function openEdit(wrapper: Wrapper, id = 42) {
  const edit = wrapper.get(`[data-row="${id}"]`).findAll('button').find(button => button.text() === 'common.edit')!
  await edit.trigger('click')
  await flushPromises()
}

async function submit(wrapper: Wrapper, form: 'create' | 'edit') {
  await wrapper.get(`#${form}-group-form`).trigger('submit')
  await flushPromises()
}

const input = (wrapper: Wrapper, form: 'create' | 'edit', tier: 'fast' | 'ultrafast') =>
  wrapper.get(`input[data-testid="${form}-openai-${tier}-multiplier"]`)
const hasInput = (wrapper: Wrapper, form: 'create' | 'edit', tier: 'fast' | 'ultrafast') =>
  wrapper.find(`input[data-testid="${form}-openai-${tier}-multiplier"]`).exists()
const value = (field: ReturnType<typeof input>) => (field.element as HTMLInputElement).value
const preview = (wrapper: Wrapper, form: 'create' | 'edit', tier: 'fast' | 'ultrafast') =>
  wrapper.find(`[data-testid="${form}-openai-${tier}-multiplier-preview"]`)

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

describe('GroupsView Fast / UltraFast tier multipliers — create', () => {
  it('shows both inputs only on OpenAI and composite groups', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    for (const { value: platform } of GROUP_PLATFORM_OPTIONS) {
      await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue(platform)
      await flushPromises()
      const expected = platform === 'openai' || platform === 'composite'
      expect(hasInput(wrapper, 'create', 'fast'), platform).toBe(expected)
      expect(hasInput(wrapper, 'create', 'ultrafast'), platform).toBe(expected)
    }
  })

  it('renders the inputs under the Fast toggles with the official default as placeholder', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    expect(input(wrapper, 'create', 'fast').attributes('placeholder')).toBe(`${K}.placeholder{"value":2}`)
    expect(input(wrapper, 'create', 'ultrafast').attributes('placeholder')).toBe(`${K}.placeholder{"value":6}`)
    expect(input(wrapper, 'create', 'fast').attributes('type')).toBe('number')
  })

  it('sends 0 (clear to the official default) for blank inputs', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await submit(wrapper, 'create')
    expect(mocks.create).toHaveBeenCalledOnce()
    const body = mocks.create.mock.calls[0][0]
    expect(body.openai_fast_multiplier).toBe(0)
    expect(body.openai_ultrafast_multiplier).toBe(0)
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('sends the numbers that were typed or picked with a chip', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', 'fast').setValue('2.5')
    await wrapper.get('[data-testid="create-openai-ultrafast-multiplier-free"]').trigger('click')
    expect(value(input(wrapper, 'create', 'ultrafast'))).toBe('1')
    await submit(wrapper, 'create')
    const body = mocks.create.mock.calls[0][0]
    expect(body.openai_fast_multiplier).toBe(2.5)
    expect(body.openai_ultrafast_multiplier).toBe(1)
    expect(typeof body.openai_fast_multiplier).toBe('number')
  })

  it('previews against the group rate typed in the same form', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await wrapper.get('#create-group-form input[data-tour="group-form-multiplier"]').setValue('0.5')
    await input(wrapper, 'create', 'fast').setValue('3')
    expect(preview(wrapper, 'create', 'fast').text()).toBe(
      `${K}.previewValue{"name":"Fast","rate":"0.5","multiplier":"3","total":"1.5"}`,
    )
    await input(wrapper, 'create', 'fast').setValue('')
    expect(preview(wrapper, 'create', 'fast').text()).toBe(`${K}.previewOfficial{"name":"Fast","rate":"0.5"}`)
  })

  it('composite groups send the fields too', async () => {
    const wrapper = await render()
    await openCreate(wrapper, 'composite')
    await input(wrapper, 'create', 'ultrafast').setValue('4')
    await submit(wrapper, 'create')
    expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({
      platform: 'composite', openai_fast_multiplier: 0, openai_ultrafast_multiplier: 4,
    }))
  })

  it('omits both fields on a platform without Fast support, even with a stale value', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', 'fast').setValue('3')
    await input(wrapper, 'create', 'ultrafast').setValue('500')
    await wrapper.get('#create-group-form [data-tour="group-form-platform"]').setValue('anthropic')
    await flushPromises()
    await submit(wrapper, 'create')
    // 隐藏起来的非法旧值也不能挡住保存
    expect(mocks.showError).not.toHaveBeenCalled()
    const body = mocks.create.mock.calls[0][0]
    expect(body.platform).toBe('anthropic')
    expect(body.openai_fast_multiplier).toBeUndefined()
    expect(body.openai_ultrafast_multiplier).toBeUndefined()
  })

  it.each([
    ['fast', '0'],
    ['fast', '-1'],
    ['fast', '100.5'],
    ['ultrafast', '1.23456'],
    ['ultrafast', '101'],
  ] as const)('blocks submit when %s is %s', async (tier, bad) => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', tier).setValue(bad)
    expect(input(wrapper, 'create', tier).attributes('aria-invalid')).toBe('true')
    await submit(wrapper, 'create')
    expect(mocks.create).not.toHaveBeenCalled()
    expect(mocks.showError).toHaveBeenCalledOnce()
    expect(mocks.showError.mock.calls[0][0]).toContain(`${K}.invalid`)

    // 改对了就能提交
    await input(wrapper, 'create', tier).setValue('2')
    await submit(wrapper, 'create')
    expect(mocks.create).toHaveBeenCalledOnce()
  })

  it('locks Fast when free Fast is on: Fast is not sent, UltraFast still is', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', 'fast').setValue('3')
    await input(wrapper, 'create', 'ultrafast').setValue('6')
    await wrapper.get('[data-testid="create-free-openai-fast"]').trigger('click')

    expect(input(wrapper, 'create', 'fast').attributes('disabled')).toBeDefined()
    expect(value(input(wrapper, 'create', 'fast'))).toBe(`${K}.lockedFree`)
    expect(input(wrapper, 'create', 'ultrafast').attributes('disabled')).toBeUndefined()

    await submit(wrapper, 'create')
    const body = mocks.create.mock.calls[0][0]
    expect(body.free_openai_fast).toBe(true)
    expect(body.openai_fast_multiplier).toBeUndefined()
    expect(body.openai_ultrafast_multiplier).toBe(6)
  })

  it('does not let a hidden invalid Fast value block saving while free Fast is on', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', 'fast').setValue('500')
    await wrapper.get('[data-testid="create-free-openai-fast"]').trigger('click')
    await submit(wrapper, 'create')
    expect(mocks.create).toHaveBeenCalledOnce()
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('resets a discarded draft when the dialog is reopened', async () => {
    const wrapper = await render()
    await openCreate(wrapper)
    await input(wrapper, 'create', 'fast').setValue('3')
    await input(wrapper, 'create', 'ultrafast').setValue('6')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await openCreate(wrapper)
    expect(value(input(wrapper, 'create', 'fast'))).toBe('')
    expect(value(input(wrapper, 'create', 'ultrafast'))).toBe('')
  })
})

describe('GroupsView Fast / UltraFast tier multipliers — edit', () => {
  const listWith = (patch: Partial<AdminGroup>) =>
    mocks.list.mockResolvedValue({ items: [group(patch)], total: 1, page: 1, page_size: 20, pages: 1 })

  it('loads stored values, shows null as blank, and submits them back unchanged', async () => {
    listWith({ openai_fast_multiplier: 2.5, openai_ultrafast_multiplier: null })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(value(input(wrapper, 'edit', 'fast'))).toBe('2.5')
    expect(value(input(wrapper, 'edit', 'ultrafast'))).toBe('')
    await submit(wrapper, 'edit')
    expect(mocks.update).toHaveBeenCalledOnce()
    const [id, body] = mocks.update.mock.calls[0]
    expect(id).toBe(42)
    expect(body.openai_fast_multiplier).toBe(2.5)
    expect(body.openai_ultrafast_multiplier).toBe(0)
  })

  it('treats a backend that does not send the fields yet as official default', async () => {
    const wrapper = await render()
    await openEdit(wrapper)
    expect(value(input(wrapper, 'edit', 'fast'))).toBe('')
    expect(value(input(wrapper, 'edit', 'ultrafast'))).toBe('')
    await submit(wrapper, 'edit')
    expect(mocks.update.mock.calls[0][1]).toMatchObject({ openai_fast_multiplier: 0, openai_ultrafast_multiplier: 0 })
  })

  it('emptying a stored value sends 0 to clear it; "free 1x" sends 1', async () => {
    listWith({ openai_fast_multiplier: 2.5, openai_ultrafast_multiplier: 6 })
    const wrapper = await render()
    await openEdit(wrapper)
    await input(wrapper, 'edit', 'fast').setValue('')
    await wrapper.get('[data-testid="edit-openai-ultrafast-multiplier-free"]').trigger('click')
    await submit(wrapper, 'edit')
    expect(mocks.update).toHaveBeenCalledWith(42, expect.objectContaining({
      openai_fast_multiplier: 0, openai_ultrafast_multiplier: 1,
    }))
  })

  it('previews against the stored group rate', async () => {
    listWith({ rate_multiplier: 0.16, openai_fast_multiplier: 2 })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(preview(wrapper, 'edit', 'fast').text()).toBe(
      `${K}.previewValue{"name":"Fast","rate":"0.16","multiplier":"2","total":"0.32"}`,
    )
  })

  it('blocks an out-of-range value on edit', async () => {
    const wrapper = await render()
    await openEdit(wrapper)
    await input(wrapper, 'edit', 'ultrafast').setValue('1000')
    await submit(wrapper, 'edit')
    expect(mocks.update).not.toHaveBeenCalled()
    expect(mocks.showError.mock.calls[0][0]).toContain(`${K}.invalid`)
  })

  it('keeps the stored Fast value (omits it) while free Fast is on', async () => {
    listWith({ free_openai_fast: true, openai_fast_multiplier: 3, openai_ultrafast_multiplier: 6 })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(input(wrapper, 'edit', 'fast').attributes('disabled')).toBeDefined()
    expect(value(input(wrapper, 'edit', 'fast'))).toBe(`${K}.lockedFree`)
    expect(value(input(wrapper, 'edit', 'ultrafast'))).toBe('6')
    await submit(wrapper, 'edit')
    const body = mocks.update.mock.calls[0][1]
    expect(body.free_openai_fast).toBe(true)
    expect(body.openai_fast_multiplier).toBeUndefined()
    expect(body.openai_ultrafast_multiplier).toBe(6)
  })

  it('does not show or send the fields for a non-OpenAI group', async () => {
    listWith({ platform: 'anthropic' })
    const wrapper = await render()
    await openEdit(wrapper)
    expect(hasInput(wrapper, 'edit', 'fast')).toBe(false)
    expect(hasInput(wrapper, 'edit', 'ultrafast')).toBe(false)
    await submit(wrapper, 'edit')
    const body = mocks.update.mock.calls[0][1]
    expect(body.openai_fast_multiplier).toBeUndefined()
    expect(body.openai_ultrafast_multiplier).toBeUndefined()
  })

  it('drops unsaved input when the edit dialog is closed and reopened', async () => {
    listWith({ openai_fast_multiplier: 2.5, openai_ultrafast_multiplier: 6 })
    const wrapper = await render()
    await openEdit(wrapper)
    await input(wrapper, 'edit', 'fast').setValue('9')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    await flushPromises()
    await openEdit(wrapper)
    expect(value(input(wrapper, 'edit', 'fast'))).toBe('2.5')
    expect(value(input(wrapper, 'edit', 'ultrafast'))).toBe('6')
  })
})
