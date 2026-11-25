import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import UserReset2FAModal from '../UserReset2FAModal.vue'
import { StepUpCancelledError } from '@/composables/useStepUp'

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

// step-up 门控：正常调用即执行动作；测试里可以切换成「需要验证 / 被拒绝 / 取消」。
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
  email: '1921089334@qq.com',
  role: 'user',
  status: 'active'
} as any

const mountModal = (user: any = targetUser) =>
  mount(UserReset2FAModal, {
    props: { show: true, user },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show', 'title'],
          emits: ['close'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        TotpStepUpDialog: true,
        UAlert: {
          props: ['title', 'description'],
          template: '<div><slot /></div>'
        },
        UAvatar: true,
        UBadge: true,
        UTextarea: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<textarea :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />'
        },
        UFormField: {
          props: ['label', 'error', 'help'],
          template: '<div><slot /><slot name="hint" /></div>'
        },
        UForm: {
          template: '<form @submit.prevent="$emit(\'submit\', $event)"><slot /></form>'
        },
        UButton: {
          props: ['label', 'disabled', 'loading'],
          emits: ['click'],
          template: '<button :disabled="disabled" @click="$emit(\'click\')">{{ label }}</button>'
        }
      }
    }
  })

const typeReason = async (wrapper: ReturnType<typeof mountModal>, text: string) => {
  await wrapper.find('textarea').setValue(text)
  await flushPromises()
}

const submit = async (wrapper: ReturnType<typeof mountModal>) => {
  await wrapper.find('form').trigger('submit')
  await flushPromises()
}

describe('UserReset2FAModal', () => {
  beforeEach(() => {
    resetUser2FA.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    stepUpRun.mockReset()
    stepUpRun.mockImplementation(async (action: () => Promise<unknown>) => action())
  })

  it('展示目标用户邮箱与警告文案', () => {
    const wrapper = mountModal()
    expect(wrapper.find('[data-test="reset-2fa-target-email"]').text()).toBe('1921089334@qq.com')
    // 警告文案来自 i18n key：标题与正文都要渲染出来（t 被替换成 key）。
    expect(wrapper.html()).toContain('admin.users.reset2fa.warningTitle')
    expect(wrapper.html()).toContain('admin.users.reset2fa.warningBody')
    // 只重置 TOTP、Passkey 保留的说明必须出现。
    expect(wrapper.html()).toContain('admin.users.reset2fa.passkeyNotice')
  })

  it('原因为空时确认按钮禁用（二次确认门禁）', async () => {
    const wrapper = mountModal()
    const confirm = wrapper.find('[data-test="reset-2fa-confirm"]')
    expect(confirm.attributes('disabled')).toBeDefined()

    // 纯空白同样不算填了原因。
    await typeReason(wrapper, '    ')
    expect(wrapper.find('[data-test="reset-2fa-confirm"]').attributes('disabled')).toBeDefined()
    expect(resetUser2FA).not.toHaveBeenCalled()
  })

  it('填了原因后可以提交，并把去空格后的原因发给后端', async () => {
    resetUser2FA.mockResolvedValue({
      user_id: 3249,
      email: targetUser.email,
      notification_queued: true,
      revoked_step_up_grants: true,
      passkey_bound: false
    })
    const wrapper = mountModal()
    await typeReason(wrapper, '  用户忘记动态口令，经工单核实  ')
    expect(wrapper.find('[data-test="reset-2fa-confirm"]').attributes('disabled')).toBeUndefined()

    await submit(wrapper)
    expect(stepUpRun).toHaveBeenCalledTimes(1)
    expect(resetUser2FA).toHaveBeenCalledWith(3249, '用户忘记动态口令，经工单核实')
    expect(showSuccess).toHaveBeenCalledWith(
      expect.stringContaining('admin.users.reset2fa.success')
    )
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(showError).not.toHaveBeenCalled()
  })

  it('原因超过 500 字时禁用提交并提示上限', async () => {
    const wrapper = mountModal()
    await typeReason(wrapper, '忘'.repeat(501))
    expect(wrapper.find('[data-test="reset-2fa-confirm"]').attributes('disabled')).toBeDefined()
    expect(wrapper.html()).toContain('admin.users.reset2fa.reasonTooLong')
    expect(resetUser2FA).not.toHaveBeenCalled()
  })

  it('邮件没发出时明确提示需要人工联系用户', async () => {
    resetUser2FA.mockResolvedValue({
      user_id: 3249,
      email: targetUser.email,
      notification_queued: false,
      revoked_step_up_grants: true,
      passkey_bound: false
    })
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')
    await submit(wrapper)
    expect(showSuccess).toHaveBeenCalledWith(
      expect.stringContaining('admin.users.reset2fa.successWithoutEmail'),
      expect.any(Number)
    )
  })

  it('用户仍绑定 Passkey 时提示登录仍需 Passkey', async () => {
    resetUser2FA.mockResolvedValue({
      user_id: 3249,
      email: targetUser.email,
      notification_queued: true,
      revoked_step_up_grants: true,
      passkey_bound: true
    })
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')
    await submit(wrapper)
    expect(showSuccess).toHaveBeenCalledWith(
      expect.stringContaining('admin.users.reset2fa.successWithPasskey'),
      expect.any(Number)
    )
  })

  it('后端返回「未启用 2FA」时本地化提示且不关闭弹窗', async () => {
    resetUser2FA.mockRejectedValue({
      response: { data: { reason: 'RESET_2FA_TARGET_NOT_ENABLED', message: 'this user does not have 2FA enabled' } }
    })
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')
    await submit(wrapper)
    expect(wrapper.find('[data-test="reset-2fa-error"]').text()).toContain('admin.users.reset2fa.targetNotEnabled')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.emitted('success')).toBeUndefined()
  })

  it('用户不存在时提示 404 文案', async () => {
    resetUser2FA.mockRejectedValue({
      response: { data: { reason: 'USER_NOT_FOUND', message: 'user not found' } }
    })
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')
    await submit(wrapper)
    expect(wrapper.find('[data-test="reset-2fa-error"]').text()).toContain('admin.users.reset2fa.userNotFound')
  })

  it('管理员目标额外展示「管理员也要尽快重绑」的警告', async () => {
    const wrapper = mountModal({ ...targetUser, role: 'admin' })
    expect(wrapper.html()).toContain('admin.users.reset2fa.adminTargetWarning')
  })

  it('用户取消 step-up 验证时不报错也不关闭弹窗', async () => {
    stepUpRun.mockRejectedValue(new StepUpCancelledError())
    const wrapper = mountModal()
    await typeReason(wrapper, '用户忘记动态口令')
    await submit(wrapper)
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
    expect(wrapper.find('[data-test="reset-2fa-error"]').exists()).toBe(false)
  })

  it('每次重新打开都会清空上次填写的原因', async () => {
    const wrapper = mountModal()
    await typeReason(wrapper, '上一次的原因')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('')
  })
})
