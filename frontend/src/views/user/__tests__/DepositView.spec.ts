import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import DepositView from '../DepositView.vue'

const mocks = vi.hoisted(() => ({
  getDepositInfo: vi.fn(), getDepositHistory: vi.fn(), copy: vi.fn(), refreshUser: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/crypto', () => ({ getDepositInfo: mocks.getDepositInfo, getDepositHistory: mocks.getDepositHistory }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ refreshUser: mocks.refreshUser }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('qrcode', () => ({ default: { toCanvas: vi.fn().mockResolvedValue(undefined) } }))
// Params are echoed so assertions can see what each message was given.
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? key + ' ' + JSON.stringify(params) : key) })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))

enableAutoUnmount(afterEach)

const hash = '0x' + 'ab'.repeat(32)
const otherHash = '0x' + 'cd'.repeat(32)

function render() {
  return mount(DepositView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Icon: true,
        LoadingSpinner: true
      }
    }
  })
}

function pendingRow(overrides: Record<string, unknown> = {}) {
  return { chain: 'bsc', tx_hash: otherHash, token: 'USDT', amount: '10', status: 'pending', credited: 0, created_at: '2026-01-02T00:00:00Z', ...overrides }
}

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  document.dispatchEvent(new Event('visibilitychange'))
}

afterEach(() => {
  vi.useRealTimers()
  Reflect.deleteProperty(document, 'visibilityState')
})

beforeEach(() => {
  vi.clearAllMocks()
  mocks.getDepositHistory.mockReset()
  mocks.refreshUser.mockResolvedValue(undefined)
  mocks.getDepositInfo.mockResolvedValue({
    enabled: true, chains: [{ chain: 'bsc', address: '0x' + '12'.repeat(20), tokens: ['USDT'] }],
    rate: 6.7, min_amount: 1, max_amount: 0
  })
  mocks.getDepositHistory.mockResolvedValue([
    { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z' },
    { chain: 'base', tx_hash: otherHash, token: 'USDC', amount: '20', status: 'pending', credited: 0, created_at: '2026-01-02T00:00:00Z' }
  ])
})

describe('user deposit history transaction hashes', () => {
  it('shows complete hashes for credited and pending deposits with their own network explorer', async () => {
    const wrapper = render()
    await flushPromises()
    const hashes = wrapper.findAll('a.deposit-view__hash')
    expect(hashes.map(link => link.text())).toEqual([hash, otherHash])
    expect(hashes.map(link => link.attributes('href'))).toEqual([
      'https://bscscan.com/tx/' + hash,
      'https://basescan.org/tx/' + otherHash
    ])
    expect(hashes[0].attributes('rel')).toBe('noopener noreferrer')
    expect(hashes[0].attributes('target')).toBe('_blank')
    expect(wrapper.get('th:nth-child(5)').text()).toBe('deposit.txHash')
  })

  it('copies the full selected transaction hash rather than its shortened label or deposit address', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('.deposit-view__hash-copy')[1].trigger('click')
    expect(mocks.copy).toHaveBeenCalledOnce()
    expect(mocks.copy).toHaveBeenCalledWith(otherHash)
  })

  it('keeps malformed hashes readable and copyable without creating an explorer link', async () => {
    mocks.getDepositHistory.mockResolvedValueOnce([
      { chain: 'bsc', tx_hash: 'legacy-reference', token: 'USDT', amount: '1', status: 'credited', credited: 6.7, created_at: '2026-01-01T00:00:00Z' }
    ])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('a.deposit-view__hash').exists()).toBe(false)
    expect(wrapper.get('code.deposit-view__hash').text()).toBe('legacy-reference')
    await wrapper.get('.deposit-view__hash-copy').trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith('legacy-reference')
  })
})

describe('user deposit confirmation progress', () => {
  it('shows the confirming status, confirmation count and progress bar for a pending row', async () => {
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 2, required_confirmations: 3, block_number: 123 })])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__badge--pending').text()).toBe('deposit.statusConfirming')
    expect(wrapper.get('.deposit-view__progress-text').text()).toBe('deposit.confirmProgress {"done":2,"required":3,"remaining":1}')
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('2')
    expect(bar.attributes('aria-valuemax')).toBe('3')
    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('width: 67%')
    expect(wrapper.get('.deposit-view__pending strong').text()).toBe('deposit.pendingSummary {"n":1}')
    expect(wrapper.findAll('.deposit-view__pending-list li').map(li => li.text())).toEqual(['10 USDT · BSC (BEP20) · 2/3'])
  })

  it('keeps the plain status when the backend sends no confirmation fields or a zero target', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      pendingRow(),
      pendingRow({ tx_hash: hash, confirmations: 1, required_confirmations: 0 })
    ])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('.deposit-view__badge--pending').map(b => b.text())).toEqual(['deposit.statusConfirming', 'deposit.statusConfirming'])
    expect(wrapper.find('.deposit-view__progress').exists()).toBe(false)
    expect(wrapper.findAll('.deposit-view__pending-list li').map(li => li.text())).toEqual(['10 USDT · BSC (BEP20)', '10 USDT · BSC (BEP20)'])
  })

  it('lists at most three confirming deposits in the summary', async () => {
    mocks.getDepositHistory.mockResolvedValue(
      ['a', 'b', 'c', 'd'].map(c => pendingRow({ tx_hash: '0x' + c.repeat(64), confirmations: 1, required_confirmations: 3 }))
    )
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('.deposit-view__pending-list li')).toHaveLength(3)
    expect(wrapper.get('.deposit-view__pending-more').text()).toBe('deposit.pendingMore {"n":1}')
  })
})

describe('user deposit auto refresh', () => {
  const fakeTimers = () => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  const creditedRow = { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z' }

  it('re-reads history every 8 seconds while a deposit is confirming and stops after leaving the page', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 1, required_confirmations: 3 })])
    const wrapper = render()
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(8000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(8000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(60000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
  })

  it('does not fast-poll when nothing is confirming', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValue([creditedRow])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(24000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)
  })

  it('pauses while the tab is hidden and refreshes as soon as it is visible again', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 1, required_confirmations: 3 })])
    render()
    await flushPromises()
    setVisibility('hidden')
    await vi.advanceTimersByTimeAsync(40000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)
    setVisibility('visible')
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
  })

  it('refreshes the balance and shows a toast once when a confirming deposit is credited', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([pendingRow({ tx_hash: hash, confirmations: 2, required_confirmations: 3 })])
      .mockResolvedValue([{ ...creditedRow, confirmations: 3, required_confirmations: 3 }])
    const wrapper = render()
    await flushPromises()
    expect(mocks.refreshUser).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(8000)
    await flushPromises()
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).toHaveBeenCalledWith('deposit.creditedToast {"amount":"67"}')
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(false)
    // Nothing is confirming any more: the 8 second poll stops and the toast is not repeated.
    await vi.advanceTimersByTimeAsync(16000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    expect(mocks.showSuccess).toHaveBeenCalledOnce()
  })

  it('does not announce deposits that were already credited when the page opened', async () => {
    mocks.getDepositHistory.mockResolvedValue([creditedRow])
    render()
    await flushPromises()
    expect(mocks.refreshUser).not.toHaveBeenCalled()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })
})
