import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import UserCreateModal from '../UserCreateModal.vue'

const { create, updateBalance, showSuccess, showError } = vi.hoisted(() => ({
  create: vi.fn(),
  updateBalance: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: {
      create,
      updateBalance
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

// 静态 import 的 TOTP 弹窗会拉起 API client / i18n 初始化，测试里用空组件替换。
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({
  default: { name: 'TotpStepUpDialog', template: '<div />' }
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({
    visible: { value: false },
    blockedReason: { value: '' },
    run: (action: () => Promise<unknown>) => action()
  }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => ''
}))

const mountModal = () => mount(UserCreateModal, {
  props: { show: true },
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

async function fillForm(wrapper: ReturnType<typeof mountModal>, balance?: string) {
  const inputs = wrapper.findAll('input')
  await inputs[0].setValue('new@example.com')
  await inputs[1].setValue('pass123')
  if (balance !== undefined) {
    await inputs[3].setValue(balance)
  }
}

describe('UserCreateModal initial balance', () => {
  beforeEach(() => {
    create.mockReset()
    updateBalance.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('非零余额不随建号提交，创建成功后走带 step-up 的余额调整接口', async () => {
    create.mockResolvedValue({ id: 7, email: 'new@example.com' })
    updateBalance.mockResolvedValue({})
    const wrapper = mountModal()

    await fillForm(wrapper, '100')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    const payload = create.mock.calls[0][0]
    expect(payload.balance).toBeUndefined()
    expect(updateBalance).toHaveBeenCalledWith(7, 100, 'set', 'admin.users.initialBalanceNote')
    expect(showSuccess).toHaveBeenCalledWith('admin.users.userCreated')
    expect(wrapper.emitted('success')).toBeTruthy()
  })

  it('显式 0 仍随建号提交，用于覆盖系统默认初始余额', async () => {
    create.mockResolvedValue({ id: 8, email: 'new@example.com' })
    const wrapper = mountModal()

    await fillForm(wrapper, '0')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(create).toHaveBeenCalledTimes(1)
    expect(create.mock.calls[0][0].balance).toBe(0)
    expect(updateBalance).not.toHaveBeenCalled()
  })

  it('余额调整失败时提示管理员，且仍然刷新用户列表', async () => {
    create.mockResolvedValue({ id: 9, email: 'new@example.com' })
    updateBalance.mockRejectedValue(new Error('boom'))
    const wrapper = mountModal()

    await fillForm(wrapper, '50')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.emitted('success')).toBeTruthy()
  })
})
