import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import OAuthCallbackView from '@/views/auth/OAuthCallbackView.vue'

const {
  routeState,
  locationState,
  routerReplaceMock,
  showErrorMock,
  showSuccessMock,
  setTokenMock,
  adoptCookieSessionMock,
  copyToClipboardMock,
  exchangePendingOAuthCompletionMock,
  apiPostMock,
} = vi.hoisted(() => ({
  routeState: {
    path: '/auth/callback',
    query: {} as Record<string, unknown>,
  },
  locationState: {
    current: {
      href: 'http://localhost/auth/callback',
      hash: '',
    } as { href: string; hash: string },
  },
  routerReplaceMock: vi.fn(),
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn(),
  setTokenMock: vi.fn(),
  adoptCookieSessionMock: vi.fn(),
  copyToClipboardMock: vi.fn(),
  exchangePendingOAuthCompletionMock: vi.fn(),
  apiPostMock: vi.fn(),
}))

vi.mock('vue-router', () => ({
  useRoute: () => routeState,
  useRouter: () => ({
    replace: (...args: any[]) => routerReplaceMock(...args),
  }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    setToken: (...args: any[]) => setTokenMock(...args),
    adoptCookieSession: (...args: any[]) => adoptCookieSessionMock(...args),
  }),
  useAppStore: () => ({
    showError: (...args: any[]) => showErrorMock(...args),
    showSuccess: (...args: any[]) => showSuccessMock(...args),
  }),
}))

vi.mock('@/api/client', () => ({
  apiClient: {
    post: (...args: any[]) => apiPostMock(...args),
  },
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    exchangePendingOAuthCompletion: (...args: any[]) => exchangePendingOAuthCompletionMock(...args),
    persistOAuthTokenContext: vi.fn(),
  }
})

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: (...args: any[]) => copyToClipboardMock(...args),
  }),
}))

