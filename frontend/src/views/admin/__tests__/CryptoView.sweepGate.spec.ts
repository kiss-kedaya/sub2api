import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params?.time ? `${key}:${params.time}` : key) }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
vi.mock('@/components/crypto/signer/SignerShardsPanel.vue', () => ({
  default: { name: 'SignerShardsPanel', emits: ['change', 'open-sweep', 'unlocked'], template: '<div data-testid="shards-panel" />' }
}))
vi.mock('@/components/crypto/signer/ShardUnlockDialog.vue', () => ({
  default: { name: 'ShardUnlockDialog', props: ['show', 'mode', 'crypto', 'locked'], emits: ['close', 'completed', 'unlocked'], template: '<div v-if="show" data-testid="unlock-dialog" />' }
}))
enableAutoUnmount(afterEach)

const hotWallet = '0x1234567890123456789012345678901234567890'

const config = () => ({
  enabled: true,
  chains: ['bsc'],
  rpc: {},
  rpc_configured: ['bsc'],
  sweep_global_threshold: 100,
  sweep_min_per_address: {},
  gas_topup: {},
  hot_wallet_evm: hotWallet,
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
let gate: Record<string, unknown>

const readyGate = () => ({
  ready: true,
  reason: '',
  channel_enabled: true,
  wallet_ok: true,
  wallet_ready: true,
  signer_locked: false,
  security_locked: false,
  destination_configured: true,
  destination_locked: true
})

function buildGate(overrides: Record<string, unknown>): Record<string, unknown> {
  return { ...readyGate(), ...overrides, ready: overrides.ready === true }
}

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  walletStatus = { ok: true, configured: true, ready: true, enabled: true, signer_locked: false }
  gate = readyGate()
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path.endsWith('/sweep/readiness')) return { data: { data: { ...gate } } }
    let data: unknown = []
    if (path.endsWith('/config')) data = config()
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ...walletStatus }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/hot-wallet/address')) data = { index: 0, evm: '0xGAS', solana: '' }
    if (path.endsWith('/sweep/plan')) data = { Entries: [], TotalUSD: 0, DryRun: true }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
  mocks.post.mockImplementation(async () => ({ data: { data: {} }, status: 200 }))
})
afterEach(() => vi.useRealTimers())

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

function runButton(wrapper: VueWrapper) {
  return wrapper.get('[data-testid="crypto-sweep-run"]')
}

describe('sweep run button follows the backend readiness verdict', () => {
  it('is disabled and names a missing sweep destination', async () => {
    gate = buildGate({ ready: false, reason: 'destination_missing', destination_configured: false })
    const wrapper = await render()
    await sweepTab(wrapper)

    expect(runButton(wrapper).attributes('disabled')).toBeDefined()
    const alert = wrapper.get('[data-testid="crypto-sweep-gate-blocked"]')
    expect(alert.text()).toContain('admin.crypto.sweepGate.destinationMissing')
    expect(alert.text()).toContain('admin.crypto.sweepGate.blockedTitle')
  })

  it('names a disabled deposit channel instead of a generic "wallet not ready"', async () => {
    gate = buildGate({ ready: false, reason: 'channel_disabled', channel_enabled: false })
    const wrapper = await render()
    await sweepTab(wrapper)

    expect(runButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="crypto-sweep-gate-blocked"]').text()).toContain('admin.crypto.sweepGate.channelDisabled')
  })

  it('is enabled when the backend gate is ready', async () => {
    const wrapper = await render()
    await sweepTab(wrapper)

    expect(runButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="crypto-sweep-gate-blocked"]').exists()).toBe(false)
  })

  it('keeps the button disabled for a locked signer and explains that verifying is not unlocking', async () => {
    walletStatus = { ok: true, configured: true, ready: false, enabled: true, signer_locked: true }
    gate = buildGate({ ready: false, reason: 'signer_locked', wallet_ready: false, signer_locked: true })
    const wrapper = await render()
    await sweepTab(wrapper)

    expect(runButton(wrapper).attributes('disabled')).toBeDefined()
    const alert = wrapper.get('[data-testid="crypto-sweep-signer-locked"]')
    expect(alert.text()).toContain('admin.crypto.sweepGate.signerLocked')
    expect(alert.text()).toContain('admin.crypto.sweepUnlockCta')

    const cta = wrapper.findAll('button').find(item => item.text() === 'admin.crypto.sweepUnlockCta')
    expect(cta).toBeTruthy()
    await cta!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="unlock-dialog"]').exists()).toBe(true)
  })

  it('shows the remaining signer window and disables the button again when it lapses', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-23T00:00:00Z'))
    const unlockedUntil = new Date(Date.now() + 90_000).toISOString()
    walletStatus = {
      ok: true,
      configured: true,
      ready: true,
      enabled: true,
      signer_locked: false,
      shards: { configured: true, vault_version: 3, locked: false, auto_enabled: false, unlocked_until: unlockedUntil, unlock_lease_seconds: 90 }
    }
    gate = { ...readyGate(), signer_unlocked_until: unlockedUntil }

    const wrapper = await render()
    await sweepTab(wrapper)

    expect(runButton(wrapper).attributes('disabled')).toBeUndefined()
    const window = wrapper.get('[data-testid="crypto-sweep-unlock-window"]')
    expect(window.text()).toContain('admin.crypto.sweepUnlockWindow')
    expect(window.text()).toContain('1:30')

    // The lease ends server-side: the next read reports the vault as locked.
    walletStatus = { ok: true, configured: true, ready: false, enabled: true, signer_locked: true }
    gate = buildGate({ ready: false, reason: 'signer_locked', wallet_ready: false, signer_locked: true })
    vi.advanceTimersByTime(91_000)
    await flushPromises()
    await flushPromises()

    expect(runButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-testid="crypto-sweep-unlock-window"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="crypto-sweep-signer-locked"]').exists()).toBe(true)
  })
})
