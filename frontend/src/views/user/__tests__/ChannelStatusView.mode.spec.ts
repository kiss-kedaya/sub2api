import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'

const isV1 = vi.fn(() => false)
const isV2 = vi.fn(() => true)

vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorV1Mode: () => isV1(),
  isChannelMonitorV2Mode: () => isV2(),
}))

vi.mock('../ChannelStatusV1View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV1View', setup: () => () => h('div', { 'data-testid': 'v1' }) }),
}))
vi.mock('../ChannelStatusV2View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV2View', setup: () => () => h('div', { 'data-testid': 'v2' }) }),
}))
vi.mock('../ChannelStatusV3View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV3View', setup: () => () => h('div', { 'data-testid': 'v3' }) }),
}))

import ChannelStatusView from '../ChannelStatusView.vue'

describe('ChannelStatusView mode switch', () => {
  beforeEach(() => {
    isV1.mockReset()
    isV2.mockReset()
  })

  it('renders V3 by default when V2 passive mode is active', () => {
    isV1.mockReturnValue(false)
    isV2.mockReturnValue(true)
    const wrapper = mount(ChannelStatusView, { global: { mocks: { $route: { query: {} } } } })
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })

  it('renders V1 when in v1 mode', () => {
    isV1.mockReturnValue(true)
    isV2.mockReturnValue(false)
    const wrapper = mount(ChannelStatusView, { global: { mocks: { $route: { query: {} } } } })
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v3"]').exists()).toBe(false)
  })
})
