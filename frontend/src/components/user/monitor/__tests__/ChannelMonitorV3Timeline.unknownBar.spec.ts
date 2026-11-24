import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key,
      locale: { value: 'zh-CN' },
    }),
  }
})

enableAutoUnmount(afterEach)

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

  it('draws nothing for slots the window has not aggregated yet', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: {
        buckets: [bucket('healthy', 0, '2026-09-11T12:00:00Z')],
        countdownSeconds: 0,
        length: 4,
      },
    })
    // One real bucket, one bar: the three empty slots keep their grid space but
    // must not paint placeholder grey, which used to read as "no traffic".
    expect(wrapper.findAll('.v3-soft-glass-bar')).toHaveLength(1)
    expect(wrapper.findAll('.v3-bar-slot')).toHaveLength(4)
  })

  it('keeps true low availability on the black band', () => {
    // cache_rate stays 0 here; the black colour now follows the cache rate.
    const low = bucket('critical', 0.8, '2026-09-11T12:00:00Z')
    low.metrics = { ...low.metrics, cache_rate: 0.9 }
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: {
        buckets: [low],
        countdownSeconds: 0,
        length: 1,
      },
    })
    expect(wrapper.get('.v3-soft-glass-bar').classes()).toContain('bg-gray-950')
  })
})

describe('ChannelMonitorV3Timeline black cache bars', () => {
  function bucketWithCache(overall: MonitorHealth['overall'], cacheRate: number, errorRate = 0): MonitorMatrixBucket {
    return {
      bucket_start: '2026-09-11T12:00:00Z',
      metrics: { ...metrics(errorRate), cache_rate: cacheRate },
      health: health(overall),
    }
  }

  it('paints a bar black when the cache rate is 0%', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('healthy', 0)], countdownSeconds: 0, length: 1 },
    })
    const bar = wrapper.get('.v3-soft-glass-bar')
    expect(bar.classes()).toContain('bg-black')
    expect(bar.classes()).not.toContain('bg-emerald-600')
    wrapper.unmount()
  })

  it.each([0.05, 0.299])('paints a bar black at a %s cache rate', (cacheRate) => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('healthy', cacheRate)], countdownSeconds: 0, length: 1 },
    })
    expect(wrapper.get('.v3-soft-glass-bar').classes()).toContain('bg-black')
    wrapper.unmount()
  })

  it('ignores availability when the cache rate is low', () => {
    // 80% error rate used to force the black band on its own; now the low cache
    // rate has to produce black even when availability is perfect.
    const perfect = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('healthy', 0, 0)], countdownSeconds: 0, length: 1 },
    })
    expect(perfect.get('.v3-soft-glass-bar').classes()).toContain('bg-black')
    perfect.unmount()

    const bad = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('critical', 0.9, 0.8)], countdownSeconds: 0, length: 1 },
    })
    // High cache rate keeps the old availability rule untouched.
    expect(bad.get('.v3-soft-glass-bar').classes()).toContain('bg-gray-950')
    bad.unmount()
  })

  it.each([0.3, 0.301])('keeps the availability colour at a %s cache rate', (cacheRate) => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('healthy', cacheRate)], countdownSeconds: 0, length: 1 },
    })
    const bar = wrapper.get('.v3-soft-glass-bar')
    expect(bar.classes()).toContain('bg-emerald-600')
    expect(bar.classes()).not.toContain('bg-black')
    wrapper.unmount()
  })

  it('keeps the grey no-data style for an unknown bucket with a 0% cache rate', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('unknown', 0)], countdownSeconds: 0, length: 1 },
    })
    const bar = wrapper.get('.v3-soft-glass-bar')
    expect(bar.classes()).toContain('bg-gray-300')
    expect(bar.classes()).not.toContain('bg-black')
    wrapper.unmount()
  })

  // The black bars still exist (cache rate < 30%); the explanatory line above the bars was removed on purpose.
  it('does not render an explanatory legend above the bars', () => {
    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucketWithCache('healthy', 0.1)], countdownSeconds: 0, length: 1 },
    })
    expect(wrapper.find('[data-testid="cache-black-legend"]').exists()).toBe(false)
    expect(wrapper.find('.channel-signal-timeline__cache-legend').exists()).toBe(false)
    expect(wrapper.get('.v3-soft-glass-bar').classes()).toContain('bg-black')
    wrapper.unmount()
  })
})

const anchorBucketStart = '2026-11-17T12:00:00Z'
const bucketSeconds = 300

function recentBucket(offset: number) {
  return bucket('healthy', 0, new Date(Date.parse(anchorBucketStart) - offset * bucketSeconds * 1000).toISOString())
}

