import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupReplaceModal from '../GroupReplaceModal.vue'

const { replaceGroup, showSuccess, showError, showInfo } = vi.hoisted(() => ({
  replaceGroup: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: { users: { replaceGroup } },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError, showInfo }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({
  default: {
    props: ['controller'],
    template:
      '<div><button data-test="totp-verify" @click="controller.onVerified()">verify</button>' +
      '<button data-test="totp-cancel" @click="controller.onCancel()">cancel</button></div>',
  },
}))

enableAutoUnmount(afterEach)

beforeEach(() => {
  replaceGroup.mockReset()
  showSuccess.mockReset()
  showError.mockReset()
  showInfo.mockReset()
})

async function selectGroup(wrapper: ReturnType<typeof mountModal>, groupId: number) {
  // Nuxt UI 的 URadioGroup 在单测里由 unplugin 直接解析，global stubs 拦不住；
  // 这里直接写 setup 状态选中目标分组，交互路径（确认按钮 -> step-up -> 重试）不变。
  (wrapper.vm as any).selectedGroupId = groupId
  await wrapper.vm.$nextTick()
}

function mountModal() {
  return mount(GroupReplaceModal, {
    props: {
      show: true,
      user: { id: 7, email: 'user@example.com' },
      oldGroup: { id: 3, name: 'Old Group' },
      allGroups: [
        { id: 2, name: 'New Group', platform: 'openai', status: 'active', is_exclusive: true, subscription_type: 'standard' },
      ],
    },
    global: {
      stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        URadioGroup: {
          props: ['modelValue', 'items'],
          emits: ['update:model-value'],
          template:
            '<div><button v-for="item in items" :key="item.value" :data-test="`group-${item.value}`"' +
            ' @click="$emit(\'update:model-value\', item.value)">{{ item.label }}</button></div>',
        },
      },
    },
  })
}

function confirmButton(wrapper: ReturnType<typeof mountModal>) {
  const button = wrapper.findAll('button').find((item) => item.text() === 'admin.users.replaceGroupConfirm')
  if (!button) throw new Error('confirm button not found')
  return button
}

function stepUpError(code: string) {
  return Object.assign(new Error(code), { code })
}

describe('replace group step-up gate', () => {
  it('prompts step-up and retries the replace after verification', async () => {
    replaceGroup
      .mockRejectedValueOnce(stepUpError('STEP_UP_REQUIRED'))
      .mockResolvedValueOnce({ migrated_keys: 2 })

    const wrapper = mountModal()
    await selectGroup(wrapper, 2)
    await confirmButton(wrapper).trigger('click')
    await flushPromises()

    expect(replaceGroup).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="totp-verify"]').trigger('click')
    await flushPromises()

    expect(replaceGroup).toHaveBeenCalledTimes(2)
    expect(replaceGroup).toHaveBeenLastCalledWith(7, 3, 2)
    expect(showSuccess).toHaveBeenCalledWith('admin.users.replaceGroupSuccess')
    expect(wrapper.emitted('close')).toBeTruthy()
    expect(showError).not.toHaveBeenCalled()
  })

  it('does not replace when the operator cancels step-up', async () => {
    replaceGroup.mockRejectedValueOnce(stepUpError('STEP_UP_REQUIRED'))

    const wrapper = mountModal()
    await selectGroup(wrapper, 2)
    await confirmButton(wrapper).trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="totp-cancel"]').trigger('click')
    await flushPromises()

    expect(replaceGroup).toHaveBeenCalledTimes(1)
    expect(showInfo).toHaveBeenCalledWith('stepUp.cancelled')
    expect(showSuccess).not.toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeFalsy()
  })

  it('shows a clear message when no second factor can be used', async () => {
    replaceGroup.mockRejectedValueOnce(stepUpError('STEP_UP_TOTP_NOT_ENABLED'))

    const wrapper = mountModal()
    await selectGroup(wrapper, 2)
    await confirmButton(wrapper).trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('stepUp.notEnabled')
    expect(replaceGroup).toHaveBeenCalledTimes(1)
  })

  it('shows a localized failure for non step-up errors', async () => {
    replaceGroup.mockRejectedValueOnce(new Error('backend exploded'))

    const wrapper = mountModal()
    await selectGroup(wrapper, 2)
    await confirmButton(wrapper).trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.users.replaceGroupFailed')
  })
})
