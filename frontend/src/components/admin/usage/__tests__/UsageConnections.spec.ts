import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import UsageConnections from '../UsageConnections.vue'
import Select from '@/components/common/Select.vue'

const { listConnectionsMock } = vi.hoisted(() => ({
  listConnectionsMock: vi.fn()
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

vi.mock('@/api/admin/usageConnections', () => ({
  listConnections: listConnectionsMock
}))

const UButtonStub = {
  props: ['disabled', 'loading'],
  emits: ['click'],
  template: '<button type="button" :disabled="disabled" @click="$emit(\'click\', $event)"><slot /></button>'
}

const UInputStub = {
  props: ['modelValue', 'placeholder', 'size'],
  emits: ['update:modelValue', 'keydown'],
  template:
    '<input :value="modelValue" :placeholder="placeholder" @input="$emit(\'update:modelValue\', $event.target.value)" @keydown="$emit(\'keydown\', $event)" />'
}

describe('UsageConnections', () => {
  beforeEach(() => {
    listConnectionsMock.mockReset()
    listConnectionsMock.mockResolvedValue({
      items: [],
      has_more: false,
      page: 1,
      page_size: 20,
      retention_days: 7
    })
  })

  function mountComponent(filters = { start_time: '2026-11-27T00:00:00Z' }, refreshKey = 0) {
    return mount(UsageConnections, {
      props: { filters, refreshKey },
      global: {
        stubs: {
          UButton: UButtonStub,
          UInput: UInputStub,
          DataTable: true,
          BaseDialog: true,
          OpsErrorDetailModal: true,
          Teleport: true
        }
      }
    })
  }

  it('uses shared Select components for state and source filters and reloads on selection change', async () => {
    const wrapper = mountComponent()
    await flushPromises()

    const selects = wrapper.findAllComponents(Select)
    expect(selects).toHaveLength(2)
    expect(selects[0].props('id')).toBe('connections-state')
    expect(selects[0].props('searchable')).toBe(false)
    expect(selects[1].props('id')).toBe('connections-source')
    expect(selects[1].props('searchable')).toBe(false)

    expect(listConnectionsMock).toHaveBeenCalledTimes(1)
    expect(listConnectionsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        page: 1,
        page_size: 20,
        start_time: '2026-11-27T00:00:00Z'
      }),
      expect.any(AbortSignal)
    )

    selects[0].vm.$emit('update:modelValue', 'failed')
    selects[0].vm.$emit('change', 'failed')
    await flushPromises()

    expect(listConnectionsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        state: 'failed',
        page: 1
      }),
      expect.any(AbortSignal)
    )

    selects[1].vm.$emit('update:modelValue', 'nginx')
    selects[1].vm.$emit('change', 'nginx')
    await flushPromises()

    expect(listConnectionsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        state: 'failed',
        source: 'nginx',
        page: 1
      }),
      expect.any(AbortSignal)
    )
  })

  it('does not reload spuriously on unrelated rerenders and reloads when filters or refreshKey change', async () => {
    const filters = { start_time: '2026-11-27T00:00:00Z' }
    const wrapper = mountComponent(filters, 1)
    await flushPromises()
    expect(listConnectionsMock).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ filters, refreshKey: 1 })
    await nextTick()
    await flushPromises()
    expect(listConnectionsMock).toHaveBeenCalledTimes(1)

    await wrapper.setProps({ refreshKey: 2 })
    await flushPromises()
    expect(listConnectionsMock).toHaveBeenCalledTimes(2)

    await wrapper.setProps({
      filters: { start_time: '2026-11-28T00:00:00Z' }
    })
    await flushPromises()
    expect(listConnectionsMock).toHaveBeenCalledTimes(3)
    expect(listConnectionsMock).toHaveBeenLastCalledWith(
      expect.objectContaining({
        start_time: '2026-11-28T00:00:00Z'
      }),
      expect.any(AbortSignal)
    )
  })
})
