import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'
import { useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'

const mocks = vi.hoisted(() => ({
  stepUp: vi.fn(),
  getPolicy: vi.fn(),
  passkeyStepUp: vi.fn(),
  passkeySupported: true,
  showError: vi.fn()
}))

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api', () => ({
  totpAPI: { stepUp: mocks.stepUp, getTwoFactorPolicy: mocks.getPolicy }
}))
vi.mock('@/api/passkey', () => ({
  passkeyAPI: { isSupported: () => mocks.passkeySupported, stepUp: mocks.passkeyStepUp }
}))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: mocks.showError }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError }) }))
enableAutoUnmount(afterEach)

const BOTH_BOUND = { required_factors: ['totp', 'passkey'], step_up_enabled: true, step_up_mode: 'any' }

/** 改动前「两个因子都要验证」流程里的文案：任选其一之后一个都不应该再出现。 */
const TWO_STEP_LEFTOVERS = [
  'stepUp.twoFactorProgress',
  'stepUp.totpVerifiedContinuePasskey',
  'stepUp.passkeyVerifiedContinueTotp',
  'stepUp.totpThenPasskeyHint'
]

function expectNoTwoStepLeftovers(wrapper: VueWrapper) {
  expect(wrapper.find('[data-testid="stepup-factor-progress"]').exists()).toBe(false)
  for (const key of TWO_STEP_LEFTOVERS) expect(wrapper.text()).not.toContain(key)
}

function mountDialog() {
  const controller = useStepUp()
  const wrapper = mount(TotpStepUpDialog, {
    props: { controller },
    global: { stubs: { teleport: true } }
  })
  // 打开动作必须在挂载后发生：弹窗在 visible 变 true 时读取因子策略。
  controller.visible.value = true
  return { controller, wrapper }
}

/** 模拟真实链路：敏感请求先被 403 STEP_UP_REQUIRED 拦下 → 弹窗验证 → 原请求重试。 */
function mountWithBlockedRequest() {
  const controller = useStepUp()
  const wrapper = mount(TotpStepUpDialog, {
    props: { controller },
    global: { stubs: { teleport: true } }
  })
  const action = vi
    .fn()
    .mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' })
    .mockResolvedValueOnce('executed')
  const pending = controller.run(action)
  return { controller, wrapper, action, pending }
}

async function submitTotp(wrapper: VueWrapper, code: string) {
  await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', code)
  await flushPromises()
}

async function verifyWithPasskey(wrapper: VueWrapper) {
  await wrapper.get('[data-testid="stepup-use-passkey"]').trigger('click')
  await flushPromises()
  await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
  await flushPromises()
}

describe('场景 A：账号同时绑定 TOTP 与 Passkey，任选其一验证即可', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue(BOTH_BOUND)
  })
  afterEach(() => vi.resetAllMocks())

  it('同时展示 TOTP 输入框、验证按钮与「用 Passkey 验证」，提示任选其一，没有两步走文案', async () => {
    const { wrapper } = mountDialog()
    await flushPromises()

    expect(mocks.getPolicy).toHaveBeenCalledOnce()
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stepup-totp-submit"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="stepup-use-passkey"]').text()).toContain('stepUp.usePasskey')
    expect(wrapper.find('[data-testid="stepup-cancel"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('stepUp.anyOneFactorHint')
    expectNoTwoStepLeftovers(wrapper)
  })

  it('只验证 TOTP 就完成：原请求立刻重试一次，不再要求 Passkey', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()
    expect(action).toHaveBeenCalledTimes(1)
    expect(controller.visible.value).toBe(true)

    await submitTotp(wrapper, '012345')

    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    expect(mocks.passkeyStepUp).not.toHaveBeenCalled()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
    expectNoTwoStepLeftovers(wrapper)
  })

  it('只验证 Passkey 就完成：原请求立刻重试一次，不再要求输入动态码', async () => {
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()

    await wrapper.get('[data-testid="stepup-use-passkey"]').trigger('click')
    await flushPromises()
    // Passkey 阶段：没有 TOTP 输入框，也没有两步走文案，仍可切回 TOTP。
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stepup-use-totp"]').exists()).toBe(true)
    expectNoTwoStepLeftovers(wrapper)

    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
  })

  it('TOTP 输错不挡路：弹窗保持打开、不重试原请求，仍可改用 Passkey 完成', async () => {
    mocks.stepUp.mockRejectedValue({ message: 'Invalid code' })
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()

    await submitTotp(wrapper, '111111')
    expect(mocks.showError).toHaveBeenCalledWith('Invalid code')
    expect(controller.visible.value).toBe(true)
    expect(action).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="stepup-use-passkey"]').attributes('disabled')).toBeUndefined()
    expectNoTwoStepLeftovers(wrapper)

    await verifyWithPasskey(wrapper)

    expect(mocks.stepUp).toHaveBeenCalledOnce()
    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
  })

  it('Passkey 失败/取消不挡路：展示错误、不重试原请求，仍可切回 TOTP 完成', async () => {
    mocks.passkeyStepUp.mockRejectedValue(new DOMException('cancelled', 'NotAllowedError'))
    mocks.stepUp.mockResolvedValue({ verified: true })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()

    await verifyWithPasskey(wrapper)
    expect(controller.visible.value).toBe(true)
    expect(action).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="stepup-passkey-error"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="stepup-use-totp"]').attributes('disabled')).toBeUndefined()
    expectNoTwoStepLeftovers(wrapper)

    await wrapper.get('[data-testid="stepup-use-totp"]').trigger('click')
    await flushPromises()
    await submitTotp(wrapper, '654321')

    expect(mocks.stepUp).toHaveBeenCalledWith('654321')
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
  })

  it('TOTP 被限速时 Passkey 仍可用', async () => {
    mocks.stepUp.mockRejectedValue({ message: 'Too many attempts', retryAfter: 30 })
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper, pending } = mountWithBlockedRequest()
    await flushPromises()

    await submitTotp(wrapper, '111111')
    expect(wrapper.find('[data-testid="totp-stepup-rate-limit"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="stepup-totp-submit"]').attributes('disabled')).toBeDefined()

    await verifyWithPasskey(wrapper)

    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
  })

  it('取消不会重试原请求，也没有任何因子被验证', async () => {
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    // 先挂上断言再触发取消，避免 pending 在没有处理器时先 reject 变成未处理的拒绝。
    const cancelled = expect(pending).rejects.toMatchObject({ code: 'STEP_UP_CANCELLED' })
    await flushPromises()

    await wrapper.get('[data-testid="stepup-cancel"]').trigger('click')
    await flushPromises()

    expect(controller.visible.value).toBe(false)
    await cancelled
    expect(action).toHaveBeenCalledTimes(1)
    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(mocks.passkeyStepUp).not.toHaveBeenCalled()
  })

  it('后端没有声明 step_up_mode（旧后端）时不许诺「任选其一」，但仍并列展示两种方式', async () => {
    mocks.getPolicy.mockResolvedValue({ required_factors: ['totp', 'passkey'], step_up_enabled: true })
    const { wrapper } = mountDialog()
    await flushPromises()

    expect(wrapper.text()).not.toContain('stepUp.anyOneFactorHint')
    expect(wrapper.text()).toContain('stepUp.hint')
    expect(wrapper.find('[data-testid="stepup-use-passkey"]').exists()).toBe(true)
    expectNoTwoStepLeftovers(wrapper)
  })
})

