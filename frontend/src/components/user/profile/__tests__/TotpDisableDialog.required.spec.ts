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
  it('defaults to an exactly six digit TOTP code for admins', async () => {
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

  it('requires a current authenticator code for non-admin users even when the server returns password', async () => {
    mocks.role = 'user'
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(wrapper.find('#password').exists()).toBe(false)
    await wrapper.get('#disable-totp-code').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ totp_code: '123456' })
  })

  it('offers server recovery only for administrators and sends only the chosen credential', async () => {
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('12345')
    await wrapper.get('[data-testid="totp-disable-method-server"]').setValue()
    expect(wrapper.find('[data-testid="totp-disable-code"]').exists()).toBe(false)
    const input = wrapper.get('[data-testid="totp-disable-server-code"]')
    expect(input.attributes('type')).toBe('password')
    expect(input.attributes('autocomplete')).toBe('off')
    expect(wrapper.text()).toContain('profile.totp.disableServerAuthorizationHint')
    for (const value of ['', '123456', 'a'.repeat(63), 'z'.repeat(64)]) {
      await input.setValue(value)
      await wrapper.get('form').trigger('submit')
      expect(mocks.disable).not.toHaveBeenCalled()
    }
    const authorizationCode = 'a1'.repeat(32)
    await input.setValue(authorizationCode)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledOnce()
    expect(mocks.disable).toHaveBeenCalledWith({ server_authorization_code: authorizationCode })
    expect(input.element).toHaveProperty('value', '')
  })

  it('clears authenticator and recovery codes when switching methods', async () => {
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    await wrapper.get('[data-testid="totp-disable-code"]').setValue('12345')
    await wrapper.get('[data-testid="totp-disable-method-server"]').setValue()
    await wrapper.get('[data-testid="totp-disable-server-code"]').setValue('a'.repeat(64))
    await wrapper.get('[data-testid="totp-disable-method-authenticator"]').setValue()
    expect(wrapper.get('[data-testid="totp-disable-code"]').element).toHaveProperty('value', '')
    await wrapper.get('[data-testid="totp-disable-method-server"]').setValue()
    expect(wrapper.get('[data-testid="totp-disable-server-code"]').element).toHaveProperty('value', '')
    await wrapper.get('form').trigger('submit')
    expect(mocks.disable).not.toHaveBeenCalled()
  })

  it('clears the rejected server authorization before retrying and does not disable 2FA', async () => {
    mocks.disable.mockRejectedValueOnce({ message: 'Server authorization expired' })
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    await wrapper.get('[data-testid="totp-disable-method-server"]').setValue()
    await wrapper.get('[data-testid="totp-disable-server-code"]').setValue('a'.repeat(64))
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith('Server authorization expired')
    expect(wrapper.get('[data-testid="totp-disable-server-code"]').element).toHaveProperty('value', '')
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('clears a server authorization immediately on cancel without submitting', async () => {
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    await wrapper.get('[data-testid="totp-disable-method-server"]').setValue()
    await wrapper.get('[data-testid="totp-disable-server-code"]').setValue('a'.repeat(64))
    await wrapper.get('.btn-secondary').trigger('click')
    expect(wrapper.get('[data-testid="totp-disable-server-code"]').element).toHaveProperty('value', '')
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(mocks.disable).not.toHaveBeenCalled()
  })

  it('requires a current authenticator code for non-admin users even when the server returns email', async () => {
    mocks.role = 'user'
    mocks.method = 'email'
    const wrapper = mount(TotpDisableDialog)
    await flushPromises()
    expect(wrapper.find('[data-testid="totp-disable-method-server"]').exists()).toBe(false)
    expect(wrapper.find('#password').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="profile.totp.enterEmailCode"]').exists()).toBe(false)
    await wrapper.get('#disable-totp-code').setValue('654321')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.disable).toHaveBeenCalledWith({ totp_code: '654321' })
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
