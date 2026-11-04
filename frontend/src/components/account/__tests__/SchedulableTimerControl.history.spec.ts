import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import SchedulableTimerControl from '../SchedulableTimerControl.vue'

const { api, toast, SelectStub } = vi.hoisted(() => ({
  api: { list: vi.fn(), preview: vi.fn(), save: vi.fn(), cancel: vi.fn() },
  toast: { showSuccess: vi.fn(), showError: vi.fn() },
  SelectStub: { name: 'AppSelectStub', props: ['modelValue', 'options'], emits: ['update:modelValue'], template: '<button type="button" v-bind="$attrs" />' }
}))
vi.mock('@/api/admin/schedulableTimers', () => ({ schedulableTimersAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => toast }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['show'], template: '<div v-if="show"><slot /></div>' } }))
vi.mock('@/components/common/Select.vue', () => ({ default: SelectStub }))

type Audit = { action: string; version: number; actor_id: number; execute_at: string; timezone: string; actual_at: string; result: string; reason: string }
const audit = (over: Partial<Audit> = {}): Audit => ({
  action: 'recurring_open', version: 1, actor_id: 0, execute_at: '2026-10-21T10:00:00Z', timezone: 'Asia/Shanghai',
  actual_at: '2026-10-21T10:00:00Z', result: 'executed', reason: '', ...over
})
const snapshot = (history: Audit[]) => ({ tasks: [], history, server_now: '2026-10-22T00:00:00Z', poll_seconds: 30, open_grace_seconds: 900, max_delay_minutes: 527040 })

let wrapper: VueWrapper
async function openHistory(history: Audit[]) {
  api.list.mockResolvedValue(snapshot(history))
  wrapper = mount(SchedulableTimerControl, { props: { accountId: 7 } })
  await flushPromises()
  await wrapper.get('[data-test="timer-history-toggle"]').trigger('click')
  await flushPromises()
}
const rows = () => wrapper.findAll('[data-test="timer-history-row"]')

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => wrapper?.unmount())

describe('compact schedule history', () => {
  it('renders one short row per record and names the shared time zone only once', async () => {
    await openHistory([
      audit({ execute_at: '2026-10-21T10:00:00Z', actual_at: '2026-10-21T10:00:00Z' }),
      audit({ action: 'recurring_close', execute_at: '2026-10-21T06:00:00Z', actual_at: '2026-10-21T06:00:00Z' }),
      audit({ execute_at: '2026-10-20T10:00:00Z', actual_at: '2026-10-20T10:00:00Z' })
    ])
    expect(rows()).toHaveLength(3)
    expect(wrapper.get('[data-test="timer-history"]').text().split('Asia/Shanghai').length - 1).toBe(1)
    expect(rows()[0].text()).toContain('10-21 18:00')
    expect(rows()[0].text()).toContain('timer.recurring_open')
    expect(rows()[0].text()).toContain('timer.results.executed')
    expect(rows()[1].text()).toContain('10-21 14:00')
    expect(wrapper.text()).not.toContain('timer.actual')
  })

  it('shows only the latest six until asked for the rest', async () => {
    const many = Array.from({ length: 9 }, (_, i) => audit({ execute_at: `2026-10-${20 - i}T10:00:00Z`, actual_at: `2026-10-${20 - i}T10:00:00Z` }))
    await openHistory(many)
    expect(rows()).toHaveLength(6)
    await wrapper.get('[data-test="timer-history-more"]').trigger('click')
    expect(rows()).toHaveLength(9)
    await wrapper.get('[data-test="timer-history-more"]').trigger('click')
    expect(rows()).toHaveLength(6)
  })

  it('offers no show-all control when everything already fits', async () => {
    await openHistory([audit(), audit({ execute_at: '2026-10-20T10:00:00Z', actual_at: '2026-10-20T10:00:00Z' })])
    expect(wrapper.find('[data-test="timer-history-more"]').exists()).toBe(false)
  })

  it('stays quiet for an on-time run and notes a late one', async () => {
    await openHistory([
      audit({ execute_at: '2026-10-21T10:00:00Z', actual_at: '2026-10-21T10:01:00Z' }),
      audit({ execute_at: '2026-10-20T10:00:00Z', actual_at: '2026-10-20T10:07:00Z' })
    ])
    expect(rows()[0].find('p').exists()).toBe(false)
    expect(rows()[1].text()).toContain('timer.lateBy')
  })

  it('explains a skipped or failed run with its reason, coloured as a problem', async () => {
    await openHistory([
      audit({ result: 'failed', reason: 'execution_error' }),
      audit({ result: 'skipped', reason: 'manual_pause', execute_at: '2026-10-20T10:00:00Z', actual_at: '2026-10-20T10:00:00Z' })
    ])
    expect(rows()[0].text()).toContain('timer.errors.execution_error')
    expect(rows()[0].find('p').classes()).toContain('text-error')
    expect(rows()[1].text()).toContain('timer.errors.manual_pause')
    expect(rows()[1].find('p').classes()).toContain('text-warning')
  })

  it('times a plan change by when it was made and notes the slot it concerns', async () => {
    await openHistory([
      audit({ result: 'scheduled', execute_at: '2026-11-01T01:00:00Z', actual_at: '2026-10-21T10:00:00Z' })
    ])
    expect(rows()[0].text()).toContain('10-21 18:00')
    expect(rows()[0].text()).toContain('timer.plannedAt')
    expect(rows()[0].text()).toContain('timer.results.scheduled')
  })

  it('keeps a zone on each row when the records span several zones', async () => {
    await openHistory([
      audit({ timezone: 'Asia/Shanghai' }),
      audit({ timezone: 'UTC', execute_at: '2026-10-20T10:00:00Z', actual_at: '2026-10-20T10:00:00Z' })
    ])
    const text = wrapper.get('[data-test="timer-history"]').text()
    expect(text).toContain('10-21 18:00')
    expect(text).toContain('10-20 10:00')
    expect(text.split('Asia/Shanghai').length - 1).toBeLessThanOrEqual(1)
  })
})
