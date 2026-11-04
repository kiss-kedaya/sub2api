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
const grantResponse = () => ({ data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300000).toISOString(), expires_in: 300 } } })
const config = () => ({ enabled: true, chains: ['bsc', 'ethereum'], rpc: {}, sweep_global_threshold: 100,
  sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0,
  sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 })

interface ServerDeposit {
  id: number; chain: string; tx_hash: string; log_index: number; from_address: string; to_address: string
  token: string; token_symbol: string; amount: string; status: string; created_at: string
}
const deposit = (id: number, status: string, amount: string, chain = 'bsc'): ServerDeposit => ({
  id, chain, tx_hash: `0xtx${id}`, log_index: 0, from_address: '0xfrom', to_address: '0xto',
  token: 'USDT', token_symbol: 'USDT', amount, status, created_at: '2026-11-05T00:00:00Z'
})
const summaryOf = (items: ServerDeposit[]) => ({
  total: items.length,
  credited: items.filter(item => item.status === 'credited').length,
  pending: items.filter(item => item.status === 'pending').length,
  ignored: items.filter(item => item.status === 'ignored').length,
  amount: items.filter(item => item.status === 'credited').reduce((sum, item) => sum + Number(item.amount), 0)
})

let serverDeposits: ServerDeposit[] = []
let confirmHandler: (body: { ids: number[]; note?: string }) => unknown | Promise<unknown>
const creditAll = ({ ids }: { ids: number[] }) => {
  serverDeposits = serverDeposits.map(item => ids.includes(item.id) ? { ...item, status: 'credited' } : item)
  return { data: { data: { results: ids.map(id => ({ id, status: 'credited', order_id: 1000 + id })), credited: ids.length, failed: 0 } }, status: 200 }
}

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  serverDeposits = [deposit(302, 'pending', '10.5'), deposit(321, 'pending', '4', 'ethereum')]
  confirmHandler = creditAll
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
  mocks.post.mockImplementation(async (path: string, body: { ids: number[]; note?: string }) => {
    if (path.endsWith('/security/verify')) return grantResponse()
    if (path.endsWith('/deposits/confirm')) return confirmHandler(body)
    return { data: { data: {} }, status: 200 }
  })
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
const rowBoxes = (wrapper: VueWrapper) => panel(wrapper).findAll('[data-testid="deposit-select-row"]')
const selectAll = (wrapper: VueWrapper) => panel(wrapper).get('[data-testid="deposit-select-all"]')
const openButton = (wrapper: VueWrapper) => wrapper.get('[data-testid="deposit-confirm-open"]')
const confirmCalls = () => mocks.post.mock.calls.filter(([path]) => path.endsWith('/deposits/confirm'))
const verifyCalls = () => mocks.post.mock.calls.filter(([path]) => path.endsWith('/security/verify'))
const depositLoads = () => mocks.get.mock.calls.filter(([path]) => path === '/admin/crypto/deposits').length
async function refresh(wrapper: VueWrapper) {
  await panel(wrapper).findAll('button').find(item => item.text() === 'common.refresh')!.trigger('click')
  await flushPromises()
}
async function enterCode(wrapper: VueWrapper, code = '234567') {
  await flushPromises()
  await wrapper.get('#crypto-security-code').setValue(code)
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
}
async function startConfirm(wrapper: VueWrapper, note = '') {
  await selectAll(wrapper).trigger('click')
  await openButton(wrapper).trigger('click')
  await flushPromises()
  if (note) await wrapper.get('[data-testid="deposit-confirm-note"]').setValue(note)
  await wrapper.get('[data-testid="deposit-confirm-submit"]').trigger('click')
  await flushPromises()
}

