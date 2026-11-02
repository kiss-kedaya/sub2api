import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import UserConcurrencyCell from '../UserConcurrencyCell.vue'

const getLoad = vi.fn()
vi.mock('@/api/admin', () => ({
  usersAPI: { getLoad: (...args: unknown[]) => getLoad(...args) },
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const PopoverStub = defineComponent({
  name: 'UPopoverStub',
  props: { open: { type: Boolean, default: false } },
  emits: ['update:open'],
  template: '<div><slot /><slot name="content" /></div>',
})

const mountCell = () => mount(UserConcurrencyCell, {
  props: { userId: 7, current: 2, max: 10 },
  global: { stubs: { UPopover: PopoverStub } },
})

describe('UserConcurrencyCell', () => {
  beforeEach(() => {
    getLoad.mockReset()
  })

  it('does not fetch live group load until the hover opens', async () => {
    const wrapper = mountCell()
    await flushPromises()
    expect(getLoad).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('2')
    expect(wrapper.text()).toContain('10')
  })

  it('loads the per-group in-flight split on hover', async () => {
    getLoad.mockResolvedValue({
      user_id: 7,
      current_concurrency: 3,
      waiting_count: 1,
      load_rate: 40,
      max_concurrency: 10,
      groups: [
        { group_id: 3, group_name: 'Claude', inflight: 2, models: ['opus'] },
        { group_id: 0, inflight: 1 },
      ],
    })
    const wrapper = mountCell()
    ;(wrapper.vm as unknown as { open: boolean }).open = true
    await wrapper.vm.$nextTick()
    await flushPromises()

    expect(getLoad).toHaveBeenCalledWith(7)
    const load = (wrapper.vm as unknown as {
      load: { waiting_count: number; groups: { group_id: number; group_name?: string }[] }
    }).load
    expect(load.waiting_count).toBe(1)
    expect(load.groups.map(group => group.group_name ?? group.group_id)).toEqual(['Claude', 0])
  })
})
