import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { chain, node, report } from './chainStatusFixtures'
import type { ChainStatusReport } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getChainStatus: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getChainStatus: mocks.getChainStatus }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)

enableAutoUnmount(afterEach)
beforeEach(() => {
  mocks.getChainStatus.mockReset()
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
})
afterEach(() => { vi.useRealTimers() })

async function render(data: ChainStatusReport) {
  mocks.getChainStatus.mockResolvedValue(data)
  const wrapper = mount(CryptoChainStatusPanel)
  await flushPromises()
  return wrapper
}
const toggleOf = (w: VueWrapper, chainName: string) => w.get(`[data-testid="chain-row-${chainName}"] [data-testid="chain-nodes-toggle"]`)
const listOf = (w: VueWrapper, chainName: string) => w.get(`[data-testid="chain-row-${chainName}"] [data-testid="chain-nodes"]`)
// v-show only sets display:none on the element, which is what a visitor sees.
const hidden = (el: { attributes: (name: string) => string | undefined }) => (el.attributes('style') ?? '').includes('display: none')

describe('RPC node list folding', () => {
  it('starts folded: the nodes are hidden and the toggle says so', async () => {
    const w = await render(report([chain({ nodes: [node({ label: 'ankr#0' }), node({ label: 'blockpi#1', host: 'bsc.blockpi.network' })] })]))
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('false')
    expect(hidden(listOf(w, 'bsc'))).toBe(true)
  })

  it('opens on a tap and folds again on the next tap', async () => {
    const w = await render(report([chain()]))
    await toggleOf(w, 'bsc').trigger('click')
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('true')
    expect(hidden(listOf(w, 'bsc'))).toBe(false)
    expect(toggleOf(w, 'bsc').get('svg, [class*="rotate-90"]').exists()).toBe(true)
    await toggleOf(w, 'bsc').trigger('click')
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('false')
    expect(hidden(listOf(w, 'bsc'))).toBe(true)
  })

  it('points the toggle at the list it folds', async () => {
    const w = await render(report([chain()]))
    expect(toggleOf(w, 'bsc').attributes('aria-controls')).toBe('chain-nodes-bsc')
    expect(listOf(w, 'bsc').attributes('id')).toBe('chain-nodes-bsc')
  })

  it('names how many nodes there are', async () => {
    const w = await render(report([chain({ nodes: [node(), node({ label: 'drpc#1', host: 'drpc.org' }), node({ label: 'x#2', host: 'x.io' })] })]))
    expect(toggleOf(w, 'bsc').text()).toContain('RPC 节点 3 个')
  })

  it('says everything is fine while folded when every node is healthy', async () => {
    const w = await render(report([chain()]))
    const summary = toggleOf(w, 'bsc').get('[data-testid="chain-nodes-summary"]')
    expect(summary.text()).toBe('全部正常')
    expect(summary.classes()).toContain('text-muted')
  })

  it('shows failing nodes in the folded line, in the error colour', async () => {
    const w = await render(report([chain({ nodes: [node(), node({ label: 'a#1', status: 'error', latency_ms: null, error_kind: 'timeout' }), node({ label: 'b#2', status: 'wrong_chain' })] })]))
    const summary = toggleOf(w, 'bsc').get('[data-testid="chain-nodes-summary"]')
    expect(summary.text()).toBe('2 个异常')
    expect(summary.classes()).toContain('text-error')
  })

  it('shows slow or lagging nodes in the folded line, in the warning colour', async () => {
    const w = await render(report([chain({ nodes: [node({ status: 'slow', latency_ms: 2500 }), node({ label: 'b#1', status: 'behind', behind: 30 })] })]))
    const summary = toggleOf(w, 'bsc').get('[data-testid="chain-nodes-summary"]')
    expect(summary.text()).toBe('2 个较慢或落后')
    expect(summary.classes()).toContain('text-warning')
  })

  it('lists both kinds when a chain has failing and slow nodes, failing first', async () => {
    const w = await render(report([chain({ nodes: [node({ status: 'error', latency_ms: null, error_kind: 'timeout' }), node({ label: 'b#1', status: 'slow', latency_ms: 2500 })] })]))
    const summary = toggleOf(w, 'bsc').get('[data-testid="chain-nodes-summary"]')
    expect(summary.text()).toBe('1 个异常 · 1 个较慢或落后')
    expect(summary.classes()).toContain('text-error')
  })

  it('does not unfold by itself when nodes fail: the visitor decides', async () => {
    const w = await render(report([chain({ state: 'error', nodes: [node({ status: 'error', latency_ms: null, error_kind: 'http_5xx' })] })]))
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('false')
    expect(hidden(listOf(w, 'bsc'))).toBe(true)
  })

  it('stays open through the 3-second refresh and keeps showing fresh numbers', async () => {
    mocks.getChainStatus.mockResolvedValue(report([chain({ nodes: [node({ latency_ms: 120 })] })]))
    const w = mount(CryptoChainStatusPanel)
    await flushPromises()
    await toggleOf(w, 'bsc').trigger('click')
    mocks.getChainStatus.mockResolvedValue(report([chain({ nodes: [node({ latency_ms: 480 })] })]))
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('true')
    expect(hidden(listOf(w, 'bsc'))).toBe(false)
    expect(w.get('[data-testid="node-ankr#0"] [data-testid="node-latency"]').text()).toBe('480 ms')
  })

  it('folds and unfolds each chain on its own', async () => {
    const w = await render(report([chain({ chain: 'bsc' }), chain({ chain: 'ethereum', nodes: [node({ label: 'e#0', host: 'eth.example' })] })]))
    await toggleOf(w, 'ethereum').trigger('click')
    expect(toggleOf(w, 'ethereum').attributes('aria-expanded')).toBe('true')
    expect(hidden(listOf(w, 'ethereum'))).toBe(false)
    expect(toggleOf(w, 'bsc').attributes('aria-expanded')).toBe('false')
    expect(hidden(listOf(w, 'bsc'))).toBe(true)
  })

  it('has no toggle for a chain without nodes', async () => {
    const w = await render(report([chain({ state: 'no_rpc', nodes: [] })]))
    expect(w.find('[data-testid="chain-row-bsc"] [data-testid="chain-nodes-toggle"]').exists()).toBe(false)
  })

  it('starts folded again when the tab is left and opened anew', async () => {
    mocks.getChainStatus.mockResolvedValue(report([chain()]))
    const first = mount(CryptoChainStatusPanel)
    await flushPromises()
    await toggleOf(first, 'bsc').trigger('click')
    first.unmount()
    const second = mount(CryptoChainStatusPanel)
    await flushPromises()
    expect(toggleOf(second, 'bsc').attributes('aria-expanded')).toBe('false')
  })
})
