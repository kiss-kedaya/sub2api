import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCryptoGrant } from '@/composables/cryptoGrant'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoWalletSecurityPanel from '../CryptoWalletSecurityPanel.vue'
import type { WalletSecurityStatus } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)
afterEach(() => { clearCryptoGrant() })

const grantCredential = 'test-payment-grant-credential-32-bytes-minimum'
const grantResponse = () => ({ data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300000).toISOString(), expires_in: 300 } } })

let state: WalletSecurityStatus
beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  state = { enabled: true, initialized: true, locked: true, deposits_paused: true,
    reason: 'unauthorized_token_outflow', locked_at: '2026-01-02T03:04:05Z', alert_pending: true }
  mocks.get.mockImplementation(async (path: string) => ({
    data: path.endsWith('/security/status')
      ? { enabled: true, feature_enabled: true, can_setup: true, read_only: false }
      : { ...state }
  }))
  mocks.post.mockImplementation(async (path: string) => path.endsWith('/security/verify') ? grantResponse() : ({ data: { ...state, locked: false, deposits_paused: false, alert_pending: false } }))
})

function render() {
  return mount(CryptoWalletSecurityPanel, { global: { stubs: { teleport: true } } })
}

async function confirmRecovery(wrapper: ReturnType<typeof render>) {
  await wrapper.get('[data-testid="wallet-security-recover"]').trigger('click')
  await wrapper.get('#wallet-security-confirmation').setValue('RECOVER')
  await wrapper.get('#wallet-security-recover-form').trigger('submit')
  await flushPromises()
}

describe('wallet security panel and recovery', () => {
  it('shows the exact lock reason, detection time, paused deposits, and pending alert', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityUnexpectedOutflow')
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityDepositsPaused')
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityAlertPending')
    expect(wrapper.findAll('dd')[1].text()).not.toBe('—')
    expect(wrapper.emitted('change')?.[0]).toEqual([state])
  })

  it('requires the confirmation phrase and a fresh complete TOTP before recovery, without enabling the channel', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="wallet-security-recover"]').trigger('click')
    await wrapper.get('#wallet-security-confirmation').setValue('recover')
    await wrapper.get('#wallet-security-recover-form').trigger('submit')
    expect(mocks.post).not.toHaveBeenCalled()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    await wrapper.get('#wallet-security-confirmation').setValue('RECOVER')
    await wrapper.get('#wallet-security-recover-form').trigger('submit')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await wrapper.get('#crypto-security-code').setValue('12345')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await wrapper.get('#crypto-security-code').setValue('123456')
    await flushPromises()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/recover'))).toHaveLength(1)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/security/recover', { confirmation: 'RECOVER' }, {
      headers: { 'X-Crypto-Grant': grantCredential, 'Cache-Control': 'no-store', Pragma: 'no-cache' }, _retry: true
    })
    expect(mocks.put).not.toHaveBeenCalled()
    expect(wrapper.emitted('recovered')).toHaveLength(1)
    expect(wrapper.text()).not.toContain('admin.crypto.walletSecurityLocked')
  })

  it('keeps an observed lock visible and disables recovery when a refresh fails', async () => {
    const wrapper = render()
    await flushPromises()
    mocks.get.mockRejectedValueOnce(new Error('status unavailable'))
    await wrapper.get('[data-testid="wallet-security-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityLocked')
    expect(wrapper.get('[role="alert"]').text()).toContain('status unavailable')
    expect(wrapper.get('[data-testid="wallet-security-recover"]').attributes('disabled')).toBeDefined()
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('does not treat a rejected backend integrity check as a successful recovery', async () => {
    const wrapper = render()
    await flushPromises()
    mocks.post.mockRejectedValueOnce({ status: 409, message: 'wallet integrity check failed' })
    await confirmRecovery(wrapper)
    await wrapper.get('#crypto-security-code').setValue('654321')
    await flushPromises()
    expect(wrapper.text()).toContain('wallet integrity check failed')
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityLocked')
    expect(wrapper.emitted('recovered')).toBeUndefined()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('cancels fresh 2FA without changing any state', async () => {
    const wrapper = render()
    await flushPromises()
    await confirmRecovery(wrapper)
    await wrapper.get('[data-testid="crypto-security-cancel"]').trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.crypto.walletSecurityLocked')
    expect(wrapper.emitted('recovered')).toBeUndefined()
  })
})
