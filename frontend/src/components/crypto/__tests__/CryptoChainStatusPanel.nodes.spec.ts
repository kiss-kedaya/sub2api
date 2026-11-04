import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { chain, node, report } from './chainStatusFixtures'
import type { ChainStatusReport } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getChainStatus: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getChainStatus: mocks.getChainStatus }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)

enableAutoUnmount(afterEach)
beforeEach(() => mocks.getChainStatus.mockReset())

async function render(data: ChainStatusReport) {
  mocks.getChainStatus.mockResolvedValue(data)
  const wrapper = mount(CryptoChainStatusPanel)
  await flushPromises()
  return wrapper
}

describe('node rows', () => {
  it('tells two keys of the same provider apart by their list position', async () => {
    // Two BlockPI keys share one host name; without a visible index the rows look identical.
    const wrapper = await render(report([chain({
      nodes: [
        node({ label: 'blockpi#1', host: 'bsc.blockpi.network', latency_ms: 380 }),
        node({ label: 'blockpi#2', host: 'bsc.blockpi.network', latency_ms: 410 })
      ]
    })]))
    const first = wrapper.get('[data-testid="node-blockpi#1"]')
    const second = wrapper.get('[data-testid="node-blockpi#2"]')
    expect(first.text()).toContain('bsc.blockpi.network')
    expect(first.get('[data-testid="node-index"]').text()).toBe('#1')
    expect(second.get('[data-testid="node-index"]').text()).toBe('#2')
  })

  it('does not repeat the position when the label already is what is shown', async () => {
    // A URL that could not be parsed has no host; the generic label already carries the position.
    const wrapper = await render(report([chain({ nodes: [node({ label: 'node#3', host: '' })] })]))
    const row = wrapper.get('[data-testid="node-node#3"]')
    expect(row.text()).toContain('node#3')
    expect(row.find('[data-testid="node-index"]').exists()).toBe(false)
  })
})
