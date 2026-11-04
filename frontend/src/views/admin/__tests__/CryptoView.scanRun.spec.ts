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

const grantCredential = 'test-payment-grant-credential-32-bytes-minimum'
const chainRow = (cursor: number) => ({ chain: 'bsc', cursor, confirmed_head: 1000, remaining: 1000 - cursor, head_known: true })
const run = (over: Record<string, unknown> = {}) => ({
  id: 'scan-1', status: 'running', started_at: '', from_block: 0, rounds: 0, busy_retries: 0, stalled_rounds: 0,
  chains: [chainRow(900)], ...over
})
const chainText = (cursor: number) => `admin.crypto.scanRunChain{"chain":"bsc","cursor":${cursor},"head":1000,"remaining":${1000 - cursor}}`

let runList: unknown[]
let runStates: unknown[]
let depositCalls: () => number

beforeEach(() => {
  vi.resetAllMocks()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  runList = []
  runStates = []
  const config = { enabled: true, chains: ['bsc'], rpc: {}, rpc_configured: ['bsc'], sweep_global_threshold: 100, sweep_min_per_address: {},
    gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0, sweep_auto: false,
    confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 }
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path.endsWith('/scan/runs')) return { data: { data: runList } }
    if (/\/scan\/runs\/[^/]+$/.test(path)) return { data: { data: runStates.length > 1 ? runStates.shift() : runStates[0] } }
    let data: unknown = []
    if (path.endsWith('/config') || path.endsWith('/config/private')) data = config
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ok: true, configured: false, ready: false, enabled: false }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/verify')) return { data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300000).toISOString(), expires_in: 300 } } }
    // The start call consumes the first state, so the first poll sees the next one.
    if (path.endsWith('/scan/run')) return { data: { data: runStates.length > 1 ? runStates.shift() : runStates[0] }, status: 202 }
    if (path.endsWith('/cancel')) return { data: { data: runStates[0] }, status: 202 }
    return { data: { data: {} }, status: 200 }
  })
  depositCalls = () => mocks.get.mock.calls.filter(([path]) => String(path).endsWith('/deposits')).length
})
afterEach(() => { clearCryptoGrant(); vi.useRealTimers(); vi.restoreAllMocks() })

function button(wrapper: VueWrapper, key: string) {
  const found = wrapper.findAll('button').find(item => item.text() === key)
  if (!found) throw new Error(`Missing button: ${key}`)
  return found
}
async function openDeposits() {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true } } })
  await flushPromises()
  const found = wrapper.findAll('[role="tab"]').find(item => item.text() === 'admin.crypto.tabDeposits')
  if (!found) throw new Error('Missing deposits tab')
  await found.trigger('mousedown', { button: 0 })
  await flushPromises()
  return wrapper
}
async function tick(ms = 2000) {
  await vi.advanceTimersByTimeAsync(ms)
  await flushPromises()
}
const scanCalls = () => mocks.post.mock.calls.filter(([path]) => String(path).endsWith('/scan/run'))

