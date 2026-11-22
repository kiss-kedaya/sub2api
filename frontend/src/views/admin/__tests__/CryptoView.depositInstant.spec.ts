import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCryptoGrant } from '@/composables/cryptoGrant'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
enableAutoUnmount(afterEach)

const config = () => ({ enabled: true, chains: ['bsc', 'ethereum'], rpc: {}, sweep_global_threshold: 100,
  sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0,
  sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 })

interface ServerDeposit {
  id: number; chain: string; tx_hash: string; log_index: number; from_address: string; to_address: string
  token: string; token_symbol: string; amount: string; status: string; created_at: string
  provisional?: boolean; provisional_amount?: number; credited_at?: string
  revoked_at?: string; revoke_reason?: string
}
const deposit = (id: number, overrides: Partial<ServerDeposit> = {}): ServerDeposit => ({
  id, chain: 'bsc', tx_hash: `0xtx${id}`, log_index: 0, from_address: '0xfrom', to_address: '0xto',
  token: 'USDT', token_symbol: 'USDT', amount: '10', status: 'credited', created_at: '2026-11-22T09:59:00Z', ...overrides
})
const summaryOf = (items: ServerDeposit[]) => ({
  total: items.length,
  credited: items.filter(item => item.status === 'credited').length,
  pending: items.filter(item => item.status === 'pending').length,
  ignored: items.filter(item => item.status === 'ignored').length,
  amount: items.filter(item => item.status === 'credited').reduce((sum, item) => sum + Number(item.amount), 0),
  provisional: items.filter(item => item.provisional && !item.revoked_at).length,
  revoked: items.filter(item => Boolean(item.revoked_at)).length
})

let serverDeposits: ServerDeposit[] = []

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  serverDeposits = [
    deposit(401, { provisional: true, provisional_amount: 67, credited_at: '2026-11-22T10:00:00Z' }),
    deposit(402, {
      status: 'reverted', provisional: true, provisional_amount: 67,
      credited_at: '2026-11-22T10:01:00Z', revoked_at: '2026-11-22T10:05:00Z',
      revoke_reason: 'reorg_noncanonical_block'
    }),
    deposit(403, { credited_at: '2026-11-22T10:02:00Z' })
  ]
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    let data: unknown = []
    if (path.endsWith('/config')) data = { ...config(), rpc_configured: [] }
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ok: true, configured: false, ready: false, enabled: false }
    if (path.endsWith('/pool')) data = { free: {}, low_watermark: 2 }
    if (path === '/admin/crypto/deposits') data = { items: serverDeposits.map(item => ({ ...item })), summary: summaryOf(serverDeposits) }
    return { data: { data }, status: 200 }
  })
  mocks.post.mockImplementation(async () => ({ data: { data: {} }, status: 200 }))
})
afterEach(() => { clearCryptoGrant(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

async function openDeposits(): Promise<VueWrapper> {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true } } })
  await flushPromises()
  const found = wrapper.findAll('[role="tab"]').find(item => item.text() === 'admin.crypto.tabDeposits')
  if (!found) throw new Error('Missing deposits tab')
  await found.trigger('mousedown', { button: 0 })
  await flushPromises()
  return wrapper
}
const panel = (wrapper: VueWrapper) => wrapper.get('#crypto-panel-deposits')

describe('Crypto deposits: instant credit state', () => {
  it('marks provisional credits and clawed-back credits with distinct badges', async () => {
    const wrapper = await openDeposits()

    const provisionalBadges = panel(wrapper).findAll('[data-testid="deposit-provisional-badge"]')
    const revokedBadges = panel(wrapper).findAll('[data-testid="deposit-revoked-badge"]')
    expect(provisionalBadges).toHaveLength(1)
    expect(provisionalBadges[0].text()).toBe('deposit.statusProvisional')
    expect(revokedBadges).toHaveLength(1)
    expect(revokedBadges[0].text()).toBe('deposit.statusRevoked')
  })

  it('translates the revoke reason for the operator instead of showing the machine code', async () => {
    const wrapper = await openDeposits()

    const reasons = panel(wrapper).findAll('[data-testid="deposit-revoked-reason"]')
    expect(reasons).toHaveLength(1)
    expect(reasons[0].text()).toBe('deposit.revokeReasonReorgNoncanonical')
    expect(panel(wrapper).text()).not.toContain('reorg_noncanonical_block')
  })

  it('keeps the final confirmation time visible for credited rows', async () => {
    const wrapper = await openDeposits()

    // credited_at is rendered by the new column; the value comes from the API row.
    const text = panel(wrapper).text()
    expect(text).toContain('admin.crypto.depCreditedAt')
    expect(panel(wrapper).findAll('tbody tr')).toHaveLength(3)
  })
})
