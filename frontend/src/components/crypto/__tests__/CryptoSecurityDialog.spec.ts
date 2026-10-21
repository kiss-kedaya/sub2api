import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import CryptoSecurityDialog from '../CryptoSecurityDialog.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), toDataURL: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('qrcode', () => ({ default: { toDataURL: mocks.toDataURL } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { enabled: true, feature_enabled: true, can_setup: true } })
  mocks.post.mockResolvedValue({ data: { success: true } })
  mocks.toDataURL.mockResolvedValue('data:image/png;base64,local-qr')
})
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

function render() {
  return mount(CryptoSecurityDialog, { global: { stubs: { teleport: true } } })
}

describe('Crypto verification UI (server authorization is tested separately)', () => {
  it('binds with the login password and local QR, discards the action, then asks for a fresh code', async () => {
    mocks.get.mockResolvedValueOnce({ data: { enabled: false, feature_enabled: true, can_setup: true } })
    mocks.post.mockImplementation(async (path: string) => ({ data: path.endsWith('/setup') ? {
      secret: 'LOCALSETUPKEY', qr_code_url: 'otpauth://totp/test?secret=LOCALSETUPKEY', setup_token: 'one-setup-token', countdown: 120
    } : { success: true } }))
    const localWrite = vi.spyOn(localStorage, 'setItem')
    const sessionWrite = vi.spyOn(sessionStorage, 'setItem')
    const wrapper = render()
    const action = vi.fn().mockResolvedValue(undefined)
    const result = wrapper.vm.verify('Export wallet', action)
    await flushPromises()
    await wrapper.get('#crypto-security-password').setValue('login-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/security/setup', { password: 'login-password' }, expect.anything())
    expect(mocks.toDataURL).toHaveBeenCalledWith('otpauth://totp/test?secret=LOCALSETUPKEY', { width: 224, margin: 2 })
    expect(wrapper.get('img').attributes('src')).toBe('data:image/png;base64,local-qr')
    await wrapper.get('#crypto-security-code').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.post).toHaveBeenLastCalledWith('/admin/crypto/security/enable', { totp_code: '123456', setup_token: 'one-setup-token' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
    expect(await result).toBe(false)
    expect(action).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.crypto.securityBound')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('LOCALSETUPKEY')
    await wrapper.get('.btn-secondary').trigger('click')
    const second = wrapper.vm.verify('Export wallet', action)
    await flushPromises()
    expect(wrapper.find('#crypto-security-password').exists()).toBe(false)
    expect((wrapper.get('#crypto-security-code').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#crypto-security-code').setValue('234567')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(await second).toBe(true)
    expect(action).toHaveBeenCalledTimes(1)
    expect(action).toHaveBeenLastCalledWith('234567')
    expect(localWrite).not.toHaveBeenCalled()
    expect(sessionWrite).not.toHaveBeenCalled()
  })

  it('cancel clears the typed code and never invokes the callback or reuses it on reopen', async () => {
    const wrapper = render()
    const action = vi.fn()
    const result = wrapper.vm.verify('Sweep', action)
    await flushPromises()
    await wrapper.get('#crypto-security-code').setValue('123456')
    await wrapper.get('.btn-secondary').trigger('click')
    expect(await result).toBe(false)
    expect(action).not.toHaveBeenCalled()
    const next = wrapper.vm.verify('Sweep', action)
    await flushPromises()
    expect((wrapper.get('#crypto-security-code').element as HTMLInputElement).value).toBe('')
    await wrapper.get('.btn-secondary').trigger('click')
    expect(await next).toBe(false)
  })

  it.each([
    ['CRYPTO_TOTP_NOT_CONFIGURED', 403, 'securityNotConfigured'],
    ['CRYPTO_TOTP_EXPIRED', 403, 'securityExpired'],
    ['CRYPTO_TOTP_REPLAY', 403, 'securityReplay'],
    ['CRYPTO_TOTP_RATE_LIMITED', 429, 'securityRateLimit']
  ])('shows server rejection %s and clears the submitted code', async (reason, status, key) => {
    const wrapper = render()
    const action = vi.fn().mockRejectedValue({ reason, status })
    const result = wrapper.vm.verify('Change address', action)
    await flushPromises()
    await wrapper.get('#crypto-security-code').setValue('123456')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe(`admin.crypto.${key}`)
    expect((wrapper.get('#crypto-security-code').element as HTMLInputElement).value).toBe('')
    expect(action).toHaveBeenCalledTimes(1)
    await wrapper.get('.btn-secondary').trigger('click')
    expect(await result).toBe(false)
  })

  it('fails closed when verification cannot be configured', async () => {
    mocks.get.mockResolvedValue({ data: { enabled: false, feature_enabled: false, can_setup: false } })
    const wrapper = render()
    const action = vi.fn()
    const result = wrapper.vm.verify('Save', action)
    await nextTick()
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.crypto.securityUnavailable')
    await wrapper.get('.btn-secondary').trigger('click')
    expect(await result).toBe(false)
    expect(action).not.toHaveBeenCalled()
  })

  it('closing while status loads discards the late response', async () => {
    let resolve!: (value: unknown) => void
    mocks.get.mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const wrapper = render()
    const action = vi.fn()
    const result = wrapper.vm.verify('Save', action)
    await nextTick()
    await wrapper.get('.btn-secondary').trigger('click')
    resolve({ data: { enabled: true, feature_enabled: true } })
    await flushPromises()
    expect(await result).toBe(false)
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(action).not.toHaveBeenCalled()
  })

  it('wipes late 2FA setup secrets and QR data if the dialog unmounts during setup', async () => {
    mocks.get.mockResolvedValueOnce({ data: { enabled: false, feature_enabled: true, can_setup: true } })
    let resolveSetup!: (value: { data: { secret: string; qr_code_url: string; setup_token: string; countdown: number } }) => void
    mocks.post.mockImplementationOnce(() => new Promise((resolve) => { resolveSetup = resolve }))
    const wrapper = render()
    const result = wrapper.vm.verify('Export wallet', vi.fn())
    await flushPromises()
    await wrapper.get('#crypto-security-password').setValue('login-password')
    await wrapper.get('form').trigger('submit')
    await Promise.resolve()

    const lateSetup = {
      secret: 'LATESETUPKEY',
      qr_code_url: 'otpauth://totp/test?secret=LATESETUPKEY',
      setup_token: 'late-setup-token',
      countdown: 120
    }
    wrapper.unmount()
    resolveSetup({ data: lateSetup })
    await flushPromises()

    expect(lateSetup).toEqual({ secret: '', qr_code_url: '', setup_token: '', countdown: 120 })
    expect(await result).toBe(false)
    expect(mocks.toDataURL).not.toHaveBeenCalled()
  })

  it('expires setup and removes the secret, QR and timer', async () => {
    vi.useFakeTimers()
    mocks.get.mockResolvedValueOnce({ data: { enabled: false, feature_enabled: true, can_setup: true } })
    mocks.post.mockResolvedValue({ data: { secret: 'EXPIRES', qr_code_url: 'otpauth://totp/test?secret=EXPIRES', setup_token: 'token', countdown: 1 } })
    const wrapper = render()
    const result = wrapper.vm.verify('Save', vi.fn())
    await flushPromises()
    await wrapper.get('#crypto-security-password').setValue('password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.text()).toContain('admin.crypto.securitySetupExpired')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('EXPIRES')
    expect(vi.getTimerCount()).toBe(0)
    await wrapper.get('.btn-secondary').trigger('click')
    expect(await result).toBe(false)
  })
})
