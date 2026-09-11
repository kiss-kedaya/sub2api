import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }),
  }
})

function metrics(errorRate: number): MonitorMetric {
  return {
    success_requests: 0,
    error_requests: 0,
    request_count: 0,
    token_count: 0,
    rpm: 0,
    tpm: 0,
    error_rate: errorRate,
    cache_rate: 0,
    cache_rate_numerator: 0,
    cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  }
}

function health(overall: MonitorHealth['overall']): MonitorHealth {
  return {
    overall,
    error_rate: overall,
    ttft: overall,
    cache: overall,
    minimum_sample: 50,
  }
}

function bucket(overall: MonitorHealth['overall'], errorRate: number, start: string): MonitorMatrixBucket {
  return {
    bucket_start: start,
    metrics: metrics(errorRate),
    health: health(overall),
  }
}

describe('ChannelMonitorV3Timeline unknown bars', () => {
  it('paints insufficient-sample bars gray instead of availability black', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: {
        buckets: [bucket('unknown', 1, '2026-09-11T12:00:00Z')],
        countdownSeconds: 0,
        length: 1,
      },
    })
    const bar = wrapper.get('.v3-soft-glass-bar')
    expect(bar.classes()).toContain('bg-gray-300')
    expect(bar.classes()).not.toContain('bg-gray-950')
  })

  it('keeps true low availability on the black band', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: {
        buckets: [bucket('critical', 0.8, '2026-09-11T12:00:00Z')],
        countdownSeconds: 0,
        length: 1,
      },
    })
    const bar = wrapper.get('.v3-soft-glass-bar')
    expect(bar.classes()).toContain('bg-gray-950')
  })
})
