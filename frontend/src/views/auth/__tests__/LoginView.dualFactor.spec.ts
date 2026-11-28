import { defineComponent, h, onMounted } from 'vue'
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
  captchaReset: vi.fn(),
  paneSetError: vi.fn(),
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
  useI18n: () => ({
    t: (key: string) => key === 'auth.errors.INVALID_CREDENTIALS' ? '邮箱或密码不正确，请重试。' : key
  })
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

const TotpFactorPaneStub = {
  name: 'TotpFactorPane',
  props: ['tempToken', 'userEmailMasked', 'accountKey'],
  emits: ['verify'],
  methods: {
    setVerifying: () => undefined,
    setError: (...args: unknown[]) => mocks.paneSetError(...args),
    resetLimit: () => undefined
  },
  // the real pane renders its default slot (the page puts the back hint there), so the stand-in does too
  template: `<div><button type="button" data-testid="totp-verify" @click="$emit('verify', '123456')">totp</button><slot /></div>`
}

const PasskeyFactorPaneStub = {
  name: 'PasskeyFactorPane',
  props: ['supported', 'verifying', 'userEmailMasked'],
  emits: ['verify'],
  template: `<div><span data-testid="passkey-modal-hint">{{ supported ? 'supported' : 'unsupported' }}</span><button type="button" data-testid="passkey-verify" :disabled="!supported || verifying" @click="$emit('verify')">verify</button></div>`
}

// The real widget hands the page a one-time proof as soon as the user passes it; this one does it on mount.
const CaptchaStub = defineComponent({
  emits: ['verify', 'expire', 'error'],
  setup(_, { expose, emit }) {
    expose({ reset: mocks.captchaReset, verifyAction: vi.fn() })
    onMounted(() => emit('verify', 'proof-1'))
    return () => h('div', { 'data-testid': 'captcha' })
  }
})

/**
 * jsdom has no matchMedia. `reduced` turns prefers-reduced-motion on, which makes the step panes swap at once (the
 * animated sequencing has its own tests below); `finePointer` is a mouse/trackpad device (the email box is focused).
 */
function stubMedia({ reduced = true, finePointer = false } = {}) {
  window.matchMedia = ((query: string) => ({
    matches: (reduced && query.includes('prefers-reduced-motion')) || (finePointer && query.includes('pointer: fine')),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false
  })) as unknown as typeof window.matchMedia
}