describe('OAuthCallbackView', () => {
  beforeEach(() => {
    routeState.path = '/auth/callback'
    routeState.query = {}
    locationState.current = {
      href: 'http://localhost/auth/callback',
      hash: '',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState.current,
    })
    routerReplaceMock.mockReset()
    showErrorMock.mockReset()
    showSuccessMock.mockReset()
    setTokenMock.mockReset()
    adoptCookieSessionMock.mockReset()
    copyToClipboardMock.mockReset()
    exchangePendingOAuthCompletionMock.mockReset()
    apiPostMock.mockReset()
    window.sessionStorage.clear()
    vi.spyOn(window.history, 'replaceState').mockImplementation(() => {})
  })
  afterEach(() => vi.restoreAllMocks())

  it('renders localized callback copy actions', () => {
    routeState.query = {
      code: 'oauth-code',
      state: 'oauth-state',
    }

    const wrapper = mount(OAuthCallbackView)

    expect(wrapper.text()).toContain('auth.oauth.callbackTitle')
    expect(wrapper.text()).toContain('auth.oauth.callbackHint')
    expect(wrapper.text()).toContain('common.copy')
    expect(wrapper.find('input[value="oauth-code"]').exists()).toBe(true)
    expect(wrapper.find('input[value="oauth-state"]').exists()).toBe(true)
  })

  it('shows query errors with a return to login instead of manual code fields', () => {
    routeState.query = {
      error: 'oauth failed',
    }

    const wrapper = mount(OAuthCallbackView)

    expect(wrapper.text()).toContain('oauth failed')
    expect(wrapper.text()).toContain('auth.backToLogin')
    expect(wrapper.find('input[readonly]').exists()).toBe(false)
    expect(wrapper.find('.bg-red-50').exists()).toBe(false)
  })

  it.each(['github', 'google'] as const)('shows a recoverable error for a %s state rejection without the legacy copy form', async () => {
    routeState.path = '/auth/oauth/callback'
    locationState.current.hash = '#error=invalid_state&error_message=invalid+oauth+state'
    const wrapper = mount(OAuthCallbackView)
    await vi.dynamicImportSettled()

    expect(wrapper.text()).toContain('auth.oauth.invalidCallbackTitle')
    expect(wrapper.text()).toContain('invalid oauth state')
    expect(wrapper.text()).toContain('auth.backToLogin')
    expect(wrapper.find('input[readonly]').exists()).toBe(false)
    expect(exchangePendingOAuthCompletionMock).not.toHaveBeenCalled()
  })

  it('adopts the BFF cookie session after the callback marker and strips the fragment first', async () => {
    Object.assign(locationState.current, {
      href: 'http://localhost/auth/oauth/callback?source=oauth',
      hash: '#session_managed=1&redirect=%2Fsettings',
      pathname: '/auth/oauth/callback',
      search: '?source=oauth',
    })
    adoptCookieSessionMock.mockResolvedValue({ id: 7 })

    mount(OAuthCallbackView)
    await vi.dynamicImportSettled()

    expect(window.history.replaceState).toHaveBeenCalledWith(window.history.state, '', '/auth/oauth/callback?source=oauth')
    expect(adoptCookieSessionMock).toHaveBeenCalledTimes(1)
    expect(setTokenMock).not.toHaveBeenCalled()
    expect(routerReplaceMock).toHaveBeenCalledWith('/settings')
  })

  it('does not render manual copy fields for direct email oauth callback visits', async () => {
    routeState.path = '/auth/oauth/callback'
    exchangePendingOAuthCompletionMock.mockRejectedValue(new Error('pending session not found'))

    const wrapper = mount(OAuthCallbackView)
    await vi.dynamicImportSettled()

    expect(exchangePendingOAuthCompletionMock).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('auth.oauth.invalidCallbackTitle')
    expect(wrapper.text()).toContain('auth.oauth.invalidCallbackHint')
    expect(wrapper.find('input[readonly]').exists()).toBe(false)
  })

  it.each(['github', 'google'])('adopts a %s pending completion owned by the BFF', async (provider) => {
    routeState.path = '/auth/oauth/callback'
    window.sessionStorage.setItem('email_oauth_pending_provider', provider)
    exchangePendingOAuthCompletionMock.mockResolvedValue({ access_token: '', session_managed: true, redirect: '/settings' })
    adoptCookieSessionMock.mockResolvedValue({ id: 7 })
    mount(OAuthCallbackView)
    await vi.dynamicImportSettled()
    expect(adoptCookieSessionMock).toHaveBeenCalledTimes(1)
    expect(setTokenMock).not.toHaveBeenCalled()
    expect(window.sessionStorage.getItem('email_oauth_pending_provider')).toBeNull()
    expect(routerReplaceMock).toHaveBeenCalledWith('/settings')
  })

  it('forwards frontend email oauth provider callbacks back to the backend callback endpoint', async () => {
    routeState.path = '/auth/oauth/callback'
    routeState.query = {
      code: 'provider-code',
      state: 'provider-state',
    }
    window.sessionStorage.setItem('email_oauth_pending_provider', 'google')

    mount(OAuthCallbackView)
    await vi.dynamicImportSettled()

    expect(locationState.current.href).toBe(
      '/api/v1/auth/oauth/google/callback?code=provider-code&state=provider-state'
    )
    expect(exchangePendingOAuthCompletionMock).not.toHaveBeenCalled()
  })

  it('submits stored affiliate code when completing invited email oauth registration', async () => {
    routeState.path = '/auth/oauth/callback'
    exchangePendingOAuthCompletionMock.mockResolvedValue({
      error: 'invitation_required',
      provider: 'google',
      redirect: '/dashboard',
      resolved_email: 'pending@example.com',
      invitation_required: true,
    })
    apiPostMock.mockResolvedValue({
      data: {
        access_token: 'token-1',
      },
    })
    window.sessionStorage.setItem('oauth_aff_code', 'AFF456')

    const wrapper = mount(OAuthCallbackView)
    await vi.dynamicImportSettled()
    const passwordInputs = wrapper.findAll('input[type="password"]')
    await passwordInputs[0].setValue('secret-123')
    await passwordInputs[1].setValue('secret-123')
    const invitationInput = wrapper.find('input[type="text"]')
    await invitationInput.setValue('INVITE456')
    await wrapper.findAll('button').at(0)?.trigger('click')

    expect(apiPostMock).toHaveBeenCalledWith('/auth/oauth/google/complete-registration', {
      password: 'secret-123',
      invitation_code: 'INVITE456',
      aff_code: 'AFF456',
    })
    expect(setTokenMock).toHaveBeenCalledWith('token-1')
  })

  it('completes email oauth registration with readonly email and without posting email', async () => {
    routeState.path = '/auth/oauth/callback'
    exchangePendingOAuthCompletionMock.mockResolvedValue({
      error: 'registration_completion_required',
      provider: 'github',
      redirect: '/dashboard',
      resolved_email: 'verified@example.com',
      invitation_required: false,
    })
    apiPostMock.mockResolvedValue({
      data: {
        access_token: 'token-2',
      },
    })

    const wrapper = mount(OAuthCallbackView)
    await vi.dynamicImportSettled()

    const emailInput = wrapper.find('input[type="email"]')
    expect(emailInput.exists()).toBe(true)
    expect((emailInput.element as HTMLInputElement).value).toBe('verified@example.com')
    expect(emailInput.attributes('readonly')).toBeDefined()
    expect(emailInput.attributes('disabled')).toBeDefined()

    const passwordInputs = wrapper.findAll('input[type="password"]')
    await passwordInputs[0].setValue('secret-456')
    await passwordInputs[1].setValue('secret-456')
    await wrapper.findAll('button').at(0)?.trigger('click')

    expect(apiPostMock).toHaveBeenCalledWith('/auth/oauth/github/complete-registration', {
      password: 'secret-456',
    })
    expect(apiPostMock.mock.calls[0][1]).not.toHaveProperty('email')
    expect(setTokenMock).toHaveBeenCalledWith('token-2')
  })
})
