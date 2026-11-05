import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import CryptoView from '../CryptoView.vue'
import { routes } from '@/router/records'
import { clearCryptoGrant } from '@/composables/cryptoGrant'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', async () => (await import('@/components/crypto/__tests__/chainStatusFixtures')).i18nMock)
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
enableAutoUnmount(afterEach)

// What GET /sweep/blocked answers: { data: [...] }, a mix of current and old-format rows.
const blocked = [
  { job_id: 49, chain: 'bsc', address: '0x5adae3a71b2c3d4e5f60718293a4b5c6d7c83389', status: 'gas_pending', error: 'awaiting canonical receipt and required confirmations', age_minutes: 12, can_reconcile: true },
  { job_id: 97, chain: 'bsc', address: '0xe1337f0a1b2c3d4e5f60718293a4b5c6d7f4c40d', status: 'failed', error: 'Payment operation failed; reconcile transaction status before retrying', age_minutes: 2175, can_reconcile: false }
]

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path === '/admin/crypto/sweep/blocked') return { data: { data: blocked }, status: 200 }
    let data: unknown = []
    if (path.endsWith('/config')) data = { enabled: true, chains: ['bsc'], rpc: {}, rpc_configured: [], sweep_global_threshold: 100, sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '', hot_wallet_solana: '', hot_index: 0, sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 }
    if (path.endsWith('/wallet/status')) data = { ok: true, configured: false, ready: false, enabled: false }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    return { data: { data }, status: 200 }
  })
})
afterEach(() => { vi.useRealTimers(); clearCryptoGrant() })

async function openSweeps() {
  const crypto = routes.filter(r => String(r.name).startsWith('AdminCrypto')).map(r => ({ ...r, component: { template: '<div />' } }))
  const router = createRouter({ history: createMemoryHistory(), routes: crypto })
  await router.push({ name: 'AdminCryptoSweeps' }); await router.isReady()
  const w = mount(CryptoView, { global: { plugins: [router], stubs: { teleport: true } } })
  await flushPromises()
  return w
}
const rowOf = (w: Awaited<ReturnType<typeof openSweeps>>, jobId: number) => {
  const row = w.findAll('tbody tr').find(r => r.text().includes(`#${jobId} `))
  if (!row) throw new Error(`no row for job #${jobId}`)
  return row
}

describe('blocked sweeps list', () => {
  it('lists every blocked job, current-format and old-format alike', async () => {
    const w = await openSweeps()
    expect(w.find('.crypto-view__blocked').exists()).toBe(true)
    expect(rowOf(w, 49).text()).toContain('bsc')
    expect(rowOf(w, 97).text()).toContain('bsc')
  })

  it('offers reconcile on a current-format job', async () => {
    const w = await openSweeps()
    const buttons = rowOf(w, 49).findAll('button').filter(b => b.text() === '对账')
    expect(buttons).toHaveLength(1)
    expect(buttons[0].attributes('disabled')).toBeUndefined()
    expect(rowOf(w, 49).find('[data-testid="blocked-legacy"]').exists()).toBe(false)
  })

  it('shows an explanation instead of a reconcile button on an old-format record', async () => {
    const w = await openSweeps()
    const row = rowOf(w, 97)
    expect(row.findAll('button').filter(b => b.text() === '对账')).toHaveLength(0)
    expect(row.get('[data-testid="blocked-legacy"]').text()).toBe('旧记录，人工处置过，不能在这里对账')
  })

  it('does not call the reconcile endpoint just by showing the list', async () => {
    await openSweeps()
    expect(mocks.post).not.toHaveBeenCalled()
  })
})
