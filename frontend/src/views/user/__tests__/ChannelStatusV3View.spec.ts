import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { getSnapshot, getMatrix, getAvailable, getUserGroupRates } = vi.hoisted(() => ({
  getSnapshot: vi.fn(),
  getMatrix: vi.fn(),
  getAvailable: vi.fn(),
  getUserGroupRates: vi.fn(),
}))

vi.mock('@/api/channelMonitorV2', () => ({
  getSnapshot,
  getMatrix,
}))

vi.mock('@/api/groups', () => ({
  default: {
    getAvailable,
    getUserGroupRates,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'zh-CN' },
    }),
  }
})

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: defineComponent({
    name: 'AppLayout',
    setup: (_, { slots }) => () => h('div', slots.default?.()),
  }),
}))

vi.mock('@/components/user/monitor/ChannelMonitorV3Card.vue', () => ({
  default: defineComponent({
    name: 'ChannelMonitorV3Card',
    props: ['row'],
    setup: (props) => () => h('div', { 'data-test': 'monitor-card' }, props.row.group_name),
  }),
}))

import ChannelStatusV3View from '../ChannelStatusV3View.vue'

describe('ChannelStatusV3View first paint', () => {
  beforeEach(() => {
    getSnapshot.mockReset()
    getMatrix.mockReset()
    getAvailable.mockReset()
    getUserGroupRates.mockReset()
    getAvailable.mockResolvedValue([])
    getUserGroupRates.mockResolvedValue({})
  })

  it('paints the card grid as soon as the matrix returns, without waiting for snapshot', async () => {
    let resolveSnapshot: ((value: unknown) => void) | undefined
    getSnapshot.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveSnapshot = resolve
        }),
    )
    getMatrix.mockResolvedValue({
      items: [
        {
          platform: 'openai',
          group_id: 7,
          group_name: 'Codex',
          metrics: {},
          health: { overall: 'healthy' },
          buckets: [],
        },
      ],
    })

    const wrapper = mount(ChannelStatusV3View)
    await flushPromises()

    expect(wrapper.get('[data-test="monitor-card"]').text()).toBe('Codex')
    expect(getSnapshot).toHaveBeenCalled()
    expect(resolveSnapshot).toBeTypeOf('function')

    resolveSnapshot?.({
      coverage: { data_through: '2026-09-05T00:00:00Z', coverage_complete: true, bootstrap: null },
      config: { refresh_interval_seconds: 60 },
      trend: [],
      metrics: { error_rate: 0, cache_rate: 0 },
    })
    await flushPromises()
    wrapper.unmount()
  })
})