describe('场景 B：账号只绑定 Passkey（行为不变）', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue({ required_factors: ['passkey'], step_up_enabled: true, step_up_mode: 'any' })
  })
  afterEach(() => vi.resetAllMocks())

  it('直接进入 Passkey 阶段，不需要 TOTP，也没有「任选其一」提示', async () => {
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()

    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stepup-use-totp"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('stepUp.anyOneFactorHint')
    expectNoTwoStepLeftovers(wrapper)

    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
  })
})

describe('场景 C：浏览器不支持 Passkey 的降级提示', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = false
    mocks.getPolicy.mockResolvedValue({ required_factors: ['passkey'], step_up_enabled: true, step_up_mode: 'any' })
  })
  afterEach(() => vi.resetAllMocks())

  it('展示降级文案，确认按钮仍在但不可点（不会只剩「取消」）', async () => {
    const { controller, wrapper } = mountDialog()
    await flushPromises()

    expect(wrapper.find('[data-testid="stepup-passkey-unsupported"]').exists()).toBe(true)
    const submit = wrapper.get('[data-testid="stepup-passkey-verify"]')
    expect(submit.attributes('disabled')).toBeDefined()
    expect(controller.visible.value).toBe(true)
  })
})

describe('场景 D：账号只绑定 TOTP（行为不变）', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue({ required_factors: ['totp'], step_up_enabled: true, step_up_mode: 'any' })
  })
  afterEach(() => vi.resetAllMocks())

  it('只有 TOTP 输入框，没有 Passkey 选项与「任选其一」提示；验证后重试一次', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    const { controller, wrapper, action, pending } = mountWithBlockedRequest()
    await flushPromises()

    expect(wrapper.find('[data-testid="stepup-use-passkey"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('stepUp.anyOneFactorHint')
    expect(wrapper.text()).toContain('stepUp.hint')
    expectNoTwoStepLeftovers(wrapper)

    await submitTotp(wrapper, '012345')

    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    expect(mocks.passkeyStepUp).not.toHaveBeenCalled()
    expect(controller.visible.value).toBe(false)
    await expect(pending).resolves.toBe('executed')
    expect(action).toHaveBeenCalledTimes(2)
  })
})

describe('场景 E：策略接口失败时按 TOTP 兜底（旧行为）', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockRejectedValue(new Error('policy unavailable'))
  })
  afterEach(() => vi.resetAllMocks())

  it('仍可完成 TOTP step-up', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    const { controller, wrapper } = mountDialog()
    await flushPromises()
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(true)
    await submitTotp(wrapper, '654321')
    expect(mocks.stepUp).toHaveBeenCalledWith('654321')
    expect(controller.visible.value).toBe(false)
  })
})
