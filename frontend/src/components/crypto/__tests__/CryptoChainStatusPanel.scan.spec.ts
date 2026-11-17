import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, provide } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { cryptoScanKey, useCryptoScan } from '@/composables/useCryptoScan'
import { chain, report } from './chainStatusFixtures'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), verify: vi.fn(), refresh: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)
enableAutoUnmount(afterEach)

const run = (over: Record<string, unknown> = {}) => ({
  id: 'scan-1', status: 'running', started_at: '', from_block: 0, chain_scope: ['bsc'], rounds: 0, busy_retries: 0, stalled_rounds: 0,
  chains: [{ chain: 'bsc', cursor: 900, confirmed_head: 1000, remaining: 100, head_known: true }], ...over
})
let running: unknown[]
let status: unknown

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  running = []
  status = run()
  mocks.refresh.mockResolvedValue(undefined)
  mocks.get.mockImplementation(async (path: string) => ({ data: { data:
    path.endsWith('/chains/status') ? report([chain(), chain({ chain: 'ethereum' }), chain({ chain: 'polygon', enabled: false, state: 'disabled' })]) :
      path.endsWith('/scan/runs') ? running : status
  } }))
  mocks.post.mockImplementation(async (path: string) => ({ data: { data: path.endsWith('/cancel') ? status : run() }, status: 202 }))
})
afterEach(() => vi.useRealTimers())

async function render() {
  const Host = defineComponent({
    components: { CryptoChainStatusPanel },
    setup() {
      provide(cryptoScanKey, useCryptoScan(mocks.verify, mocks.refresh, mocks))
      return {}
    },
    template: '<CryptoChainStatusPanel />'
  })
  const wrapper = mount(Host)
  await flushPromises()
  return wrapper
}

const startCalls = () => mocks.post.mock.calls.filter(([path]) => path === '/admin/crypto/scan/run')

describe('per-chain catch-up from node status', () => {
  it('clicking BSC sends only BSC, shows progress, disables every chain and frees them on completion', async () => {
    const w = await render()
    await w.get('[data-testid="chain-scan-bsc"]').trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 0 }, expect.objectContaining({ params: { async: '1', chain: 'bsc' } }))
    expect(startCalls()).toHaveLength(1)
    expect(mocks.verify).not.toHaveBeenCalled()
    expect(w.text()).toContain('补扫范围：bsc')
    expect(w.text()).toContain('bsc：区块 900 / 1000，剩余 100')
    for (const name of ['bsc', 'ethereum', 'polygon']) expect(w.get(`[data-testid="chain-scan-${name}"]`).attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="chain-scan-ethereum"]').text()).toBe('补扫进行中')

    status = run({ status: 'completed', stop_reason: 'caught_up' })
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(mocks.refresh).toHaveBeenCalledWith(true)
    expect(w.get('[data-testid="chain-scan-ethereum"]').attributes('disabled')).toBeUndefined()
    expect(w.get('[data-testid="chain-scan-polygon"]').attributes('disabled')).toBeDefined()
  })

  it('resumes an existing run and cancellation uses the shared second-factor flow', async () => {
    running = [run()]
    const w = await render()
    expect(startCalls()).toHaveLength(0)
    expect(w.get('[data-testid="chain-scan-bsc"]').attributes('disabled')).toBeDefined()
    let approve!: () => Promise<void>
    mocks.verify.mockImplementation(async (_label, action) => { approve = () => action({}); return false })
    const cancel = w.findAll('button').find(button => button.text() === '取消补扫')!
    await cancel.trigger('click')
    await flushPromises()
    expect(mocks.verify).toHaveBeenCalledWith('取消正在运行的补扫。已扫描的进度会保留。', expect.any(Function))
    expect(mocks.post).not.toHaveBeenCalled()
    await approve()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/scan/runs/scan-1/cancel', null, expect.any(Object))
  })

  it('does not scan a disabled chain and reattaches after a conflicting start', async () => {
    const w = await render()
    await w.get('[data-testid="chain-scan-polygon"]').trigger('click')
    expect(startCalls()).toHaveLength(0)
    running = [run({ chain_scope: ['ethereum'] })]
    mocks.post.mockRejectedValueOnce({ status: 409, message: 'Scan busy' })
    await w.get('[data-testid="chain-scan-bsc"]').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('补扫范围：ethereum')
    expect(mocks.showError).not.toHaveBeenCalled()
    expect(w.get('[data-testid="chain-scan-bsc"]').attributes('disabled')).toBeDefined()
  })

  it('keeps buttons disabled after a lost progress response, then reconnects without a status-page resume', async () => {
    running = [run()]
    const w = await render()
    const get = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation(async (path: string) => {
      if (path.endsWith('/scan/runs/scan-1')) throw new Error('Progress response lost')
      return get(path)
    })
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalled()
    expect(w.get('[data-testid="chain-scan-ethereum"]').attributes('disabled')).toBeDefined()
    mocks.get.mockImplementation(get)
    running = [] // The server can finish before listScanRuns sees it again.
    status = run({ status: 'completed', stop_reason: 'caught_up' })
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(w.text()).toContain('补扫范围：bsc')
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(w.get('[data-testid="chain-scan-ethereum"]').attributes('disabled')).toBeUndefined()
    expect(startCalls()).toHaveLength(0)
  })

  it('discovers and cancels an existing run even when node status is unavailable', async () => {
    running = [run()]
    const get = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation(async (path: string) => {
      if (path.endsWith('/chains/status')) throw { status: 503 }
      return get(path)
    })
    const w = await render()
    expect(w.find('[data-testid="chain-status-error"]').exists()).toBe(true)
    expect(w.get('[data-testid="crypto-scan-progress"]').text()).toContain('补扫范围：bsc')
    const cancel = w.findAll('button').find(button => button.text() === '取消补扫')!
    await cancel.trigger('click')
    expect(mocks.verify).toHaveBeenCalled()
    expect(startCalls()).toHaveLength(0)
  })

  it('leaving node status stops polling without cancelling the run', async () => {
    running = [run()]
    const w = await render()
    w.unmount()
    const calls = mocks.get.mock.calls.length
    await vi.advanceTimersByTimeAsync(6000)
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledTimes(calls)
    expect(mocks.post).not.toHaveBeenCalled()
  })
})