function mountLogin(options: { attachTo?: HTMLElement } = {}) {
  return mount(LoginView, {
    ...options,
    global: {
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        DingTalkOAuthSection: true,
        EmailOAuthButtons: true,
        Icon: true,
        LinuxDoOAuthSection: true,
        LoginAgreementPrompt: true,
        OidcOAuthSection: true,
        PasskeyFactorPane: PasskeyFactorPaneStub,
        RouterLink: { template: '<a><slot /></a>' },
        TotpFactorPane: TotpFactorPaneStub,
        TurnstileWidget: CaptchaStub,
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

const paneState = (wrapper: ReturnType<typeof mountLogin>, testId: string) =>
  wrapper.get(`[data-testid="${testId}"]`).attributes('data-pane-state')

describe('登录双因子流程（TOTP / Passkey 策略）', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stubMedia()
    mocks.passkeySupported = true
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
  })

  it('只绑定 Passkey 的账号：密码后在卡片里进入第二步，验证成功后登录', async () => {
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
    expect(wrapper.find('[data-testid="login-step-head"]').exists()).toBe(false)
    await submitCredentials(wrapper)

    expect(mocks.login).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="passkey-verify"]').exists()).toBe(true)
    // the step head and the heading follow the step, like the sign-up page
    expect(wrapper.get('[data-testid="login-step-head"]').text()).toContain('auth.stepProgress')
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('auth.passkeyLoginTitle')
    // nothing covers the page: the second step is a pane of the same card, not a modal
    expect(wrapper.find('.fixed').exists()).toBe(false)

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

  it('两者都绑：先 TOTP 再 Passkey，全部通过才登录；TOTP 这一步不需要再点任何入口', async () => {
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
    expect(wrapper.find('[data-testid="login-authenticator"]').exists()).toBe(false)
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

  it('只绑 TOTP：密码后直接是卡片里的第二步（没有「使用认证器验证」按钮、没有提示句），验证后登录', async () => {
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

    expect(wrapper.find('[data-testid="login-authenticator"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('auth.totpChallengeHint')
    expect(wrapper.text()).not.toContain('auth.verifyWithAuthenticator')
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('profile.totp.loginTitle')
    expect(wrapper.get('[data-testid="login-step-head"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="passkey-verify"]').exists()).toBe(false)
    expect(paneState(wrapper, 'login-factor-pane')).toBe('active')
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('idle')

    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    await flushPromises()
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })

  it('密码填错的人也会进入第二步（服务端两种情况回同一个响应）：页面告诉他返回上一步，点上一步回到第一步且已填内容还在', async () => {
    mocks.login
      .mockResolvedValueOnce({ requires_2fa: true, temp_token: 'temp-challenge' })
      .mockResolvedValueOnce({ id: 1 })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(wrapper.get('[data-testid="login-challenge-back-hint"]').text()).toBe('auth.loginChallengeBackHint')
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(true)

    await wrapper.get('[data-testid="login-back"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="login-step-head"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('auth.welcomeBack')
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('active')
    expect((wrapper.get('input[type="email"]').element as HTMLInputElement).value).toBe('user@example.test')
    expect((wrapper.get('input[type="password"]').element as HTMLInputElement).value).toBe('correct-password')

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.login2FA).not.toHaveBeenCalled()
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })

  it('改了邮箱或密码（例如浏览器自动填充）就丢掉已有的挑战，回到第一步', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-challenge' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(true)

    await wrapper.get('input[type="password"]').setValue('updated-password')
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('active')
  })

  it('全站关闭 TOTP 时提示邮箱或密码错误，不出现第二步', async () => {
    mocks.login.mockRejectedValue({ status: 401, reason: 'INVALID_CREDENTIALS' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(mocks.showError).toHaveBeenCalledWith('邮箱或密码不正确，请重试。')
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(false)
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('第二步验证码不对：统一提示留在这一步，不跳走', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })
    mocks.login2FA.mockRejectedValue({ reason: 'INVALID_CREDENTIALS', retryAfter: 12 })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.paneSetError).toHaveBeenCalledWith('profile.totp.loginInvalidCredentials', 12)
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(true)
    expect(mocks.push).not.toHaveBeenCalled()
  })

  it('第二步校验进行中「上一步」不可点，结束后恢复', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })
    let finish: (value: unknown) => void = () => undefined
    mocks.login2FA.mockReturnValue(new Promise((resolve) => { finish = resolve }))

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(wrapper.get('[data-testid="login-back"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    expect(wrapper.get('[data-testid="login-back"]').attributes('disabled')).toBeDefined()

    finish({ access_token: 'token', token_type: 'Bearer', user: { id: 1 } })
    await flushPromises()
    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
  })
})

describe('登录页验证码（Turnstile）只在需要重来时重置', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stubMedia()
    mocks.passkeySupported = true
    mocks.getPublicSettings.mockResolvedValue({
      ...publicSettings,
      turnstile_enabled: true,
      turnstile_site_key: 'site-key'
    })
  })

  // Before this, every attempt reset the widget in `finally`, including the ones that went on to the second step:
  // the widget fell back to its spinner and the sign-in button stayed greyed out under the second step.
  it('拿到第二步挑战时不重置（验证码在第二步用不上，重置只会让它回到「正在验证」转圈）', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(mocks.login).toHaveBeenCalledWith(expect.objectContaining({ turnstile_token: 'proof-1' }))
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(true)
    expect(mocks.captchaReset).not.toHaveBeenCalled()
  })

  it('点「上一步」回到第一步时重置一次，拿新的验证（旧的已经随上一次提交用掉）', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    await wrapper.get('[data-testid="login-back"]').trigger('click')
    await flushPromises()

    expect(mocks.captchaReset).toHaveBeenCalledTimes(1)
  })

  it('第二步期间验证码过期不弹「验证已过期」（小组件在隐藏的第一格里，用户看不到也不需要它）', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(true)

    wrapper.findComponent(CaptchaStub).vm.$emit('expire')
    await flushPromises()
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('第一步期间验证码过期要提示（用户需要重新验证才能点登录）', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    wrapper.findComponent(CaptchaStub).vm.$emit('expire')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith('auth.turnstileExpired')
  })

  it('登录失败时重置一次，下一次提交要用新的验证', async () => {
    mocks.login.mockRejectedValue({ status: 401, reason: 'INVALID_CREDENTIALS' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(mocks.captchaReset).toHaveBeenCalledTimes(1)
  })

  it('登录成功跳转前不重置（页面马上离开，重置只会让小组件在跳转前闪一下）', async () => {
    mocks.login.mockResolvedValue({ id: 1 })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(mocks.push).toHaveBeenCalledWith('/dashboard')
    expect(mocks.captchaReset).not.toHaveBeenCalled()
  })
})

describe('登录页在手机上的输入框行为', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stubMedia()
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
  })

  // A focused input that becomes `disabled` loses focus, and on a phone that drops the soft keyboard; the viewport
  // resizes and the whole card jumps once per attempt. `readonly` keeps focus and the keyboard.
  it('提交过程中输入框只读而不是禁用', async () => {
    let finish: (value: unknown) => void = () => undefined
    mocks.login.mockReturnValue(new Promise((resolve) => { finish = resolve }))

    const wrapper = mountLogin()
    await flushPromises()
    await wrapper.get('input[type="email"]').setValue('user@example.test')
    await wrapper.get('input[type="password"]').setValue('correct-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    for (const selector of ['input[type="email"]', 'input[type="password"]']) {
      expect(wrapper.get(selector).attributes('readonly')).toBeDefined()
      expect(wrapper.get(selector).attributes('disabled')).toBeUndefined()
    }

    finish({ id: 1 })
    await flushPromises()
    expect(wrapper.get('input[type="email"]').attributes('readonly')).toBeUndefined()
  })

  it('邮箱框不再带 autofocus（手机一进页面就弹键盘）', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    expect(wrapper.get('input[type="email"]').attributes('autofocus')).toBeUndefined()
  })

  it('第二步里按回车不会再次提交第一步的凭据', async () => {
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })

    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(mocks.login).toHaveBeenCalledTimes(1)

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.login).toHaveBeenCalledTimes(1)
  })
})

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

