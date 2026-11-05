import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import type { GroupSignalSource } from '@/utils/groupRateSignals'
import ChannelMonitorV3Card from '../ChannelMonitorV3Card.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, te: () => true }),
  }
})

function metrics(): MonitorMetric {
  return {
    success_requests: 80,
    error_requests: 20,
    request_count: 100,
    token_count: 1000,
    rpm: 1,
    tpm: 10,
    error_rate: 0.2,
    cache_rate: 0.5,
    cache_rate_numerator: 50,
    cache_rate_denominator: 100,
    ttft: { sample_count: 100, p50_ms: 200, p95_ms: 400, avg_ms: 250 },
    duration: { sample_count: 100, p50_ms: 800, p95_ms: 1200, avg_ms: 900 },
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

function row(overall: MonitorHealth['overall']): MonitorMatrixRow {
  return {
    platform: 'openai',
    group_id: 1,
    group_name: 'Codex',
    metrics: metrics(),
    health: health(overall),
    buckets: [
      {
        bucket_start: '2026-09-09T12:00:00Z',
        metrics: metrics(),
        health: health(overall),
      },
    ],
  }
}

function mountCard(options: {
  overall?: MonitorHealth['overall']
  signals?: GroupSignalSource | null
} = {}) {
  return mount(ChannelMonitorV3Card, {
    attachTo: document.body,
    props: {
      row: row(options.overall ?? 'healthy'),
      countdownSeconds: 0,
      timelineLength: 12,
      userRateMultiplier: 0.08,
      signals: options.signals ?? null,
    },
    global: {
      stubs: {
        ProviderIcon: true,
        ChannelMonitorV3Timeline: true,
      },
    },
  })
}

const rateUp: GroupSignalSource = {
  recent_rate_change: { direction: 'up', delta: 0.03, previous_rate: 0.06, changed_at: '2026-11-01T12:00:00Z' },
}
const rateDown: GroupSignalSource = {
  recent_rate_change: { direction: 'down', delta: 0.02, previous_rate: 0.11, changed_at: '2026-11-01T12:00:00Z' },
}
const newGroup: GroupSignalSource = { is_new: true, launched_at: '2026-11-05T01:00:00Z', launch_rate: 0.06 }

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ChannelMonitorV3Card freshness and rate signals', () => {
  it('never renders the retired 样本不足 copy', () => {
    for (const overall of ['healthy', 'warning', 'critical', 'unknown'] as const) {
      const wrapper = mountCard({ overall })
      expect(wrapper.text()).not.toContain('channelMonitorV3.unknown')
      expect(wrapper.text()).not.toContain('样本不足')
      expect(wrapper.find('[data-testid="channel-status-sample-badge"]').exists()).toBe(false)
      wrapper.unmount()
    }
  })

  it('shows the NEW marker beside the rate only for a group the backend flags as new', () => {
    const fresh = mountCard({ signals: newGroup })
    expect(fresh.get('[data-testid="group-rate-signal-new"]').exists()).toBe(true)
    expect(fresh.get('[data-testid="group-rate-signal-new"]').text()).toBe('groupSignals.newBadge')
    fresh.unmount()

    const old = mountCard({ signals: { is_new: false } })
    expect(old.find('[data-testid="group-rate-signal-new"]').exists()).toBe(false)
    old.unmount()
  })

  it('keeps the signals in the meta row, right after the user rate chip', () => {
    const wrapper = mountCard({ signals: rateUp })
    const meta = wrapper.get('.channel-signal-card__meta')
    const kids = Array.from(meta.element.children)
    const rateChip = kids.findIndex((el) => el.textContent?.includes('channelMonitorV3.userRate'))
    const signals = kids.findIndex((el) => el.getAttribute('data-testid') === 'group-rate-signals')
    expect(rateChip).toBeGreaterThanOrEqual(0)
    expect(signals).toBe(rateChip + 1)
    expect(wrapper.find('.channel-signal-card__header > [data-testid="group-rate-signals"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders an up arrow in the warning tone for a rate increase', () => {
    const wrapper = mountCard({ signals: rateUp })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(chip.attributes('data-direction')).toBe('up')
    expect(chip.text()).toContain('0.03')
    expect(chip.classes()).toContain('text-warning-700')
    expect(chip.classes()).not.toContain('text-success-700')
    wrapper.unmount()
  })

  it('renders a down arrow in the success tone for a rate decrease', () => {
    const wrapper = mountCard({ signals: rateDown })
    const chip = wrapper.get('[data-testid="group-rate-signal-rate"]')
    expect(chip.attributes('data-direction')).toBe('down')
    expect(chip.text()).toContain('0.02')
    expect(chip.classes()).toContain('text-success-700')
    wrapper.unmount()
  })

  it('hides every signal when the backend sends none', () => {
    const wrapper = mountCard()
    expect(wrapper.find('[data-testid="group-rate-signals"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows both markers when a new group was also repriced', () => {
    const wrapper = mountCard({ signals: { ...newGroup, ...rateUp } })
    expect(wrapper.find('[data-testid="group-rate-signal-rate"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="group-rate-signal-new"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
