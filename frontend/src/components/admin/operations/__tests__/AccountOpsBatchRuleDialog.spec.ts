import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BatchRuleDialog from '../AccountOpsBatchRuleDialog.vue'
import { saveAccountOpsRuleGroups, saveAccountOpsRulesBatch } from '@/api/admin/accountOps'
import type { AccountOpsConfig, AccountOpsThresholdAccount } from '@/api/admin/accountOps'

vi.mock('@/api/admin/accountOps', () => ({
  saveAccountOpsRulesBatch: vi.fn(),
  saveAccountOpsRuleGroups: vi.fn()
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const config: AccountOpsConfig = {
  enabled: false,
  recipient: '',
  balance_low: true,
  weekly_quota: true,
  cooldown_minutes: 60
}
const keyAccounts: AccountOpsThresholdAccount[] = [
  { account_id: 7, account_name: 'Alpha key', type: 'apikey', platform: 'openai', balance: 3.2, unit: 'USD', balance_status: 'ok', received_at: null, usage_windows: [] },
  { account_id: 12, account_name: 'Beta key', type: 'apikey', platform: 'openai', balance: 10, unit: 'USD', balance_status: 'ok', received_at: null, usage_windows: [] }
]
const oauthAccounts: AccountOpsThresholdAccount[] = [
  {
    ...keyAccounts[0]!, type: 'oauth', account_name: 'Alpha oauth',
    usage_windows: [
      { window: 'five_hour', label: '5 hours', used_percent: 20, status: 'ok' },
      { window: 'seven_day', label: '7 days', used_percent: 70, status: 'ok' }
    ]
  },
  {
    ...keyAccounts[1]!, type: 'oauth', account_name: 'Beta oauth',
    usage_windows: [{ window: 'seven_day', label: '7 days', used_percent: 80, status: 'ok' }]
  }
]
const mixedAccounts: AccountOpsThresholdAccount[] = [
  { ...oauthAccounts[0]!, account_id: 21, unit: 'CNY' },
  keyAccounts[0]!,
  { ...oauthAccounts[1]!, account_id: 22, unit: 'EUR' },
  keyAccounts[1]!
]

function create(accounts = keyAccounts) {
  return mount(BatchRuleDialog, {
    props: { show: true, accounts },
    global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } }
  })
}

beforeEach(() => {
  vi.mocked(saveAccountOpsRulesBatch).mockReset().mockResolvedValue(config)
  vi.mocked(saveAccountOpsRuleGroups).mockReset().mockResolvedValue(config)
})

describe('batch account rules', () => {
  it('starts with no amount, enabled rules and both notices, and submits only the selected IDs', async () => {
    const wrapper = create()
    expect(wrapper.find('[data-testid="batch-rule-threshold"]').exists()).toBe(true)
    expect((wrapper.get('[data-testid="batch-rule-threshold"]').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('[data-testid="batch-rule-enabled"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="batch-rule-notify-alert"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="batch-rule-notify-recovery"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('Alpha key')
    expect(wrapper.text()).toContain('Beta key')
    expect(wrapper.text()).toContain('accountOps.batchReplaceHint')

    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="batch-rule-enabled"]').setValue(false)
    await wrapper.get('[data-testid="batch-rule-notify-alert"]').setValue(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveAccountOpsRulesBatch).toHaveBeenCalledTimes(1)
    expect(saveAccountOpsRuleGroups).not.toHaveBeenCalled()
    expect(saveAccountOpsRulesBatch).toHaveBeenCalledWith([7, 12], {
      metric: 'balance', threshold: 5, unit: 'USD', enabled: false,
      notify_alert: false, notify_recovery: true
    })
    expect(wrapper.emitted('saved')).toEqual([[config]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('offers only shared OAuth windows plus any and preserves notification flags', async () => {
    const wrapper = create(oauthAccounts)
    const windowSelect = wrapper.get('[data-testid="batch-rule-window"]')
    expect(windowSelect.findAll('option').map(option => option.element.value)).toEqual(['any', 'seven_day'])
    expect((windowSelect.element as HTMLSelectElement).value).toBe('any')
    await windowSelect.setValue('seven_day')
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('85')
    await wrapper.get('[data-testid="batch-rule-notify-recovery"]').setValue(false)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(saveAccountOpsRulesBatch).toHaveBeenCalledTimes(1)
    expect(saveAccountOpsRulesBatch).toHaveBeenCalledWith([7, 12], {
      metric: 'quota', threshold_percent: 85, window: 'seven_day', enabled: true,
      notify_alert: true, notify_recovery: false
    })
    wrapper.unmount()
  })

  it('explains mixed current currencies and prevents a batch save', async () => {
    const wrapper = create([keyAccounts[0]!, { ...keyAccounts[1]!, unit: 'CNY' }])
    expect(wrapper.text()).toContain('accountOps.batchMixedUnits')
    expect(wrapper.get('[data-testid="save-batch-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('5')
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.batchMixedUnits']])
    wrapper.unmount()
  })

  it('rejects an empty selection even when submitted programmatically', async () => {
    const wrapper = create([])
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.batchInvalidSelection']])
    wrapper.unmount()
  })

  it.each([
    ['empty balance', keyAccounts, ''],
    ['negative balance', keyAccounts, '-1'],
    ['excessive balance', keyAccounts, '1000000000001'],
    ['zero quota', oauthAccounts, '0'],
    ['excessive quota', oauthAccounts, '101']
  ])('rejects an invalid threshold: %s', async (_name, accounts, amount) => {
    const wrapper = create(accounts)
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue(amount)
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.invalidThreshold']])
    wrapper.unmount()
  })

  it('keeps the form disabled during a request and suppresses duplicate submissions', async () => {
    let resolve!: (value: AccountOpsConfig) => void
    vi.mocked(saveAccountOpsRulesBatch).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = create()
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('0')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('fieldset').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="save-batch-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRulesBatch).toHaveBeenCalledTimes(1)
    resolve(config)
    await flushPromises()
    expect(wrapper.emitted('saved')).toEqual([[config]])
    wrapper.unmount()
  })

  it('ignores an old success after the dialog closes and reopens for other accounts', async () => {
    let resolve!: (value: AccountOpsConfig) => void
    vi.mocked(saveAccountOpsRulesBatch).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = create()
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('5')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('[data-testid="cancel-batch-rules"]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, accounts: oauthAccounts })
    expect((wrapper.get('[data-testid="batch-rule-threshold"]').element as HTMLInputElement).value).toBe('')
    resolve(config)
    await flushPromises()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.get('fieldset').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('ignores a failed request after unmount', async () => {
    let reject!: (reason: Error) => void
    vi.mocked(saveAccountOpsRulesBatch).mockReturnValue(new Promise((_resolve, fail) => { reject = fail }))
    const wrapper = create()
    await wrapper.get('[data-testid="batch-rule-threshold"]').setValue('5')
    await wrapper.get('form').trigger('submit')
    wrapper.unmount()
    reject(new Error('request failed'))
    await flushPromises()
    expect(wrapper.emitted('error')).toBeUndefined()
  })
})

describe('mixed account rule batches', () => {
  it('submits two independently configured groups in one operation, ignoring OAuth currency fields', async () => {
    const wrapper = create(mixedAccounts)
    expect(wrapper.find('[data-testid="batch-balance-rule-threshold"]').exists()).toBe(true)
    const quotaWindows = wrapper.get('[data-testid="batch-quota-rule-window"]')
    expect(quotaWindows.findAll('option').map(option => option.element.value)).toEqual(['any', 'seven_day'])
    expect(wrapper.get('[data-testid="save-batch-rules"]').attributes('disabled')).toBeUndefined()
    for (const metric of ['balance', 'quota']) {
      expect((wrapper.get(`[data-testid="batch-${metric}-rule-threshold"]`).element as HTMLInputElement).value).toBe('')
      for (const field of ['enabled', 'notify-alert', 'notify-recovery']) {
        expect((wrapper.get(`[data-testid="batch-${metric}-rule-${field}"]`).element as HTMLInputElement).checked).toBe(true)
      }
    }
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue('5.5')
    await wrapper.get('[data-testid="batch-balance-rule-enabled"]').setValue(false)
    await wrapper.get('[data-testid="batch-balance-rule-notify-alert"]').setValue(false)
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue('85')
    await wrapper.get('[data-testid="batch-quota-rule-notify-recovery"]').setValue(false)
    await quotaWindows.setValue('seven_day')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(saveAccountOpsRuleGroups).toHaveBeenCalledTimes(1)
    expect(saveAccountOpsRuleGroups).toHaveBeenCalledWith([
      { account_ids: [7, 12], rule: { metric: 'balance', threshold: 5.5, unit: 'USD', enabled: false, notify_alert: false, notify_recovery: true } },
      { account_ids: [21, 22], rule: { metric: 'quota', threshold_percent: 85, window: 'seven_day', enabled: true, notify_alert: true, notify_recovery: false } }
    ])
    expect(wrapper.emitted('saved')).toEqual([[config]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it.each([
    ['missing balance', '', '85'],
    ['missing quota', '5', ''],
    ['invalid quota', '5', '101']
  ])('does not save either group with %s', async (_name, balance, quota) => {
    const wrapper = create(mixedAccounts)
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue(balance)
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue(quota)
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRuleGroups).not.toHaveBeenCalled()
    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.invalidThreshold']])
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })

  it('blocks both groups when the API Key subset mixes current currencies', async () => {
    const wrapper = create(mixedAccounts.map(account => account.account_id === 12 ? { ...account, unit: 'CNY' } : account))
    expect(wrapper.text()).toContain('accountOps.batchMixedUnits')
    expect(wrapper.get('[data-testid="save-batch-rules"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue('85')
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRuleGroups).not.toHaveBeenCalled()
    expect(saveAccountOpsRulesBatch).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.batchMixedUnits']])
    wrapper.unmount()
  })

  it('keeps both groups editable with their values intact after a failed save', async () => {
    vi.mocked(saveAccountOpsRuleGroups).mockRejectedValue({ response: { data: { message: 'Atomic save failed' } } })
    const wrapper = create(mixedAccounts)
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue('85')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('error')).toEqual([['Atomic save failed']])
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect((wrapper.get('[data-testid="batch-balance-rule-threshold"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('[data-testid="batch-quota-rule-threshold"]').element as HTMLInputElement).value).toBe('85')
    expect(wrapper.findAll('fieldset').every(fieldset => fieldset.attributes('disabled') === undefined)).toBe(true)
    expect(wrapper.get('[data-testid="save-batch-rules"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('preserves entered values on sample refresh and resets both when account conditions change', async () => {
    const wrapper = create(mixedAccounts)
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue('85')
    await wrapper.setProps({ accounts: mixedAccounts.map(account => ({ ...account, balance: 100 })) })
    expect((wrapper.get('[data-testid="batch-balance-rule-threshold"]').element as HTMLInputElement).value).toBe('5')
    expect((wrapper.get('[data-testid="batch-quota-rule-threshold"]').element as HTMLInputElement).value).toBe('85')
    await wrapper.setProps({ accounts: mixedAccounts.map(account => ({ ...account, unit: 'CNY' })) })
    expect((wrapper.get('[data-testid="batch-balance-rule-threshold"]').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('[data-testid="batch-quota-rule-threshold"]').element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })

  it('blocks duplicate requests for both groups and ignores completion after closing', async () => {
    let resolve!: (value: AccountOpsConfig) => void
    vi.mocked(saveAccountOpsRuleGroups).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = create(mixedAccounts)
    await wrapper.get('[data-testid="batch-balance-rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="batch-quota-rule-threshold"]').setValue('85')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.findAll('fieldset').every(fieldset => fieldset.attributes('disabled') !== undefined)).toBe(true)
    await wrapper.get('form').trigger('submit')
    expect(saveAccountOpsRuleGroups).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="cancel-batch-rules"]').trigger('click')
    resolve(config)
    await flushPromises()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
