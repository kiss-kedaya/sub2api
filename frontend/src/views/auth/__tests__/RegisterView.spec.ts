import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import RegisterView from '@/views/auth/RegisterView.vue'

const {
  getPublicSettingsMock,
  registerMock,
  showErrorMock,
  showSuccessMock,
  sendVerifyCodeMock,
  pushMock,
  verifyActionMock,
  appStoreMock,
  routeQueryMock
} = vi.hoisted(() => ({
  getPublicSettingsMock: vi.fn(),
  registerMock: vi.fn(),
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn(),
  sendVerifyCodeMock: vi.fn(),
  pushMock: vi.fn(),
  verifyActionMock: vi.fn(),
  routeQueryMock: { value: {} as Record<string, string> },
  appStoreMock: {
    cachedPublicSettings: null as { promo_code_enabled?: boolean } | null,
    showError: (...args: unknown[]) => showErrorMock(...args),
    showSuccess: (...args: unknown[]) => showSuccessMock(...args),
    showWarning: vi.fn()
  }
}))

const publicSettings = {
  registration_enabled: true,
  email_verify_enabled: false,
  promo_code_enabled: false,
  invitation_code_enabled: false,
  affiliate_enabled: true,
  turnstile_enabled: true,
  turnstile_site_key: 'site-key',
  site_name: 'Sub2API',
  registration_email_suffix_whitelist: [],
  linuxdo_oauth_enabled: false,
  wechat_oauth_enabled: false,
  oidc_oauth_enabled: false,
  github_oauth_enabled: false,
  google_oauth_enabled: false
}

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock }),
  useRoute: () => ({ query: routeQueryMock.value })
}))

vi.mock('vue-i18n', () => ({
  createI18n: () => ({
    global: {
      t: (key: string) => key
    }
  }),
  useI18n: () => ({
    t: (key: string) =>
      key === 'auth.emailDomainRegistrationLimit'
        ? '该邮箱域名无法注册新账户。请使用主流邮箱注册；如需使用企业邮箱，请联系客服添加域名白名单。'
        : key,
    locale: { value: 'en' }
  })
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({ register: (...args: unknown[]) => registerMock(...args) }),
  useAppStore: () => appStoreMock
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    getPublicSettings: (...args: unknown[]) => getPublicSettingsMock(...args),
    sendVerifyCode: (...args: unknown[]) => sendVerifyCodeMock(...args)
  }
})

function mountRegister(options: { realAgreementPrompt?: boolean } = {}) {
  return mount(RegisterView, {
    global: {
      // The real LoginAgreementPrompt renders document titles through RouterLink,
      // so the stub must keep its default slot content for these specs.
      renderStubDefaultSlot: options.realAgreementPrompt === true,
      stubs: {
        AuthLayout: { template: '<div><slot /><slot name="footer" /></div>' },
        Icon: true,
        TurnstileWidget: {
          template: '<div data-testid="turnstile-widget" />',
          methods: { verifyAction: verifyActionMock, reset: vi.fn() }
        },
        LoginAgreementPrompt: options.realAgreementPrompt ? false : true,
        EmailOAuthButtons: true,
        LinuxDoOAuthSection: true,
        WechatOAuthSection: true,
        OidcOAuthSection: true,
        RouterLink: true,
        transition: false
      }
    }
  })
}

async function clickStepAction(wrapper: ReturnType<typeof mountRegister>): Promise<void> {
  await wrapper.get('.auth-step-footer .btn-primary').trigger('click')
  await flushPromises()
}

async function reachVerifyStep(wrapper: ReturnType<typeof mountRegister>): Promise<void> {
  await wrapper.get('#email').setValue('user@example.com')
  await wrapper.get('#password').setValue('secret-123')
  // account -> verify is a single swap (prefers-reduced-motion is mocked as
  // true, so the step transition is synchronous). Loop defensively so the helper
  // does not depend on how many panes the flow currently has.
  let guard = 0
  while (!wrapper.find('button[type="submit"]').exists() && guard < 4) {
    await clickStepAction(wrapper)
    guard += 1
  }
}

