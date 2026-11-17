import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { useCryptoScan } from '../useCryptoScan'
import type { ScanRun } from '@/api/admin/crypto'

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const run: ScanRun = {
  id: 'scan-1', status: 'running', started_at: '', from_block: 0,
  rounds: 0, busy_retries: 0, stalled_rounds: 0, chains: []
}
const done = { ...run, status: 'completed' as const }
const response = (data: unknown) => ({ data: { data } })
const tick = async (ms: number) => { await vi.advanceTimersByTimeAsync(ms); await flushPromises() }

function render() {
  let scan!: ReturnType<typeof useCryptoScan>
  const refresh = vi.fn().mockResolvedValue(undefined)
  const store = { showSuccess: vi.fn(), showError: vi.fn() }
  const wrapper = mount(defineComponent({
    setup() { scan = useCryptoScan(vi.fn(), refresh, store); return () => null }
  }))
  return { scan, wrapper, refresh, store }
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  client.get.mockResolvedValue(response([run]))
})
afterEach(() => vi.useRealTimers())

describe('scan recovery without a node-status poller', () => {
  it('unlocks a vanished run only after a successful empty discovery', async () => {
    const { scan } = render()
    await scan.resume()
    client.get.mockRejectedValueOnce({ status: 404 }).mockResolvedValue(response([]))
    await tick(2000)
    expect(client.get).toHaveBeenLastCalledWith('/admin/crypto/scan/runs')
    expect(scan.activeScan.value).toBeNull()
    expect(scan.running.value).toBe(false)
    expect(client.post).not.toHaveBeenCalled()

    client.post.mockResolvedValue(response(run))
    void scan.start(['bsc'])
    await flushPromises()
    expect(client.post).toHaveBeenCalledTimes(1)
  })

  it('attaches to the replacement run after a restart without starting one', async () => {
    const { scan, refresh } = render()
    await scan.resume()
    const replacement = { ...run, id: 'scan-2' }
    client.get.mockRejectedValueOnce({ status: 404 }).mockResolvedValueOnce(response([replacement]))
      .mockResolvedValue(response({ ...done, id: replacement.id }))
    await tick(2000)
    expect(scan.activeScan.value?.id).toBe('scan-2')
    await tick(2000)
    expect(client.get).toHaveBeenLastCalledWith('/admin/crypto/scan/runs/scan-2', expect.any(Object))
    expect(refresh).toHaveBeenCalledWith(true)
    expect(scan.running.value).toBe(false)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('keeps a missing run locked if discovery fails, then retries after the connection returns', async () => {
    const { scan } = render()
    await scan.resume()
    client.get.mockRejectedValueOnce({ status: 404 }).mockRejectedValueOnce(new Error('offline'))
    await tick(2000)
    expect(scan.activeScan.value?.id).toBe(run.id)
    expect(scan.running.value).toBe(true)
    client.get.mockResolvedValue(response([]))
    await tick(1000)
    expect(scan.running.value).toBe(false)
    expect(client.get.mock.calls.filter(([path]) => path.endsWith('/scan-1'))).toHaveLength(1)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('retries transient progress failures and refreshes completed deposits without resume being called again', async () => {
    const { scan, refresh } = render()
    await scan.resume()
    client.get.mockRejectedValueOnce(new Error('offline')).mockResolvedValue(response(done))
    await tick(2000)
    expect(scan.running.value).toBe(true)
    expect(refresh).not.toHaveBeenCalled()
    expect(client.get.mock.calls.filter(([path]) => path === '/admin/crypto/scan/runs')).toHaveLength(1)
    await tick(3000)
    expect(refresh).toHaveBeenCalledWith(true)
    expect(scan.activeScan.value).toBeNull()
    expect(scan.running.value).toBe(false)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('retries a failed initial discovery without creating a scan', async () => {
    client.get.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(response([run]))
      .mockResolvedValue(response(done))
    const { scan, refresh } = render()
    await scan.resume()
    await tick(3000)
    expect(refresh).toHaveBeenCalledWith(true)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('bounds automatic retries during a prolonged outage and lets an explicit resume try again', async () => {
    const { scan } = render()
    await scan.resume()
    client.get.mockRejectedValue(new Error('offline'))
    await tick(60000)
    expect(client.get.mock.calls.filter(([path]) => path.endsWith('/scan-1'))).toHaveLength(4)
    const calls = client.get.mock.calls.length
    await tick(60000)
    expect(client.get).toHaveBeenCalledTimes(calls)
    expect(scan.running.value).toBe(true)
    client.get.mockResolvedValue(response(done))
    await scan.resume()
    await tick(2000)
    expect(scan.running.value).toBe(false)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('resets the retry budget after a successful progress read', async () => {
    const { scan, refresh } = render()
    await scan.resume()
    client.get.mockRejectedValueOnce(new Error('offline')).mockRejectedValueOnce(new Error('offline'))
      .mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(response({ ...run, rounds: 1 }))
      .mockRejectedValueOnce(new Error('offline')).mockResolvedValue(response(done))
    await tick(15000)
    expect(scan.activeScan.value?.rounds).toBe(1)
    await tick(5000)
    expect(refresh).toHaveBeenCalledWith(true)
    expect(scan.running.value).toBe(false)
    expect(client.post).not.toHaveBeenCalled()
  })

  it('ignores a late discovery response after the view unmounts', async () => {
    let resolve!: (value: ReturnType<typeof response>) => void
    client.get.mockReturnValue(new Promise(ready => { resolve = ready }))
    const { scan, wrapper, refresh } = render()
    const pending = scan.resume()
    wrapper.unmount()
    resolve(response([run]))
    await pending
    await tick(6000)
    expect(scan.activeScan.value).toBeNull()
    expect(client.get).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
    expect(client.post).not.toHaveBeenCalled()
  })

  it('clears the retry timer on unmount without cancelling the server run', async () => {
    const { scan, wrapper, refresh } = render()
    await scan.resume()
    client.get.mockRejectedValue(new Error('offline'))
    await tick(2000)
    wrapper.unmount()
    const calls = client.get.mock.calls.length
    expect(vi.getTimerCount()).toBe(0)
    await tick(60000)
    expect(client.get).toHaveBeenCalledTimes(calls)
    expect(refresh).not.toHaveBeenCalled()
    expect(client.post).not.toHaveBeenCalled()
  })
})
