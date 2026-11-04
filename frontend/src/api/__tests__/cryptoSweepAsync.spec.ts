import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { startSweep, waitForSweepRun, type SweepRun } from '@/api/admin/crypto'

const initial: SweepRun = { id: 'sweep-test', dry_run: true, chains: ['bsc'], started_at: '', finished: false }
beforeEach(() => { vi.resetAllMocks(); vi.useFakeTimers() })
afterEach(() => vi.useRealTimers())

describe('asynchronous sweep contract', () => {
  it('starts once with the verification grant and polls until a confirmed task result', async () => {
    const finished = { ...initial, finished: true, summary: { planned: 1, swept: 0, failed: 0, pending: 1, gas_topups: 0, dry_run: true, entries: [] } }
    client.post.mockResolvedValue({ data: { data: initial }, status: 202 })
    client.get.mockResolvedValueOnce({ data: { data: initial } }).mockResolvedValueOnce({ data: { data: finished } })
    const started = await startSweep(true, { credential: 'test-payment-grant' })
    const update = vi.fn()
    const result = waitForSweepRun(started, new AbortController().signal, update)
    await vi.advanceTimersByTimeAsync(4000)
    expect(await result).toEqual(finished)
    expect(client.post).toHaveBeenCalledTimes(1)
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/sweep/run', null, expect.objectContaining({
      _retry: true, headers: { 'X-Crypto-Grant': 'test-payment-grant' }, params: { dry_run: '1', async: '1' }
    }))
    expect(client.get).toHaveBeenCalledTimes(2)
    expect(update.mock.calls.map(([run]) => run.finished)).toEqual([false, false, true])
  })

  it('leaving the page cancels only polling without submitting or canceling a sweep', async () => {
    const controller = new AbortController()
    const result = waitForSweepRun(initial, controller.signal, vi.fn())
    const rejection = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await rejection
    await vi.advanceTimersByTimeAsync(4000)
    expect(client.post).not.toHaveBeenCalled()
    expect(client.get).not.toHaveBeenCalled()
  })

  it('a lost status response is unknown, never a reason to re-submit the payment operation', async () => {
    client.get.mockRejectedValue(new Error('unavailable'))
    const result = waitForSweepRun(initial, new AbortController().signal, vi.fn())
    const rejection = expect(result).rejects.toThrow('unavailable')
    await vi.advanceTimersByTimeAsync(2000)
    await rejection
    expect(client.post).not.toHaveBeenCalled()
  })
})
