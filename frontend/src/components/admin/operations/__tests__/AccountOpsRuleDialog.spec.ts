import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import RuleDialog from '../AccountOpsRuleDialog.vue'
import { saveAccountOpsRule } from '@/api/admin/accountOps'
vi.mock('@/api/admin/accountOps', () => ({ saveAccountOpsRule: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const account = { account_id: 1, account_name: 'key', type: 'apikey', platform: 'openai', balance: 3.2, unit: 'USD', balance_status: 'ok', received_at: null, usage_windows: [] } as const
const create = () => mount(RuleDialog, { props: { show: true, account: { ...account, usage_windows: [] }, balanceRule: null, quotaRule: null }, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } } })
describe('per-account transition policy', () => {
  it('retains the saved currency until an operator explicitly switches and re-enters the amount', async () => {
    vi.mocked(saveAccountOpsRule).mockResolvedValue({ enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60 })
    const wrapper = mount(RuleDialog, { props: { show: true, account: { ...account, unit: 'CNY', usage_windows: [] }, balanceRule: { account_id: 1, enabled: true, threshold: 5, unit: 'USD' }, quotaRule: null }, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } } })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsRule).toHaveBeenLastCalledWith(1, expect.objectContaining({ threshold: 5, unit: 'USD' }))
    await wrapper.setProps({ show: false }); await wrapper.setProps({ show: true })
    await wrapper.get('[data-testid="rule-use-current-unit"]').trigger('click')
    expect((wrapper.get('[data-testid="rule-threshold"]').element as HTMLInputElement).value).toBe('')
    await wrapper.get('[data-testid="rule-threshold"]').setValue('36')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsRule).toHaveBeenLastCalledWith(1, expect.objectContaining({ threshold: 36, unit: 'CNY' }))
    wrapper.unmount()
  })

  it('defaults both notices on and can save a recovery-only amount rule', async () => {
    vi.mocked(saveAccountOpsRule).mockResolvedValue({ enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60 })
    const wrapper = create()
    expect((wrapper.get('[data-testid="rule-notify-alert"]').element as HTMLInputElement).checked).toBe(true)
    expect((wrapper.get('[data-testid="rule-notify-recovery"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="rule-threshold"]').setValue('5')
    await wrapper.get('[data-testid="rule-notify-alert"]').setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsRule).toHaveBeenCalledWith(1, { metric: 'balance', enabled: true, threshold: 5, unit: 'USD', notify_alert: false, notify_recovery: true })
    expect(wrapper.emitted('saved')).toHaveLength(1)
    wrapper.unmount()
  })
  it('rejects an empty amount instead of treating it as zero', async () => {
    vi.mocked(saveAccountOpsRule).mockClear()
    const wrapper = create(); await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsRule).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toEqual([['accountOps.invalidThreshold']])
    wrapper.unmount()
  })
})
