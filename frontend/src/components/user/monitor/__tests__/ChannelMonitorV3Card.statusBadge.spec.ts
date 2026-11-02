import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MonitorHealth, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import type { GroupRateChange } from '@/features/channel-monitor-v2/groupSignals'
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

function rateChange(direction: 'up' | 'down'): GroupRateChange {
  const previous = direction === 'up' ? 0.06 : 0.11
  const current = direction === 'up' ? 0.09 : 0.09
  return {
    direction,
    previous,
    current,
    delta: current - previous,
    changedAt: '2026-11-01T12:00:00Z',
  }
}

function mountCard(options: {
  overall?: MonitorHealth['overall']
  isNew?: boolean
  rateChange?: GroupRateChange | null
} = {}) {
  return mount(ChannelMonitorV3Card, {
    attachTo: document.body,
    props: {
      row: row(options.overall ?? 'healthy'),
      countdownSeconds: 0,
      timelineLength: 12,
      isNew: options.isNew ?? false,
      rateChange: options.rateChange ?? null,
    },
    global: {
      stubs: {
        ProviderIcon: true,
        ChannelMonitorV3Timeline: true,
      },
    },
  })
}

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

  it('shows the NEW badge only for a group created within 24h', () => {
    const fresh = mountCard({ isNew: true })
    expect(fresh.get('[data-testid="channel-status-new-badge"]').text()).toBe('NEW')
    fresh.unmount()

    const old = mountCard({ isNew: false })
    expect(old.find('[data-testid="channel-status-new-badge"]').exists()).toBe(false)
    old.unmount()
  })

  it('renders a red up arrow with the delta for a rate increase', () => {
    const wrapper = mountCard({ rateChange: rateChange('up') })
    const badge = wrapper.get('[data-testid="channel-status-rate-change"]')
    expect(badge.text()).toContain('+0.03x')
    const chip = wrapper.get('[data-testid="channel-status-rate-change-chip"]')
    expect(chip.attributes('data-direction')).toBe('up')
    expect(chip.classes().join(' ')).toContain('text-red-600')
    expect(chip.classes().join(' ')).not.toContain('text-emerald-600')
    wrapper.unmount()
  })

  it('renders a green down arrow with the delta for a rate decrease', () => {
    const wrapper = mountCard({ rateChange: rateChange('down') })
    const badge = wrapper.get('[data-testid="channel-status-rate-change"]')
    expect(badge.text()).toContain('-0.02x')
    const chip = wrapper.get('[data-testid="channel-status-rate-change-chip"]')
    expect(chip.attributes('data-direction')).toBe('down')
    expect(chip.classes().join(' ')).toContain('text-emerald-600')
    wrapper.unmount()
  })

  it('hides the rate signal when no change is available', () => {
    const wrapper = mountCard()
    expect(wrapper.find('[data-testid="channel-status-rate-change"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('explains the rate change on hover', async () => {
    const wrapper = mountCard({ rateChange: rateChange('up') })
    await wrapper.get('[data-testid="channel-status-rate-change"]').trigger('mouseenter')
    await new Promise(resolve => setTimeout(resolve, 0))
    const tooltip = document.body.querySelector('[role="tooltip"]')
    expect(tooltip?.textContent).toContain('channelMonitorV3.rateChangeTip')
    wrapper.unmount()
  })
})