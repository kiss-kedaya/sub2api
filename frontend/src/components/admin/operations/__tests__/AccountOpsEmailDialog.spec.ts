import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import Dialog from '../AccountOpsEmailDialog.vue'
import { saveAccountOpsNotificationSettings } from '@/api/admin/accountOps'
vi.mock('@/api/admin/accountOps', () => ({ saveAccountOpsNotificationSettings: vi.fn() }))
vi.mock('vue-i18n', async importOriginal => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
// The backend enforces step-up (TOTP) whenever a recipient is supplied; control the controller from the spec.
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
const props = { show: true, config, smtpConfigured: true }
const mountDialog = () => mount(Dialog, { props, global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, TotpStepUpDialog: true } } })
const submit = async (wrapper: ReturnType<typeof mountDialog>, recipient = 'ops@example.com') => {
  await wrapper.get('#account-ops-email-name').setValue('运营邮箱')
  await wrapper.get('#account-ops-email').setValue(recipient)
  await wrapper.get('#account-ops-email-form').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  stepUp.cancelled = false
  stepUp.blocked = false
  stepUp.reason = ''
  stepUp.run.mockImplementation((action: () => Promise<unknown>) => action())
})

describe('notification email editor', () => {
  it('saves the recipient through the step-up controller', async () => {
    vi.mocked(saveAccountOpsNotificationSettings).mockResolvedValue({ ...config, recipient: 'ops@example.com', email_name: '运营邮箱' })
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(stepUp.run).toHaveBeenCalledTimes(1)
    expect(saveAccountOpsNotificationSettings).toHaveBeenCalledWith({ recipient: 'ops@example.com', email_name: '运营邮箱' })
    expect(wrapper.emitted('saved')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.emitted('error')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps the required-recipient validation out of the API call', async () => {
    const wrapper = mountDialog()
    await wrapper.get('#account-ops-email-form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('error')).toEqual([['accountOps.emailRequired']])
    expect(saveAccountOpsNotificationSettings).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('stays silent when the TOTP prompt is cancelled', async () => {
    stepUp.cancelled = true
    stepUp.run.mockRejectedValueOnce(new Error('cancelled'))
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(saveAccountOpsNotificationSettings).not.toHaveBeenCalled()
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

  it('keeps the parent error event contract for ordinary API failures', async () => {
    vi.mocked(saveAccountOpsNotificationSettings).mockRejectedValue({ message: 'smtp unavailable' })
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(wrapper.emitted('error')).toEqual([['smtp unavailable']])
    expect(wrapper.emitted('saved')).toBeUndefined()
    wrapper.unmount()
  })
})
