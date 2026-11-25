import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import UserReset2FAModal from '../UserReset2FAModal.vue'

// 确认按钮的禁用逻辑（原因合法才可点确认）用真实的 Nuxt UI 控件验证：
// 这里只替身对话框外壳（BaseDialog，需要 Teleport）与验证码弹窗，UButton / UForm /
// UFormField / UTextarea / UAlert 全部是真实组件，不在 stubs 里替换，
// 这样「按钮到底有没有被禁用」是真实 DOM 的 disabled，而不是替身自己转发出来的属性。

const { resetUser2FA, showSuccess, showError, stepUpRun } = vi.hoisted(() => ({
  resetUser2FA: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  stepUpRun: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { users: { resetUser2FA } }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/composables/useStepUp', async () => {
  const actual = await vi.importActual<Record<string, unknown>>('@/composables/useStepUp')
  return {
    ...actual,
    useStepUp: () => ({ run: stepUpRun })
  }
})

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key
    })
  }
})

enableAutoUnmount(afterEach)

const targetUser = {
  id: 3249,
  email: 'user-3249@example.test',
  role: 'user',
  status: 'active'
} as any

const mountModal = () =>
  mount(UserReset2FAModal, {
    props: { show: true, user: targetUser },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        TotpStepUpDialog: true
      }
    }
  })

type Modal = ReturnType<typeof mountModal>

const confirmButton = (wrapper: Modal) => wrapper.get('[data-test="reset-2fa-confirm"]')
const typeReason = async (wrapper: Modal, text: string) => {
  await wrapper.get('textarea').setValue(text)
  await flushPromises()
}

describe('UserReset2FAModal 确认按钮门禁（真实 Nuxt UI 控件）', () => {
  beforeEach(() => {
    resetUser2FA.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    stepUpRun.mockReset()
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => action())
    resetUser2FA.mockResolvedValue({
      user_id: 3249,
      email: targetUser.email,
      notification_queued: true,
      revoked_step_up_grants: true,
      passkey_bound: false,
      incomplete_steps: []
    })
  })

  it('确认按钮是真实的 <button>，打开时原因为空所以是禁用的', () => {
    const wrapper = mountModal()
    const button = confirmButton(wrapper)
    expect(button.element.tagName).toBe('BUTTON')
    expect((button.element as HTMLButtonElement).disabled).toBe(true)
  })

  it('只有原因合法（去空格后 1~500 字）才可点确认', async () => {
    const wrapper = mountModal()
    const isDisabled = () => (confirmButton(wrapper).element as HTMLButtonElement).disabled

    // 纯空白不算原因。
    await typeReason(wrapper, '    ')
    expect(isDisabled()).toBe(true)

    // 合法原因：按钮可点。
    await typeReason(wrapper, '用户忘记动态口令，已通过工单核实身份')
    expect(isDisabled()).toBe(false)

    // 正好 500 个字可以，501 个字不行。
    await typeReason(wrapper, '忘'.repeat(500))
    expect(isDisabled()).toBe(false)
    await typeReason(wrapper, '忘'.repeat(501))
    expect(isDisabled()).toBe(true)

    // 清空后重新回到禁用。
    await typeReason(wrapper, '')
    expect(isDisabled()).toBe(true)
  })

  it('原因不合法时点确认什么都不会发生；合法后提交的是去空格后的原因', async () => {
    const wrapper = mountModal()

    await confirmButton(wrapper).trigger('click')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(stepUpRun).not.toHaveBeenCalled()
    expect(resetUser2FA).not.toHaveBeenCalled()

    await typeReason(wrapper, '  用户忘记动态口令  ')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(resetUser2FA).toHaveBeenCalledWith(3249, '用户忘记动态口令')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('提交期间确认按钮保持禁用，不会重复提交', async () => {
    const pending: { resolve?: (value: unknown) => void } = {}
    resetUser2FA.mockReturnValue(new Promise((resolve) => { pending.resolve = resolve }))
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect((confirmButton(wrapper).element as HTMLButtonElement).disabled).toBe(true)

    // 再次提交被门禁挡住。
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(resetUser2FA).toHaveBeenCalledTimes(1)

    pending.resolve?.({
      user_id: 3249,
      email: targetUser.email,
      notification_queued: true,
      revoked_step_up_grants: true,
      passkey_bound: false
    })
    await flushPromises()
    expect(wrapper.emitted('success')).toHaveLength(1)
  })
})
