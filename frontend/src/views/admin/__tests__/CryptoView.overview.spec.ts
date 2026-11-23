import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import CryptoView from '../CryptoView.vue'
import { routes } from '@/router/records'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
  securityRefresh: vi.fn(async () => undefined),
  signerRefresh: vi.fn(async () => undefined)
}))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
    showWarning: mocks.showWarning
  })
}))
vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
    showWarning: mocks.showWarning
  })
}))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? `${key}${JSON.stringify(params)}` : key) })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({
  default: {
    name: 'CryptoWalletSecurityPanel',
    emits: ['change', 'recovered'],
    methods: { refresh: mocks.securityRefresh },
    template: '<div data-testid="security-panel-stub" />'
  }
}))
vi.mock('@/components/crypto/signer/SignerShardsPanel.vue', () => ({
  default: {
    name: 'SignerShardsPanel',
    emits: ['change', 'open-sweep', 'unlocked', 'expired'],
    methods: { refresh: mocks.signerRefresh },
    template: '<div data-testid="shards-panel-stub" />'
  }
}))
vi.mock('@/components/crypto/signer/ShardUnlockDialog.vue', () => ({
  default: { name: 'ShardUnlockDialog', props: ['show', 'mode', 'crypto', 'locked'], template: '<div v-if="show" data-testid="unlock-dialog" />' }
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

const unlockedShards = { configured: true, vault_version: 3, locked: false, auto_enabled: false }
const lockedShards = { configured: true, vault_version: 3, locked: true, auto_enabled: false }
const healthySecurity = { enabled: true, initialized: true, locked: false, deposits_paused: false, alert_pending: false }

let walletStatus: Record<string, unknown>
let gate: Record<string, unknown>

beforeEach(() => {
  vi.resetAllMocks()
  mocks.securityRefresh.mockResolvedValue(undefined)
  mocks.signerRefresh.mockResolvedValue(undefined)
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  walletStatus = { ok: true, configured: true, ready: true, enabled: true, signer_locked: false, shards: { ...unlockedShards } }
  gate = readyGate()
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path.endsWith('/sweep/readiness')) return { data: { data: { ...gate } } }
    let data: unknown = []
    if (path.endsWith('/config')) data = { ...config(), rpc: {}, rpc_configured: ['bsc'] }
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 3, deposit_count: 4, credited_total: 5, swept_total: 6 } }
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

async function mountOverview() {
  const crypto = routes.filter(route => String(route.name).startsWith('AdminCrypto')).map(route => ({ ...route, component: { template: '<div />' } }))
  const router = createRouter({ history: createMemoryHistory(), routes: crypto })
  await router.push({ name: 'AdminCrypto' })
  await router.isReady()
  const wrapper = mount(CryptoView, { global: { plugins: [router], stubs: { teleport: true } } })
  await flushPromises()
  return { wrapper, router }
}

function segment(wrapper: VueWrapper, id: string) {
  return wrapper.get(`[data-testid="overview-status-${id}"]`)
}

function callsOf(pathSuffix: string): number {
  return mocks.get.mock.calls.filter(([path]) => String(path).endsWith(pathSuffix)).length
}

describe('crypto overview status strip and primary actions', () => {
  it('merges wallet, security monitor and signer into one three-segment strip', async () => {
    const { wrapper } = await mountOverview()
    const security = wrapper.findComponent({ name: 'CryptoWalletSecurityPanel' })
    security.vm.$emit('change', { ...healthySecurity })
    await flushPromises()

    expect(segment(wrapper, 'wallet').attributes('data-state')).toBe('ok')
    expect(segment(wrapper, 'security').attributes('data-state')).toBe('ok')
    expect(segment(wrapper, 'signer').attributes('data-state')).toBe('ok')
    // Available segments carry no recovery entry.
    expect(wrapper.find('[data-testid="overview-status-wallet-action"]').exists()).toBe(false)
  })

  it('marks an unconfigured wallet red and links straight to the configuration', async () => {
    walletStatus = { ok: true, configured: false, ready: false, enabled: false }
    gate = { ...readyGate(), ready: false, reason: 'wallet_not_ready', wallet_ready: false }
    const { wrapper, router } = await mountOverview()

    expect(segment(wrapper, 'wallet').attributes('data-state')).toBe('error')
    const action = wrapper.get('[data-testid="overview-status-wallet-action"]')
    expect(action.text()).toContain('admin.crypto.overview.walletAction')
    await action.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('AdminCryptoSettings')
    // Setup guidance exists as steps instead of a wall of disabled controls.
    expect(wrapper.get('[data-testid="crypto-wallet-setup-steps"]').text()).toContain('admin.crypto.walletSetupStep1')
  })

  it('turns the signer segment red for a lapsed window and sends the entry to unlock', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
    vi.setSystemTime(new Date('2026-11-23T00:00:00.000Z'))
    walletStatus = {
      ok: true,
      configured: true,
      ready: true,
      enabled: true,
      signer_locked: false,
      shards: { ...lockedShards, locked: false, unlocked_until: new Date(Date.now() - 5_000).toISOString() }
    }
    gate = { ...readyGate(), ready: false, reason: 'signer_locked', signer_locked: true }
    const { wrapper, router } = await mountOverview()
    const push = vi.spyOn(router, 'push')

    const signer = segment(wrapper, 'signer')
    expect(signer.attributes('data-state')).toBe('error')
    expect(signer.text()).toContain('admin.crypto.signer.lockedExpired')

    await wrapper.get('[data-testid="overview-status-signer-action"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('AdminCryptoSweeps')
    // The unlock intent travels in the URL, exactly like the 2.3.153 flow.
    const pushed = push.mock.calls.map(([target]) => target as { query?: Record<string, unknown> })
    expect(pushed.some(target => String(target.query?.unlock ?? '') === '1')).toBe(true)
  })

  it('offers the two primary overview exits with the unlock intent when locked', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true, signer_locked: true, shards: { ...lockedShards } }
    gate = { ...readyGate(), ready: false, reason: 'signer_locked', signer_locked: true }
    const { wrapper, router } = await mountOverview()

    const sweep = wrapper.get('[data-testid="overview-run-sweep"]')
    const addresses = wrapper.get('[data-testid="overview-view-addresses"]')
    expect(sweep.attributes('href')).toContain('/admin/crypto/sweeps')
    expect(sweep.attributes('href')).toContain('unlock=1')
    expect(addresses.attributes('href')).toContain('/admin/crypto/addresses')

    await sweep.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('AdminCryptoSweeps')
  })

  it('keeps one refresh control that updates status and statistics together', async () => {
    const { wrapper } = await mountOverview()
    // No card-level refresh buttons survive on the overview: one page control
    // drives every status source plus the statistics.
    expect(wrapper.find('[data-testid="wallet-security-refresh"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="signer-shards-refresh"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="crypto-overview-refresh"]').text()).toContain('admin.crypto.overview.refreshAll')

    const statusCalls = callsOf('/wallet/status')
    const gateCalls = callsOf('/sweep/readiness')
    const addressCalls = callsOf('/addresses')
    const depositCalls = callsOf('/deposits')

    await wrapper.get('[data-testid="crypto-overview-refresh"]').trigger('click')
    await flushPromises()

    expect(callsOf('/wallet/status')).toBe(statusCalls + 1)
    expect(callsOf('/sweep/readiness')).toBe(gateCalls + 1)
    expect(callsOf('/addresses')).toBe(addressCalls + 1)
    expect(callsOf('/deposits')).toBe(depositCalls + 1)
    expect(mocks.securityRefresh).toHaveBeenCalledTimes(1)
    expect(mocks.signerRefresh).toHaveBeenCalledTimes(1)
  })
})
