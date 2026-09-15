import { beforeEach, describe, expect, it, vi } from 'vitest'
import { reactive } from 'vue'

const { list } = vi.hoisted(() => ({ list: vi.fn() }))
const auth = reactive({ user: { id: 1 } as { id: number } | null, isAuthenticated: true })
vi.mock('@/api/keys', () => ({ keysAPI: { list } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))

const allowed = { items: [{ status: 'active', group: { platform: 'gemini', allow_batch_image_generation: true } }], pages: 1 }
const denied = { items: [], pages: 1 }
function deferred() {
  let resolve!: (value: typeof denied | typeof allowed) => void
  let reject!: (error: Error) => void
  const promise = new Promise<typeof denied | typeof allowed>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
beforeEach(() => { vi.resetModules(); list.mockReset(); auth.user = { id: 1 }; auth.isAuthenticated = true })

describe('batch image access session cache', () => {
  it.each([true, false])('reloads access after switching users (previous access: %s)', async (previous) => {
    list.mockResolvedValueOnce(previous ? allowed : denied).mockResolvedValueOnce(previous ? denied : allowed)
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const state = useBatchImageAccess()
    expect(await state.refreshBatchImageAccess()).toBe(previous)
    auth.user = { id: 2 }
    expect(state.canUseBatchImage.value).toBe(false)
    expect(await state.refreshBatchImageAccess()).toBe(!previous)
    expect(list).toHaveBeenCalledTimes(2)
    expect(state.canUseBatchImage.value).toBe(!previous)
  })

  it('does not reuse an unauthenticated result after login', async () => {
    auth.user = null; auth.isAuthenticated = false
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const state = useBatchImageAccess()
    expect(await state.refreshBatchImageAccess()).toBe(false)
    auth.user = { id: 1 }; auth.isAuthenticated = true
    list.mockResolvedValue(allowed)
    expect(await state.refreshBatchImageAccess()).toBe(true)
  })

  it.each(['resolve', 'reject'] as const)('ignores an old user request that later %ss', async (settle) => {
    const old = deferred()
    list.mockReturnValueOnce(old.promise).mockResolvedValueOnce(allowed)
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const state = useBatchImageAccess()
    const oldLoad = state.refreshBatchImageAccess()
    auth.user = { id: 2 }
    expect(await state.refreshBatchImageAccess()).toBe(true)
    if (settle === 'resolve') old.resolve(denied)
    else old.reject(new Error('old request failed'))
    await oldLoad
    expect(state.canUseBatchImage.value).toBe(true)
    expect(await state.refreshBatchImageAccess()).toBe(true)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('keeps the new user request pending when an older request finishes', async () => {
    const old = deferred(); const current = deferred()
    list.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const state = useBatchImageAccess()
    const oldLoad = state.refreshBatchImageAccess()
    auth.user = { id: 2 }
    const newLoad = state.refreshBatchImageAccess()
    expect(list).toHaveBeenCalledTimes(2)
    old.resolve(denied); await oldLoad
    expect(state.batchImageAccessLoading.value).toBe(true)
    const sharedLoad = state.refreshBatchImageAccess()
    expect(list).toHaveBeenCalledTimes(2)
    current.resolve(allowed)
    expect(await newLoad).toBe(true); expect(await sharedLoad).toBe(true)
    expect(state.batchImageAccessLoading.value).toBe(false)
  })

  it('hides cached access immediately on logout', async () => {
    list.mockResolvedValue(allowed)
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const state = useBatchImageAccess()
    await state.refreshBatchImageAccess()
    expect(state.canUseBatchImage.value).toBe(true)
    auth.user = null; auth.isAuthenticated = false
    expect(state.canUseBatchImage.value).toBe(false)
  })

  it('still shares in-flight and completed results between consumers for the same user', async () => {
    const response = deferred(); list.mockReturnValue(response.promise)
    const { useBatchImageAccess } = await import('../useBatchImageAccess')
    const first = useBatchImageAccess(); const second = useBatchImageAccess()
    const one = first.refreshBatchImageAccess(); const two = second.refreshBatchImageAccess()
    expect(list).toHaveBeenCalledTimes(1)
    response.resolve(allowed)
    expect(await one).toBe(true); expect(await two).toBe(true)
    expect(await second.refreshBatchImageAccess()).toBe(true)
    expect(list).toHaveBeenCalledTimes(1)
  })
})
