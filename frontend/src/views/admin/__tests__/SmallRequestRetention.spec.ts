import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import SmallRequestRetentionPanel from '../SmallRequestRetentionPanel.vue'
import { highlightRetentionJSON } from '../smallRequestRetentionFormat'
import * as api from '@/api/admin/smallRequestRetention'
import type { SmallRecord, SmallDetail } from '@/api/admin/smallRequestRetention'
import { createSmallRetentionDemo } from '../../../../dev/small-retention'

const state = vi.hoisted(() => ({ stepUp: vi.fn(), confirm: vi.fn(), toast: vi.fn() }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['show', 'title'], template: '<section v-if="show"><slot /></section>' } }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ visible: ref(false), run: state.stepUp, onCancel: vi.fn() }) }))
vi.mock('@/composables/useConfirmation', () => ({ confirm: state.confirm }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: state.toast, showError: vi.fn() }) }))
vi.mock('@/api/admin/smallRequestRetention', () => ({ getSmallStats: vi.fn(), listSmallRecords: vi.fn(), getSmallDetail: vi.fn(), clearSmallRecords: vi.fn() }))
const record = {
  id: 1, user_id: 11, api_key_id: 12, model: 'text-test', status: 200, repeat_count: 9,
  last_seen: '2026-11-18T00:00:00Z', expires_at: '2026-11-19T00:00:00Z',
  input_tokens: 50, cache_read_tokens: 100, cache_creation_tokens: 20, output_tokens: 5,
  actual_cost: '0.00000009', omitted_responses: 2, request_ids: ['synthetic-request'],
  data: { path: '/v1/responses', method: 'POST', replay_reasons: ['external_context:previous_response_id'] }
} as SmallRecord
const detail = {
  record, request_headers: { authorization: ['<REPLACE_CREDENTIAL>'], 'x-list': ['one', 'two'] },
  request_body: '{ "key": "business-value", "signature": "business-signature" }',
  curl: 'curl https://example.invalid/v1/responses',
  responses: [
    { fingerprint: 'first', repeat_count: 2, first_seen: record.last_seen, headers: { 'content-type': ['text/event-stream'] }, body: 'data: first\n\n', data: { status: 200, usage: { settlement: 'settled', account_id: 13 } } },
    { fingerprint: 'latest', repeat_count: 3, first_seen: record.last_seen, headers: {}, body: 'data: latest\n\n', data: { status: 500, usage: { settlement: 'failed', account_id: 14 } } },
  ]
} as SmallDetail
beforeEach(() => {
  vi.resetAllMocks()
  state.stepUp.mockImplementation(async (action: () => Promise<unknown>) => action())
  state.confirm.mockResolvedValue(true)
  Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true })
  vi.mocked(api.getSmallStats).mockResolvedValue({ ready: true, records: 1, requests: 9, duplicates: 8, raw_bytes: 900, stored_bytes: 100, stored_limit_bytes: 4294967296, quota_full: false, saved_bytes: 800, oldest: null, newest: null, skipped: { usage_unknown: 2 }, repeat_distribution: { '2-9': 1 } })
  vi.mocked(api.listSmallRecords).mockResolvedValue({ items: [record], has_more: false })
  vi.mocked(api.getSmallDetail).mockResolvedValue(detail)
})
describe('small request retention', () => {
  it('shows deduplicated totals and opens content through 2FA without eager payload reads', async () => {
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    expect(api.getSmallDetail).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('170 / 5')
    expect(wrapper.text()).toContain('admin.requestCapture.small.notReplayable')
    expect(wrapper.text()).toContain('admin.requestCapture.small.skipReasons.usage_unknown')
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    expect(state.stepUp).toHaveBeenCalledTimes(1)
    expect(api.getSmallDetail).toHaveBeenCalledWith(1)
    expect(wrapper.text()).toContain('<REPLACE_CREDENTIAL>')
    expect(wrapper.text()).toContain('external_context:previous_response_id')
    wrapper.unmount()
  })
  it('does not fetch bodies when step-up is refused and displays errors', async () => {
    state.stepUp.mockRejectedValue(new Error('STEP_UP_REQUIRED'))
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    expect(api.getSmallDetail).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('STEP_UP_REQUIRED')
    wrapper.unmount()
  })
  it('preserves request bytes when copying curl and gates clear through 2FA', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-testid="small-copy-curl"]').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenCalledWith(detail.curl)
    const clear = wrapper.findAll('button').find(button => button.text() === 'admin.requestCapture.small.clear')
    expect(clear).toBeDefined(); await clear!.trigger('click'); await flushPromises()
    expect(state.confirm).toHaveBeenCalledTimes(1)
    expect(api.clearSmallRecords).toHaveBeenCalledTimes(1)
    expect(state.stepUp).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
  it('opens the whole row through the same step-up gate and clears pending reads when unmounted', async () => {
    let resolve!: (value: SmallDetail) => void
    vi.mocked(api.getSmallDetail).mockImplementation(() => new Promise(value => { resolve = value }))
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').element.closest('tr')!.click(); await flushPromises()
    expect(state.stepUp).toHaveBeenCalledTimes(1)
    expect(api.getSmallDetail).toHaveBeenCalledWith(1)
    wrapper.unmount()
    resolve(detail); await flushPromises()
    expect(document.querySelector('[data-testid="small-retention-detail"]')).toBeNull()
  })
  it('offers exact status values and keeps the other-code filter numeric', async () => {
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    const status = wrapper.findComponent({ name: 'Select' })
    status.vm.$emit('update:modelValue', '502'); await flushPromises()
    await wrapper.get('[data-testid="small-filters"]').trigger('submit'); await flushPromises()
    expect(api.listSmallRecords).toHaveBeenLastCalledWith({ page: 1, page_size: 20, status: 502 })
    status.vm.$emit('update:modelValue', 'other'); await flushPromises()
    await wrapper.get('input[aria-label="admin.requestCapture.small.otherStatus"]').setValue('418')
    await wrapper.get('[data-testid="small-filters"]').trigger('submit'); await flushPromises()
    expect(api.listSmallRecords).toHaveBeenLastCalledWith({ page: 1, page_size: 20, status: 418 })
    wrapper.unmount()
  })
  it('shows a disabled empty state and delegates enabling to the existing parent save flow', async () => {
    vi.mocked(api.getSmallStats).mockResolvedValue({ ready: true, records: 0, requests: 0, duplicates: 0, raw_bytes: 0, stored_bytes: 0, stored_limit_bytes: 4294967296, quota_full: false, saved_bytes: 0, oldest: null, newest: null, skipped: {}, repeat_distribution: {} })
    vi.mocked(api.listSmallRecords).mockResolvedValue({ items: [], has_more: false })
    const wrapper = mount(SmallRequestRetentionPanel, { props: { configLoaded: true, enabled: false, ready: true } }); await flushPromises()
    expect(wrapper.find('[data-testid="small-retention-disabled-empty"]').exists()).toBe(true)
    await wrapper.findAll('button').find(button => button.text() === 'admin.requestCapture.small.enable')!.trigger('click')
    expect(wrapper.emitted('toggle')).toEqual([[true]])
    await wrapper.setProps({ ready: false })
    expect(wrapper.get<HTMLButtonElement>('[data-testid="small-retention-toggle"]').element.disabled).toBe(true)
    expect(wrapper.text()).toContain('admin.requestCapture.small.notReady')
    wrapper.unmount()
  })
  it('copies body wire text rather than formatted JSON and confirms success', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    const tabs = wrapper.findComponent({ name: 'Tabs' })
    tabs.vm.$emit('update:modelValue', 'request_body'); await flushPromises()
    await wrapper.get('[data-testid="small-copy-body"]').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenLastCalledWith(detail.request_body)
    expect(state.toast).toHaveBeenCalledWith('admin.requestCapture.small.copied')
    tabs.vm.$emit('update:modelValue', 'response_body'); await flushPromises()
    await wrapper.get('[data-testid="small-copy-body"]').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenLastCalledWith(detail.responses[0]!.body)
    wrapper.unmount()
  })
  it('shows the shared body quota without disabling the existing retention switch', async () => {
    vi.mocked(api.getSmallStats).mockResolvedValue({ ready: true, records: 1, requests: 9, duplicates: 8, raw_bytes: 900, stored_bytes: 100, stored_limit_bytes: 4294967296, quota_full: true, saved_bytes: 800, oldest: null, newest: null, skipped: { quota_full: 2 }, repeat_distribution: {} })
    const wrapper = mount(SmallRequestRetentionPanel, { props: { configLoaded: true, enabled: true, ready: true } }); await flushPromises()
    expect(wrapper.find('[data-testid="small-retention-quota-full"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.requestCapture.small.skipReasons.quota_full')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="small-retention-toggle"]').element.disabled).toBe(false)
    wrapper.unmount()
  })
  it('explains expired representative billing without inventing zero usage or delivery values', async () => {
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.requestCapture.small.correlations')!.trigger('click'); await flushPromises()
    expect(wrapper.get('[data-testid="small-first-usage-expired"]').text()).toBe('admin.requestCapture.small.firstUsageExpired')
    expect(wrapper.text()).not.toContain('admin.requestCapture.small.milliseconds')
    expect(wrapper.text()).not.toContain('admin.requestCapture.small.nonStream')
    expect(wrapper.text()).not.toContain('admin.requestCapture.small.firstCharge')
    expect(wrapper.text()).toContain('170 / 5')
    wrapper.unmount()
  })
  it('keeps expired sample bodies readable and switches to a live billing sample', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const expired = { ...detail.responses[0]!, data: { status: 200, partial: false } }
    vi.mocked(api.getSmallDetail).mockResolvedValue({ ...detail, responses: [expired, detail.responses[1]!] })
    const wrapper = mount(SmallRequestRetentionPanel); await flushPromises()
    await wrapper.get('[data-testid="small-detail-1"]').trigger('click'); await flushPromises()
    wrapper.findComponent({ name: 'Tabs' }).vm.$emit('update:modelValue', 'response_body'); await flushPromises()
    expect(wrapper.get('[data-testid="small-sample-usage-expired"]').text()).toBe('admin.requestCapture.small.sampleUsageExpired')
    expect(wrapper.text()).not.toContain('admin.requestCapture.small.sampleCharge')
    expect(wrapper.text()).toContain('data: first')
    await wrapper.get('[data-testid="small-copy-body"]').trigger('click'); await flushPromises()
    expect(writeText).toHaveBeenLastCalledWith(expired.body)
    const selector = wrapper.findAllComponents({ name: 'Select' }).at(-1)!
    selector.vm.$emit('update:modelValue', 1); await flushPromises()
    expect(wrapper.find('[data-testid="small-sample-usage-expired"]').exists()).toBe(false)
    await wrapper.findAll('button').find(button => button.text() === 'admin.requestCapture.small.sampleUsage')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('admin.requestCapture.small.sampleCharge')
    expect(wrapper.text()).toContain('#14')
    expect(wrapper.text()).toContain('data: latest')
    wrapper.unmount()
  })
  it('previews rolling group expiry and both expired representative associations', () => {
    const preview = createSmallRetentionDemo(new Date('2026-11-18T10:00:00Z'))
    const result = preview('GET', '/api/v1/admin/request-captures/small/1', new URLSearchParams(), {}) as SmallDetail
    expect(new Date(result.record.expires_at).getTime() - new Date(result.record.last_seen).getTime()).toBe(24 * 60 * 60 * 1000)
    expect(result.record.data.first_usage).toBeUndefined()
    expect(result.responses[0]!.data.usage).toBeUndefined()
    expect(result.responses[1]!.data.usage).toBeDefined()
  })
  it('never renders retained HTML as executable markup in JSON display', () => {
    const highlighted = highlightRetentionJSON('{"key":"<img src=x onerror=alert(1)>","signature":"unchanged"}')
    expect(highlighted).not.toContain('<img')
    expect(highlighted).toContain('&lt;img')
    expect(highlighted).toContain('signature')
    expect(highlightRetentionJSON('data: raw SSE\n\n')).toBe('')
  })
})
