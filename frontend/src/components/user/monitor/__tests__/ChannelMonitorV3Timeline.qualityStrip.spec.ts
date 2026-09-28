import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      locale: { value: 'zh-CN' },
    }),
  }
})

function metrics(errorRate = 0): MonitorMetric {
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
  return { overall, error_rate: overall, ttft: overall, cache: overall, minimum_sample: 50 }
}

function bucket(start: string): MonitorMatrixBucket {
  return { bucket_start: start, metrics: metrics(), health: health('healthy') }
}

function mountTimeline(props: Record<string, unknown>) {
  return mount(ChannelMonitorV3Timeline, {
    props: { countdownSeconds: 0, length: 4, ...props },
  })
}

// The aggregated quality strip was replaced by ChannelMonitorV3QualityHistory
// (per-check chips with the artwork preview); the timeline must never render
// the old strip again.
describe('ChannelMonitorV3Timeline quality strip removal', () => {
  it('does not render the aggregated degradation strip', () => {
    const wrapper = mountTimeline({ buckets: [bucket('2026-09-26T05:00:00Z')] })
    expect(wrapper.find('[data-testid="channel-quality-timeline"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('monitorCommon.qualityLegend')
    expect(wrapper.find('.channel-signal-timeline__quality').exists()).toBe(false)
  })
})
