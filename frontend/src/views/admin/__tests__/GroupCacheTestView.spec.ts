// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupCacheTestView from '../GroupCacheTestView.vue'
import { DEFAULT_CACHE_SETTINGS, loadCacheHistory, saveCacheHistory, type CacheTestRun } from '@/utils/groupCacheTest'

const mocks = vi.hoisted(() => ({
  groups: vi.fn(), candidates: vi.fn(), keys: vi.fn(), getKey: vi.fn(), execute: vi.fn(), reconcile: vi.fn(),
  auth: { user: { id: 1 } }, saveAs: vi.fn()
}))
vi.mock('@/api/admin/groups', () => ({ getAll: mocks.groups, getModelAllowlistCandidates: mocks.candidates }))
vi.mock('@/api/keys', () => ({ list: mocks.keys, getById: mocks.getKey }))
vi.mock('@/api/groupCacheTest', () => ({ executeCacheRun: mocks.execute, reconcileCacheBills: mocks.reconcile }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('file-saver', () => ({ saveAs: mocks.saveAs }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
const key = { id: 10, name: 'My key', key: 'NEVER_STORE_SECRET', status: 'active', user_id: 1, group_id: 7, quota: 0 }
const result = (items: unknown[]) => ({ items, pages: 1 })
let wrapper: ReturnType<typeof mount>
const mountView = () => mount(GroupCacheTestView, { global: { stubs: {
  AppLayout: { template: '<main><slot /></main>' },
  RouterLink: { template: '<a><slot /></a>' }
} } })
async function configure() {
  await flushPromises()
  await wrapper.get('[data-testid="cache-group"]').setValue(7)
  await flushPromises()
  await wrapper.get('[data-testid="cache-model"]').setValue('test-model')
}
beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  mocks.groups.mockResolvedValue([{ id: 7, name: 'Demo group', platform: 'openai' }, { id: 8, name: 'Other group', platform: 'anthropic' }])
  mocks.candidates.mockResolvedValue(['test-model'])
  mocks.keys.mockResolvedValue(result([key]))
  mocks.getKey.mockResolvedValue(key)
  mocks.execute.mockImplementation(async (run: CacheTestRun, _key: string, _signal: AbortSignal, changed: () => void) => {
    run.status = 'completed'; run.finishedAt = new Date().toISOString(); changed()
  })
  mocks.reconcile.mockResolvedValue(undefined)
})
afterEach(() => wrapper?.unmount())

describe('GroupCacheTestView', () => {
  it('requires a model and an owned, active key bound to the chosen group', async () => {
    mocks.keys.mockResolvedValue(result([key, { ...key, id: 11, user_id: 2 }, { ...key, id: 12, group_id: 8 }, { ...key, id: 13, status: 'inactive' }]))
    wrapper = mountView()
    expect(wrapper.get('[data-testid="cache-start"]').attributes('disabled')).toBeDefined()
    await configure()
    expect(wrapper.get('[data-testid="cache-key"]').findAll('option').map(option => option.text())).toEqual(['admin.groupCacheTest.selectKey', 'My key · #10'])
    expect(wrapper.get('[data-testid="cache-start"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.execute).toHaveBeenCalledTimes(1)
    expect(mocks.execute.mock.calls[0][1]).toBe('NEVER_STORE_SECRET')
    expect(JSON.stringify(loadCacheHistory(1))).not.toContain('NEVER_STORE_SECRET')
    expect(wrapper.text()).not.toContain('NEVER_STORE_SECRET')
  })
  it('excludes multi-group smart-routing keys and rechecks before sending', async () => {
    mocks.keys.mockResolvedValue(result([{ ...key, group_ids: [7, 8] }]))
    wrapper = mountView(); await configure()
    expect(wrapper.get('[data-testid="cache-key"]').findAll('option')).toHaveLength(1)
    expect(wrapper.get('[data-testid="cache-start"]').attributes('disabled')).toBeDefined()
    wrapper.unmount()
    mocks.keys.mockResolvedValue(result([key]))
    mocks.getKey.mockResolvedValue({ ...key, group_ids: [7, 8] })
    wrapper = mountView(); await configure()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.execute).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.groupCacheTest.keyChanged')
  })
  it('revalidates group membership just before the paid test', async () => {
    wrapper = mountView(); await configure()
    mocks.getKey.mockResolvedValue({ ...key, group_id: 8 })
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(mocks.execute).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.groupCacheTest.keyChanged')
  })
  it('ignores a stale key-list result after the selected group changes', async () => {
    let resolveOld!: (value: unknown) => void
    mocks.keys.mockImplementation((_page: number, _size: number, filters: { group_id: number }) =>
      filters.group_id === 7 ? new Promise(resolve => { resolveOld = resolve }) : Promise.resolve(result([{ ...key, id: 20, group_id: 8, name: 'Other key' }])))
    wrapper = mountView(); await flushPromises()
    await wrapper.get('[data-testid="cache-group"]').setValue(7); await flushPromises()
    await wrapper.get('[data-testid="cache-group"]').setValue(8); await flushPromises()
    resolveOld(result([key])); await flushPromises()
    expect(wrapper.get('[data-testid="cache-key"]').text()).toContain('Other key')
    expect(wrapper.get('[data-testid="cache-key"]').text()).not.toContain('My key')
    expect((wrapper.get('[data-testid="cache-protocol"]').element as HTMLSelectElement).value).toBe('messages')
  })
  it('keeps unavailable keys explicit and does not start tests automatically', async () => {
    mocks.keys.mockRejectedValue(new Error('offline'))
    wrapper = mountView(); await configure()
    expect(wrapper.text()).toContain('admin.groupCacheTest.keysError')
    expect(wrapper.get('[data-testid="cache-start"]').attributes('disabled')).toBeDefined()
    expect(mocks.execute).not.toHaveBeenCalled()
  })
  it('aborts the active run when stopped, and on unmount', async () => {
    let activeSignal: AbortSignal | undefined
    mocks.execute.mockImplementation(async (_run: CacheTestRun, _key: string, signal: AbortSignal) => {
      activeSignal = signal
      await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }))
    })
    wrapper = mountView(); await configure()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(activeSignal?.aborted).toBe(false)
    await wrapper.get('[data-testid="cache-stop"]').trigger('click'); await flushPromises()
    expect(activeSignal?.aborted).toBe(true)
    expect(mocks.reconcile).not.toHaveBeenCalled()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const nextSignal = activeSignal
    wrapper.unmount()
    expect(nextSignal?.aborted).toBe(true)
  })
  it('checks bills from history without sending another model request', async () => {
    const run: CacheTestRun = { version: 1, id: 'history', startedAt: '2026-10-12T00:00:00Z', finishedAt: '2026-10-12T00:01:00Z', status: 'completed', groupId: 7, groupName: 'History group', keyId: 10, keyName: 'Old key', settings: { ...DEFAULT_CACHE_SETTINGS, model: 'test-model' }, rounds: [{
      index: 1, status: 'completed', startedAt: '2026-10-12T00:00:00Z', requestId: 'client:demo',
      tokens: { input: 100, output: 1, read: null, write: null }, responseModel: 'test-model', serviceTier: null,
      firstTextMs: 5, durationMs: 7, error: null, httpStatus: 200, bill: null, billingState: 'pending'
    }] }
    saveCacheHistory(1, [run])
    wrapper = mountView(); await flushPromises()
    await wrapper.get('li button').trigger('click')
    expect(wrapper.text()).toContain('admin.groupCacheTest.billing.pending')
    expect(wrapper.text()).not.toContain('$0.000000')
    await wrapper.get('[data-testid="cache-reconcile"]').trigger('click'); await flushPromises()
    expect(mocks.reconcile).toHaveBeenCalledTimes(1)
    expect(mocks.execute).not.toHaveBeenCalled()
    expect(mocks.getKey).not.toHaveBeenCalled()
  })
})
