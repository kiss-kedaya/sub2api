import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import TotpSetupModal from '../TotpSetupModal.vue'
import TotpDisableDialog from '../TotpDisableDialog.vue'

const { api, showError } = vi.hoisted(() => ({
  api: {
    getVerificationMethod: vi.fn(), sendVerifyCode: vi.fn(),
    initiateSetup: vi.fn(), enable: vi.fn(), disable: vi.fn(),
  },
  showError: vi.fn(),
}))
vi.mock('@/api', () => ({ totpAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { role: 'user' } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn() } }))

enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.resetAllMocks()
  api.getVerificationMethod.mockImplementation(async (operation: string) => ({ method: operation === 'disable' ? 'totp' : 'password' }))
  api.initiateSetup.mockResolvedValue({ secret: 'EXAMPLE', qr_code_url: '', setup_token: 'setup-token' })
  api.disable.mockResolvedValue({ success: true })
})

describe('personal TOTP disable', () => {
  const open = async () => {
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(api.getVerificationMethod).toHaveBeenCalledWith('disable')
    return wrapper
  }

  it('accepts only a six-digit authenticator code and preserves a leading zero', async () => {
    const wrapper = await open()
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('profile.totp.disableTotpHint')
    expect(wrapper.text()).not.toContain('profile.totp.sendCode')
    const input = wrapper.get('[data-testid="totp-disable-code"]')
    expect(input.attributes('autocomplete')).toBe('one-time-code')
    for (const value of ['', '12345', '1234567', '12a456']) {
      await input.setValue(value)
      await wrapper.get('form').trigger('submit')
      expect(api.disable).not.toHaveBeenCalled()
    }
    await input.setValue('012345')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.disable).toHaveBeenCalledOnce()
    expect(api.disable).toHaveBeenCalledWith({ totp_code: '012345' })
    expect(api.sendVerifyCode).not.toHaveBeenCalled()
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('keeps 2FA enabled and clears the submitted code when verification fails', async () => {
    api.disable.mockRejectedValueOnce({ status: 400, message: 'Authenticator code expired' })
    const wrapper = await open()
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('654321')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(showError).toHaveBeenCalledWith('Authenticator code expired')
    expect(wrapper.get('[data-testid="totp-disable-code"]').element).toHaveProperty('value', '')
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
  })

  it('closes without disabling when cancelled', async () => {
    const wrapper = await open()
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('654321')
    await wrapper.findAll('button').find(button => button.text() === 'common.cancel')!.trigger('click')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(api.disable).not.toHaveBeenCalled()
  })

  it.each(['email', 'password'] as const)('never downgrades ordinary user verification to a legacy %s method', async method => {
    api.getVerificationMethod.mockResolvedValueOnce({ method })
    const wrapper = await open()
    expect(wrapper.find('input[type="password"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('profile.totp.sendCode')
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.disable).toHaveBeenCalledWith({ totp_code: '123456' })
  })

  it('does not send an additional disable request while the first is pending', async () => {
    let finish!: (value: { success: boolean }) => void
    api.disable.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await open()
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('654321')
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(api.disable).toHaveBeenCalledOnce()
    finish({ success: true })
    await flushPromises()
  })
})

describe('TOTP enable with the existing identity verification', () => {
  it('uses the administrator password before showing the QR code', async () => {
    const wrapper = mount(TotpSetupModal)
    await flushPromises()
    expect(api.getVerificationMethod).toHaveBeenCalledWith('enable')
    await wrapper.get('input[type="password"]').setValue('admin-password')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(api.initiateSetup).toHaveBeenCalledWith({ password: 'admin-password' })
    expect(wrapper.text()).toContain('EXAMPLE')
    expect(api.sendVerifyCode).not.toHaveBeenCalled()
  })

  it('preserves email verification for ordinary users', async () => {
    api.getVerificationMethod.mockResolvedValueOnce({ method: 'email' })
    const wrapper = mount(TotpSetupModal)
    await flushPromises()
    await wrapper.get('input[maxlength="6"]').setValue('123456')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(api.initiateSetup).toHaveBeenCalledWith({ email_code: '123456' })
  })

  it('fails closed for an unexpected setup method', async () => {
    api.getVerificationMethod.mockResolvedValueOnce({ method: 'totp' })
    const wrapper = mount(TotpSetupModal)
    await flushPromises()
    expect(wrapper.emitted('close')).toHaveLength(1)
    await wrapper.get('.btn-primary').trigger('click')
    expect(api.initiateSetup).not.toHaveBeenCalled()
  })
})
