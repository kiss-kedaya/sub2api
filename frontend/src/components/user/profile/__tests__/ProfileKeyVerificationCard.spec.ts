import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import type { ApiKeyVerificationFactors } from '@/types'
import ProfileKeyVerificationCard from '../ProfileKeyVerificationCard.vue'

const mocks = vi.hoisted(() => ({
  getVerificationFactors: vi.fn(),
  setCreateVerificationMode: vi.fn(),
  beginKeyPasskeyVerification: vi.fn(),
  finishKeyPasskeyVerification: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    getVerificationFactors: mocks.getVerificationFactors,
    setCreateVerificationMode: mocks.setCreateVerificationMode,
    beginKeyPasskeyVerification: mocks.beginKeyPasskeyVerification,
    finishKeyPasskeyVerification: mocks.finishKeyPasskeyVerification,
  },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})
enableAutoUnmount(afterEach)

// 个人资料页「创建 API Key 时需要验证身份」开关：显示服务端算好的有效值；
// 从开拨到关（变宽松）先弹三选一校验，从关拨到开（变严）直接保存；没有可用因子时置灰。

const factorsOf = (overrides: Partial<ApiKeyVerificationFactors> = {}): ApiKeyVerificationFactors => ({
  password_available: true,
  totp_enabled: false,
  passkey_count: 0,
  preferred: 'password',
  available_methods: ['password'],
  seal_available: true,
  create_verification_required: false,
  create_verification_mode: 'auto',
  create_verification_configurable: true,
  ...overrides,
})

const totpAccount = (overrides: Partial<ApiKeyVerificationFactors> = {}) =>
  factorsOf({
    totp_enabled: true,
    preferred: 'totp',
    available_methods: ['totp', 'password'],
    create_verification_required: true,
    ...overrides,
  })

