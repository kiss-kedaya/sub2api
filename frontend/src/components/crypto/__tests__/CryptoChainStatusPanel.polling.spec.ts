import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoChainStatusPanel from '../CryptoChainStatusPanel.vue'
import { chain, report } from './chainStatusFixtures'
import type { ChainStatusReport } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getChainStatus: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getChainStatus: mocks.getChainStatus }))
vi.mock('vue-i18n', async () => (await import('./chainStatusFixtures')).i18nMock)

enableAutoUnmount(afterEach)

let visibility: 'visible' | 'hidden' = 'visible'
function setVisibility(next: 'visible' | 'hidden') {
  visibility = next
  document.dispatchEvent(new Event('visibilitychange'))
}

beforeEach(() => {
  visibility = 'visible'
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility })
  // Only the interval is faked: promises and flushPromises keep running normally.
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  mocks.getChainStatus.mockReset()
  mocks.getChainStatus.mockResolvedValue(report())
})
afterEach(() => {
  vi.useRealTimers()
  delete (document as unknown as Record<string, unknown>).visibilityState
})

async function mountPanel(props: { active?: boolean } = {}) {
  const wrapper = mount(CryptoChainStatusPanel, { props })
  await flushPromises()
  return wrapper
}
async function tick(ms: number) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}

describe('polling', () => {
  it('fetches at once and then every 3 seconds', async () => {
    await mountPanel()
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    await tick(2999)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    await tick(1)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(2)
    await tick(3000)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(3)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('does nothing while the page is hidden, and starts when it becomes visible', async () => {
    visibility = 'hidden'
    await mountPanel()
    expect(mocks.getChainStatus).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    setVisibility('visible')
    await flushPromises()
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('pauses and clears its timer when the page is hidden, and refreshes at once when it returns', async () => {
    await mountPanel()
    setVisibility('hidden')
    await flushPromises()
    expect(vi.getTimerCount()).toBe(0)
    await tick(30_000)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await flushPromises()
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('follows the active prop: idle while another tab is shown', async () => {
    const wrapper = await mountPanel({ active: false })
    expect(mocks.getChainStatus).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    await wrapper.setProps({ active: true })
    await flushPromises()
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(1)
    await wrapper.setProps({ active: false })
    await tick(30_000)
    expect(vi.getTimerCount()).toBe(0)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
  })

  it('cleans up the timer and the visibility listener on unmount', async () => {
    const remove = vi.spyOn(document, 'removeEventListener')
    const wrapper = await mountPanel()
    expect(vi.getTimerCount()).toBe(1)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
    expect(remove).toHaveBeenCalledWith('visibilitychange', expect.any(Function))
    setVisibility('hidden')
    setVisibility('visible')
    await tick(30_000)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
  })

  it('never has two requests in flight', async () => {
    let release!: (r: ChainStatusReport) => void
    mocks.getChainStatus.mockReset()
    mocks.getChainStatus.mockReturnValue(new Promise<ChainStatusReport>(resolve => { release = resolve }))
    await mountPanel()
    await tick(15_000)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(1)
    release(report())
    await flushPromises()
    await tick(3000)
    expect(mocks.getChainStatus).toHaveBeenCalledTimes(2)
  })

  it('drops a response that arrives after polling was stopped', async () => {
    let release!: (r: ChainStatusReport) => void
    mocks.getChainStatus.mockReset()
    mocks.getChainStatus.mockReturnValue(new Promise<ChainStatusReport>(resolve => { release = resolve }))
    const wrapper = await mountPanel()
    await wrapper.setProps({ active: false })
    release(report())
    await flushPromises()
    expect(wrapper.find('[data-testid^="chain-row-"]').exists()).toBe(false)
  })
})

describe('out-of-date and failed data', () => {
  it('keeps the last data and marks it out of date when a refresh fails, then recovers', async () => {
    const wrapper = await mountPanel()
    expect(wrapper.find('[data-testid="chain-row-bsc"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="chain-status-stale"]').exists()).toBe(false)

    mocks.getChainStatus.mockRejectedValueOnce(new Error('boom: https://rpc.example/SECRETKEY'))
    await tick(3000)
    expect(wrapper.find('[data-testid="chain-row-bsc"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="chain-status-stale"]').text()).toBe('数据已过期')
    expect(wrapper.find('[data-testid="chain-status-error"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('SECRETKEY')

    await tick(3000)
    expect(wrapper.find('[data-testid="chain-status-stale"]').exists()).toBe(false)
  })

  it('shows an error with a working retry when there is nothing to keep', async () => {
    mocks.getChainStatus.mockReset()
    mocks.getChainStatus.mockRejectedValueOnce(new Error('down'))
    mocks.getChainStatus.mockResolvedValue(report([chain({ chain: 'ethereum' })]))
    const wrapper = await mountPanel()
    const alert = wrapper.get('[data-testid="chain-status-error"]')
    expect(alert.text()).toContain('无法获取节点状态')
    expect(wrapper.find('[data-testid="chain-status-stale"]').exists()).toBe(false)

    const retry = alert.findAll('button').find(b => b.text() === '刷新')
    expect(retry).toBeDefined()
    await retry!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="chain-status-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="chain-row-ethereum"]').exists()).toBe(true)
  })
})
