import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import DepositView from '../DepositView.vue'

const mocks = vi.hoisted(() => ({ getDepositInfo: vi.fn(), getDepositHistory: vi.fn(), copy: vi.fn() }))
vi.mock('@/api/crypto', () => ({ getDepositInfo: mocks.getDepositInfo, getDepositHistory: mocks.getDepositHistory }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('qrcode', () => ({ default: { toCanvas: vi.fn().mockResolvedValue(undefined) } }))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
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

beforeEach(() => {
  vi.clearAllMocks()
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