describe('RegisterView invitation layout', () => {
  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    showErrorMock.mockReset()
    pushMock.mockReset()
    verifyActionMock.mockReset()
    routeQueryMock.value = {}
    appStoreMock.cachedPublicSettings = null
    sessionStorage.removeItem('register_data')
    verifyActionMock.mockResolvedValue({ token: 'ticket', randstr: 'randstr' })
    getPublicSettingsMock.mockResolvedValue(publicSettings)
    registerMock.mockResolvedValue({})
  })

  it.each([
    { enabled: true, email: 'signup+tag@example.com', rejected: true },
    { enabled: false, email: 'signup+tag@example.com', rejected: false },
    { enabled: true, email: 'first.last@example.com', rejected: false }
  ])('enforces signup alias policy without rejecting ordinary distinct mailboxes ($email / $enabled)', async ({ enabled, email, rejected }) => {
    getPublicSettingsMock.mockResolvedValueOnce({ ...publicSettings, turnstile_enabled: false, registration_email_alias_block_enabled: enabled })
    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue(email)
    await wrapper.get('#password').setValue('valid-test-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    if (rejected) {
      expect(registerMock).not.toHaveBeenCalled()
      expect(showErrorMock).toHaveBeenCalledWith('auth.emailAliasNotAllowed')
    } else {
      expect(registerMock).toHaveBeenCalledWith(expect.objectContaining({ email }))
    }
    wrapper.unmount()
  })

  it('does not flash the promo-code field before disabled settings finish loading', async () => {
    let resolveSettings!: (settings: typeof publicSettings) => void
    getPublicSettingsMock.mockReturnValueOnce(
      new Promise<typeof publicSettings>((resolve) => {
        resolveSettings = resolve
      })
    )

    const wrapper = mountRegister()

    expect(wrapper.find('#promo_code').exists()).toBe(false)

    resolveSettings(publicSettings)
    await flushPromises()

    expect(wrapper.find('#promo_code').exists()).toBe(false)
  })

  it('uses injected public settings to show an enabled promo-code field on first render', () => {
    appStoreMock.cachedPublicSettings = { promo_code_enabled: true }
    getPublicSettingsMock.mockReturnValueOnce(new Promise(() => {}))

    const wrapper = mountRegister()

    expect(wrapper.find('#promo_code').exists()).toBe(true)
  })

  it('keeps the optional affiliate invitation field on the first (account) step, before Turnstile', async () => {
    const wrapper = mountRegister()
    await flushPromises()

    // 邀请码合并进第 1 步：不再是独立的一步。
    const accountPane = wrapper.get('[data-testid="register-account-pane"]')
    const invitationField = accountPane.get('[data-testid="affiliate-invitation-field"]')
    const turnstile = wrapper.get('[data-testid="registration-turnstile"]')

    expect(invitationField.get('input').attributes('id')).toBe('affiliate_code')
    expect(invitationField.text()).toContain('common.optional')
    expect(
      invitationField.element.compareDocumentPosition(turnstile.element) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    // 账号 + 验证 = 2 步，没有额外的邀请码步
    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(2)
    expect(wrapper.find('[data-pane-state="details"]').exists()).toBe(false)
  })

  it('uses the mandatory invitation field without duplicating the affiliate field', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      invitation_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.find('[data-testid="affiliate-invitation-field"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="register-account-pane"]').get('#invitation_code').exists()).toBe(true)
    // 账号（含邀请码）+ 验证
    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(2)
  })

  it('collapses to a single step when invitation/promo is enabled but no verify step is required', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      login_agreement_enabled: false,
      invitation_code_enabled: false,
      affiliate_enabled: true,
      promo_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    const accountPane = wrapper.get('[data-testid="register-account-pane"]')
    expect(accountPane.get('#affiliate_code').exists()).toBe(true)
    expect(accountPane.get('#promo_code').exists()).toBe(true)
    // 只有一步：不显示第 N 步/进度点/上一步
    expect(wrapper.find('[data-testid="register-step-head"]').exists()).toBe(false)
    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(0)
    expect(wrapper.find('.auth-step-back').exists()).toBe(false)
    expect(wrapper.get('button[type="submit"]').exists()).toBe(true)
  })

  it('keeps promo-code field on step 1 while the verify step is still a separate page', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: true,
      turnstile_site_key: 'site-key',
      promo_code_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()

    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(2)
    const accountPane = wrapper.get('[data-testid="register-account-pane"]')
    expect(accountPane.get('#promo_code').exists()).toBe(true)
    // 账号步还不是最后一步，footer 主按钮是「继续」
    expect(wrapper.find('button[type="submit"]').exists()).toBe(false)

    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await clickStepAction(wrapper)

    expect(wrapper.get('[data-testid="register-verify-pane"]').attributes('data-pane-state')).toBe('active')
    expect(wrapper.get('[data-testid="register-account-pane"]').attributes('data-pane-state')).toBe('idle')
  })

  it('submits a non-whitelist email domain so the backend can enforce its registration quota', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com'],
      registration_email_domain_quota_enabled: true
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('first@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'first@custom.example' })
    )
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  it('shows the localized registration domain quota message returned by the backend', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com'],
      registration_email_domain_quota_enabled: true
    })
    registerMock.mockRejectedValueOnce({
      reason: 'EMAIL_DOMAIN_REGISTRATION_LIMIT',
      message: 'raw backend message'
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('second@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(showErrorMock).toHaveBeenCalledWith(
      '该邮箱域名无法注册新账户。请使用主流邮箱注册；如需使用企业邮箱，请联系客服添加域名白名单。'
    )
  })

  // 域名限量注册开关默认关闭：恢复 PR5423 之前的客户端白名单预检，非白名单域名不发起注册请求。
  it('rejects a non-whitelist email domain locally when the domain quota switch is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com']
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('first@custom.example')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()
    // 校验失败通过 validationToastMessage watcher 弹 toast
    expect(showErrorMock).toHaveBeenCalledWith('auth.emailSuffixNotAllowedWithAllowed')
    expect(wrapper.get('#email').classes()).toContain('input-error')
  })

  it('still submits whitelisted email domains when the domain quota switch is disabled', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false,
      registration_email_suffix_whitelist: ['allowed.com']
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('user@allowed.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'user@allowed.com' })
    )
    expect(showErrorMock).not.toHaveBeenCalled()
  })

  // 与后端 ValidateRegistrationEmailFormat 同口径：超长/特殊字符邮箱在本地就拦下。
  it('rejects emails with special characters or invalid local parts locally', async () => {
    getPublicSettingsMock.mockResolvedValueOnce({
      ...publicSettings,
      turnstile_enabled: false
    })

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('a&b@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('auth.emailFormatInvalid')
    expect(wrapper.get('#email').classes()).toContain('input-error')
  })
})