describe('Crypto deposits: manual confirmation', () => {
  it('offers a checkbox on pending rows only, and the header selects only those', async () => {
    serverDeposits = [deposit(302, 'pending', '10.5'), deposit(303, 'credited', '7'), deposit(321, 'pending', '4', 'ethereum'), deposit(304, 'ignored', '1')]
    const wrapper = await openDeposits()
    const rows = panel(wrapper).findAll('tbody tr')
    expect(rows).toHaveLength(4)
    for (const row of rows) {
      expect(row.find('[data-testid="deposit-select-row"]').exists(), row.text()).toBe(row.text().includes('deposit.statusPending'))
    }
    expect(rowBoxes(wrapper)).toHaveLength(2)
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)

    await selectAll(wrapper).trigger('click')
    expect(rowBoxes(wrapper).map(box => box.attributes('aria-checked'))).toEqual(['true', 'true'])
    expect(openButton(wrapper).text()).toBe('admin.crypto.depositConfirmAction{"count":2,"total":"14.50"}')

    await selectAll(wrapper).trigger('click')
    expect(rowBoxes(wrapper).map(box => box.attributes('aria-checked'))).toEqual(['false', 'false'])
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)
  })

  it('tracks single selections in the button count and total', async () => {
    const wrapper = await openDeposits()
    await rowBoxes(wrapper)[1].trigger('click')
    expect(openButton(wrapper).text()).toBe('admin.crypto.depositConfirmAction{"count":1,"total":"4.00"}')
    expect(selectAll(wrapper).attributes('aria-checked')).toBe('mixed')
    await rowBoxes(wrapper)[0].trigger('click')
    expect(openButton(wrapper).text()).toBe('admin.crypto.depositConfirmAction{"count":2,"total":"14.50"}')
    await rowBoxes(wrapper)[1].trigger('click')
    await rowBoxes(wrapper)[0].trigger('click')
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)
  })

  it('adds no checkbox column when the page has nothing pending', async () => {
    serverDeposits = [deposit(303, 'credited', '7'), deposit(304, 'ignored', '1')]
    const wrapper = await openDeposits()
    expect(panel(wrapper).findAll('tbody tr')).toHaveLength(2)
    expect(panel(wrapper).find('[data-testid="deposit-select-all"]').exists()).toBe(false)
    expect(rowBoxes(wrapper)).toHaveLength(0)
  })

  it('shows count, total and chains in a confirmation before sending or asking for any code', async () => {
    const wrapper = await openDeposits()
    await selectAll(wrapper).trigger('click')
    await openButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="deposit-confirm-count"]').text()).toBe('2')
    expect(wrapper.get('[data-testid="deposit-confirm-total"]').text()).toBe('14.50')
    expect(wrapper.get('[data-testid="deposit-confirm-chains"]').text()).toBe('bsc, ethereum')
    expect(wrapper.get('[data-testid="deposit-confirm-warning"]').text()).toContain('admin.crypto.depositConfirmWarning')
    expect(confirmCalls()).toHaveLength(0)
    expect(verifyCalls()).toHaveLength(0)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
  })

  it('cancelling the confirmation sends nothing and keeps the selection', async () => {
    const wrapper = await openDeposits()
    await selectAll(wrapper).trigger('click')
    await openButton(wrapper).trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="deposit-confirm-cancel"]').trigger('click')
    await flushPromises()
    expect(confirmCalls()).toHaveLength(0)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(openButton(wrapper).text()).toContain('"count":2')
  })

  it('asks for a fresh code, then sends the ids with the grant, shows each result and refreshes the list and summary', async () => {
    const wrapper = await openDeposits()
    const loadsBefore = depositLoads()
    await startConfirm(wrapper, '  ticket 88  ')
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    expect(wrapper.get('.crypto-security__operation').text()).toBe('admin.crypto.depositConfirmVerify{"count":2,"total":"14.50","chains":"bsc, ethereum"}')
    expect(confirmCalls()).toHaveLength(0)

    await enterCode(wrapper)
    expect(verifyCalls()).toHaveLength(1)
    expect(confirmCalls()).toHaveLength(1)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/deposits/confirm', { ids: [302, 321], note: 'ticket 88' },
      expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential }, _retry: true }))

    const items = wrapper.findAll('[data-testid="deposit-result-item"]')
    expect(items).toHaveLength(2)
    expect(wrapper.get('[data-testid="deposit-result-summary"]').text()).toBe('admin.crypto.depositResultSummary{"credited":2,"failed":0}')
    expect(items[0].text()).toContain('#302 · bsc · 10.5')
    expect(items[0].text()).toContain('admin.crypto.depositResultCredited')
    expect(items[0].text()).toContain('admin.crypto.depositResultOrder{"id":1302}')

    expect(depositLoads()).toBe(loadsBefore + 1)
    expect(rowBoxes(wrapper)).toHaveLength(0)
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)
    // The status filter also lists "pending", so look at the table rows and the stat card only.
    expect(panel(wrapper).findAll('tbody tr').map(row => row.text()).join(' ')).not.toContain('deposit.statusPending')
    expect(panel(wrapper).text()).toMatch(/admin\.crypto\.depPending0(?!\d)/)
  })

  it('sends no note key when the operator left it blank', async () => {
    const wrapper = await openDeposits()
    await startConfirm(wrapper)
    await enterCode(wrapper)
    expect(confirmCalls()[0][1]).toEqual({ ids: [302, 321] })
  })

  it('shows a rejected deposit with its reason and leaves it selectable for another look', async () => {
    confirmHandler = ({ ids }) => {
      serverDeposits = serverDeposits.map(item => item.id === 302 ? { ...item, status: 'credited' } : item)
      return { data: { data: { results: [
        { id: ids[0], status: 'credited', order_id: 555 },
        { id: ids[1], status: 'rejected', message: 'receipt not found' }
      ], credited: 1, failed: 1 } }, status: 200 }
    }
    const wrapper = await openDeposits()
    await startConfirm(wrapper)
    await enterCode(wrapper)
    const items = wrapper.findAll('[data-testid="deposit-result-item"]')
    expect(wrapper.get('[data-testid="deposit-result-summary"]').text()).toBe('admin.crypto.depositResultSummary{"credited":1,"failed":1}')
    expect(items[1].text()).toContain('admin.crypto.depositResultRejected')
    expect(items[1].text()).toContain('receipt not found')
    // Only the still-pending row keeps a checkbox, and it starts unselected.
    expect(rowBoxes(wrapper)).toHaveLength(1)
    expect(rowBoxes(wrapper)[0].attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)
  })

  it('shows a request-level failure in the verification dialog, with no results, and still refreshes the list', async () => {
    confirmHandler = () => { throw { status: 500, message: 'upstream exploded' } }
    const wrapper = await openDeposits()
    const loadsBefore = depositLoads()
    await startConfirm(wrapper)
    await enterCode(wrapper)
    expect(wrapper.get('.crypto-security__error').text()).toContain('upstream exploded')
    expect(wrapper.find('[data-testid="deposit-result-item"]').exists()).toBe(false)
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    // The request may have reached the server, so the list is reloaded and the selection reset.
    expect(depositLoads()).toBe(loadsBefore + 1)
    expect(wrapper.find('[data-testid="deposit-confirm-open"]').exists()).toBe(false)
    expect(confirmCalls()).toHaveLength(1)
  })

  it('does nothing further when the code prompt is cancelled', async () => {
    const wrapper = await openDeposits()
    const loadsBefore = depositLoads()
    await startConfirm(wrapper)
    await wrapper.get('[data-testid="crypto-security-cancel"]').trigger('click')
    await flushPromises()
    expect(confirmCalls()).toHaveLength(0)
    expect(verifyCalls()).toHaveLength(0)
    expect(depositLoads()).toBe(loadsBefore)
    expect(openButton(wrapper).text()).toContain('"count":2')
    expect(wrapper.find('[data-testid="deposit-result-item"]').exists()).toBe(false)
  })

  it('reuses a live grant for the next confirmation without another code', async () => {
    const wrapper = await openDeposits()
    await startConfirm(wrapper)
    await enterCode(wrapper)
    await wrapper.get('[data-testid="deposit-result-close"]').trigger('click')
    await flushPromises()

    serverDeposits = [...serverDeposits, deposit(400, 'pending', '2')]
    await refresh(wrapper)
    await startConfirm(wrapper)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(verifyCalls()).toHaveLength(1)
    expect(confirmCalls()).toHaveLength(2)
    expect(confirmCalls()[1][1]).toEqual({ ids: [400] })
    expect(confirmCalls()[1][2]).toEqual(expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
    expect(wrapper.findAll('[data-testid="deposit-result-item"]')).toHaveLength(1)
  })

  it('blocks a second confirmation while one is in flight', async () => {
    let release!: () => void
    confirmHandler = (body) => new Promise(resolve => { release = () => resolve(creditAll(body)) })
    const wrapper = await openDeposits()
    await startConfirm(wrapper)
    await enterCode(wrapper)
    expect(confirmCalls()).toHaveLength(1)
    expect(openButton(wrapper).attributes('disabled')).toBeDefined()
    await openButton(wrapper).trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="deposit-confirm-count"]').exists()).toBe(false)
    release()
    await flushPromises()
    expect(confirmCalls()).toHaveLength(1)
    expect(wrapper.findAll('[data-testid="deposit-result-item"]')).toHaveLength(2)
  })

  it('drops a selected row from the selection once a refresh shows it is no longer pending', async () => {
    const wrapper = await openDeposits()
    await selectAll(wrapper).trigger('click')
    expect(openButton(wrapper).text()).toContain('"count":2')
    serverDeposits = serverDeposits.map(item => item.id === 302 ? { ...item, status: 'credited' } : item)
    await refresh(wrapper)
    expect(openButton(wrapper).text()).toBe('admin.crypto.depositConfirmAction{"count":1,"total":"4.00"}')
    expect(rowBoxes(wrapper)).toHaveLength(1)
  })
})
