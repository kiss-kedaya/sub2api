import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
vi.mock('@/components/crypto/signer/SignerShardsPanel.vue', () => ({
  default: { name: 'SignerShardsPanel', emits: ['change', 'open-sweep'], template: '<div data-testid="shards-panel" />' }
}))
vi.mock('@/components/crypto/signer/ShardUnlockDialog.vue', () => ({
  default: { name: 'ShardUnlockDialog', props: ['show', 'mode', 'crypto', 'locked'], template: '<div v-if="show" data-testid="unlock-dialog" />' }
}))
enableAutoUnmount(afterEach)

const config = () => ({
  enabled: true,
  chains: ['bsc'],
  rpc: { bsc: 'https://one.example' },
  sweep_global_threshold: 100,
  sweep_min_per_address: {},
  gas_topup: {},
  hot_wallet_evm: '0x1234567890123456789012345678901234567890',
  hot_wallet_solana: '',
  hot_index: 0,
  sweep_auto: false,
  confirmations: {},
  scan_range: {},
  credit_min_amount: 1,
  credit_max_amount: 10000,
  credit_multiplier: 1
})

let walletStatus: Record<string, unknown>
// The backend-owned readiness verdict; the page renders this instead of
// deriving "can I sweep?" from wallet/status.ready on its own.
let sweepGate: Record<string, unknown>

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  walletStatus = { ok: true, configured: true, ready: true, enabled: true }
  sweepGate = { ready: true, attemptable: true, reason: '', wallet_ok: true, wallet_ready: true, channel_enabled: true, destination_configured: true, destination_locked: true }
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path.endsWith('/sweep/readiness')) return { data: { data: { ...sweepGate } } }
    let data: unknown = []
    if (path.endsWith('/config')) data = { ...config(), rpc: {}, rpc_configured: ['bsc'] }
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ...walletStatus }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/hot-wallet/address')) data = { index: 0, evm: '0xGAS', solana: '' }
    if (path.endsWith('/sweep/plan')) data = { Entries: [{ Chain: 'bsc', Address: '0xDEPOSIT', Token: 'USDT', AmountDecimal: 10 }], TotalUSD: 10, DryRun: true }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/operation-intent')) return { data: { accepted: true }, status: 200 }
    return { data: { data: {} }, status: 200 }
  })
})
afterEach(() => vi.restoreAllMocks())

function button(wrapper: VueWrapper, key: string) {
  const found = wrapper.findAll('button').find(item => item.text() === key)
  if (!found) throw new Error(`Missing button: ${key}`)
  return found
}

async function render() {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}

async function sweepTab(wrapper: VueWrapper) {
  const tab = wrapper.findAll('[role="tab"]').find(item => item.text() === 'admin.crypto.tabSweep')
  if (!tab) throw new Error('missing sweep tab')
  await tab.trigger('mousedown', { button: 0 })
  await flushPromises()
}

describe('sweep unlock entry when the v3 vault signer is locked', () => {
  beforeEach(() => {
    // A locked v3 vault reports ready=false by design: the seeds cannot be
    // loaded without the data key. The gate still marks the run button
    // attemptable so a click can open the unlock dialog.
    walletStatus = {
      ok: true,
      configured: true,
      ready: false,
      enabled: true,
      signer_locked: true,
      shards: { configured: true, vault_version: 3, locked: true, auto_enabled: false }
    }
    sweepGate = {
      ready: false,
      attemptable: true,
      reason: 'signer_locked',
      wallet_ok: true,
      wallet_ready: false,
      channel_enabled: true,
      signer_locked: true,
      destination_configured: true,
      destination_locked: true
    }
  })

  it('disables the run button while locked, explains why and offers the unlock CTA', async () => {
    const wrapper = await render()
    await sweepTab(wrapper)

    expect(wrapper.find('[data-testid="crypto-sweep-signer-locked"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="unlock-dialog"]').exists()).toBe(false)
    expect(button(wrapper, 'admin.crypto.sweepRun').attributes('disabled')).toBeDefined()

    const cta = button(wrapper, 'admin.crypto.sweepUnlockCta')
    await cta.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="unlock-dialog"]').exists()).toBe(true)
    expect(mocks.post.mock.calls.filter(([path]) => String(path).endsWith('/sweep/run'))).toHaveLength(0)
  })

  it('keeps the ordinary verification flow when the signer is not locked', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true, signer_locked: false }
    sweepGate = { ready: true, attemptable: true, reason: '', wallet_ok: true, wallet_ready: true, channel_enabled: true, signer_locked: false, destination_configured: true, destination_locked: true }
    const wrapper = await render()
    await sweepTab(wrapper)
    expect(wrapper.find('[data-testid="crypto-sweep-signer-locked"]').exists()).toBe(false)
    await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="unlock-dialog"]').exists()).toBe(false)
  })
})
