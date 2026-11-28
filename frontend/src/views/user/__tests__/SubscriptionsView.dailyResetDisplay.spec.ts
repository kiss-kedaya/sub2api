import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const { getMySubscriptions } = vi.hoisted(() => ({
  getMySubscriptions: vi.fn()
}))

vi.mock('@/api/subscriptions', () => ({
  default: { getMySubscriptions }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    cachedPublicSettings: {}
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

describe('User SubscriptionsView daily reset display', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-07T02:00:00Z'))
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('displays next anchored 24h reset in window 1 and quotaEnds in final window when boundary == expires_at', async () => {
    getMySubscriptions.mockResolvedValue([
      {
        id: 201,
        user_id: 1,
        group_id: 10,
        status: 'active',
        starts_at: '2026-08-06T04:00:00Z',
        expires_at: '2026-08-08T04:00:00Z',
        daily_window_start: '2026-08-06T04:00:00Z',
        next_daily_reset_at: '2026-08-07T04:00:00Z',
        weekly_window_start: null,
        monthly_window_start: null,
        daily_usage_usd: 25,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        created_at: '2026-08-06T04:00:00Z',
        updated_at: '2026-08-06T04:00:00Z',
        group: { id: 10, name: '24h Window 1', platform: 'anthropic', rate_multiplier: 1, daily_limit_usd: 100, daily_reset_mode: 'anchored_24h' }
      },
      {
        id: 202,
        user_id: 1,
        group_id: 11,
        status: 'active',
        starts_at: '2026-08-05T04:00:00Z',
        expires_at: '2026-08-07T04:00:00Z',
        daily_window_start: '2026-08-06T04:00:00Z',
        next_daily_reset_at: null,
        weekly_window_start: null,
        monthly_window_start: null,
        daily_usage_usd: 90,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        created_at: '2026-08-05T04:00:00Z',
        updated_at: '2026-08-06T04:00:00Z',
        group: { id: 11, name: '24h Final Window', platform: 'openai', rate_multiplier: 1, daily_limit_usd: 100, daily_reset_mode: 'anchored_24h' }
      }
    ])

    const wrapper = mount(SubscriptionsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          UserPageHeader: true,
          Icon: true
        }
      }
    })
    await flushPromises()

    const cards = wrapper.findAll('.signal-subscription-card')
    expect(cards).toHaveLength(2)
    expect(cards[0].text()).toContain('userSubscriptions.resetIn:{"time":"2h 0m"}')
    expect(cards[1].text()).toContain('userSubscriptions.quotaEndsIn:{"time":"2h 0m"}')
    expect(cards[1].text()).not.toContain('userSubscriptions.resetIn')
  })
})
