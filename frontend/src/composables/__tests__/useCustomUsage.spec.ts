import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick, ref } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { useCustomUsage } from '../useCustomUsage'
import type { AccountListItem } from '@/types'
const api = vi.hoisted(() => ({ getCachedUsage: vi.fn(), getConfig: vi.fn(), queryUsage: vi.fn() }))
vi.mock('@/api/admin/customUsage', () => api)
const rows = ref<AccountListItem[]>([])
const active = ref(true)
const pageKey = ref('1')
let hook: ReturnType<typeof useCustomUsage>
let wrapper: VueWrapper
const result = { enabled: true, configured: true, remaining: 9, unit: 'USD' }
const account = (id: number) => ({ id, type: 'apikey', credentials: { base_url: 'https://example.test' } }) as AccountListItem
function start(count = 3) {
  rows.value = Array.from({ length: count }, (_, index) => account(index + 1))
  wrapper = mount(defineComponent({ setup() { hook = useCustomUsage(rows, active, pageKey); return () => null } }))
}
async function visible(ids: number[]) { ids.forEach(id => hook.setVisible(id, true)); await vi.advanceTimersByTimeAsync(70); await flushPromises() }
describe('visible custom usage refresh', () => {
  beforeEach(() => {
    vi.useFakeTimers(); vi.resetAllMocks(); active.value = true; pageKey.value = '1'
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    api.getCachedUsage.mockImplementation(async (ids: number[]) => ({ items: Object.fromEntries(ids.map(id => [id, { ...result }])) }))
    api.getConfig.mockResolvedValue({ enabled: true, interval_minutes: 0 })
    api.queryUsage.mockResolvedValue({ ...result, remaining: 8 })
  })
  afterEach(() => { wrapper?.unmount(); vi.useRealTimers() })
  it('does not query hidden rows and interval 0 never triggers automatic network queries', async () => {
    start(); await visible([1]); await vi.advanceTimersByTimeAsync(30 * 60000)
    expect(api.getCachedUsage).toHaveBeenCalledTimes(1)
    expect(api.getCachedUsage.mock.calls[0][0]).toEqual([1])
    expect(api.queryUsage).not.toHaveBeenCalled()
    hook.refresh(1); await flushPromises()
    expect(api.queryUsage).toHaveBeenCalledWith(1, { force: true }, expect.any(AbortSignal))
    hook.refresh(2); await flushPromises()
    expect(api.queryUsage).toHaveBeenCalledTimes(1)
  })
  it('uses the backend batch schedule without per-account configuration reads', async () => {
    api.getCachedUsage.mockResolvedValue({ items: { 1: { ...result, interval_minutes: 0 } } })
    start(); await visible([1]); await vi.advanceTimersByTimeAsync(600000)
    expect(api.getConfig).not.toHaveBeenCalled()
    expect(api.queryUsage).not.toHaveBeenCalled()
  })
  it('loads cached snapshots in chunks of at most 50', async () => {
    api.getCachedUsage.mockImplementation(async (ids: number[]) => ({ items: Object.fromEntries(ids.map(id => [id, { ...result, enabled: false }])) }))
    start(101); await visible(Array.from({ length: 101 }, (_, i) => i + 1)); await vi.advanceTimersByTimeAsync(200)
    expect(api.getCachedUsage.mock.calls.map(call => call[0].length)).toEqual([50, 50, 1])
    expect(api.getConfig).not.toHaveBeenCalled()
    expect(api.queryUsage).not.toHaveBeenCalled()
  })
  it('limits automatic refresh to two requests and stops off-screen accounts', async () => {
    api.getConfig.mockResolvedValue({ enabled: true, interval_minutes: 5 })
    start(4); await visible([1, 2, 3, 4])
    hook.setVisible(4, false)
    api.queryUsage.mockImplementation((_id: number, _body: unknown, signal: AbortSignal) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new Error('cancelled')))))
    await vi.advanceTimersByTimeAsync(15000)
    expect(api.queryUsage).toHaveBeenCalledTimes(2)
    expect(api.queryUsage.mock.calls.map(call => call[0])).toEqual([1, 2])
    expect(api.queryUsage.mock.calls[0][1]).toEqual({ force: false })
    hook.setVisible(1, false); await flushPromises()
    expect(api.queryUsage.mock.calls[0][2].aborted).toBe(true)
    expect(api.queryUsage.mock.calls.map(call => call[0])).toEqual([1, 2, 3])
  })
  it('does not let a delayed cached result overwrite newly saved settings', async () => {
    let resolve!: (value: unknown) => void
    api.getCachedUsage.mockReturnValue(new Promise(done => { resolve = done }))
    start(); await visible([1])
    hook.configSaved(1, { enabled: false, interval_minutes: 0 })
    resolve({ items: { 1: result } }); await flushPromises()
    expect(hook.states.value[1].result?.enabled).toBe(false)
    expect(api.getConfig).not.toHaveBeenCalled()
  })
  it('aborts page requests and ignores late responses after pagination', async () => {
    let resolve!: (value: unknown) => void
    api.getCachedUsage.mockReturnValue(new Promise(done => { resolve = done }))
    start(); await visible([1])
    const signal = api.getCachedUsage.mock.calls[0][1] as AbortSignal
    pageKey.value = '2'; await nextTick()
    expect(signal.aborted).toBe(true)
    resolve({ items: { 1: result } }); await flushPromises()
    expect(hook.states.value).toEqual({})
  })
  it('aborts queries on unmount and never updates discarded state', async () => {
    start(); await visible([1])
    let resolve!: (value: unknown) => void
    api.queryUsage.mockReturnValue(new Promise(done => { resolve = done }))
    hook.refresh(1); await flushPromises()
    const signal = api.queryUsage.mock.calls[0][2] as AbortSignal
    wrapper.unmount(); expect(signal.aborted).toBe(true)
    resolve({ ...result, remaining: 999 }); await flushPromises()
    expect(hook.states.value[1].result?.remaining).toBe(9)
  })
  it('stops network queries in background tabs and skips disabled configuration', async () => {
    api.getConfig.mockResolvedValue({ enabled: false, interval_minutes: 5 })
    start(); await visible([1]); await vi.advanceTimersByTimeAsync(300000)
    expect(api.queryUsage).not.toHaveBeenCalled()
    hook.configSaved(1, { enabled: true, interval_minutes: 5 })
    expect(api.queryUsage).not.toHaveBeenCalled()
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(600000)
    expect(api.queryUsage).not.toHaveBeenCalled()
  })
  it('keeps prior balance and only a safe error flag after failure', async () => {
    start(); await visible([1])
    api.queryUsage.mockRejectedValue({ message: 'private-key' })
    hook.refresh(1); await flushPromises()
    expect(hook.states.value[1]).toMatchObject({ error: true, loading: false, result: { remaining: 9 } })
    expect(JSON.stringify(hook.states.value)).not.toContain('private-key')
  })
  it('resumes config discovery when an interrupted table load finishes', async () => {
    start(); await visible([1])
    active.value = false; await nextTick()
    active.value = true; await nextTick(); await flushPromises()
    expect(api.queryUsage).not.toHaveBeenCalled()
  })
})
