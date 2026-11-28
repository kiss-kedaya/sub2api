import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DingTalkCallbackView from '../DingTalkCallbackView.vue'

const replace = vi.fn()
const showSuccess = vi.fn()
const showError = vi.fn()
const setToken = vi.fn()
const setPendingAuthSession = vi.fn()
const clearPendingAuthSession = vi.fn()
const exchangePendingOAuthCompletion = vi.fn()
const exchangeOAuthLoginCode = vi.fn()
const getPublicSettings = vi.fn()
const login2FA = vi.fn()
const login2FAPasskeyFinish = vi.fn()
const acceptLoginChallenge = vi.fn()
const apiClientPost = vi.fn()

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: {}
  }),
  useRouter: () => ({
    replace
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) => {
        if (key === 'auth.oauthFlow.totpHint') {
          return `verify ${params?.account ?? ''}`.trim()
        }
        return key
      }
    })
  }
})

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    setToken,
    setPendingAuthSession,
    clearPendingAuthSession
  }),
  useAppStore: () => ({
    showSuccess,
    showError
  })
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post: (...args: any[]) => apiClientPost(...args)
  }
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    exchangeOAuthLoginCode: (...args: any[]) => exchangeOAuthLoginCode(...args),
    exchangePendingOAuthCompletion: (...args: any[]) => exchangePendingOAuthCompletion(...args),
    getPublicSettings: (...args: any[]) => getPublicSettings(...args),
    login2FA: (...args: any[]) => login2FA(...args),
    login2FAPasskeyFinish: (...args: any[]) => login2FAPasskeyFinish(...args)
  }
})

vi.mock('@/api/passkey', () => ({
  passkeyAPI: {
    isSupported: () => true,
    acceptLoginChallenge: (...args: any[]) => acceptLoginChallenge(...args)
  }
}))

const mountDingTalk = () =>
  mount(DingTalkCallbackView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /></div>' },
        Icon: true,
        RouterLink: { template: '<a><slot /></a>' },
        transition: false
      }
    }
  })

