// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TrafficPanel from '../TrafficPanel.vue'
import Select from '@/components/common/Select.vue'
import { Bar } from 'vue-chartjs'
import type { TrafficReport } from '@/api/traffic'

const { getTraffic, getTrafficKeys, getTrafficHealth, searchUsers, searchApiKeys, listGroups } = vi.hoisted(() => ({
  getTraffic: vi.fn(), getTrafficKeys: vi.fn(), getTrafficHealth: vi.fn(), searchUsers: vi.fn(), searchApiKeys: vi.fn(), listGroups: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { usage: { searchUsers, searchApiKeys }, groups: { list: listGroups } } }))
vi.mock('@/api/traffic', async importOriginal => ({ ...await importOriginal<typeof import('@/api/traffic')>(), getTraffic, getTrafficKeys, getTrafficHealth }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/api/client', () => ({ apiClient: { get: vi.fn() } }))
vi.mock('vue-chartjs', () => ({ Bar: { name: 'Bar', props: ['data', 'options'], template: '<div data-testid="traffic-chart" />' } }))

const fixture = (): TrafficReport => ({
  upload_bytes: 1024, download_bytes: 2048, total_bytes: 3072, requests: 1,
  period: 'month', start: '2026-10-01T00:00:00+08:00', end: '2026-11-01T00:00:00+08:00',
  timezone: 'Asia/Shanghai', page: 1, page_size: 20, has_more: true, next_cursor: 'cursor-page-2',
  recorded_since: '2026-10-25T01:00:00+08:00', measurement_version: 1,
  trend: [{ bucket: '2026-10-25', upload_bytes: 1024, download_bytes: 2048, total_bytes: 3072, requests: 1 }],
  items: [{ event_id: 'event-1', user_id: 7, api_key_id: 8, group_id: 9, request_id: 'request-1',
    endpoint: '/v1/messages', method: 'POST', started_at: '2026-10-25T01:00:00+08:00', finished_at: '2026-10-25T01:01:00+08:00',
    upload_bytes: 1024, download_bytes: 2048, status_code: 499, outcome: 'canceled', request_complete: false, measurement_version: 1 }]
})
let wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.useRealTimers() })
beforeEach(() => {
  vi.clearAllMocks(); getTraffic.mockResolvedValue(fixture())
  searchUsers.mockResolvedValue([{ id: 7, email: 'demo@example.test', deleted: false }])
  searchApiKeys.mockResolvedValue([])
  getTrafficKeys.mockImplementation((_admin, _user, q = '') => Promise.resolve([{ id: 8, name: 'Production key', deleted: false }].filter(key => key.name.toLowerCase().includes(q.toLowerCase()))))
  getTrafficHealth.mockResolvedValue({ status: 'ok', pending_events: 0, quarantined_events: 0, oldest_pending_age_seconds: 0, persistence_failures: 0, checked_at: '2026-10-25T01:00:00+08:00' })
})
const render = (admin = false) => { const wrapper = mount(TrafficPanel, { props: { admin, apiKeyOptions: [{ value: null, label: 'All keys' }, { value: 8, label: 'Production key' }, { value: 9, label: 'Test key' }] }, global: { stubs: { Teleport: true } } }); wrappers.push(wrapper); return wrapper }

// The period picker is a UTabs control: reka tabs switch on a left mousedown.
const selectPeriod = (wrapper: ReturnType<typeof mount>, period: string) =>
  wrapper.get('#traffic-period').findAll('button[role="tab"]').find(tab => tab.text() === `traffic.periods.${period}`)!.trigger('mousedown')

