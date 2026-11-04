import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { cancelScanRun, getScanRun, listScanRuns, startCryptoScan, waitForScanRun, type ScanRun } from '@/api/admin/crypto'

const running: ScanRun = {
  id: 'scan-1', status: 'running', started_at: '', from_block: 0, rounds: 0, busy_retries: 0, stalled_rounds: 0,
  chains: [{ chain: 'bsc', cursor: 900, confirmed_head: 1000, remaining: 100, head_known: true }]
}
beforeEach(() => { vi.resetAllMocks(); vi.useFakeTimers() })
afterEach(() => vi.useRealTimers())

describe('background catch-up scan client', () => {
  it('starts one async scan from the saved cursor and needs no verification', async () => {
    client.post.mockResolvedValue({ data: { data: running }, status: 202 })
    expect(await startCryptoScan()).toEqual(running)
    expect(client.post).toHaveBeenCalledTimes(1)
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 0 }, {
      headers: {}, _retry: true, timeout: 30000, params: { async: '1' }
    })
  })

  it('sends the rewind block and a payment grant only when the caller supplies them', async () => {
    client.post.mockResolvedValue({ data: { data: running }, status: 202 })
    await startCryptoScan(12345, { credential: 'test-payment-grant' })
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 12345 }, expect.objectContaining({
      headers: { 'X-Crypto-Grant': 'test-payment-grant' }, params: { async: '1' }
    }))
  })

  it('treats a synchronous answer from an older server as an already finished pass', async () => {
    client.post.mockResolvedValue({ data: { data: { completed: true } }, status: 200 })
    expect(await startCryptoScan()).toBeNull()
    client.post.mockResolvedValue({ data: {}, status: 200 })
    expect(await startCryptoScan()).toBeNull()
  })

  it('polls every two seconds until the run leaves running and reports each state', async () => {
    const progressed = { ...running, rounds: 2, chains: [{ ...running.chains![0], cursor: 950, remaining: 50 }] }
    const done: ScanRun = { ...progressed, status: 'completed', stop_reason: 'caught_up' }
    client.get.mockResolvedValueOnce({ data: { data: progressed } }).mockResolvedValueOnce({ data: { data: done } })
    const update = vi.fn()
    const result = waitForScanRun(running, new AbortController().signal, update)
    await vi.advanceTimersByTimeAsync(4000)
    expect(await result).toEqual(done)
    expect(client.get).toHaveBeenCalledTimes(2)
    expect(client.get).toHaveBeenCalledWith('/admin/crypto/scan/runs/scan-1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect(update.mock.calls.map(([run]) => run.status)).toEqual(['running', 'running', 'completed'])
    expect(client.post).not.toHaveBeenCalled()
  })

  it('stops on failed and cancelled states too, not only on completed', async () => {
    for (const status of ['failed', 'cancelled'] as const) {
      client.get.mockResolvedValueOnce({ data: { data: { ...running, status } } })
      const result = waitForScanRun(running, new AbortController().signal, vi.fn())
      await vi.advanceTimersByTimeAsync(2000)
      expect((await result).status).toBe(status)
    }
  })

  it('leaving the page stops only the polling and never touches the scan', async () => {
    const controller = new AbortController()
    const result = waitForScanRun(running, controller.signal, vi.fn())
    const rejection = expect(result).rejects.toMatchObject({ name: 'AbortError' })
    controller.abort()
    await rejection
    await vi.advanceTimersByTimeAsync(4000)
    expect(client.get).not.toHaveBeenCalled()
    expect(client.post).not.toHaveBeenCalled()
  })

  it('a lost status response is unknown and never a reason to start another scan', async () => {
    client.get.mockRejectedValue(new Error('unavailable'))
    const result = waitForScanRun(running, new AbortController().signal, vi.fn())
    const rejection = expect(result).rejects.toThrow('unavailable')
    await vi.advanceTimersByTimeAsync(2000)
    await rejection
    expect(client.post).not.toHaveBeenCalled()
  })

  it('cancelling carries the payment proof and encodes the run id', async () => {
    client.post.mockResolvedValue({ data: { data: running }, status: 202 })
    await cancelScanRun('scan/1', { credential: 'test-payment-grant' })
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/scan/runs/scan%2F1/cancel', null, {
      headers: { 'X-Crypto-Grant': 'test-payment-grant' }, _retry: true, timeout: 30000
    })
  })

  it('reads one run and the list, and never returns a non-array list', async () => {
    client.get.mockResolvedValueOnce({ data: { data: running } })
    expect(await getScanRun('scan-1')).toEqual(running)
    client.get.mockResolvedValueOnce({ data: { data: [running] } })
    expect(await listScanRuns()).toEqual([running])
    client.get.mockResolvedValueOnce({ data: { data: null } })
    expect(await listScanRuns()).toEqual([])
  })
})
