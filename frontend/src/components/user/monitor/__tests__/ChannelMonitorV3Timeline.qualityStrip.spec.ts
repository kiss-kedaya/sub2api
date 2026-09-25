import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric, MonitorQualityBucket } from '@/api/channelMonitorV2'
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

function quality(start: string, checked: number, degraded: number): MonitorQualityBucket {
  return { bucket_start: start, checked, degraded }
}

function mountTimeline(props: Record<string, unknown>) {
  return mount(ChannelMonitorV3Timeline, {
    props: { countdownSeconds: 0, length: 4, ...props },
  })
}

describe('ChannelMonitorV3Timeline quality strip', () => {
  it('renders no strip when the group has detection disabled and no buckets', () => {
    const wrapper = mountTimeline({ buckets: [bucket('2026-09-26T05:00:00Z')] })
    expect(wrapper.find('[data-testid="channel-quality-timeline"]').exists()).toBe(false)
  })

  it('renders the strip when quality buckets are present', () => {
    const wrapper = mountTimeline({
      buckets: [bucket('2026-09-26T05:00:00Z'), bucket('2026-09-26T05:05:00Z')],
      qualityBuckets: [quality('2026-09-26T05:05:00Z', 6, 2)],
    })
    const strip = wrapper.get('[data-testid="channel-quality-timeline"]')
    expect(strip.exists()).toBe(true)
    const bars = strip.findAll('.v3-soft-glass-bar')
    expect(bars.length).toBe(4)
  })

  it('paints a bucket with any degraded account red and a clean bucket green', () => {
    const wrapper = mountTimeline({
      buckets: [
        bucket('2026-09-26T04:55:00Z'),
        bucket('2026-09-26T05:00:00Z'),
        bucket('2026-09-26T05:05:00Z'),
        bucket('2026-09-26T05:10:00Z'),
      ],
      qualityBuckets: [
        quality('2026-09-26T05:05:00Z', 5, 0),
        quality('2026-09-26T05:10:00Z', 5, 1),
      ],
    })
    const bars = wrapper.get('[data-testid="channel-quality-timeline"]').findAll('.v3-soft-glass-bar')
    expect(bars[2].classes()).toContain('bg-emerald-500')
    expect(bars[3].classes()).toContain('bg-red-500')
  })

  it('places a data gap at the NOW end, not at the past end', () => {
    // Three buckets of history plus one missing trailing slot.
    const wrapper = mountTimeline({
      length: 4,
      buckets: [
        bucket('2026-09-26T04:00:00Z'),
        bucket('2026-09-26T04:05:00Z'),
        bucket('2026-09-26T04:10:00Z'),
      ],
    })
    const bars = wrapper.findAll('.v3-timeline-bars')[0].findAll('.v3-soft-glass-bar')
    expect(bars.length).toBe(4)
    // Oldest slots carry real availability bars; the trailing slot is the
    // gray insufficient-sample bar.
    expect(bars[0].classes()).not.toContain('bg-gray-300')
    expect(bars[1].classes()).not.toContain('bg-gray-300')
    expect(bars[2].classes()).not.toContain('bg-gray-300')
    expect(bars[3].classes()).toContain('bg-gray-300')
  })

  it('aligns a trailing quality bucket into the trailing health slot', () => {
    const wrapper = mountTimeline({
      length: 4,
      buckets: [
        bucket('2026-09-26T04:00:00Z'),
        bucket('2026-09-26T04:05:00Z'),
        bucket('2026-09-26T04:10:00Z'),
      ],
      qualityBuckets: [quality('2026-09-26T04:10:00Z', 4, 2)],
    })
    const bars = wrapper.get('[data-testid="channel-quality-timeline"]').findAll('.v3-soft-glass-bar')
    // The probe lands in the same slot as its health bucket (index 2), not the
    // trailing empty slot.
    expect(bars[2].classes()).toContain('bg-red-500')
    expect(bars[3].classes()).toContain('bg-gray-300')
  })

  it('keeps an undated probe out of the past end when health buckets are missing', () => {
    const wrapper = mountTimeline({
      length: 4,
      buckets: [],
      qualityBuckets: [quality('2026-09-26T04:10:00Z', 4, 1)],
    })
    const bars = wrapper.get('[data-testid="channel-quality-timeline"]').findAll('.v3-soft-glass-bar')
    // With no health axis to align to, the probe parks at NOW rather than the
    // oldest slot.
    expect(bars[3].classes()).toContain('bg-red-500')
    expect(bars[0].classes()).toContain('bg-gray-300')
  })
})
