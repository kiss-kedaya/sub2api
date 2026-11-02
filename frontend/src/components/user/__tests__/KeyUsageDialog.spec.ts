import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import KeyUsageDialog from '../KeyUsageDialog.vue'

const getMyApiKeyUsage = vi.fn()
vi.mock('@/api', () => ({
  usageAPI: { getMyApiKeyUsage: (...args: unknown[]) => getMyApiKeyUsage(...args) },
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const BaseDialogStub = defineComponent({
  props: { show: Boolean, title: String },
  template: '<div v-if="show"><h2>{{ title }}</h2><slot /><slot name="footer" /></div>',
})
const ButtonStub = defineComponent({
  props: { label: String },
  emits: ['click'],
  template: '<button @click="$emit(\'click\')">{{ label }}</button>',
})
const TableStub = defineComponent({
  props: { data: { type: Array, default: () => [] } },
  template: '<div data-test="usage-table"><div v-for="row in data" :key="row.date">{{ row.date }} {{ row.actual_cost }}</div></div>',
})

const response = {
  api_key_id: 9,
  start_date: '2026-11-03',
  end_date: '2026-11-03',
  timezone: 'Asia/Shanghai',
  today: { date: '2026-11-03', requests: 3, input_tokens: 10, output_tokens: 20, cache_read_tokens: 0, cache_write_tokens: 0, total_tokens: 30, cost: 1.5, actual_cost: 1.23 },
  range: { requests: 3, input_tokens: 10, output_tokens: 20, cache_read_tokens: 0, cache_write_tokens: 0, total_tokens: 30, cost: 1.5, actual_cost: 1.23 },
  items: [{ date: '2026-11-03', requests: 3, input_tokens: 10, output_tokens: 20, cache_read_tokens: 0, cache_write_tokens: 0, total_tokens: 30, cost: 1.5, actual_cost: 1.23 }],
}

const mountDialog = (show = true) => mount(KeyUsageDialog, {
  props: { show, apiKey: { id: 9, name: 'test' } as never },
  global: {
    stubs: {
      BaseDialog: BaseDialogStub,
      UButton: ButtonStub,
      UTable: TableStub,
      UInput: true,
      UAlert: true,
      USkeleton: true,
    },
  },
})

describe('KeyUsageDialog', () => {
  beforeEach(() => {
    getMyApiKeyUsage.mockReset()
    getMyApiKeyUsage.mockResolvedValue(response)
  })

  it('loads today by default and shows today plus range totals', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    expect(getMyApiKeyUsage).toHaveBeenCalledTimes(1)
    const [id, params] = getMyApiKeyUsage.mock.calls[0]
    expect(id).toBe(9)
    expect(params.days).toBe(1)
    expect(typeof params.timezone).toBe('string')
    expect(wrapper.text()).toContain('$1.2300')
  })

  it('switches to the 7-day preset and re-queries', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    const button = wrapper.findAll('button').find(node => node.text() === 'keys.usageDialog.preset7d')
    expect(button).toBeTruthy()
    await button!.trigger('click')
    await flushPromises()
    expect(getMyApiKeyUsage.mock.calls.at(-1)?.[1].days).toBe(7)
  })

  it('does not query while closed', async () => {
    mountDialog(false)
    await flushPromises()
    expect(getMyApiKeyUsage).not.toHaveBeenCalled()
  })
})
