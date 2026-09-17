import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { Line } from 'vue-chartjs'

import TokenUsageTrend from '../TokenUsageTrend.vue'

const messages: Record<string, string> = {
  'admin.dashboard.tokenUsageTrend': 'Token Usage Trend',
  'admin.dashboard.noDataAvailable': 'No data available',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-chartjs', () => ({
  Line: {
    props: ['data', 'options'],
    template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>',
  },
}))

describe('TokenUsageTrend', () => {
  const sampleTrend = [{
    date: '2026-09-17', requests: 1, input_tokens: 100, output_tokens: 50,
    cache_creation_tokens: 20, cache_read_tokens: 300, cost: 0.02, actual_cost: 0.01,
  }]

  it('updates chart label colors when the existing theme toggle changes the root class', async () => {
    const wasDark = document.documentElement.classList.contains('dark')
    document.documentElement.classList.remove('dark')
    const wrapper = mount(TokenUsageTrend, { props: { trendData: sampleTrend } })
    try {
      const lightColor = wrapper.getComponent(Line).props('options').scales.x.ticks.color
      document.documentElement.classList.add('dark')
      await flushPromises()
      expect(wrapper.getComponent(Line).props('options').scales.x.ticks.color).not.toBe(lightColor)
      document.documentElement.classList.remove('dark')
      await flushPromises()
      expect(wrapper.getComponent(Line).props('options').scales.x.ticks.color).toBe(lightColor)
    } finally {
      wrapper.unmount()
      document.documentElement.classList.toggle('dark', wasDark)
    }
  })

  it('keeps the default animation unless the user console explicitly opts in', async () => {
    const wrapper = mount(TokenUsageTrend, { props: { trendData: sampleTrend } })
    expect(wrapper.getComponent(Line).props('options')).not.toHaveProperty('animation')
    await wrapper.setProps({ animationDuration: 180 })
    expect(wrapper.getComponent(Line).props('options').animation.duration).toBe(180)
    await wrapper.setProps({ animationDuration: 0 })
    expect(wrapper.getComponent(Line).props('options').animation.duration).toBe(0)
    wrapper.unmount()
  })

  it('calculates cache hit rate against all prompt tokens', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 500,
            output_tokens: 100,
            cache_creation_tokens: 0,
            cache_read_tokens: 1500,
            cost: 0.01,
            actual_cost: 0.005,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    // Hit rate = 1500 / (500 + 1500 + 0) * 100 = 75%
    expect(hitRateDataset.data[0]).toBe(75)
  })

  it('returns 0 hit rate when all prompt tokens are zero', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 0,
            input_tokens: 0,
            output_tokens: 0,
            cache_creation_tokens: 0,
            cache_read_tokens: 0,
            cost: 0,
            actual_cost: 0,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    expect(hitRateDataset.data[0]).toBe(0)
  })

  it('includes cache_creation_tokens in denominator for Anthropic models', () => {
    const wrapper = mount(TokenUsageTrend, {
      props: {
        trendData: [
          {
            date: '2026-05-08',
            requests: 1,
            input_tokens: 200,
            output_tokens: 50,
            cache_creation_tokens: 300,
            cache_read_tokens: 500,
            cost: 0.02,
            actual_cost: 0.01,
          },
        ],
      },
      global: {
        stubs: {
          LoadingSpinner: true,
        },
      },
    })

    const chartData = JSON.parse(wrapper.find('.chart-data').text())
    const hitRateDataset = chartData.datasets.find(
      (ds: any) => ds.label === 'Cache Hit Rate'
    )
    // Hit rate = 500 / (200 + 500 + 300) * 100 = 50%
    expect(hitRateDataset.data[0]).toBe(50)
  })
})
