import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserAllowedGroupsModal from '../UserAllowedGroupsModal.vue'
import type { AdminUser } from '@/types'

const { listGroups, updateUser, showSuccess, showError, showInfo } = vi.hoisted(() => ({
  listGroups: vi.fn(),
  updateUser: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: { list: listGroups },
    users: { update: updateUser },
  },
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

const targetUser = {
  id: 7,
  email: 'user@example.com',
  allowed_groups: [] as number[],
  group_rates: {} as Record<number, number>,
  restrict_public_groups: false,
} as unknown as AdminUser

function groupsPage() {
  return {
    items: [
      {
        id: 2,
        name: 'Exclusive Group',
        platform: 'anthropic',
        status: 'active',
        is_exclusive: true,
        subscription_type: 'standard',
        rate_multiplier: 1,
      },
    ],
    total: 1,
    page: 1,
    page_size: 1000,
    pages: 1,
  }
}

async function mountModal() {
  const wrapper = mount(UserAllowedGroupsModal, {
    props: { show: false, user: targetUser },
    global: {
      stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        PlatformIcon: true,
        UFormField: { template: '<div><slot /></div>' },
        UCheckbox: {
          props: ['modelValue'],
          emits: ['update:model-value'],
          template: '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:model-value\', !modelValue)" />',
        },
        USwitch: { template: '<div />' },
        UInput: { template: '<input />' },
      },
    },
  })
  // 弹窗按「show 从 false 变 true」触发加载，直接在挂载时给 true 不会跑 load()。
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

function selectExclusiveGroup(wrapper: ReturnType<typeof mountModal>) {
  // Nuxt UI 的 UCheckbox 在单测里由 unplugin 直接解析，global stubs 拦不住；
  // 这里直接切换 setup 状态里的勾选，保存链路（step-up -> 重试 -> 成功）不变。
  (wrapper.vm as any).groupConfigs[0].isSelected = true
}

function stepUpError(code: string) {
  return Object.assign(new Error(code), { code })
}

beforeEach(() => {
  listGroups.mockReset()
  listGroups.mockResolvedValue(groupsPage())
  updateUser.mockReset()
  showSuccess.mockReset()
  showError.mockReset()
  showInfo.mockReset()
})

describe('user group config step-up gate', () => {
  it('requires step-up before saving rates/allowed groups and retries once verified', async () => {
    updateUser
      .mockRejectedValueOnce(stepUpError('STEP_UP_REQUIRED'))
      .mockResolvedValueOnce({ id: 7 })
    const wrapper = await mountModal()
    await flushPromises()

    // 勾选专属分组：保存会写入 allowed_groups（权限变更）。
    selectExclusiveGroup(wrapper)
    const pending = (wrapper.vm as any).handleSave()
    await flushPromises()
    expect(updateUser).toHaveBeenCalledTimes(1)

    await wrapper.get('[data-test="totp-verify"]').trigger('click')
    await pending
    await flushPromises()

    expect(updateUser).toHaveBeenCalledTimes(2)
    const payload = updateUser.mock.calls[1][1]
    expect(payload.allowed_groups).toContain(2)
    expect(showSuccess).toHaveBeenCalledWith('admin.users.groupConfigUpdated')
    expect(wrapper.emitted('success')).toBeTruthy()
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('does not save when step-up is cancelled and keeps the dialog open', async () => {
    updateUser.mockRejectedValueOnce(stepUpError('STEP_UP_REQUIRED'))
    const wrapper = await mountModal()
    await flushPromises()

    selectExclusiveGroup(wrapper)
    const pending = (wrapper.vm as any).handleSave()
    await flushPromises()
    await wrapper.get('[data-test="totp-cancel"]').trigger('click')
    await pending
    await flushPromises()

    expect(updateUser).toHaveBeenCalledTimes(1)
    expect(showInfo).toHaveBeenCalledWith('stepUp.cancelled')
    expect(wrapper.emitted('close')).toBeFalsy()
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows a clear error when no second factor is available', async () => {
    updateUser.mockRejectedValueOnce(stepUpError('STEP_UP_TOTP_NOT_ENABLED'))
    const wrapper = await mountModal()
    await flushPromises()

    selectExclusiveGroup(wrapper)
    await (wrapper.vm as any).handleSave()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('stepUp.notEnabled')
    expect(showSuccess).not.toHaveBeenCalled()
  })
})