describe('DingTalkCallbackView', () => {
  beforeEach(() => {
    replace.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    setToken.mockReset()
    setPendingAuthSession.mockReset()
    clearPendingAuthSession.mockReset()
    exchangeOAuthLoginCode.mockReset()
    exchangePendingOAuthCompletion.mockReset()
    getPublicSettings.mockReset()
    login2FA.mockReset()
    login2FAPasskeyFinish.mockReset()
    acceptLoginChallenge.mockReset()
    apiClientPost.mockReset()
    getPublicSettings.mockResolvedValue({
      turnstile_enabled: false,
      turnstile_site_key: ''
    })
    window.location.hash = ''
    localStorage.clear()
    sessionStorage.clear()
  })

  it('exchanges one-time fragment code for token pair without pending exchange', async () => {
    window.location.hash = '#code=dt-one-time-code&redirect=%2Fsettings'
    exchangeOAuthLoginCode.mockResolvedValue({
      access_token: 'dt-code-access',
      refresh_token: 'dt-code-refresh',
      expires_in: 3600,
      redirect: '/settings'
    })
    setToken.mockResolvedValue({})

    mountDingTalk()
    await flushPromises()

    expect(exchangeOAuthLoginCode).toHaveBeenCalledWith('dt-one-time-code')
    expect(exchangePendingOAuthCompletion).not.toHaveBeenCalled()
    expect(setToken).toHaveBeenCalledWith('dt-code-access')
    expect(replace).toHaveBeenCalledWith('/settings')
  })

  it('completes passkey-only 2FA challenge returned from pending exchange', async () => {
    const pkOptions = { publicKey: { challenge: 'dt-pk-only-chal' } }
    exchangePendingOAuthCompletion.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'dt-pk-only-temp',
      user_email_masked: 'p***k@example.com',
      required_factors: ['passkey'],
      passed_factors: [],
      next_factor: 'passkey',
      passkey_session_token: 'dt-pk-only-sess',
      passkey_options: pkOptions,
      redirect: '/dashboard'
    })
    acceptLoginChallenge.mockResolvedValue({ id: 'dt-cred-1', type: 'public-key' })
    login2FAPasskeyFinish.mockResolvedValue({
      access_token: 'dt-pk-only-access',
      refresh_token: 'dt-pk-only-refresh',
      expires_in: 3600
    })
    setToken.mockResolvedValue({})

    const wrapper = mountDingTalk()
    await flushPromises()

    expect(wrapper.find('[data-testid="login-passkey-pane"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="dingtalk-bind-login-totp"]').exists()).toBe(false)

    await wrapper.get('[data-testid="passkey-login-verify"]').trigger('click')
    await flushPromises()

    expect(login2FAPasskeyFinish).toHaveBeenCalledWith({
      temp_token: 'dt-pk-only-temp',
      session_token: 'dt-pk-only-sess',
      credential: { id: 'dt-cred-1', type: 'public-key' }
    })
    expect(setToken).toHaveBeenCalledWith('dt-pk-only-access')
    expect(replace).toHaveBeenCalledWith('/dashboard')
  })

  it('progresses from TOTP to passkey during dual-factor 2FA challenge', async () => {
    const pkOptions = { publicKey: { challenge: 'dt-dual-chal' } }
    exchangePendingOAuthCompletion.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'dt-dual-temp',
      user_email_masked: 'd***l@example.com',
      required_factors: ['totp', 'passkey'],
      passed_factors: [],
      next_factor: 'totp',
      redirect: '/profile'
    })
    login2FA.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'dt-dual-temp',
      user_email_masked: 'd***l@example.com',
      required_factors: ['totp', 'passkey'],
      passed_factors: ['totp'],
      next_factor: 'passkey',
      passkey_session_token: 'dt-dual-pk-sess',
      passkey_options: pkOptions
    })
    acceptLoginChallenge.mockResolvedValue({ id: 'dt-dual-cred', type: 'public-key' })
    login2FAPasskeyFinish.mockResolvedValue({ access_token: 'dt-dual-token' })
    setToken.mockResolvedValue({})

    const wrapper = mountDingTalk()
    await flushPromises()

    await wrapper.get('[data-testid="dingtalk-bind-login-totp"]').setValue('123456')
    await wrapper.get('[data-testid="dingtalk-bind-login-totp-submit"]').trigger('click')
    await flushPromises()

    expect(setToken).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="login-passkey-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="passkey-login-verify"]').trigger('click')
    await flushPromises()

    expect(login2FAPasskeyFinish).toHaveBeenCalledWith({
      temp_token: 'dt-dual-temp',
      session_token: 'dt-dual-pk-sess',
      credential: { id: 'dt-dual-cred', type: 'public-key' }
    })
    expect(setToken).toHaveBeenCalledWith('dt-dual-token')
    expect(replace).toHaveBeenCalledWith('/profile')
  })

  it('does not disable bind-login submit when turnstile_enabled is true but turnstile_site_key is empty', async () => {
    getPublicSettings.mockResolvedValue({
      turnstile_enabled: true,
      turnstile_site_key: ''
    })
    exchangePendingOAuthCompletion.mockResolvedValue({
      error: 'adopt_existing_user_by_email',
      redirect: '/dashboard',
      email: 'existing@example.com'
    })
    apiClientPost.mockResolvedValue({
      data: {
        access_token: 'dt-bind-access',
        refresh_token: 'dt-bind-refresh',
        expires_in: 3600
      }
    })
    setToken.mockResolvedValue({})

    const wrapper = mountDingTalk()
    await flushPromises()

    await wrapper.get('[data-testid="dingtalk-bind-login-password"]').setValue('secret-password')
    const submitBtn = wrapper.get('[data-testid="dingtalk-bind-login-submit"]')
    expect((submitBtn.element as HTMLButtonElement).disabled).toBe(false)

    await submitBtn.trigger('click')
    await flushPromises()

    expect(apiClientPost).toHaveBeenCalledWith('/auth/oauth/pending/bind-login', {
      email: 'existing@example.com',
      password: 'secret-password',
      adopt_display_name: false,
      adopt_avatar: false
    })
    expect(setToken).toHaveBeenCalledWith('dt-bind-access')
  })
})
