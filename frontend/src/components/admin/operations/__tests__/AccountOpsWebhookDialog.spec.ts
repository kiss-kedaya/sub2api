import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import Dialog from '../AccountOpsWebhookDialog.vue'
import { saveAccountOpsWebhook } from '@/api/admin/accountOps'
vi.mock('@/api/admin/accountOps', () => ({ saveAccountOpsWebhook: vi.fn() }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
// The backend requires step-up (TOTP) for channel credential writes; control the controller from the spec.
const stepUp = vi.hoisted(() => ({
  run: vi.fn(),
  cancelled: false,
  blocked: false,
  reason: ''
}))
vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ visible: { value: false }, blockedReason: { value: '' }, run: stepUp.run, prompt: vi.fn(), onVerified: vi.fn(), onCancel: vi.fn() }),
  isStepUpCancelled: () => stepUp.cancelled,
  isStepUpBlocked: () => stepUp.blocked,
  stepUpBlockReason: () => stepUp.reason
}))

const config = { enabled: false, recipient: '', balance_low: true, weekly_quota: true, cooldown_minutes: 60 }
const props = { show: true, hook: { id: 'one', provider: 'feishu' as const, enabled: true, url_configured: true }, encryptionConfigured: true }
const mountDialog = () => mount(Dialog, { props, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, TotpStepUpDialog: true } } })
const submit = async (wrapper: ReturnType<typeof mountDialog>) => {
  await wrapper.get('[data-testid="account-ops-webhook-name-one"]').setValue('值班群')
  await wrapper.get('[data-testid="account-ops-webhook-secret-one"]').setValue('test-only')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  stepUp.cancelled = false
  stepUp.blocked = false
  stepUp.reason = ''
  stepUp.run.mockImplementation((action: () => Promise<unknown>) => action())
})

describe('independent bot editor', () => {
  it('saves one bot through the step-up controller and erases transient credentials when closing', async () => {
    vi.mocked(saveAccountOpsWebhook).mockResolvedValue(config)
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(stepUp.run).toHaveBeenCalledTimes(1)
    expect(saveAccountOpsWebhook).toHaveBeenCalledWith('one', { name: '值班群', provider: 'auto', enabled: true, secret: 'test-only' })
    expect((wrapper.get('[data-testid="account-ops-webhook-secret-one"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('saved')).toHaveLength(1)
    wrapper.unmount()
  })

  it('keeps the editor silent when the TOTP prompt is cancelled', async () => {
    stepUp.cancelled = true
    stepUp.run.mockRejectedValueOnce(new Error('cancelled'))
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(saveAccountOpsWebhook).not.toHaveBeenCalled()
    expect(wrapper.emitted('error')).toBeUndefined()
    expect(wrapper.emitted('saved')).toBeUndefined()
    wrapper.unmount()
  })

  it('reports a step-up blocked reason instead of the raw API error', async () => {
    stepUp.blocked = true
    stepUp.reason = 'STEP_UP_TOTP_NOT_ENABLED'
    stepUp.run.mockRejectedValueOnce(new Error('This operation requires recent two-factor verification'))
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(wrapper.emitted('error')).toEqual([['stepUp.notEnabled']])
    wrapper.unmount()
  })

  it('reports admin API key sessions that cannot pass step-up', async () => {
    stepUp.blocked = true
    stepUp.reason = 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
    stepUp.run.mockRejectedValueOnce(new Error('forbidden'))
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(wrapper.emitted('error')).toEqual([['stepUp.adminApiKeyForbidden']])
    wrapper.unmount()
  })

  it('keeps parent error contract for ordinary API failures', async () => {
    vi.mocked(saveAccountOpsWebhook).mockRejectedValue({ message: 'network down' })
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(wrapper.emitted('error')).toEqual([['network down']])
    wrapper.unmount()
  })
})
