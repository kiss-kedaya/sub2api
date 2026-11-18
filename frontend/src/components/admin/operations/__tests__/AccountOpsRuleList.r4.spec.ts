import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RuleList from '../AccountOpsRuleList.vue'
import type { AccountOpsConfig, AccountOpsGroup, AccountOpsThresholdAccount } from '@/api/admin/accountOps'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const config: AccountOpsConfig = { enabled: true, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60, balance_thresholds: [{ account_id: 9101, enabled: true, threshold: 5, unit: 'USD' }] }
const group: AccountOpsGroup = { id: 'manual-r4', name: 'Synthetic wallets', default_name: 'Synthetic wallets', provider: 'new_api', site: 'https://example.invalid', mode: 'manual', account_ids: [9101, 9102] }
const account: AccountOpsThresholdAccount = { account_id: 9101, account_name: 'Synthetic first', platform: 'openai', type: 'apikey', balance: null, unit: 'USD', balance_status: 'unknown', received_at: null, usage_windows: [] }

describe('R4 wallet representative', () => {
  it.each([
    [undefined, undefined],
    ['synthetic-wallet-a', 'synthetic-wallet-b'],
    ['synthetic-wallet-a', undefined]
  ])('does not warn from an unproven second wallet (%s, %s)', (first, second) => {
    const wrapper = mount(RuleList, { props: { accounts: [{ ...account, wallet_identity: first }, { ...account, account_id: 9102, wallet_identity: second, balance: 2, balance_status: 'ok' }], groups: [group], config, loading: false, ready: true, error: '' } })
    expect(wrapper.text()).toContain('accountOps.ruleStates.unknown')
    expect(wrapper.text()).not.toContain('2.00 USD')
    expect(wrapper.text()).not.toContain('accountOps.ruleStates.warning')
    wrapper.unmount()
  })
  it('keeps the configured wallet unknown when its account row is missing', () => {
    const wrapper = mount(RuleList, { props: { accounts: [{ ...account, account_id: 9102, balance: 2, balance_status: 'ok' }], groups: [group], config, loading: false, ready: true, error: '' } })
    expect(wrapper.text()).toContain('accountOps.ruleStates.unknown')
    expect(wrapper.text()).not.toContain('2.00 USD')
    wrapper.unmount()
  })
})
