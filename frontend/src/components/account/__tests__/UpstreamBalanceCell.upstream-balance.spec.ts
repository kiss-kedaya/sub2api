import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import BalanceCell from '../UpstreamBalanceCell.vue'
import type { Account, UpstreamBalanceData } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'en' }, t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${Object.values(values).join(',')}` : key }) }))
const now = Date.parse('2026-11-18T00:00:00Z')
function render(data: UpstreamBalanceData) {
  const account = { type: 'apikey', extra: { upstream_billing_probe: { status: 'ok', upstream_kind: 'newapi', balance: { status: 'ok', data, received_at: new Date(now).toISOString(), fresh_until: new Date(now + 60_000).toISOString() } } } } as Account
  return mount(BalanceCell, { props: { account, now }, global: { stubs: { HelpTooltip: { template: '<div><slot name="trigger"/><slot/></div>' }, UIcon: true } } })
}

describe('New API balance display', () => {
  it('shows key quota and wallet balance as separate scopes', () => {
    const wrapper = render({ remaining: 3.3, wallet_balance: 19.6, wallet_status: 'available', unit: 'USD' })
    expect(wrapper.text()).toContain('upstreamBalance.keyQuotaShort')
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toContain('$3.30')
    expect(wrapper.get('[data-testid="upstream-balance-wallet-state"]').text()).toContain('$19.60')
    wrapper.unmount()
  })
  it('does not invent zero when wallet permission is absent', () => {
    const wrapper = render({ remaining: 3.3, wallet_status: 'forbidden', unit: 'USD' })
    expect(wrapper.get('[data-testid="upstream-balance-wallet-state"]').text()).toContain('upstreamBalance.forbidden')
    expect(wrapper.text()).not.toContain('$0.00')
    wrapper.unmount()
  })
  it('shows unlimited instead of a finite balance', () => {
    const wrapper = render({ unlimited: true, wallet_status: 'unlimited', unit: 'USD' })
    expect(wrapper.get('[data-testid="upstream-balance-value"]').text()).toContain('upstreamBalance.unlimited')
    expect(wrapper.text()).not.toContain('$0.00')
    wrapper.unmount()
  })
})
