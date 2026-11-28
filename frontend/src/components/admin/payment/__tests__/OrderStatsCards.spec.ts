import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OrderStatsCards from '../OrderStatsCards.vue'
import type { DashboardStats } from '@/types/payment'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('OrderStatsCards', () => {
  it('renders cleanly when stats subfields are null or undefined', () => {
    const partialStats = {} as DashboardStats
    const wrapper = mount(OrderStatsCards, {
      props: { stats: partialStats },
    })
    expect(wrapper.text()).toContain('0 payment.admin.orders')
    expect(wrapper.text()).not.toContain('undefined')
  })
})
