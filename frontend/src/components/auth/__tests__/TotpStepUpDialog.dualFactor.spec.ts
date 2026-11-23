import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
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

describe('场景 A：账号同时绑定 TOTP 与 Passkey', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue({ required_factors: ['totp', 'passkey'], step_up_enabled: true })
  })
  afterEach(() => vi.resetAllMocks())

  it('先验 TOTP 再验 Passkey，两个都通过才放行原请求', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper } = mountDialog()
    await flushPromises()

    expect(mocks.getPolicy).toHaveBeenCalledOnce()
    // 第一阶段必须是 TOTP（顺序固定先 TOTP 后 Passkey）。
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stepup-passkey-verify"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('stepUp.totpThenPasskeyHint')

    await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', '012345')
    await flushPromises()

    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    // TOTP 通过后不关闭弹窗，进入 Passkey 阶段。
    expect(controller.visible.value).toBe(true)
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stepup-passkey-verify"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('stepUp.totpVerifiedContinuePasskey')

    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(false)
  })

  it('Passkey 阶段失败时弹窗保持打开并展示错误，不 resolve prompt', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    mocks.passkeyStepUp.mockRejectedValue(new Error('passkey failed'))
    const { controller, wrapper } = mountDialog()
    await flushPromises()
    await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', '012345')
    await flushPromises()
    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    expect(controller.visible.value).toBe(true)
    expect(wrapper.find('[data-testid="stepup-passkey-error"]').exists()).toBe(true)
    expect(mocks.showError).toHaveBeenCalledWith('passkey failed')
  })
})

describe('场景 B：账号只绑定 Passkey', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue({ required_factors: ['passkey'], step_up_enabled: true })
  })
  afterEach(() => vi.resetAllMocks())

  it('直接进入 Passkey 阶段，不需要 TOTP', async () => {
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper } = mountDialog()
    await flushPromises()

    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(false)
  })
})

describe('场景 C：浏览器不支持 Passkey 的降级提示', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = false
    mocks.getPolicy.mockResolvedValue({ required_factors: ['passkey'], step_up_enabled: true })
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

describe('场景 D：账号同时绑定 TOTP 与 Passkey 时可任选先验证 Passkey', () => {
  beforeEach(() => {
    sessionStorage.clear()
    mocks.passkeySupported = true
    mocks.getPolicy.mockResolvedValue({ required_factors: ['totp', 'passkey'], step_up_enabled: true })
  })
  afterEach(() => vi.resetAllMocks())

  it('TOTP 阶段并列展示「用 Passkey 验证」，先 Passkey 再 TOTP 才算完成', async () => {
    mocks.stepUp.mockResolvedValue({ verified: true })
    mocks.passkeyStepUp.mockResolvedValue({ verified: true, factor: 'passkey' })
    const { controller, wrapper } = mountDialog()
    await flushPromises()

    // 两个因子都绑定时，TOTP 阶段就能看到并行的 Passkey 选项。
    const usePasskey = wrapper.get('[data-testid="stepup-use-passkey"]')
    expect(usePasskey.text()).toContain('stepUp.usePasskey')
    await usePasskey.trigger('click')
    await flushPromises()

    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="stepup-passkey-verify"]').exists()).toBe(true)
    // 还没验 TOTP，仍可切回 TOTP 阶段。
    expect(wrapper.find('[data-testid="stepup-use-totp"]').exists()).toBe(true)

    await wrapper.get('[data-testid="stepup-passkey-verify"]').trigger('click')
    await flushPromises()

    // Passkey 通过后不能直接关闭：TOTP 因子还没完成。
    expect(mocks.passkeyStepUp).toHaveBeenCalledOnce()
    expect(controller.visible.value).toBe(true)
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('stepUp.passkeyVerifiedContinueTotp')

    await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', '012345')
    await flushPromises()

    expect(mocks.stepUp).toHaveBeenCalledWith('012345')
    expect(controller.visible.value).toBe(false)
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
    await wrapper.getComponent(TotpCodeInput).vm.$emit('complete', '654321')
    await flushPromises()
    expect(mocks.stepUp).toHaveBeenCalledWith('654321')
    expect(controller.visible.value).toBe(false)
  })
})
