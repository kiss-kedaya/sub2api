import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import TicketsView from '../TicketsView.vue'
import type { TicketDetail } from '@/api/tickets'
import { formatCurrency } from '@/utils/format'

const mocks = vi.hoisted(() => ({
  list: vi.fn(), stats: vi.fn(), detail: vi.fn(), read: vi.fn(),
  reply: vi.fn(), update: vi.fn(), create: vi.fn(),
  showError: vi.fn(), showSuccess: vi.fn(),
}))

vi.mock('@/api/tickets', async importOriginal => ({
  ...await importOriginal<typeof import('@/api/tickets')>(),
  ticketsAPI: () => mocks,
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))

vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: ref('en') }),
}))

type Requester = NonNullable<TicketDetail['requester']>
let wrapper: VueWrapper | undefined

function detailFixture(requester: Partial<Requester> = {}): TicketDetail {
  const timestamp = '2026-09-24T10:00:00Z'
  return {
    ticket: {
      id: 1, user_id: 9, subject: 'Statistics test', contact: 'contact.example.test',
      category: 'billing', priority: 'normal', status: 'open', assignee_id: null,
      assignee_name: '', user_name: 'Test', created_at: timestamp, updated_at: timestamp,
      last_message_at: timestamp, last_message_preview: '', last_message_id: 0, unread_count: 0,
    },
    messages: [],
    has_more: false,
    requester: {
      id: 9, username: 'Test', email: 'test@example.test', balance: 12.5,
      status: 'active', created_at: timestamp,
      today_tokens: 0, today_cost: 0, recharged_14d: 0,
      ...requester,
    },
  }
}

async function mountDetail(detail: TicketDetail, admin = true) {
  mocks.list.mockResolvedValue({ items: [detail.ticket], total: 1, page: 1, page_size: 20 })
  mocks.detail.mockResolvedValue(detail)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:scope(admin/)?tickets/:id?', component: { template: '<div />' } }],
  })
  await router.push(admin ? '/admin/tickets/1' : '/tickets/1')
  await router.isReady()
  wrapper = shallowMount(TicketsView, {
    props: { admin },
    global: {
      plugins: [router],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TicketTimeline: {
          template: '<div />',
          methods: { scrollToBottom: vi.fn() },
        },
      },
    },
  })
  await flushPromises()
  expect(wrapper.get('.ticket-detail-title h2').text()).toBe('Statistics test')
  return wrapper
}

function expectStats(values: string[], available: boolean[]) {
  const stats = wrapper!.get('.ticket-requester-stats')
  expect(stats.findAll('dt').map(label => label.text())).toEqual([
    'tickets.todayTokens', 'tickets.todayCost', 'tickets.recharged14d',
  ])
  const cells = stats.findAll('dd')
  expect(cells.map(cell => cell.text())).toEqual(values)
  expect(cells.map(cell => cell.attributes('title'))).toEqual(
    available.map(value => value ? undefined : 'tickets.statsUnavailable'),
  )
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  mocks.stats.mockResolvedValue({
    total: 1, open: 1, in_progress: 0, waiting_user: 0, resolved: 0, closed: 0, unread: 0,
  })
  mocks.read.mockResolvedValue(undefined)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.useRealTimers()
})

describe('TicketsView requester statistics availability', () => {
  it.each([
    [true, true], [false, false], [false, true], [true, false],
  ])('distinguishes real zero from unavailable stats (usage=%s, recharge=%s)', async (usage, recharge) => {
    await mountDetail(detailFixture({ usage_stats_available: usage, recharge_stats_available: recharge }))
    expectStats([
      usage ? '0' : '—',
      usage ? formatCurrency(0) : '—',
      recharge ? formatCurrency(0) : '—',
    ], [usage, usage, recharge])
    expect(wrapper!.get('.ticket-requester-info').text()).toContain(formatCurrency(12.5))
  })

  it('shows unavailable instead of trusting legacy numeric values without flags', async () => {
    const detail = detailFixture({ today_tokens: 2500, today_cost: 1.25, recharged_14d: 42 })
    expect(detail.requester).not.toHaveProperty('usage_stats_available')
    expect(detail.requester).not.toHaveProperty('recharge_stats_available')
    await mountDetail(detail)
    expectStats(['—', '—', '—'], [false, false, false])
  })

  it('shows unavailable when an older backend omits both flags and numeric fields', async () => {
    const detail = detailFixture()
    delete detail.requester!.today_tokens
    delete detail.requester!.today_cost
    delete detail.requester!.recharged_14d
    await mountDetail(detail)
    expectStats(['—', '—', '—'], [false, false, false])
  })

  it.each(['usage', 'recharge'])('handles a missing %s availability flag independently', async missing => {
    await mountDetail(detailFixture(missing === 'usage'
      ? { recharge_stats_available: true }
      : { usage_stats_available: true }))
    const usage = missing !== 'usage'
    const recharge = missing !== 'recharge'
    expectStats([usage ? '0' : '—', usage ? formatCurrency(0) : '—', recharge ? formatCurrency(0) : '—'],
      [usage, usage, recharge])
  })

  it('preserves successful nonzero statistics formatting', async () => {
    await mountDetail(detailFixture({
      usage_stats_available: true, recharge_stats_available: true,
      today_tokens: 2500, today_cost: 1.25, recharged_14d: 42,
    }))
    expectStats(['2.5K', formatCurrency(1.25), formatCurrency(42)], [true, true, true])
  })

  it('does not display stale numeric values when both statistics queries failed', async () => {
    await mountDetail(detailFixture({
      usage_stats_available: false, recharge_stats_available: false,
      today_tokens: 2500, today_cost: 1.25, recharged_14d: 42,
    }))
    expectStats(['—', '—', '—'], [false, false, false])
  })

  it.each([true, false, undefined])('hides requester statistics from ordinary users (availability=%s)', async available => {
    await mountDetail(detailFixture({
      usage_stats_available: available, recharge_stats_available: available,
    }), false)
    expect(wrapper!.find('.ticket-requester-stats').exists()).toBe(false)
    expect(wrapper!.find('.ticket-requester-info').exists()).toBe(false)
    expect(wrapper!.text()).not.toContain('tickets.todayTokens')
    expect(wrapper!.text()).not.toContain('tickets.todayCost')
    expect(wrapper!.text()).not.toContain('tickets.recharged14d')
    expect(wrapper!.find('[title="tickets.statsUnavailable"]').exists()).toBe(false)
    expect(wrapper!.get('.ticket-user-contact').text()).toContain('contact.example.test')
  })

  it('keeps the detail usable when the requester is absent', async () => {
    const detail = detailFixture()
    delete detail.requester
    await mountDetail(detail)
    expect(wrapper!.find('.ticket-requester-stats').exists()).toBe(false)
    expect(wrapper!.find('.ticket-requester-info').exists()).toBe(false)
  })

  it('removes unavailable titles after refresh succeeds with real zero values', async () => {
    await mountDetail(detailFixture({ usage_stats_available: false, recharge_stats_available: false }))
    expectStats(['—', '—', '—'], [false, false, false])
    mocks.detail.mockResolvedValue(detailFixture({ usage_stats_available: true, recharge_stats_available: true }))
    await wrapper!.get('button[aria-label="tickets.refresh"]').trigger('click')
    await flushPromises()
    expectStats(['0', formatCurrency(0), formatCurrency(0)], [true, true, true])
  })
})