describe('登录页两步之间的过渡（与注册页同一套：旧的一格先退出，再进新的一格，任何时刻只有一格在屏幕上）', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stubMedia({ reduced: false })
    mocks.passkeySupported = true
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })
  })

  it('前进：第一格先退出 150ms（这段时间第二格不在屏幕上、内容也没挂载），再换成第二格', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)

    expect(paneState(wrapper, 'login-credentials-pane')).toBe('leaving')
    expect(paneState(wrapper, 'login-factor-pane')).toBe('idle')
    // heading and step head still belong to the pane that is on screen
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('auth.welcomeBack')
    expect(wrapper.find('[data-testid="login-step-head"]').exists()).toBe(false)
    // the code box focuses itself when it mounts: it must not exist while its pane is hidden
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(false)

    await sleep(190)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('idle')
    expect(paneState(wrapper, 'login-factor-pane')).toBe('active')
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('profile.totp.loginTitle')
    expect(wrapper.find('[data-testid="login-step-head"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(true)
  })

  it('后退：第二格先退出（内容还在），再换回第一格，并在第一格回到屏幕后才重置验证码', async () => {
    mocks.getPublicSettings.mockResolvedValue({ ...publicSettings, turnstile_enabled: true, turnstile_site_key: 'site-key' })
    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    await sleep(190)
    expect(paneState(wrapper, 'login-factor-pane')).toBe('active')

    await wrapper.get('[data-testid="login-back"]').trigger('click')
    // the challenge is gone, but the pane still shows its content while it plays its exit
    expect(paneState(wrapper, 'login-factor-pane')).toBe('leaving')
    expect(wrapper.find('[data-testid="totp-verify"]').exists()).toBe(true)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('idle')
    expect(mocks.captchaReset).not.toHaveBeenCalled()

    await sleep(190)
    await flushPromises()
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('active')
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="login-heading"]').text()).toBe('auth.welcomeBack')
    expect(mocks.captchaReset).toHaveBeenCalledTimes(1)
  })

  it('第一格退出的这 150ms 里再按回车不会重复提交（登录请求只发一次）', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('leaving')

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.login).toHaveBeenCalledTimes(1)
  })

  it('退出过程中又改了账号（自动填充）：回到第一格，不会卡在两格之间', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('leaving')

    await wrapper.get('input[type="password"]').setValue('updated-password')
    await sleep(190)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('active')
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
  })

  it('退出过程中又改了账号时，已经用掉的验证码也要换新的（页面没离开第一格，但那次提交已经把它用掉了）', async () => {
    mocks.getPublicSettings.mockResolvedValue({ ...publicSettings, turnstile_enabled: true, turnstile_site_key: 'site-key' })
    const wrapper = mountLogin()
    await flushPromises()
    await submitCredentials(wrapper)
    expect(paneState(wrapper, 'login-credentials-pane')).toBe('leaving')
    expect(mocks.captchaReset).not.toHaveBeenCalled()

    await wrapper.get('input[type="password"]').setValue('updated-password')
    await flushPromises()
    expect(mocks.captchaReset).toHaveBeenCalledTimes(1)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it('第一次进页面第一格不再播自己的入场动画（卡片自己已经在升起），回来之后才播', async () => {
    const wrapper = mountLogin()
    await flushPromises()
    expect(wrapper.get('[data-testid="login-credentials-pane"]').classes()).toContain('login-pane-static')

    await submitCredentials(wrapper)
    expect(wrapper.get('[data-testid="login-credentials-pane"]').classes()).not.toContain('login-pane-static')
  })
})

