import { describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import Dialog from '../AccountOpsWebhookDialog.vue'
import { saveAccountOpsWebhook } from '@/api/admin/accountOps'
vi.mock('@/api/admin/accountOps', () => ({ saveAccountOpsWebhook: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('independent bot editor', () => {
  it('saves one bot and erases transient credentials when closing', async () => {
    vi.mocked(saveAccountOpsWebhook).mockResolvedValue({ enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60 })
    const props = { show: true, hook: { id: 'one', provider: 'feishu' as const, enabled: true, url_configured: true }, encryptionConfigured: true }
    const wrapper = mount(Dialog, { props, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } } } })
    await wrapper.get('[data-testid="account-ops-webhook-name-one"]').setValue('值班群')
    await wrapper.get('[data-testid="account-ops-webhook-secret-one"]').setValue('test-only')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(saveAccountOpsWebhook).toHaveBeenCalledWith('one', { name: '值班群', provider: 'auto', enabled: true, secret: 'test-only' })
    expect((wrapper.get('[data-testid="account-ops-webhook-secret-one"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('saved')).toHaveLength(1)
    wrapper.unmount()
  })
})
