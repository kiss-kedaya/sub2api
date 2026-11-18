import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RuleList from '../AccountOpsRuleList.vue'
import type { AccountOpsConfig, AccountOpsGroup, AccountOpsThresholdAccount } from '@/api/admin/accountOps'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key }) }))

const config: AccountOpsConfig = {
  enabled: true, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60,
  balance_thresholds: [{ account_id: 8101, enabled: true, threshold: 5, unit: 'USD' }]
}
const group: AccountOpsGroup = { id: 'synthetic-wallet', name: 'Synthetic wallet', default_name: 'Synthetic wallet', provider: 'newapi', site: 'https://example.invalid', mode: 'manual', account_ids: [8101, 8102] }
const account: AccountOpsThresholdAccount = { account_id: 8101, account_name: 'Synthetic key', platform: 'openai', type: 'apikey', balance: null, unit: 'USD', balance_status: 'disabled', wallet_identity: 'synthetic-shared-wallet', received_at: null, usage_windows: [] }

function create(accounts: AccountOpsThresholdAccount[]) {
  return mount(RuleList, { props: { accounts, groups: [group], config, loading: false, ready: true, error: '' } })
}

describe('upstream balance rules', () => {
  it('uses a readable wallet member instead of a disabled first key', () => {
    const wrapper = create([account, { ...account, account_id: 8102, balance: 12, balance_status: 'ok' }])
    expect(wrapper.text()).toContain('12.00 USD')
    expect(wrapper.text()).toContain('accountOps.partialBalance')
    expect(wrapper.text()).toContain('accountOps.ruleStates.normal')
    expect(wrapper.text()).not.toContain('accountOps.ruleStates.unknown')
    wrapper.unmount()
  })

  it('shows key quota separately from an unauthorized account wallet', () => {
    const wrapper = create([{ ...account, upstream_kind: 'newapi', key_balance: 20, balance_status: 'forbidden', wallet_status: 'forbidden' }])
    expect(wrapper.text()).toContain('20.00 USD')
    expect(wrapper.text()).toContain('accountOps.observations.forbidden')
    expect(wrapper.text()).not.toMatch(/(?:^|[^\d])0\.00 USD/)
    wrapper.unmount()
  })

  it('reports stale age without calling it a failed query', () => {
    vi.setSystemTime(new Date('2026-11-18T00:20:00Z'))
    const wrapper = create([{ ...account, balance_status: 'stale', received_at: '2026-11-18T00:00:00Z' }])
    expect(wrapper.text()).toContain('accountOps.observations.stale')
    expect(wrapper.text()).toContain('accountOps.balanceAge:{"minutes":20}')
    expect(wrapper.text()).not.toContain('accountOps.observations.failed')
    wrapper.unmount()
    vi.useRealTimers()
  })
})