describe('回到第一步时的光标（输错密码的人一键改回来）', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    stubMedia()
    mocks.passkeySupported = true
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
    mocks.login.mockResolvedValue({ requires_2fa: true, temp_token: 'temp-totp', required_factors: ['totp'], next_factor: 'totp' })
  })

  async function mountAttached() {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const wrapper = mountLogin({ attachTo: host })
    await flushPromises()
    await submitCredentials(wrapper)
    return { wrapper, host }
  }

  it('提示那一行本身就是返回按钮：点它回到第一步，光标在密码框里且内容被选中（直接重新输入就行）', async () => {
    const { wrapper, host } = await mountAttached()
    const hint = wrapper.get('[data-testid="login-challenge-back-hint"]')
    expect(hint.element.tagName).toBe('BUTTON')

    await hint.trigger('click')
    await flushPromises()

    const password = wrapper.get('input[type="password"]').element as HTMLInputElement
    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(document.activeElement).toBe(password)
    expect([password.selectionStart, password.selectionEnd]).toEqual([0, 'correct-password'.length])
    wrapper.unmount()
    host.remove()
  })

  it('底部的「上一步」同样把光标放进密码框', async () => {
    const { wrapper, host } = await mountAttached()
    await wrapper.get('[data-testid="login-back"]').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('input[type="password"]').element)
    wrapper.unmount()
    host.remove()
  })

  it('不是用户主动返回的（自动填充改了账号导致丢掉挑战）不抢光标', async () => {
    const { wrapper, host } = await mountAttached()
    const email = wrapper.get('input[type="email"]').element as HTMLInputElement
    email.focus()
    await wrapper.get('input[type="password"]').setValue('autofilled-password')
    await flushPromises()

    expect(wrapper.find('[data-testid="login-factor-pane"]').exists()).toBe(false)
    expect(document.activeElement).not.toBe(wrapper.get('input[type="password"]').element)
    wrapper.unmount()
    host.remove()
  })

  it('校验验证码的过程中提示行也不能点（和「上一步」一样）', async () => {
    mocks.login2FA.mockReturnValue(new Promise(() => undefined))
    const { wrapper, host } = await mountAttached()
    await wrapper.get('[data-testid="totp-verify"]').trigger('click')
    expect(wrapper.get('[data-testid="login-challenge-back-hint"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
    host.remove()
  })
})

describe('登录页的初始焦点', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.getPublicSettings.mockResolvedValue(publicSettings)
  })

  it('鼠标/触控板设备：设置加载完、输入框可用后聚焦邮箱框', async () => {
    stubMedia({ finePointer: true })
    const host = document.createElement('div')
    document.body.appendChild(host)
    const wrapper = mountLogin({ attachTo: host })
    await flushPromises()
    expect(document.activeElement).toBe(wrapper.get('input[type="email"]').element)
    wrapper.unmount()
    host.remove()
  })

  it('手机（粗指针）：不主动聚焦，不弹键盘', async () => {
    stubMedia({ finePointer: false })
    const host = document.createElement('div')
    document.body.appendChild(host)
    const wrapper = mountLogin({ attachTo: host })
    await flushPromises()
    expect(document.activeElement).not.toBe(wrapper.get('input[type="email"]').element)
    wrapper.unmount()
    host.remove()
  })
})
