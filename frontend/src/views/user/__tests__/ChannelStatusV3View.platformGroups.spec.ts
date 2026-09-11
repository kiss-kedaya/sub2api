import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { MonitorMatrixRow, MonitorSnapshot } from '@/api/channelMonitorV2'

const getMatrix = vi.fn()
const getSnapshot = vi.fn()

vi.mock('@/api/channelMonitorV2', () => ({
  getMatrix: (...args: unknown[]) => getMatrix(...args),
  getSnapshot: (...args: unknown[]) => getSnapshot(...args),
}))
vi.mock('@/api/groups', () => ({
  default: {
    getAvailable: vi.fn().mockResolvedValue([]),
    getUserGroupRates: vi.fn().mockResolvedValue({}),
  },
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn() }),
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<div><slot /></div>' },
}))
vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }),
  }
})

import ChannelStatusV3View from '../ChannelStatusV3View.vue'

function coverage() {
  return {
    requested_start: '2026-09-11T00:00:00Z',
    coverage_start: '2026-09-11T00:00:00Z',
    data_through: '2026-09-11T12:00:00Z',
    computed_at: '2026-09-11T12:00:00Z',
    aggregation_lag_seconds: 0,
    coverage_complete: true,
    bucket_seconds: 300,
  }
}

function metrics() {
  return {
    success_requests: 10,
    error_requests: 0,
    request_count: 10,
    token_count: 100,
    rpm: 1,
    tpm: 10,
    error_rate: 0,
    cache_rate: 0.2,
    cache_rate_numerator: 2,
    cache_rate_denominator: 10,
    ttft: { sample_count: 10, p50_ms: 100, p95_ms: 200, avg_ms: 120 },
    duration: { sample_count: 10, p50_ms: 300, p95_ms: 400, avg_ms: 320 },
  }
}

function health() {
  return {
    overall: 'healthy' as const,
    error_rate: 'healthy' as const,
    ttft: 'healthy' as const,
    cache: 'healthy' as const,
    minimum_sample: 5,
  }
}

function row(platform: string, groupId: number, groupName: string): MonitorMatrixRow {
  return {
    platform,
    group_id: groupId,
    group_name: groupName,
    metrics: metrics(),
    health: health(),
    buckets: [{ bucket_start: '2026-09-11T12:00:00Z', metrics: metrics(), health: health() }],
  }
}

const snapshot: MonitorSnapshot = {
  config: {
    version: 1,
    enabled: true,
    refresh_interval_seconds: 60,
    platforms: [],
    group_ids: [],
    health_thresholds: {
      minimum_sample: 5,
      warning_error_rate: 0.1,
      critical_error_rate: 0.3,
      target_ttft_ms: 400,
      warning_ttft_ms: 800,
      critical_ttft_ms: 1500,
      warning_cache_rate: 0.2,
      critical_cache_rate: 0.05,
      error_weight: 0.5,
      ttft_weight: 0.3,
      cache_weight: 0.2,
    },
  },
  coverage: coverage(),
  metrics: metrics(),
  health: health(),
  trend: [],
}

describe('ChannelStatusV3View platform grouping', () => {
  beforeEach(() => {
    getSnapshot.mockResolvedValue(snapshot)
    getMatrix.mockResolvedValue({
      coverage: coverage(),
      group_by: 'platform_group',
      items: [
        row('openai', 2, 'Codex'),
        row('gemini', 9, 'Gemini'),
        row('openai', 1, 'GPT'),
      ],
    })
  })

  it('renders one section per platform instead of a flat card grid', async () => {
    const wrapper = mount(ChannelStatusV3View, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Icon: true,
          EmptyState: true,
          ChannelMonitorV3Card: defineComponent({
            name: 'ChannelMonitorV3Card',
            props: ['row'],
            setup: (props) => () => h('div', { 'data-testid': `card-${props.row.group_id}` }, props.row.group_name),
          }),
        },
      },
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="channel-status-platform-openai"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="channel-status-platform-gemini"]').exists()).toBe(true)
    const openaiCards = wrapper.get('[data-testid="channel-status-platform-openai"]').findAll('[data-testid^="card-"]')
    expect(openaiCards.map((card) => card.text())).toEqual(['GPT', 'Codex'])
  })
})