describe('TrafficPanel', () => {
  it.each([['day', 'hour'], ['week', 'day'], ['month', 'day'], ['year', 'month']] as const)('plots independent %s buckets, including decreases and idle buckets', async (period, bucket) => {
    const uploads = [900, 100, 0, 400], downloads = [1800, 200, 0, 800]
    const trend = uploads.map((upload, i) => ({ bucket: `bucket-${i}`, upload_bytes: upload, download_bytes: downloads[i]!, total_bytes: upload + downloads[i]!, requests: upload ? 1 : 0 }))
    getTraffic.mockResolvedValue({ ...fixture(), period, trend, upload_bytes: 1400, download_bytes: 2800, total_bytes: 4200 })
    const wrapper = render(); await flushPromises()
    await selectPeriod(wrapper, period); await flushPromises()
    const data = wrapper.getComponent(Bar).props('data') as { labels: string[]; datasets: Array<{ data: number[] }> }
    expect(data.labels).toEqual(trend.map(point => point.bucket))
    expect(data.datasets.map(dataset => dataset.data)).toEqual([uploads, downloads])
    expect(data.datasets[0]!.data[1]).toBeLessThan(data.datasets[0]!.data[0]!)
    expect(data.datasets.every(dataset => dataset.data[2] === 0)).toBe(true)
    expect(wrapper.text()).toContain(`traffic.trendScopes.${bucket}`)
  })
  it('renders traffic independently of token usage, with scope and partial results', async () => {
    const wrapper = render(); await flushPromises()
    expect(getTraffic).toHaveBeenCalledWith(false, expect.objectContaining({ period: 'month', view: 'summary' }), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('3.00 KiB')
    expect(wrapper.text()).toContain('/v1/messages')
    expect(wrapper.text()).toContain('traffic.scope')
    expect(wrapper.text()).toContain('traffic.partialRead')
    expect(wrapper.text()).toContain('traffic.outcomes.canceled')
    expect(wrapper.text()).toContain('Asia/Shanghai')
    expect(wrapper.find('#traffic-user').exists()).toBe(false)
  })
  it('queries calendar periods, moves through history, paginates and resets filters', async () => {
    const wrapper = render(true); await flushPromises()
    vi.useFakeTimers()
    const userInput = wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]')
    await userInput.trigger('focus'); await userInput.setValue('demo')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(searchUsers).toHaveBeenLastCalledWith('demo')
    await wrapper.findAll('button').find(b => b.text().includes('demo@example.test'))!.trigger('click'); await flushPromises()
    expect(getTrafficKeys).toHaveBeenLastCalledWith(true, 7, '')
    await wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').trigger('focus')
    await wrapper.findAll('button').find(b => b.text().includes('Production key'))!.trigger('click'); await flushPromises()
    expect(listGroups).not.toHaveBeenCalled()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ user_id: 7, api_key_id: 8 }), expect.any(AbortSignal))
    const nextPage = wrapper.findAll('button').find(b => b.text() === 'traffic.next')!
    await nextPage.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ view: 'details', cursor: 'cursor-page-2', date: '2026-10-01' }), expect.any(AbortSignal))
    await selectPeriod(wrapper, 'year'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ period: 'year', view: 'summary' }), expect.any(AbortSignal))
    await wrapper.findAll('button').find(b => b.text() === 'traffic.previousPeriod')!.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ date: '2025-10-01' }), expect.any(AbortSignal))
    await wrapper.findAll('button').find(b => b.text() === 'common.reset')!.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ period: 'month', view: 'summary' }), expect.any(AbortSignal))
    expect(getTraffic.mock.lastCall?.[1]).not.toHaveProperty('user_id')
    expect(getTraffic.mock.lastCall?.[1]).not.toHaveProperty('api_key_id')
    expect(wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]').element).toHaveProperty('value', '')
    expect(wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').element).toHaveProperty('value', '')
  })
  it('searches only the signed-in user’s key names and applies or clears their selection', async () => {
    vi.useFakeTimers()
    const wrapper = render(); await flushPromises()
    const select = wrapper.getComponent(Select)
    expect(select.props('searchable')).toBe(true)
    expect(wrapper.get('#traffic-key').text()).toContain('usage.allApiKeys')
    await wrapper.get('#traffic-key').trigger('click')
    await wrapper.get('.select-search-input').setValue('Production')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(wrapper.findAll('[role="option"]').map(option => option.text())).toEqual(['usage.allApiKeys', 'Production key'])
    await wrapper.findAll('[role="option"]').find(option => option.text() === 'Production key')!.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(false, expect.objectContaining({ api_key_id: 8, view: 'summary' }), expect.any(AbortSignal))
    expect(getTraffic.mock.lastCall?.[1]).not.toHaveProperty('user_id')
    await wrapper.get('#traffic-key').trigger('click')
    await wrapper.findAll('[role="option"]').find(option => option.text() === 'usage.allApiKeys')!.trigger('click'); await flushPromises()
    expect(getTraffic.mock.lastCall?.[1]).not.toHaveProperty('api_key_id')
    await wrapper.findAll('button').find(b => b.text() === 'common.reset')!.trigger('click')
    expect(wrapper.get('#traffic-key').text()).toContain('usage.allApiKeys')
    expect(searchUsers).not.toHaveBeenCalled()
    expect(searchApiKeys).not.toHaveBeenCalled()
    expect(getTrafficKeys.mock.calls.every(call => call[0] === false && call[1] === undefined)).toBe(true)
  })
  it('resets typed searches and cancels pending lookups', async () => {
    vi.useFakeTimers()
    const wrapper = render(true); await flushPromises()
    await wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]').setValue('pending user')
    await wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').setValue('pending key')
    await wrapper.findAll('button').find(b => b.text() === 'common.reset')!.trigger('click')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    expect(searchUsers).not.toHaveBeenCalled()
    expect(searchApiKeys).not.toHaveBeenCalled()
    expect(wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]').element).toHaveProperty('value', '')
    expect(wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').element).toHaveProperty('value', '')
  })
  it('searches and filters a deleted own key without exposing another user scope', async () => {
    vi.useFakeTimers()
    getTrafficKeys.mockResolvedValue([{ id: 33, name: 'Old project', deleted: true }])
    const wrapper = render(); await flushPromises()
    await wrapper.get('#traffic-key').trigger('click')
    await wrapper.get('.select-search-input').setValue('Old')
    await vi.advanceTimersByTimeAsync(300); await flushPromises()
    const option = wrapper.findAll('[role="option"]').find(option => option.text().includes('Old project'))!
    expect(option.text()).toContain('traffic.deletedKey')
    await option.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(false, expect.objectContaining({ api_key_id: 33 }), expect.any(AbortSignal))
    expect(getTrafficKeys).toHaveBeenLastCalledWith(false, undefined, 'Old', expect.any(AbortSignal))
    expect(wrapper.get('#traffic-key').text()).toContain('Old project')
    await wrapper.get('#traffic-key').trigger('click'); await flushPromises()
    expect(wrapper.get('.select-search-input').element).toHaveProperty('value', '')
    expect(getTrafficKeys).toHaveBeenLastCalledWith(false, undefined, '', expect.any(AbortSignal))
    expect(wrapper.get('#traffic-key').text()).toContain('Old project')
  })
  it('uses history keys for the selected admin user and marks deleted choices', async () => {
    vi.useFakeTimers()
    getTrafficKeys.mockResolvedValue([{ id: 33, name: 'Deleted project', deleted: true }])
    const wrapper = render(true); await flushPromises()
    const input = wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]')
    await input.trigger('focus'); await input.setValue('demo'); await vi.advanceTimersByTimeAsync(300)
    await wrapper.findAll('button').find(button => button.text().includes('demo@example.test'))!.trigger('click'); await flushPromises()
    await wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').trigger('focus')
    const key = wrapper.findAll('button').find(button => button.text().includes('Deleted project'))!
    expect(key.text()).toContain('traffic.deletedKey')
    await key.trigger('click'); await flushPromises()
    expect(getTraffic).toHaveBeenLastCalledWith(true, expect.objectContaining({ user_id: 7, api_key_id: 33 }), expect.any(AbortSignal))
    expect(getTrafficKeys).toHaveBeenLastCalledWith(true, 7, '')
    expect(searchApiKeys).not.toHaveBeenCalled()
  })
  it('ignores stale own key searches and retains the selected key label', async () => {
    vi.useFakeTimers()
    const wrapper = render(); await flushPromises()
    let resolveOld!: (keys: Array<{id: number;name: string;deleted: boolean}>) => void
    getTrafficKeys.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await wrapper.get('#traffic-key').trigger('click'); await wrapper.get('.select-search-input').setValue('old'); await vi.advanceTimersByTimeAsync(300)
    const oldSignal = getTrafficKeys.mock.lastCall![3] as AbortSignal
    getTrafficKeys.mockResolvedValueOnce([{id: 33,name: 'Current key',deleted: true}])
    await wrapper.get('.select-search-input').setValue('current'); await vi.advanceTimersByTimeAsync(300); await flushPromises()
    resolveOld([{id: 34,name: 'Stale key',deleted: true}]); await flushPromises()
    expect(oldSignal.aborted).toBe(true); expect(wrapper.text()).not.toContain('Stale key')
    await wrapper.findAll('[role="option"]').find(option => option.text().includes('Current key'))!.trigger('click')
    expect(wrapper.get('#traffic-key').text()).toContain('Current key')
  })
  it('reports node-local backlog and preserves reports when health or key searches fail', async () => {
    getTrafficHealth.mockResolvedValueOnce({status: 'degraded',pending_events: 4,quarantined_events: 1,oldest_pending_age_seconds: 90,persistence_failures: 0,checked_at: '2026-10-25T01:00:00+08:00'})
    const admin = render(true); await flushPromises()
    expect(admin.get('[data-testid="traffic-health"]').text()).toContain('traffic.healthStatuses.degraded')
    expect(admin.get('[data-testid="traffic-health"]').text()).toContain('traffic.nodeHealthScope')
    getTrafficHealth.mockRejectedValueOnce(new Error('unavailable'))
    await admin.findAll('button').find(button => button.text() === 'common.refresh')!.trigger('click'); await flushPromises()
    expect(admin.get('[data-testid="traffic-health"]').text()).toContain('traffic.healthFailed')
    expect(admin.text()).toContain('3.00 KiB')
    getTrafficKeys.mockRejectedValueOnce(new Error('unavailable'))
    const user = render(); await flushPromises()
    expect(user.text()).toContain('traffic.keysFailed'); expect(user.text()).toContain('3.00 KiB')
    await user.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(user.text()).not.toContain('traffic.keysFailed')
    expect(user.find('[data-testid="traffic-health"]').exists()).toBe(false)
  })
  it('shows queue pressure and degraded optional storage independently of business records', async () => {
    getTrafficHealth.mockResolvedValueOnce({status: 'degraded', pending_events: 0, quarantined_events: 0, oldest_pending_age_seconds: 0, persistence_failures: 2, checked_at: '2026-10-25T01:00:00+08:00', queued_events: 1024, queue_capacity: 1024, processing_events: 32, dropped_events: 9, storage_available: false})
    const wrapper = render(true); await flushPromises()
    expect(wrapper.get('[data-testid="traffic-health"]').text()).toContain('traffic.queueSummary')
    expect(wrapper.get('[data-testid="traffic-health"]').text()).toContain('traffic.storageUnavailable')
    expect(wrapper.text()).toContain('3.00 KiB')
    expect(wrapper.text()).toContain('/v1/messages')
  })
  it('clears stale results on failure and distinguishes no records from historical collection', async () => {
    const wrapper = render(); await flushPromises()
    getTraffic.mockRejectedValueOnce(new Error('unavailable'))
    await wrapper.findAll('button').find(b => b.text() === 'common.refresh')!.trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('traffic.recordsFailed')
    expect(wrapper.text()).not.toContain('/v1/messages')
    getTraffic.mockResolvedValue({ ...fixture(), items: [], trend: [], recorded_since: null, requests: 0 })
    await wrapper.findAll('button').find(b => b.text() === 'common.refresh')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('traffic.notRecorded')
    expect(wrapper.text()).toContain('traffic.history')
  })
  it('aborts outdated requests and never displays their responses', async () => {
    let resolveFirst!: (data: TrafficReport) => void
    getTraffic.mockImplementationOnce(() => new Promise<TrafficReport>(resolve => { resolveFirst = resolve }))
    const wrapper = render()
    const firstSignal = getTraffic.mock.calls[0]![2] as AbortSignal
    await selectPeriod(wrapper, 'day'); await flushPromises()
    expect(firstSignal.aborted).toBe(true)
    resolveFirst({ ...fixture(), items: [{ ...fixture().items[0]!, endpoint: '/stale' }] }); await flushPromises()
    expect(wrapper.text()).not.toContain('/stale')
    wrapper.unmount()
    expect((getTraffic.mock.lastCall![2] as AbortSignal).aborted).toBe(true)
  })
  it('paginates only details, retains totals, and reuses cursors when going back', async () => {
    const wrapper = render(); await flushPromises()
    getTraffic.mockClear()
    const click = async (label: string) => { await wrapper.findAll('button').find(b => b.text() === label)!.trigger('click'); await flushPromises() }
    await click('traffic.next')
    expect(getTraffic).toHaveBeenCalledTimes(1)
    expect(getTraffic.mock.lastCall?.[1]).toMatchObject({ view: 'details', cursor: 'cursor-page-2' })
    expect(getTraffic.mock.lastCall?.[1]).not.toHaveProperty('page')
    expect(wrapper.text()).toContain('3.00 KiB')
    await click('traffic.previous')
    expect(getTraffic.mock.lastCall?.[1]).toMatchObject({ view: 'details', cursor: undefined })
    await click('traffic.next')
    await selectPeriod(wrapper, 'day'); await flushPromises()
    const detailCalls = getTraffic.mock.calls.filter(call => call[1].view === 'details')
    expect(detailCalls.at(-1)?.[1].cursor).toBeUndefined()
  })
  it('keeps details usable while summary is slow or fails', async () => {
    let rejectSummary!: (error: Error) => void
    getTraffic.mockImplementation((_admin, query) => query.view === 'summary'
      ? new Promise((_resolve, reject) => { rejectSummary = reject }) : Promise.resolve(fixture()))
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('/v1/messages')
    await wrapper.findAll('button').find(b => b.text() === 'traffic.next')!.trigger('click'); await flushPromises()
    expect(getTraffic.mock.lastCall?.[1]).toMatchObject({ view: 'details', cursor: 'cursor-page-2' })
    rejectSummary(new Error('summary timeout')); await flushPromises()
    expect(wrapper.text()).toContain('traffic.failed')
    expect(wrapper.text()).toContain('/v1/messages')
  })
  it('retries a failed next page with the same cursor and preserves the summary', async () => {
    const wrapper = render(); await flushPromises()
    getTraffic.mockRejectedValueOnce(new Error('details timeout'))
    await wrapper.findAll('button').find(b => b.text() === 'traffic.next')!.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('traffic.recordsFailed')
    expect(wrapper.text()).toContain('3.00 KiB')
    expect(wrapper.text()).not.toContain('/v1/messages')
    await wrapper.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(getTraffic.mock.lastCall?.[1]).toMatchObject({ view: 'details', cursor: 'cursor-page-2' })
    expect(wrapper.text()).not.toContain('traffic.recordsFailed')
    expect(wrapper.text()).toContain('/v1/messages')
  })
  it('aligns independently loaded periods when the first requests straddle midnight', async () => {
    let resolveDetails!: (data: TrafficReport) => void
    getTraffic.mockImplementationOnce(() => new Promise(resolve => { resolveDetails = resolve }))
    const wrapper = render(); await flushPromises()
    const november = { ...fixture(), start: '2026-11-01T00:00:00+08:00', end: '2026-12-01T00:00:00+08:00' }
    getTraffic.mockResolvedValue(november)
    resolveDetails(november); await flushPromises()
    expect(getTraffic.mock.lastCall?.[1]).toMatchObject({ view: 'summary', date: '2026-11-01' })
    expect(wrapper.text()).toContain('2026-11-01')
    expect(wrapper.text()).not.toContain('2026-10-01 —')
  })

})