describe('RegisterView login agreement gate', () => {
  const agreementSettings = {
    ...publicSettings,
    affiliate_enabled: false,
    turnstile_enabled: false,
    login_agreement_enabled: true,
    login_agreement_mode: 'checkbox',
    login_agreement_updated_at: '2026-03-31',
    login_agreement_documents: [
      { id: 'terms', title: 'Terms of Service', content_md: '# Terms' }
    ]
  }

  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    showErrorMock.mockReset()
    pushMock.mockReset()
    appStoreMock.cachedPublicSettings = null
    localStorage.removeItem('sub2api_login_agreement_consent')
    getPublicSettingsMock.mockResolvedValue(publicSettings)
  })

  // 回归 issue #328：同意条款只在登录页勾选时，注册页账号步骤也必须能正常输入。
  it.each(['checkbox', 'modal'])(
    'keeps the account fields editable while the login agreement is unaccepted (%s mode)',
    async (mode) => {
      getPublicSettingsMock.mockResolvedValueOnce({
        ...agreementSettings,
        login_agreement_mode: mode
      })

      const wrapper = mountRegister()
      await flushPromises()

      expect(wrapper.get('#email').attributes('disabled')).toBeUndefined()
      expect(wrapper.get('#password').attributes('disabled')).toBeUndefined()

      await wrapper.get('#email').setValue('user@example.com')
      await wrapper.get('#password').setValue('secret-123')

      expect((wrapper.get('#email').element as HTMLInputElement).value).toBe('user@example.com')
      expect((wrapper.get('#password').element as HTMLInputElement).value).toBe('secret-123')
      wrapper.unmount()
    }
  )

  // 条款门禁仍然归属于最终注册动作：账号可持续填写，但未同意前不能提交注册。
  it('still blocks the final registration action until the agreement is accepted', async () => {
    getPublicSettingsMock.mockResolvedValueOnce(agreementSettings)

    const wrapper = mountRegister()
    await flushPromises()

    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('.auth-step-footer .btn-primary').trigger('click')
    await flushPromises()

    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    expect(registerMock).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('unlocks the final registration action once the stored agreement matches', async () => {
    getPublicSettingsMock.mockResolvedValueOnce(agreementSettings)
    localStorage.setItem(
      'sub2api_login_agreement_consent',
      JSON.stringify({ revision: '2026-03-31:terms:Terms of Service' })
    )

    const wrapper = mountRegister()
    await flushPromises()

    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await wrapper.get('.auth-step-footer .btn-primary').trigger('click')
    await flushPromises()

    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})

// 线上回归：agreement-only（验证码全关）时验证步只剩 Turnstile（不渲染）
// 和 modal 模式的 LoginAgreementPrompt（内容在弹窗里），页面整块空白。
describe('RegisterView verify step pane content', () => {
  const agreementDocuments = [
    { id: 'terms', title: 'Terms of Service', content_md: '# Terms' },
    { id: 'privacy', title: 'Privacy Policy', content_md: '# Privacy' }
  ]

  // Two pages now: account (which also owns the invitation fields) + verify.
  const agreementOnlySettings = {
    ...publicSettings,
    affiliate_enabled: true,
    turnstile_enabled: false,
    login_agreement_enabled: true,
    login_agreement_mode: 'modal',
    login_agreement_updated_at: '2026-03-31',
    login_agreement_documents: agreementDocuments
  }

  const captchaOnlySettings = {
    ...publicSettings,
    affiliate_enabled: true,
    turnstile_enabled: true,
    turnstile_site_key: 'site-key',
    login_agreement_enabled: false
  }

  const captchaAndAgreementSettings = {
    ...agreementOnlySettings,
    turnstile_enabled: true,
    turnstile_site_key: 'site-key'
  }

  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    showErrorMock.mockReset()
    pushMock.mockReset()
    appStoreMock.cachedPublicSettings = null
    localStorage.removeItem('sub2api_login_agreement_consent')
    getPublicSettingsMock.mockResolvedValue(publicSettings)
  })

  it.each(['modal', 'checkbox'])(
    'renders the agreement sentence, document links and checkbox on the verify step (agreement-only, %s mode)',
    async (mode) => {
      getPublicSettingsMock.mockResolvedValueOnce({
        ...agreementOnlySettings,
        login_agreement_mode: mode
      })

      const wrapper = mountRegister({ realAgreementPrompt: true })
      await flushPromises()
      await reachVerifyStep(wrapper)

      const pane = wrapper.get('[data-testid="register-verify-pane"]')
      expect(pane.attributes('data-pane-state')).toBe('active')
      // 验证码全关：验证步不得出现 Turnstile 容器
      expect(pane.find('[data-testid="registration-turnstile"]').exists()).toBe(false)

      const agreement = pane.get('[data-testid="registration-agreement"]')
      expect(agreement.text()).toContain('legal.loginAgreementPrompt.checkboxPrefix')
      expect(agreement.text()).toContain('Terms of Service')
      expect(agreement.text()).toContain('Privacy Policy')
      expect(agreement.findAll('router-link-stub')).toHaveLength(2)

      const checkbox = agreement.get('input[type="checkbox"]')
      expect((checkbox.element as HTMLInputElement).checked).toBe(false)
      // 未勾选时「继续/注册」按钮禁用（沿用现有 agreementGateActive 校验）
      expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()

      await checkbox.setValue(true)

      expect((checkbox.element as HTMLInputElement).checked).toBe(true)
      // 勾选后仍留在验证步，内容不消失
      expect(wrapper.find('[data-testid="registration-agreement"]').exists()).toBe(true)
      expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
      expect(JSON.parse(localStorage.getItem('sub2api_login_agreement_consent') || '{}').revision)
        .toBe('2026-03-31:terms:Terms of Service|privacy:Privacy Policy')
      wrapper.unmount()
    }
  )

  // 生产表现为空白的原因之一：条款已在弹窗里同意过（localStorage 有记录），
  // modal 模式不再渲染任何页面内容。修复后仍需显示勾选行。
  it('keeps the agreement row visible when the consent was already stored (modal mode)', async () => {
    getPublicSettingsMock.mockResolvedValueOnce(agreementOnlySettings)
    localStorage.setItem(
      'sub2api_login_agreement_consent',
      JSON.stringify({ revision: '2026-03-31:terms:Terms of Service|privacy:Privacy Policy' })
    )

    const wrapper = mountRegister({ realAgreementPrompt: true })
    await flushPromises()
    await reachVerifyStep(wrapper)

    const agreement = wrapper.get('[data-testid="registration-agreement"]')
    expect(agreement.text()).toContain('Terms of Service')
    expect((agreement.get('input[type="checkbox"]').element as HTMLInputElement).checked).toBe(true)
    expect(agreement.isVisible()).toBe(true)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('still renders the Turnstile container when only a captcha is required', async () => {
    getPublicSettingsMock.mockResolvedValueOnce(captchaOnlySettings)

    const wrapper = mountRegister({ realAgreementPrompt: true })
    await flushPromises()
    await reachVerifyStep(wrapper)

    const pane = wrapper.get('[data-testid="register-verify-pane"]')
    expect(pane.get('[data-testid="registration-turnstile"]').exists()).toBe(true)
    expect(pane.find('[data-testid="registration-agreement"]').exists()).toBe(false)
    expect(pane.find('[data-testid="turnstile-widget"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('stacks the captcha above the agreement when both are required', async () => {
    getPublicSettingsMock.mockResolvedValueOnce(captchaAndAgreementSettings)

    const wrapper = mountRegister({ realAgreementPrompt: true })
    await flushPromises()
    await reachVerifyStep(wrapper)

    const pane = wrapper.get('[data-testid="register-verify-pane"]')
    const turnstile = pane.get('[data-testid="registration-turnstile"]')
    const agreement = pane.get('[data-testid="registration-agreement"]')

    expect(
      turnstile.element.compareDocumentPosition(agreement.element) &
        Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy()
    // 两者都是 verify pane 的直接子元素：竖向排列，间距由 .auth-step-pane > * + * 统一
    expect(turnstile.element.parentElement).toBe(pane.element)
    expect(agreement.element.parentElement).toBe(pane.element)
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})

// 老板要求：邮箱验证不再跳转到独立页，而是作为注册卡片里的最后一步。
describe('RegisterView in-card email verification', () => {
  const emailVerifySettings = {
    ...publicSettings,
    affiliate_enabled: false,
    turnstile_enabled: false,
    login_agreement_enabled: false,
    email_verify_enabled: true
  }

  beforeEach(() => {
    getPublicSettingsMock.mockReset()
    registerMock.mockReset()
    showErrorMock.mockReset()
    showSuccessMock.mockReset()
    sendVerifyCodeMock.mockReset()
    pushMock.mockReset()
    appStoreMock.cachedPublicSettings = null
    sessionStorage.clear()
    localStorage.clear()
    getPublicSettingsMock.mockResolvedValue(publicSettings)
    sendVerifyCodeMock.mockResolvedValue({ countdown: 60 })
    registerMock.mockResolvedValue({})
  })

  async function enterEmailVerifyStep(wrapper: ReturnType<typeof mountRegister>): Promise<void> {
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await clickStepAction(wrapper)
    await flushPromises()
    await flushPromises()
  }

  it('moves into the in-card verification step instead of the standalone page', async () => {
    getPublicSettingsMock.mockResolvedValue(emailVerifySettings)

    const wrapper = mountRegister()
    await enterEmailVerifyStep(wrapper)

    const pane = wrapper.get('[data-testid="register-email-verify-pane"]')
    expect(pane.attributes('data-pane-state')).toBe('active')
    expect(pane.text()).toContain('auth.verifyYourEmail')
    expect(pane.text()).toContain('user@example.com')
    expect(pane.get('#code').exists()).toBe(true)
    // 账号 + 邮箱验证 = 2 步
    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(2)
    expect(sendVerifyCodeMock).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'user@example.com' })
    )
    expect(pushMock).not.toHaveBeenCalledWith('/email-verify')
    wrapper.unmount()
  })

  it('creates the account with the 6-digit code and redirects to the dashboard', async () => {
    getPublicSettingsMock.mockResolvedValue(emailVerifySettings)

    const wrapper = mountRegister()
    await enterEmailVerifyStep(wrapper)

    await wrapper.get('#code').setValue('123456')
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()

    expect(registerMock).toHaveBeenCalledWith(
      expect.objectContaining({
        email: 'user@example.com',
        password: 'secret-123',
        verify_code: '123456'
      })
    )
    expect(showSuccessMock).toHaveBeenCalled()
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
    wrapper.unmount()
  })

  it('shows the countdown and resends the code after it expires', async () => {
    getPublicSettingsMock.mockResolvedValue(emailVerifySettings)
    sendVerifyCodeMock.mockResolvedValueOnce({ countdown: 0 }).mockResolvedValue({ countdown: 60 })

    const wrapper = mountRegister()
    await enterEmailVerifyStep(wrapper)

    const resendButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('auth.resendCode'))
    expect(resendButton).toBeTruthy()

    await resendButton!.trigger('click')
    await flushPromises()

    expect(sendVerifyCodeMock).toHaveBeenCalledTimes(2)
    const countdownButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('auth.resendCountdown'))
    expect(countdownButton?.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('keeps the entered code, sent state and countdown when going back to the account step', async () => {
    getPublicSettingsMock.mockResolvedValue(emailVerifySettings)

    const wrapper = mountRegister()
    await enterEmailVerifyStep(wrapper)
    await wrapper.get('#code').setValue('123456')

    await wrapper.get('.auth-step-back').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="register-account-pane"]').attributes('data-pane-state')).toBe('active')

    await clickStepAction(wrapper)
    await flushPromises()

    expect(wrapper.get('[data-testid="register-email-verify-pane"]').attributes('data-pane-state')).toBe('active')
    // Both the pane's verification field and the shared segmented code input hold
    // the code now; read it back from the digit cells instead of a single input.
    const digits = wrapper
      .findAll('[data-testid^="verification-code-digit-"]')
      .map(input => (input.element as HTMLInputElement).value)
      .join('')
    expect(digits).toBe('123456')
    expect(wrapper.text()).toContain('auth.codeSentSuccess')
    // 数据没变，不重新发码
    expect(sendVerifyCodeMock).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('keeps the agreement step before the email verification step (3 steps)', async () => {
    getPublicSettingsMock.mockResolvedValue({
      ...emailVerifySettings,
      login_agreement_enabled: true,
      login_agreement_mode: 'checkbox',
      login_agreement_updated_at: '2026-03-31',
      login_agreement_documents: [{ id: 'terms', title: 'Terms of Service', content_md: '# Terms' }]
    })

    const wrapper = mountRegister({ realAgreementPrompt: true })
    await flushPromises()
    await wrapper.get('#email').setValue('user@example.com')
    await wrapper.get('#password').setValue('secret-123')
    await clickStepAction(wrapper)
    await flushPromises()

    // 账号 -> 协议 -> 邮箱验证
    expect(wrapper.findAll('.auth-step-dot')).toHaveLength(3)
    expect(wrapper.get('[data-testid="register-verify-pane"]').attributes('data-pane-state')).toBe('active')
    const continueButton = wrapper.get('.auth-step-footer .btn-primary')
    expect(continueButton.attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="registration-agreement"] input[type="checkbox"]').setValue(true)
    expect(continueButton.attributes('disabled')).toBeUndefined()

    await continueButton.trigger('click')
    await flushPromises()
    await flushPromises()
    expect(wrapper.get('[data-testid="register-email-verify-pane"]').attributes('data-pane-state')).toBe('active')
    wrapper.unmount()
  })

  it('resends the code for a changed email after returning to edit it', async () => {
    getPublicSettingsMock.mockResolvedValue(emailVerifySettings)

    const wrapper = mountRegister()
    await enterEmailVerifyStep(wrapper)

    await wrapper.get('.auth-step-back').trigger('click')
    await flushPromises()
    await wrapper.get('#email').setValue('other@example.com')
    await clickStepAction(wrapper)
    await flushPromises()
    await flushPromises()

    expect(sendVerifyCodeMock).toHaveBeenCalledTimes(2)
    expect(sendVerifyCodeMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ email: 'other@example.com' })
    )
    expect(wrapper.get('[data-testid="register-email-verify-pane"]').text()).toContain('other@example.com')
    wrapper.unmount()
  })
})

describe('RegisterView autofill contract', () => {
  it('marks the register form as a standard new-credential form', async () => {
    const wrapper = mountRegister()
    await flushPromises()

    const forms = wrapper.findAll('form')
    expect(forms.length).toBeGreaterThan(0)
    expect(forms[0].attributes('autocomplete')).toBe('on')

    const email = wrapper.get('#email')
    expect(email.attributes('name')).toBe('email')
    expect(email.attributes('autocomplete')).toBe('username')

    const password = wrapper.get('#password')
    expect(password.attributes('name')).toBe('password')
    expect(password.attributes('autocomplete')).toBe('new-password')
  })

  it('redirects back to sanitized route.query.redirect after registration and rejects external redirects', async () => {
    getPublicSettingsMock.mockResolvedValue({ ...publicSettings, turnstile_enabled: false })
    routeQueryMock.value = { redirect: '/docs?page=agent-setup&agent=opencode' }

    const wrapper = mountRegister()
    await flushPromises()
    await wrapper.get('#email').setValue('newuser@example.com')
    await wrapper.get('#password').setValue('valid-test-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(pushMock).toHaveBeenCalledWith('/docs?page=agent-setup&agent=opencode')
    wrapper.unmount()

    pushMock.mockReset()
    routeQueryMock.value = { redirect: 'https://evil.example/steal' }
    const unsafeWrapper = mountRegister()
    await flushPromises()
    await unsafeWrapper.get('#email').setValue('newuser2@example.com')
    await unsafeWrapper.get('#password').setValue('valid-test-password')
    await unsafeWrapper.get('form').trigger('submit')
    await flushPromises()
    expect(pushMock).toHaveBeenCalledWith('/dashboard')
    unsafeWrapper.unmount()
  })
})
