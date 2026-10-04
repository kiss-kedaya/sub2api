import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MonitorQualityEvent } from '@/api/channelMonitorV2'
import ChannelMonitorV3QualityHistory from '../ChannelMonitorV3QualityHistory.vue'
import { getQualityArtwork, getQualityEvents } from '@/api/channelMonitorV2'

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

vi.mock('@/api/channelMonitorV2', () => ({
  getQualityEvents: vi.fn(),
  getQualityArtwork: vi.fn(),
}))

const mockedEvents = vi.mocked(getQualityEvents)
const mockedArtwork = vi.mocked(getQualityArtwork)
enableAutoUnmount(afterEach)

function event(id: number, status: 'success' | 'degraded'): MonitorQualityEvent {
  return {
    id,
    group_id: 43,
    account_id: 7,
    model_id: 'claude-sonnet-5',
    status,
    error_message: status === 'degraded' ? 'feet do not plausibly contact crank pedals' : '',
    created_at: '2026-09-28T04:28:14+08:00',
  }
}

function mountHistory(props: Record<string, unknown> = {}) {
  return mount(ChannelMonitorV3QualityHistory, {
    props: { groupId: 43, enabled: true, ...props },
    global: { stubs: { teleport: true } },
  })
}

describe('ChannelMonitorV3QualityHistory', () => {
  it('pauses periodic history requests when the page is hidden', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    mockedEvents.mockResolvedValue([])
    const wrapper = mountHistory()
    try {
      await flushPromises()
      expect(mockedEvents).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(120000)
      expect(mockedEvents).toHaveBeenCalledTimes(1)
      visibility.mockReturnValue('visible')
      await vi.advanceTimersByTimeAsync(60000); await flushPromises()
      expect(mockedEvents).toHaveBeenCalledTimes(2)
    } finally { wrapper.unmount(); visibility.mockRestore(); vi.useRealTimers() }
  })
  it('shows tested and actual models without a duplicate fingerprint row or chip provider label', async () => {
    mockedEvents.mockResolvedValue([{ ...event(9, 'degraded'), model_id: 'gpt-6-astra', quality_provider: 'chanshui',
      audit_summary: { score: 0, candidate_model: 'gpt-5.6-luna', sections: [{ name: 'fingerprint', status: '', score: null }] } }])
    const wrapper = mountHistory(); await flushPromises()
    await wrapper.get('[data-testid="quality-history-chip-9"]').trigger('mouseenter'); await flushPromises()
    expect(wrapper.get('[data-testid="quality-tested-model"]').text()).toContain('gpt-6-astra')
    expect(wrapper.get('[data-testid="quality-actual-model"]').text()).toContain('gpt-5.6-luna')
    expect(wrapper.get('[data-testid="quality-audit-summary"]').text()).not.toContain('monitorCommon.qualitySections.fingerprint')
    expect(wrapper.get('[data-testid="quality-history-chip-9"]').text()).not.toContain('monitorCommon.qualityChanshui')
    expect(mockedArtwork).not.toHaveBeenCalled()
  })
  it('shows a safe Chanshui summary without requesting artwork', async () => {
    mockedEvents.mockResolvedValue([{ ...event(9, 'success'), quality_provider: 'chanshui',
      audit_summary: { score: 100, candidate_model: 'gpt-6-astra', sections: [{ name: 'fingerprint', status: '', score: null }] } }])
    const wrapper = mountHistory(); await flushPromises()
    await wrapper.get('[data-testid="quality-history-chip-9"]').trigger('mouseenter'); await flushPromises()
    expect(mockedArtwork).not.toHaveBeenCalled()
    expect(wrapper.find('iframe').exists()).toBe(false)
    expect(wrapper.get('[data-testid="quality-audit-summary"]').text()).toContain('gpt-6-astra')
    expect(wrapper.text()).toContain('100')
    wrapper.unmount()
  })
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('does not fetch or render when detection is disabled', async () => {
    const wrapper = mountHistory({ enabled: false })
    await flushPromises()
    expect(mockedEvents).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="channel-quality-history"]').exists()).toBe(false)
  })

  it('renders one chip per completed check and marks degraded ones', async () => {
    mockedEvents.mockResolvedValue([event(2, 'degraded'), event(1, 'success')])
    const wrapper = mountHistory()
    await flushPromises()
    expect(mockedEvents).toHaveBeenCalledWith(43, 30)
    const chips = wrapper.findAll('.quality-history-chip')
    expect(chips).toHaveLength(2)
    expect(chips[0].classes()).toContain('is-degraded')
    expect(chips[1].classes()).toContain('is-pass')
  })

  it('shows the empty state when no completed checks exist', async () => {
    mockedEvents.mockResolvedValue([])
    const wrapper = mountHistory()
    await flushPromises()
    expect(wrapper.text()).toContain('monitorCommon.qualityHistoryEmpty')
  })

  it('lazily loads the artwork on hover and renders a sandboxed frame', async () => {
    mockedEvents.mockResolvedValue([event(2, 'degraded')])
    mockedArtwork.mockResolvedValue('<html><svg></svg></html>')
    const wrapper = mountHistory()
    await flushPromises()
    expect(mockedArtwork).not.toHaveBeenCalled()

    await wrapper.find('[data-testid="quality-history-chip-2"]').trigger('mouseenter')
    expect(mockedArtwork).toHaveBeenCalledWith(43, 2, expect.anything())
    await flushPromises()

    const frame = wrapper.find('[data-testid="quality-history-artwork"]')
    expect(frame.exists()).toBe(true)
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('srcdoc')).toContain('<svg>')
    expect(wrapper.text()).toContain('feet do not plausibly contact crank pedals')
    expect(wrapper.get('[data-testid="quality-history-event-id"]').text()).toBe('#2')
  })

  it('reuses the cached artwork on a second hover', async () => {
    mockedEvents.mockResolvedValue([event(2, 'success')])
    mockedArtwork.mockResolvedValue('<html></html>')
    const wrapper = mountHistory()
    await flushPromises()
    const chip = wrapper.find('[data-testid="quality-history-chip-2"]')
    await chip.trigger('mouseenter')
    await flushPromises()
    await wrapper.find('.quality-history-strip').trigger('mouseleave')
    await chip.trigger('mouseenter')
    await flushPromises()
    expect(mockedArtwork).toHaveBeenCalledTimes(1)
  })

  it('reports a failed artwork load without breaking the popover', async () => {
    mockedEvents.mockResolvedValue([event(3, 'success')])
    mockedArtwork.mockRejectedValue(new Error('boom'))
    const wrapper = mountHistory()
    await flushPromises()
    await wrapper.find('[data-testid="quality-history-chip-3"]').trigger('mouseenter')
    await flushPromises()
    expect(wrapper.text()).toContain('monitorCommon.qualityArtworkFailed')
    expect(wrapper.find('[data-testid="quality-history-artwork"]').exists()).toBe(false)
  })
})