function mountTimeline(buckets: MonitorMatrixBucket[], timing: { anchorBucketStart?: string; bucketSeconds?: number } = { anchorBucketStart, bucketSeconds }) {
  return mount(ChannelMonitorV3Timeline, {
    props: { buckets, countdownSeconds: 0, length: 18, ...timing },
    global: { stubs: { teleport: true } },
  })
}

function occupiedSlots(wrapper: ReturnType<typeof mountTimeline>) {
  return wrapper.findAll('.v3-bar-slot')
    .flatMap((slot, index) => slot.find('.v3-soft-glass-bar').exists() ? [index] : [])
}

describe('ChannelMonitorV3Timeline time alignment', () => {
  it('places five recent buckets in the last five of eighteen slots', () => {
    const wrapper = mountTimeline(Array.from({ length: 5 }, (_, index) => recentBucket(index)))
    expect(wrapper.findAll('.v3-bar-slot')).toHaveLength(18)
    expect(occupiedSlots(wrapper)).toEqual([13, 14, 15, 16, 17])
    expect(wrapper.findAll('.v3-bar-hitbox').slice(0, 13).every(button => button.attributes('aria-label') === '-')).toBe(true)
  })

  it('keeps the last slot empty when another group has the newest bucket', () => {
    const wrapper = mountTimeline(Array.from({ length: 17 }, (_, index) => recentBucket(index + 1)))
    expect(occupiedSlots(wrapper)).toEqual(Array.from({ length: 17 }, (_, index) => index))
    expect(wrapper.findAll('.v3-bar-hitbox')[17].attributes('aria-label')).toBe('-')
  })

  it('leaves internal gaps without shifting newer buckets', () => {
    const wrapper = mountTimeline(Array.from({ length: 18 }, (_, index) => index)
      .filter(offset => offset !== 7 && offset !== 8)
      .map(recentBucket))
    expect(occupiedSlots(wrapper)).toEqual(Array.from({ length: 18 }, (_, index) => index).filter(index => index !== 9 && index !== 10))
  })

  it.each([
    {},
    { bucketSeconds },
    { anchorBucketStart },
    { anchorBucketStart: 'invalid', bucketSeconds },
    { anchorBucketStart: '1970-01-01T00:00:00Z', bucketSeconds },
    { anchorBucketStart, bucketSeconds: 0 },
    { anchorBucketStart, bucketSeconds: -300 },
    { anchorBucketStart, bucketSeconds: NaN },
    { anchorBucketStart, bucketSeconds: Infinity },
  ])('right-aligns recent buckets when timing is missing or invalid: %j', (timing) => {
    const wrapper = mountTimeline(Array.from({ length: 5 }, (_, index) => recentBucket(index)), timing)
    expect(occupiedSlots(wrapper)).toEqual([13, 14, 15, 16, 17])
  })

  it('drops buckets outside the window or with invalid dates', () => {
    const wrapper = mountTimeline([
      recentBucket(18), recentBucket(-1), recentBucket(0),
      bucket('healthy', 0, 'invalid'),
    ])
    expect(occupiedSlots(wrapper)).toEqual([17])
  })

  it('keeps the newer bucket when two timestamps round to the same slot', () => {
    const newer = bucket('critical', 0.8, anchorBucketStart)
    newer.metrics = { ...newer.metrics, cache_rate: 0.9 }
    const older = (() => { const b = bucket('healthy', 0, '2026-11-17T11:59:30Z'); b.metrics = { ...b.metrics, cache_rate: 0.9 }; return b })()
    const wrapper = mountTimeline([newer, older])
    expect(occupiedSlots(wrapper)).toEqual([17])
    expect(wrapper.get('.v3-soft-glass-bar').classes()).toContain('bg-gray-950')
  })

  it('uses slot indices for focus, tooltips, neighbors and animation delays', async () => {
    const wrapper = mountTimeline(Array.from({ length: 5 }, (_, index) => recentBucket(index)))
    const slots = wrapper.findAll('.v3-bar-slot')
    await slots[0].trigger('mouseenter')
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false)
    await slots[13].get('button').trigger('focus')
    expect(slots[13].get('button').classes()).toContain('is-active')
    expect(slots[14].get('button').classes()).toContain('is-neighbor')
    expect(slots[13].get('.v3-soft-glass-bar').attributes('style')).toContain('animation-delay: 234ms')
    expect(wrapper.get('[role="tooltip"]').text()).toBe(slots[13].get('button').attributes('aria-label'))
    await slots[13].get('button').trigger('blur')
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false)
  })
})
