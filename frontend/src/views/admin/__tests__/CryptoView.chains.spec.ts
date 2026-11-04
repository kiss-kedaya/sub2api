import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import CryptoView from '../CryptoView.vue'
import { routes } from '@/router/records'
import { chain, report } from '@/components/crypto/__tests__/chainStatusFixtures'
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

const STATUS_PATH = '/admin/crypto/chains/status'
const statusCalls = () => mocks.get.mock.calls.filter(([path]) => path === STATUS_PATH).length
let chainReport = report()

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  chainReport = report()
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path === STATUS_PATH) return { data: { data: chainReport }, status: 200 }
    let data: unknown = []
    if (path.endsWith('/config')) data = { enabled: true, chains: ['bsc'], rpc: {}, rpc_configured: [], sweep_global_threshold: 100, sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '', hot_wallet_solana: '', hot_index: 0, sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 }
    if (path.endsWith('/addresses')) data = { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ok: true, configured: false, ready: false, enabled: false }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
})
afterEach(() => { vi.useRealTimers(); clearCryptoGrant() })

const tabByName = (w: VueWrapper, name: string) => {
  const found = w.findAll('[role="tab"]').find(item => item.text() === name)
  if (!found) throw new Error(`Missing tab: ${name}`)
  return found
}
const openTab = async (w: VueWrapper, name: string) => { await tabByName(w, name).trigger('mousedown', { button: 0 }); await flushPromises() }
const advance = async (ms: number) => { await vi.advanceTimersByTimeAsync(ms); await flushPromises() }

describe('node status tab (plain mount)', () => {
  const render = async () => { const w = mount(CryptoView, { global: { stubs: { teleport: true } } }); await flushPromises(); return w }

  it('sits after the deposits tab, stays idle until opened, then loads and polls every 3 seconds', async () => {
    const w = await render()
    const labels = w.findAll('[role="tab"]').map(t => t.text())
    expect(labels.indexOf('节点状态')).toBe(labels.indexOf('到账记录') + 1)
    expect(statusCalls()).toBe(0)
    await openTab(w, '节点状态')
    expect(statusCalls()).toBe(1)
    expect(w.find('[data-testid="chain-row-bsc"]').exists()).toBe(true)
    await advance(3000)
    expect(statusCalls()).toBe(2)
  })

  it('stops requesting as soon as another tab is opened', async () => {
    const w = await render()
    await openTab(w, '节点状态')
    await openTab(w, '地址')
    const before = statusCalls()
    await advance(30_000)
    expect(statusCalls()).toBe(before)
    expect(w.find('[data-testid="chain-status-panel"]').exists()).toBe(false)
  })

  it('"open settings" on a not-enabled chain switches to the configuration tab', async () => {
    chainReport = report([chain({ enabled: false, state: 'disabled' })])
    const w = await render()
    await openTab(w, '节点状态')
    await w.get('[data-testid="chain-open-config"]').trigger('click')
    await flushPromises()
    expect(tabByName(w, '配置').attributes('aria-selected')).toBe('true')
    const before = statusCalls()
    await advance(30_000)
    expect(statusCalls()).toBe(before)
  })
})

describe('node status tab (routed, real route records)', () => {
  const crypto = routes.filter(r => String(r.name).startsWith('AdminCrypto')).map(r => ({ ...r, component: { template: '<div />' } }))
  async function render(name: string) {
    const router = createRouter({ history: createMemoryHistory(), routes: crypto })
    await router.push({ name }); await router.isReady()
    const w = mount(CryptoView, { global: { plugins: [router], stubs: { teleport: true } } })
    await flushPromises()
    return { w, router }
  }

  it('has its own route, listed after deposits and before settings', async () => {
    const { w, router } = await render('AdminCryptoChains')
    expect(router.resolve({ name: 'AdminCryptoChains' }).path).toBe('/admin/crypto/chains')
    expect(router.getRoutes().find(r => r.name === 'AdminCryptoChains')?.meta.section).toBe('chains')
    const links = w.findAll('a').map(a => a.text()).filter(t => ['概览', '地址', '归集', '到账记录', '节点状态', '配置'].includes(t))
    expect(links).toEqual(['概览', '地址', '归集', '到账记录', '节点状态', '配置'])
  })

  it('polls only while its route is shown, including after returning to the overview', async () => {
    const { router } = await render('AdminCryptoChains')
    expect(statusCalls()).toBe(1)
    await advance(3000)
    expect(statusCalls()).toBe(2)
    await router.push({ name: 'AdminCrypto' }); await flushPromises()
    const idle = statusCalls()
    await advance(30_000)
    expect(statusCalls()).toBe(idle)
    await router.push({ name: 'AdminCryptoChains' }); await flushPromises()
    expect(statusCalls()).toBe(idle + 1)
  })

  it('"open settings" navigates to the settings route and polling stops', async () => {
    chainReport = report([chain({ enabled: false, state: 'disabled' })])
    const { w, router } = await render('AdminCryptoChains')
    await w.get('[data-testid="chain-open-config"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('AdminCryptoSettings')
    const before = statusCalls()
    await advance(30_000)
    expect(statusCalls()).toBe(before)
  })
})
