import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import TotpDisableDialog from '../TotpDisableDialog.vue'
const mocks = vi.hoisted(() => ({ role: 'admin', method: 'password', disable: vi.fn(), showError: vi.fn() }))
vi.mock('@/api', () => ({ totpAPI: {
  getVerificationMethod: async () => ({ method: mocks.method }), disable: mocks.disable
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn(), showError: mocks.showError }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { role: mocks.role } }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => { vi.clearAllMocks(); mocks.role = 'admin'; mocks.method = 'password'; mocks.disable.mockResolvedValue({ success: true }) })

describe('Administrator TOTP disable UI protection', () => {
  it('requires only an exactly six digit TOTP code for admins', async () => {
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    const input = wrapper.get('#disable-totp-code')
    expect(input.attributes('required')).toBeDefined()
    expect(wrapper.find('#password').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="profile.totp.enterEmailCode"]').exists()).toBe(false)
    for (const value of ['', '12345', 'abcdef']) {
      await input.setValue(value)
      await wrapper.get('form').trigger('submit')
      expect(wrapper.get('[type="submit"]').attributes('disabled')).toBeDefined()
      expect(mocks.disable).not.toHaveBeenCalled()
    }
    await input.setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledTimes(1)
    expect(mocks.disable).toHaveBeenLastCalledWith({ totp_code: '123456' })
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('does not render or submit email verification for admins when password is the old method', async () => {
    mocks.method = 'email'
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(wrapper.find('#password').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="profile.totp.enterEmailCode"]').exists()).toBe(false)
    expect(wrapper.findAll('button').some(button => button.text() === 'profile.totp.sendCode')).toBe(false)
    await wrapper.get('#disable-totp-code').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ totp_code: '123456' })
  })

  it('preserves the old password-only request for non-admin users', async () => {
    mocks.role = 'user'
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(wrapper.get('#disable-totp-code').attributes('required')).toBeUndefined()
    await wrapper.get('#password').setValue('login-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ password: 'login-password' })
  })

  it('preserves the old email-code request for non-admin users', async () => {
    mocks.role = 'user'
    mocks.method = 'email'
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(wrapper.find('#password').exists()).toBe(false)
    await wrapper.get('input[placeholder="profile.totp.enterEmailCode"]').setValue('654321')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ email_code: '654321' })
  })

  it('shows server rejection and removes the consumed input before retry', async () => {
    mocks.disable.mockRejectedValueOnce({ message: 'Invalid TOTP code' })
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    await wrapper.get('#disable-totp-code').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ totp_code: '123456' })
    expect(mocks.showError).toHaveBeenCalledWith('Invalid TOTP code')
    expect((wrapper.get('#disable-totp-code').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('success')).toBeUndefined()
  })
})