describe('background catch-up scan on the deposits tab', () => {
  it('starts without a second factor, shows progress and finishes with a refreshed list', async () => {
    runStates = [run(), run({ rounds: 1, chains: [chainRow(950)] }), run({ status: 'completed', stop_reason: 'caught_up', rounds: 2, chains: [chainRow(1000)] })]
    const wrapper = await openDeposits()
    const before = depositCalls()
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()

    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 0 }, expect.objectContaining({ params: { async: '1' } }))
    expect(mocks.post.mock.calls.filter(([path]) => String(path).endsWith('/security/verify'))).toHaveLength(0)
    expect(wrapper.text()).toContain('admin.crypto.scanDepositsRunning')
    expect(wrapper.text()).toContain(chainText(900))
    expect(button(wrapper, 'admin.crypto.scanDeposits').attributes('disabled')).toBeDefined()

    await tick()
    expect(wrapper.text()).toContain(chainText(950))
    expect(wrapper.text()).toContain('admin.crypto.scanRunRounds{"count":1}')
    expect(mocks.showSuccess).not.toHaveBeenCalled()

    await tick()
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.scanDepositsDone')
    expect(wrapper.text()).not.toContain('admin.crypto.scanDepositsRunning')
    expect(depositCalls()).toBeGreaterThan(before)
    expect(button(wrapper, 'admin.crypto.scanDeposits').attributes('disabled')).toBeUndefined()
  })

  it('keeps a stopped scan on screen with why it stopped and the last error', async () => {
    runStates = [run({ status: 'failed', stop_reason: 'stalled', rounds: 5, stalled_rounds: 5, last_error: 'RPC connection failed' })]
    const wrapper = await openDeposits()
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith('admin.crypto.scanRunStalled')
    expect(wrapper.text()).toContain('admin.crypto.scanRunStalled')
    expect(wrapper.text()).toContain('admin.crypto.scanRunLastError{"message":"RPC connection failed"}')
    expect(wrapper.text()).toContain(chainText(900))
    expect(wrapper.text()).not.toContain('admin.crypto.scanRunCancel')
    expect(button(wrapper, 'admin.crypto.scanDeposits').attributes('disabled')).toBeUndefined()
  })

  it('cancelling asks for a fresh second factor and then follows the run to cancelled', async () => {
    runStates = [run(), run({ status: 'cancelled', stop_reason: 'cancelled', rounds: 1, chains: [chainRow(930)] })]
    const wrapper = await openDeposits()
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.scanRunCancel').trigger('click')
    await flushPromises()

    expect(mocks.post.mock.calls.filter(([path]) => String(path).endsWith('/cancel'))).toHaveLength(0)
    await wrapper.get('#crypto-security-code').setValue('123456')
    await wrapper.get('#crypto-security-form').trigger('submit')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/scan/runs/scan-1/cancel', null,
      expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))

    await tick()
    expect(wrapper.text()).toContain('admin.crypto.scanRunCancelled')
    expect(wrapper.text()).toContain(chainText(930))
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('reattaches to a scan that is already running when the tab opens, without starting another', async () => {
    runList = [run()]
    runStates = [run({ status: 'completed', stop_reason: 'caught_up', rounds: 3, chains: [chainRow(1000)] })]
    runStates.unshift(run({ rounds: 2, chains: [chainRow(960)] }))
    const wrapper = await openDeposits()
    expect(wrapper.text()).toContain('admin.crypto.scanDepositsRunning')
    expect(scanCalls()).toHaveLength(0)

    await tick()
    await tick()
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.scanDepositsDone')
    expect(scanCalls()).toHaveLength(0)
  })

  it('a 409 on start attaches to the scan that is already running instead of reporting an error', async () => {
    runStates = [run({ status: 'completed', stop_reason: 'caught_up', chains: [chainRow(1000)] })]
    const wrapper = await openDeposits()
    mocks.post.mockRejectedValueOnce({ status: 409, message: 'A catch-up scan is already running' })
    runList = [run()]
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()
    await tick()

    expect(mocks.showError).not.toHaveBeenCalled()
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.scanDepositsDone')
  })

  it('a start that fails with nothing running shows the server message and frees the button', async () => {
    const wrapper = await openDeposits()
    mocks.post.mockRejectedValueOnce({ status: 503, message: 'Payment scanner unavailable' })
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()

    expect(mocks.showError).toHaveBeenCalledWith('Payment scanner unavailable')
    expect(button(wrapper, 'admin.crypto.scanDeposits').attributes('disabled')).toBeUndefined()
  })

  it('leaving the page stops only the polling and never cancels the scan', async () => {
    runStates = [run()]
    const wrapper = await openDeposits()
    await button(wrapper, 'admin.crypto.scanDeposits').trigger('click')
    await flushPromises()
    const polls = () => mocks.get.mock.calls.filter(([path]) => /\/scan\/runs\/scan-1$/.test(String(path))).length
    const before = polls()
    wrapper.unmount()
    await tick(6000)

    expect(polls()).toBe(before)
    expect(mocks.post.mock.calls.filter(([path]) => String(path).endsWith('/cancel'))).toHaveLength(0)
  })
})
