import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'

import type { AdminGroup } from '@/types'
import GroupRateMultipliersModal from '../GroupRateMultipliersModal.vue'

const {
  getGroupRateMultipliers,
  batchSetGroupRateMultipliers,
  showSuccess,
  showError,
  translate
} = vi.hoisted(() => ({
  getGroupRateMultipliers: vi.fn(),
  batchSetGroupRateMultipliers: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  translate: vi.fn((key: string) => key)
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      getGroupRateMultipliers,
      batchSetGroupRateMultipliers
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: translate })
  }
})

const group = {
  id: 7,
  name: 'Test Group',
  platform: 'openai',
  rate_multiplier: 2
} as unknown as AdminGroup

const entry = (userId: number, rateMultiplier: number) => ({
  user_id: userId,
  user_name: `user${userId}`,
  user_email: `user${userId}@example.com`,
  user_notes: '',
  user_status: 'active',
  rate_multiplier: rateMultiplier,
  rpm_override: null
})

function mountModal() {
  return mount(GroupRateMultipliersModal, {
    props: { show: false, group },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Pagination: { template: '<div data-testid="pagination" />' },
        PlatformIcon: true
      }
    }
  })
}

async function openModal(wrapper: VueWrapper) {
  await wrapper.setProps({ show: true })
  await flushPromises()
}

const buttonByText = (wrapper: VueWrapper, text: string) =>
  wrapper.findAll('button').find((button) => button.text() === text)

describe('GroupRateMultipliersModal batch tools', () => {
  beforeEach(() => {
    getGroupRateMultipliers.mockReset()
    batchSetGroupRateMultipliers.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    translate.mockClear()

    getGroupRateMultipliers.mockResolvedValue([entry(1, 2), entry(2, 3)])
    batchSetGroupRateMultipliers.mockResolvedValue({ message: 'ok' })
  })

  it('批量设置指定倍率会把全部条目的专属倍率统一覆盖后保存', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    await wrapper.get<HTMLInputElement>('#batch-set-rate').setValue('1.5')
    const applySet = wrapper.get('[data-testid="apply-batch-set-rate"]')
    expect(applySet.attributes('disabled')).toBeUndefined()
    await applySet.trigger('click')
    await flushPromises()

    // 应用后输入框清空，最终倍率预览列不因批量设置而残留
    expect(wrapper.get<HTMLInputElement>('#batch-set-rate').element.value).toBe('')

    const save = buttonByText(wrapper, 'common.save')
    expect(save).toBeTruthy()
    await save!.trigger('click')
    await flushPromises()

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledTimes(1)
    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(7, [
      { user_id: 1, rate_multiplier: 1.5 },
      { user_id: 2, rate_multiplier: 1.5 }
    ])
  })

  it('批量设置指定倍率在倍率为空、0 或负数时不可应用', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    const applySet = () => wrapper.get('[data-testid="apply-batch-set-rate"]')
    expect(applySet().attributes('disabled')).toBeDefined()

    await wrapper.get<HTMLInputElement>('#batch-set-rate').setValue('0')
    expect(applySet().attributes('disabled')).toBeDefined()

    await wrapper.get<HTMLInputElement>('#batch-set-rate').setValue('-1')
    expect(applySet().attributes('disabled')).toBeDefined()

    await wrapper.get<HTMLInputElement>('#batch-set-rate').setValue('0.001')
    expect(applySet().attributes('disabled')).toBeUndefined()
  })

  it('保留原有的批量乘以倍数能力', async () => {
    const wrapper = mountModal()
    await openModal(wrapper)

    await wrapper.get<HTMLInputElement>('#batch-adjust-factor').setValue('2')
    const applyFactor = buttonByText(wrapper, 'admin.groups.applyMultiplier')
    expect(applyFactor).toBeTruthy()
    await applyFactor!.trigger('click')
    await flushPromises()

    const save = buttonByText(wrapper, 'common.save')
    await save!.trigger('click')
    await flushPromises()

    expect(batchSetGroupRateMultipliers).toHaveBeenCalledWith(7, [
      { user_id: 1, rate_multiplier: 4 },
      { user_id: 2, rate_multiplier: 6 }
    ])
  })
})
