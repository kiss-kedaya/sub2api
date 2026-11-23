import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import LoginView from '@/views/auth/LoginView.vue'

const mocks = vi.hoisted(() => ({
  getPublicSettings: vi.fn(),
  push: vi.fn(),
  login: vi.fn(),
  login2FA: vi.fn(),
  login2FAPasskeyFinish: vi.fn(),
  loginWithPasskey: vi.fn(),
  acceptLoginChallenge: vi.fn(),
  passkeySupported: true,
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn()
}))

const publicSettings = {
  registration_enabled: true,
  turnstile_enabled: false,
  turnstile_site_key: '',
  tencent_captcha_enabled: false,
  tencent_captcha_app_id: '',
  aliyun_captcha_enabled: false,
  aliyun_captcha_scene_id: '',
  aliyun_captcha_prefix: '',
  linuxdo_oauth_enabled: false,
  dingtalk_oauth_enabled: false,
  wechat_oauth_enabled: false,
  backend_mode_enabled: false,
  oidc_oauth_enabled: false,
  oidc_oauth_provider_name: 'OIDC',
  github_oauth_enabled: false,
  google_oauth_enabled: false,
  password_reset_enabled: false,
  passkey_enabled: false,
  login_agreement_enabled: false,
  login_agreement_documents: []
}

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: mocks.push,
    currentRoute: { value: { query: {} } }
  })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({ global: { t: (key: string) => key } }),
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    login: mocks.login,
    login2FA: mocks.login2FA,
    login2FAPasskeyFinish: mocks.login2FAPasskeyFinish,
    loginWithPasskey: mocks.loginWithPasskey
  }),
  useAppStore: () => ({
    showError: mocks.showError,
    showSuccess: mocks.showSuccess,
    showWarning: mocks.showWarning
  })
}))

vi.mock('@/api/passkey', () => ({
  passkeyAPI: {
    isSupported: () => mocks.passkeySupported,
    acceptLoginChallenge: mocks.acceptLoginChallenge
  }
}))

vi.mock('@/api/auth', () => ({
  buildOAuthLoginStartURL: vi.fn(),
  getPublicSettings: (...args: unknown[]) => mocks.getPublicSettings(...args),
  isTotp2FARequired: (response: { requires_2fa?: boolean } | null) => response?.requires_2fa === true,
  isWeChatWebOAuthEnabled: vi.fn(() => false),
  startOAuthLogin: vi.fn()
}))

const TotpLoginModalStub = {
  name: 'TotpLoginModal',
  props: ['tempToken', 'userEmailMasked', 'accountKey'],
  emits: ['verify', 'cancel'],
  methods: {
    setVerifying: () => undefined,
    setError: () => undefined,
    resetLimit: () => undefined
  },
  template: `<button data-testid="totp-verify" @click="$emit('verify', '123456')">totp</button>`
}

const PasskeyLoginModalStub = {
  name: 'PasskeyLoginModal',
  props: ['supported', 'verifying', 'userEmailMasked'],
  emits: ['verify', 'cancel'],
  template: `<div><span data-testid="passkey-modal-hint">{{ supported ? 'supported' : 'unsupported' }}</span><button data-testid="passkey-verify" :disabled="!supported || verifying" @click="$emit('verify')">verify</button></div>`
}

function mountLogin() {
  return mount(LoginView, {
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        DingTalkOAuthSection: true,
        EmailOAuthButtons: true,
        Icon: true,
        LinuxDoOAuthSection: true,
        LoginAgreementPrompt: true,
        OidcOAuthSection: true,
        PasskeyLoginModal: PasskeyLoginModalStub,
        RouterLink: { template: '<a><slot /></a>' },
        TotpLoginModal: TotpLoginModalStub,
        TurnstileWidget: true,
        WechatOAuthSection: true,
        transition: false
      }
    }
  })
}

async function submitCredentials(wrapper: ReturnType<typeof mountLogin>) {
  await wrapper.get('input[type="email"]').setValue('user@example.test')
  await wrapper.get('input[type="password"]').setValue('correct-password')
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

describe('登录双因子流程（TOTP / Passkey 策略）', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.passkeySupported = true
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
  })

  it('只看绑定 Passkey 的账号：密码后弹 Passkey，验证成功后登录', async () => {
    mocks.login.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temp-passkey-only',
      required_factors: ['passkey'],
      next_factor: 'passkey',
      passkey_session_token: 'passkey-session',
      passkey_options: { publicKey: { challenge: 'AA' } }
    })
    mocks.acceptLoginChallenge.mockResolvedValue({ id: 'credential' })
    mocks.login2FAPasskeyFinish.mockResolvedValue({ id: 1 })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(mocks.login).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="passkey-verify"]').exists()).toBe(true)

    await wrapper.get('[data-testid="passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.acceptLoginChallenge).toHaveBeenCalledWith({ publicKey: { challenge: 'AA' } })
    expect(mocks.login2FAPasskeyFinish).toHaveBeenCalledWith(
      'temp-passkey-only',
      'passkey-session',
      { id: 'credential' }
    )
    expect(mocks.showSuccess).toHaveBeenCalledWith('auth.loginSuccess')
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })

  it('两者都绑：先 TOTP 再 Passkey，全部通过才登录', async () => {
    mocks.login.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temp-both',
      required_factors: ['totp', 'passkey'],
      next_factor: 'totp'
    })
    mocks.login2FA.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temp-both',
      required_factors: ['totp', 'passkey'],
      passed_factors: ['totp'],
      next_factor: 'passkey',
      passkey_session_token: 'passkey-session-2',
      passkey_options: { publicKey: { challenge: 'BB' } }
    })
    mocks.acceptLoginChallenge.mockResolvedValue({ id: 'credential-2' })
    mocks.login2FAPasskeyFinish.mockResolvedValue({ id: 1 })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(true)

    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.login2FA).toHaveBeenCalledWith('temp-both', '123456', 'user@example.test', 'correct-password')
    expect(mocks.push).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="passkey-verify"]').exists()).toBe(true)

    await wrapper.get('[data-testid="passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.login2FAPasskeyFinish).toHaveBeenCalledWith(
      'temp-both',
      'passkey-session-2',
      { id: 'credential-2' }
    )
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })

  it('浏览器不支持 Passkey：展示降级文案且验证按钮禁用', async () => {
    mocks.passkeySupported = false
    mocks.login.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temp-unsupported',
      required_factors: ['passkey'],
      next_factor: 'passkey',
      passkey_session_token: 'passkey-session',
      passkey_options: { publicKey: { challenge: 'CC' } }
    })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(wrapper.find('[data-testid="passkey-modal-hint"]').text()).toBe('unsupported')
    expect(wrapper.get('[data-testid="passkey-verify"]').attributes('disabled')).toBeDefined()
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('只绑 TOTP：保持原有 TOTP 单因子流程', async () => {
    mocks.login.mockResolvedValue({
      requires_2fa: true,
      temp_token: 'temp-totp',
      required_factors: ['totp'],
      next_factor: 'totp'
    })
    mocks.login2FA.mockResolvedValue({ access_token: 'token', token_type: 'Bearer', user: { id: 1 } })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="passkey-verify"]').exists()).toBe(false)

    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    await flushPromises()
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })
})