async function render(initial: ApiKeyVerificationFactors | Error) {
  if (initial instanceof Error) mocks.getVerificationFactors.mockRejectedValue(initial)
  else mocks.getVerificationFactors.mockResolvedValue(initial)
  const wrapper = mount(ProfileKeyVerificationCard, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

const sw = (wrapper: VueWrapper) => wrapper.get('[data-test="key-verification-switch"]')
// BaseDialog 把内容传送到 body，所以对话框里的元素从 document 里找。
const inDialog = <T extends HTMLElement = HTMLElement>(name: string) =>
  document.body.querySelector<T>(`[data-test="${name}"]`)
const dialogOpen = () => inDialog('key-verification-dialog') !== null

async function typeInto(selector: string, value: string) {
  const input = document.body.querySelector<HTMLInputElement>(selector)
  expect(input).not.toBeNull()
  input!.value = value
  input!.dispatchEvent(new Event('input', { bubbles: true }))
  await flushPromises()
}

async function clickAndSettle(element: Element | null) {
  expect(element).not.toBeNull()
  ;(element as HTMLElement).click()
  await flushPromises()
}

beforeEach(() => {
  vi.resetAllMocks()
  // 校验组件会记住「上次用哪种方式验证」，不同用例之间不能互相影响默认选中的因子。
  window.localStorage.clear()
})

afterEach(() => {
  document.body.innerHTML = ''
  document.body.classList.remove('modal-open')
})

describe('ProfileKeyVerificationCard', () => {
  it('shows the server-computed effective value, not the stored label', async () => {
    // 开了 2FA、没设置过（auto）：有效值是开。
    let wrapper = await render(totpAccount())
    expect(mocks.getVerificationFactors).toHaveBeenCalledTimes(1)
    expect(sw(wrapper).attributes('role')).toBe('switch')
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-test="key-verification-status"]').text()).toBe('profile.keyVerification.stateOn')
    wrapper.unmount()

    // 仅有登录密码、没设置过：有效值是关。
    wrapper = await render(factorsOf())
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="key-verification-status"]').text()).toBe('profile.keyVerification.stateOff')
    wrapper.unmount()

    // 用户把仅有密码的账号设成 on：有效值是开；2FA 账号设成 off：有效值是关。
    wrapper = await render(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
    wrapper.unmount()
    wrapper = await render(totpAccount({ create_verification_required: false, create_verification_mode: 'off' }))
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
  })

  it('has an accessible name and description and is a real keyboard-operable switch', async () => {
    const wrapper = await render(factorsOf())
    const control = sw(wrapper)
    expect(control.element.tagName).toBe('BUTTON')
    expect(control.attributes('disabled')).toBeUndefined()
    expect(control.attributes('tabindex')).not.toBe('-1')
    // 名称来自 <label for>，说明通过 aria-describedby 关联到默认值说明与当前状态。
    const label = wrapper.get('[data-test="key-verification-label"]')
    expect(label.attributes('for')).toBe(control.attributes('id'))
    expect(label.text()).toBe('profile.keyVerification.switchLabel')
    const described = (control.attributes('aria-describedby') ?? '').split(/\s+/)
    expect(described).toContain('key-verification-hint')
    expect(described).toContain('key-verification-status')
    expect(wrapper.get('#key-verification-hint').text()).toBe('profile.keyVerification.switchHint')
    expect(wrapper.get('#key-verification-status').attributes('role')).toBe('status')
  })

  it('turning it on (tighter) saves straight away without asking for verification', async () => {
    mocks.setCreateVerificationMode.mockResolvedValue(
      factorsOf({ create_verification_required: true, create_verification_mode: 'on' }),
    )
    const wrapper = await render(factorsOf())

    await sw(wrapper).trigger('click')
    await flushPromises()

    expect(dialogOpen()).toBe(false)
    expect(mocks.setCreateVerificationMode).toHaveBeenCalledTimes(1)
    // 变严不带校验材料：第二个参数为空，请求体里不会有 verification 字段（见 api/__tests__/keys.createVerification.spec.ts）。
    expect(mocks.setCreateVerificationMode).toHaveBeenCalledWith('on', undefined)
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
    expect(mocks.showSuccess).toHaveBeenCalledWith('profile.keyVerification.savedOn')
  })

  it('clicking the label toggles it too', async () => {
    mocks.setCreateVerificationMode.mockResolvedValue(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))
    const wrapper = await render(factorsOf())

    await wrapper.get('[data-test="key-verification-label"]').trigger('click')
    await flushPromises()

    expect(mocks.setCreateVerificationMode).toHaveBeenCalledWith('on', undefined)
  })

  it('turning it off (looser) asks for verification first and only saves after it passes', async () => {
    mocks.setCreateVerificationMode.mockResolvedValue(
      totpAccount({ create_verification_required: false, create_verification_mode: 'off' }),
    )
    const wrapper = await render(totpAccount())

    await sw(wrapper).trigger('click')
    await flushPromises()

    // 对话框里复用创建 Key 的三选一校验组件；还没有请求，开关也没有被乐观地翻过去。
    expect(dialogOpen()).toBe(true)
    expect(document.body.querySelector('[data-test="key-factor-verification"]')).not.toBeNull()
    expect(mocks.setCreateVerificationMode).not.toHaveBeenCalled()
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')

    // 默认选中推荐因子（TOTP）：填动态码、确认。
    await typeInto('[data-test="key-factor-totp-input"]', '123456')
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(mocks.setCreateVerificationMode).toHaveBeenCalledTimes(1)
    expect(mocks.setCreateVerificationMode).toHaveBeenCalledWith('off', { method: 'totp', totp_code: '123456' })
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
    expect(dialogOpen()).toBe(false)
    expect(mocks.showSuccess).toHaveBeenCalledWith('profile.keyVerification.savedOff')
  })

  it('turning it off with a login password sends the password factor', async () => {
    mocks.setCreateVerificationMode.mockResolvedValue(
      factorsOf({ create_verification_required: false, create_verification_mode: 'off' }),
    )
    const wrapper = await render(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))

    await sw(wrapper).trigger('click')
    await flushPromises()
    await typeInto('[data-test="key-factor-password-input"] input, input[data-test="key-factor-password-input"]', 'my-login-password')
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(mocks.setCreateVerificationMode).toHaveBeenCalledWith('off', { method: 'password', password: 'my-login-password' })
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
  })

  it('does not call the server when the verification is incomplete', async () => {
    const wrapper = await render(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))

    await sw(wrapper).trigger('click')
    await flushPromises()
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(mocks.setCreateVerificationMode).not.toHaveBeenCalled()
    expect(dialogOpen()).toBe(true)
    expect(inDialog('key-factor-error')?.textContent).toContain('keys.factor.passwordRequired')
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
  })

  it('keeps the dialog open with a specific message when the server rejects the verification', async () => {
    mocks.setCreateVerificationMode.mockRejectedValue({ status: 400, reason: 'VERIFY_PASSWORD_INVALID', message: 'password is incorrect' })
    const wrapper = await render(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))

    await sw(wrapper).trigger('click')
    await flushPromises()
    await typeInto('[data-test="key-factor-password-input"] input, input[data-test="key-factor-password-input"]', 'wrong')
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(dialogOpen()).toBe(true)
    expect(inDialog('key-verification-error')?.textContent).toContain('keys.factor.passwordInvalid')
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    // 校验材料一次性：失败后输入框被清空，不留着用过的凭据。
    const input = document.body.querySelector<HTMLInputElement>('input[data-test="key-factor-password-input"], [data-test="key-factor-password-input"] input')
    expect(input?.value ?? '').toBe('')
  })

  it.each([
    ['VERIFY_PASSWORD_RATE_LIMITED', 'keys.factor.passwordRateLimited'],
    ['VERIFY_TOTP_INVALID', 'keys.factor.totpInvalid'],
    ['VERIFY_PASSKEY_FAILED', 'keys.factor.passkeyFailed'],
    ['VERIFY_FACTOR_UNAVAILABLE', 'keys.factor.factorUnavailable'],
    ['VERIFY_REQUIRED', 'keys.factor.requiredAll'],
  ])('maps %s to its own message', async (reason, key) => {
    mocks.setCreateVerificationMode.mockRejectedValue({ status: 400, reason, message: 'x' })
    const wrapper = await render(totpAccount())

    await sw(wrapper).trigger('click')
    await flushPromises()
    await typeInto('[data-test="key-factor-totp-input"]', '123456')
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(inDialog('key-verification-error')?.textContent).toContain(key)
    expect(dialogOpen()).toBe(true)
  })

  it('cancelling the dialog changes nothing and sends nothing', async () => {
    const wrapper = await render(totpAccount())

    await sw(wrapper).trigger('click')
    await flushPromises()
    await clickAndSettle(inDialog('key-verification-cancel'))

    expect(dialogOpen()).toBe(false)
    expect(mocks.setCreateVerificationMode).not.toHaveBeenCalled()
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
  })

  it('is disabled with an explanation when the account has nothing to verify with', async () => {
    const wrapper = await render(
      factorsOf({
        password_available: false,
        preferred: '',
        available_methods: [],
        seal_available: false,
        create_verification_configurable: false,
      }),
    )

    expect(sw(wrapper).attributes('disabled')).toBeDefined()
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-test="key-verification-status"]').text()).toBe('profile.keyVerification.unavailable')

    await sw(wrapper).trigger('click')
    await flushPromises()
    expect(mocks.setCreateVerificationMode).not.toHaveBeenCalled()
    expect(dialogOpen()).toBe(false)
  })

  it('ignores extra clicks while a save is in flight', async () => {
    let resolveSave: (value: ApiKeyVerificationFactors) => void = () => {}
    mocks.setCreateVerificationMode.mockReturnValue(new Promise<ApiKeyVerificationFactors>((resolve) => { resolveSave = resolve }))
    const wrapper = await render(factorsOf())

    await sw(wrapper).trigger('click')
    await sw(wrapper).trigger('click')
    await flushPromises()
    expect(mocks.setCreateVerificationMode).toHaveBeenCalledTimes(1)
    expect(sw(wrapper).attributes('disabled')).toBeDefined()

    resolveSave(factorsOf({ create_verification_required: true, create_verification_mode: 'on' }))
    await flushPromises()
    expect(sw(wrapper).attributes('aria-checked')).toBe('true')
    expect(sw(wrapper).attributes('disabled')).toBeUndefined()
  })

  it('shows a save failure and leaves the switch where the server says it is', async () => {
    mocks.setCreateVerificationMode.mockRejectedValue({ status: 500, message: 'boom' })
    const wrapper = await render(factorsOf())

    await sw(wrapper).trigger('click')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith('boom')
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
    expect(sw(wrapper).attributes('disabled')).toBeUndefined()
  })

  it('reloads the real state and tells the user when the setting was changed elsewhere', async () => {
    mocks.setCreateVerificationMode.mockRejectedValue({ status: 409, reason: 'API_KEY_CREATE_VERIFICATION_CONFLICT', message: 'x' })
    const wrapper = await render(totpAccount())
    mocks.getVerificationFactors.mockResolvedValue(
      totpAccount({ create_verification_required: false, create_verification_mode: 'off' }),
    )

    await sw(wrapper).trigger('click')
    await flushPromises()
    await typeInto('[data-test="key-factor-totp-input"]', '123456')
    await clickAndSettle(inDialog('key-verification-confirm'))

    expect(mocks.showError).toHaveBeenCalledWith('profile.keyVerification.conflict')
    expect(mocks.getVerificationFactors).toHaveBeenCalledTimes(2)
    expect(dialogOpen()).toBe(false)
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
  })

  it('blocks editing after a failed read and recovers after a successful refresh', async () => {
    const wrapper = await render(new Error('network'))

    expect(wrapper.find('[data-test="key-verification-switch"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="key-verification-load-error"]').text()).toContain('profile.keyVerification.loadFailed')

    mocks.getVerificationFactors.mockResolvedValue(factorsOf())
    await wrapper.get('[data-test="key-verification-refresh"]').trigger('click')
    await flushPromises()
    expect(sw(wrapper).attributes('aria-checked')).toBe('false')
  })

  it('does not offer a switch the server cannot honour (older server without the new fields)', async () => {
    const legacy = {
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: false,
    } as ApiKeyVerificationFactors
    const wrapper = await render(legacy)

    expect(wrapper.find('[data-test="key-verification-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="key-verification-load-error"]').exists()).toBe(true)
  })
})
