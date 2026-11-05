import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCryptoGrant } from '@/composables/cryptoGrant'
import { clearAddressOwnerCache } from '@/composables/useAddressOwners'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'

// Same mocking boundary as CryptoView.security.spec.ts: the HTTP client is replaced, everything
// above it (api functions, composables, components) is real. Answers use the backend's real shape:
// {"data": ...} with no `code`, which the real client would not unwrap either.
const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
enableAutoUnmount(afterEach)

// Fixture identities and addresses only.
const addr = (n: number) => '0x' + n.toString(16).padStart(40, '0')
const hash = (n: number) => '0x' + n.toString(16).padStart(64, '0')
const grantCredential = 'test-payment-grant-credential-32-bytes-minimum'
const recent = new Date(Date.now() - 2 * 86400000).toISOString()

const record = (id: number, over: Record<string, unknown> = {}) => ({
  id, chain: 'bsc', from_address: addr(id), to_address: addr(9999), token: '0xtoken', token_symbol: 'USDT', amount: '10',
  gas_tx_hash: '', sweep_tx_hash: hash(id), status: 'swept', error: '', created_at: recent, updated_at: recent, ...over
})

let recordPages: (params: Record<string, unknown>) => unknown[]
let blocked: unknown[]
let plan: unknown
let owners: Record<string, unknown>
let ownersStatus: number

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  clearAddressOwnerCache()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  recordPages = () => []
  blocked = []
  plan = { Entries: [], TotalUSD: 0, DryRun: true }
  owners = {}
  ownersStatus = 200
  const config = { enabled: true, chains: ['bsc'], rpc: {}, rpc_configured: [], sweep_global_threshold: 100, sweep_min_per_address: {}, gas_topup: {},
    hot_wallet_evm: addr(0xfeed), hot_wallet_solana: '', hot_index: 0, sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1,
    credit_max_amount: 10000, credit_multiplier: 1 }
  mocks.get.mockImplementation(async (path: string, options?: { params?: Record<string, unknown> }) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    if (path.endsWith('/address-owners')) {
      if (ownersStatus !== 200) throw { status: ownersStatus, message: 'page not found' }
      const asked = ([] as string[]).concat((options?.params?.address as string[] | undefined) ?? [])
      return { data: { data: { owners: Object.fromEntries(asked.filter(key => key in owners).map(key => [key, owners[key]])) } } }
    }
    if (path.endsWith('/sweep/records')) return { data: { data: recordPages(options?.params ?? {}) } }
    if (path.endsWith('/sweep/blocked')) return { data: { data: blocked } }
    if (path.endsWith('/sweep/plan')) return { data: { data: plan } }
    if (path.endsWith('/sweep/runs')) return { data: { data: [] } }
    if (path.endsWith('/config')) return { data: { data: config } }
    if (path.endsWith('/wallet/status')) return { data: { data: { ok: true, configured: true, ready: true, enabled: true } } }
    if (path.endsWith('/pool')) return { data: { data: { free: { evm: 10 }, low_watermark: 2 } } }
    if (path.endsWith('/hot-wallet/address')) return { data: { data: { index: 0, evm: addr(0xfeed), solana: '' } } }
    if (path.endsWith('/addresses')) return { data: { data: { items: [], total: 0, summary: { address_count: 0, deposit_count: 0, credited_total: 0, swept_total: 0 } } } }
    if (path.endsWith('/deposits')) return { data: { data: { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } } } }
    return { data: { data: [] } }
  })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/verify')) {
      return { data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300000).toISOString(), expires_in: 300 } } }
    }
    if (path.endsWith('/sweep/run')) {
      return { data: { data: { id: 'sweep-fixture', dry_run: true, chains: [], started_at: '', finished: true, summary: {
        planned: 1, swept: 0, failed: 0, pending: 0, gas_topups: 0, dry_run: true,
        entries: [{ chain: 'bsc', address: addr(31), amount: '5', gas_tx_hash: '', sweep_tx_hash: '', status: 'planned' }]
      } } }, status: 202 }
    }
    return { data: { data: {} }, status: 200 }
  })
})
afterEach(() => { clearCryptoGrant(); vi.restoreAllMocks() })

function button(wrapper: VueWrapper, key: string) {
  const found = wrapper.findAll('button').find(item => item.text() === key)
  if (!found) throw new Error(`Missing button: ${key}`)
  return found
}
// The page switches to a different layout when a router exists (useSectionRoute), so like the
// security spec this one stays router-less. ULink then warns about the missing router injection on
// every render; drop that one known message so any other warning stays visible.
const warnHandler = (message: string) => {
  if (!message.includes('Symbol(route location)')) console.warn(`[Vue warn]: ${message}`)
}

async function openSweepTab() {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true }, config: { warnHandler } } })
  await flushPromises()
  const found = wrapper.findAll('[role="tab"]').find(item => item.text() === 'admin.crypto.tabSweep')
  if (!found) throw new Error('Missing sweep tab')
  await found.trigger('mousedown', { button: 0 })
  await flushPromises()
  return wrapper
}
async function verify(wrapper: VueWrapper, code = '123456') {
  await flushPromises()
  if (!wrapper.find('#crypto-security-code').exists()) return
  await wrapper.get('#crypto-security-code').setValue(code)
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
}
/** Fill every table: a blocked job, a dry-run summary entry, a plan entry and some history. */
async function fillAllFourTables(wrapper: VueWrapper) {
  blocked = [{ job_id: 9, chain: 'bsc', address: addr(21), status: 'sweep_pending', error: '', age_minutes: 12, can_reconcile: true }]
  plan = { Entries: [{ Chain: 'bsc', Address: addr(41), Token: 'USDT', AmountDecimal: 7 }], TotalUSD: 7, DryRun: true }
  recordPages = () => [record(3), record(2), record(1)]
  await button(wrapper, 'common.refresh').trigger('click')
  await flushPromises()
  await button(wrapper, 'admin.crypto.sweepDryRun').trigger('click')
  await verify(wrapper)
  await flushPromises()
}
const tables = (wrapper: VueWrapper) => wrapper.findAll('#crypto-panel-sweep table')
const headers = (table: ReturnType<typeof tables>[number]) => table.findAll('thead th').map(th => th.text())
const ownerTexts = (table: ReturnType<typeof tables>[number]) =>
  table.findAll('tbody tr').map(row => row.findAll('td')[2]?.text() ?? '')

describe('sweep page: who owns each address', () => {
  it('gives all four tables a user column', async () => {
    const wrapper = await openSweepTab()
    await fillAllFourTables(wrapper)
    const found = tables(wrapper)
    expect(found).toHaveLength(4) // blocked, summary, plan, history
    expect(headers(found[0])).toEqual(['admin.crypto.chain', 'admin.crypto.address', 'admin.crypto.sweepView.colUser', 'common.status', 'admin.crypto.blockedAge', ''])
    expect(headers(found[1])).toEqual(['admin.crypto.chain', 'admin.crypto.address', 'admin.crypto.sweepView.colUser', 'common.status', 'TX'])
    expect(headers(found[2])).toEqual(['admin.crypto.chain', 'admin.crypto.address', 'admin.crypto.sweepView.colUser', 'admin.crypto.amount'])
    expect(headers(found[3])).toEqual([
      'admin.crypto.colTime', 'admin.crypto.chain', 'admin.crypto.sweepView.colUser',
      'admin.crypto.sweepView.colDepositAddress', 'admin.crypto.amount', 'common.status', 'TX'
    ])
  })

  it('keeps the blocked table address cell as "#jobId · address"', async () => {
    const wrapper = await openSweepTab()
    await fillAllFourTables(wrapper)
    const cells = tables(wrapper)[0].findAll('tbody tr')[0].findAll('td')
    expect(cells[1].text()).toBe('#9 · 0x000000…000015')
  })

  it('fills the user column in each table from one lookup', async () => {
    owners[addr(21)] = { user_id: 210, username: 'blocked-user', email: 'blocked@example.test', deleted: false }
    owners[addr(31)] = { user_id: 310, username: '', email: 'summary-only-email@example.test', deleted: false }
    owners[addr(41)] = { user_id: 410, username: 'plan-user', email: 'plan@example.test', deleted: true }
    owners[addr(1)] = { user_id: 10, username: 'history-user', email: 'history@example.test', deleted: false }
    const wrapper = await openSweepTab()
    await fillAllFourTables(wrapper)
    const found = tables(wrapper)
    expect(ownerTexts(found[0])[0]).toContain('blocked-user')
    expect(ownerTexts(found[1])[0]).toContain('summary-only-email@example.test')
    expect(ownerTexts(found[2])[0]).toContain('plan-user')
    expect(ownerTexts(found[2])[0]).toContain('admin.crypto.sweepView.ownerDeleted')
    const history = ownerTexts(found[3])
    expect(history[2]).toContain('history-user') // record 1 is the third row
    expect(history[0]).toBe('—') // nobody owns record 3's address
  })

  it('never asks for the same address twice, however many tables show it', async () => {
    // One address appears in the blocked table, the plan and the history.
    blocked = [{ job_id: 9, chain: 'bsc', address: addr(1), status: 'sweep_pending', error: '', age_minutes: 5, can_reconcile: true }]
    plan = { Entries: [{ Chain: 'bsc', Address: addr(1), Token: 'USDT', AmountDecimal: 7 }], TotalUSD: 7, DryRun: true }
    recordPages = () => [record(3), record(2), record(1)]
    const wrapper = await openSweepTab()
    await button(wrapper, 'admin.crypto.loadPlan').trigger('click')
    await flushPromises()
    const asked = mocks.get.mock.calls
      .filter(([path]) => String(path).endsWith('/address-owners'))
      .flatMap(([, options]) => options.params.address as string[])
    expect(asked.length).toBeGreaterThan(0)
    expect(new Set(asked).size).toBe(asked.length)
    expect(asked).toContain(addr(1))
  })

  it('keeps every table usable and says nothing when the backend has no owners endpoint', async () => {
    ownersStatus = 404
    const wrapper = await openSweepTab()
    await fillAllFourTables(wrapper)
    const found = tables(wrapper)
    expect(found).toHaveLength(4)
    for (const table of found) {
      for (const owner of ownerTexts(table)) expect(owner).toBe('—')
    }
    expect(mocks.showError).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('page not found')
  })

  it('treats any other lookup failure the same way', async () => {
    ownersStatus = 500
    const wrapper = await openSweepTab()
    await fillAllFourTables(wrapper)
    expect(tables(wrapper)).toHaveLength(4)
    expect(mocks.showError).not.toHaveBeenCalled()
  })

  it('shows the blocked section now that the list is unwrapped from {"data": [...]}', async () => {
    blocked = [{ job_id: 9, chain: 'bsc', address: addr(21), status: 'sweep_pending', error: '', age_minutes: 12, can_reconcile: true }]
    const wrapper = await openSweepTab()
    expect(wrapper.find('.crypto-view__blocked').exists()).toBe(true)
    expect(wrapper.findAll('.crypto-view__blocked tbody tr')).toHaveLength(1)
  })
})

describe('sweep page: history paging', () => {
  const page = (high: number, count: number) => Array.from({ length: count }, (_, i) => record(high - i))
  const recordRequests = () => mocks.get.mock.calls.filter(([path]) => String(path).endsWith('/sweep/records'))

  it('asks for 100 rows without a cursor, then pages from the oldest id on "load more"', async () => {
    recordPages = params => (params.before_id ? page(200, 30) : page(300, 100))
    const wrapper = await openSweepTab()
    expect(recordRequests()).toHaveLength(1)
    expect(recordRequests()[0][1].params).toEqual(expect.objectContaining({ limit: 100 }))
    expect(recordRequests()[0][1].params.before_id).toBeUndefined()
    expect(wrapper.get('[data-testid="crypto-sweep-loaded"]').text()).toBe('admin.crypto.sweepView.loaded{"count":100}')

    await wrapper.get('[data-testid="crypto-sweep-load-more"]').trigger('click')
    await flushPromises()
    expect(recordRequests()).toHaveLength(2)
    expect(recordRequests()[1][1].params).toEqual(expect.objectContaining({ limit: 100, before_id: 201 }))
    expect(wrapper.get('[data-testid="crypto-sweep-loaded"]').text()).toBe('admin.crypto.sweepView.loaded{"count":130}')
    expect(wrapper.find('[data-testid="crypto-sweep-load-more"]').exists()).toBe(false) // a short page is the end
    expect(wrapper.findAll('#crypto-panel-sweep tbody tr')).toHaveLength(130)
  }, 30000)

  it('has no "load more" when the first page is short', async () => {
    recordPages = () => page(10, 10)
    const wrapper = await openSweepTab()
    expect(wrapper.find('[data-testid="crypto-sweep-load-more"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="crypto-sweep-loaded"]').text()).toBe('admin.crypto.sweepView.loaded{"count":10}')
  })

  it('reports a failed "load more" without losing the rows', async () => {
    recordPages = params => {
      if (params.before_id) throw { status: 500, message: 'boom' }
      return page(300, 100)
    }
    const wrapper = await openSweepTab()
    await wrapper.get('[data-testid="crypto-sweep-load-more"]').trigger('click')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith('boom')
    expect(wrapper.get('[data-testid="crypto-sweep-loaded"]').text()).toBe('admin.crypto.sweepView.loaded{"count":100}')
    expect(wrapper.find('[data-testid="crypto-sweep-load-more"]').exists()).toBe(true) // can retry
  }, 30000)

  it('reloads from the first page when the panel asks to refresh', async () => {
    recordPages = () => page(10, 10)
    const wrapper = await openSweepTab()
    const before = recordRequests().length
    await wrapper.get('[data-testid="crypto-sweep-refresh"]').trigger('click')
    await flushPromises()
    expect(recordRequests().length).toBe(before + 1)
    expect(recordRequests().at(-1)![1].params.before_id).toBeUndefined()
  })

  it('shows the plain "no data" text when there is no history and no plan', async () => {
    const wrapper = await openSweepTab()
    expect(wrapper.get('[data-testid="crypto-sweep-empty"]').text()).toBe('common.noData')
  })

  it('hides the history while a plan is on screen and there is no history (as before)', async () => {
    plan = { Entries: [{ Chain: 'bsc', Address: addr(41), Token: 'USDT', AmountDecimal: 7 }], TotalUSD: 7, DryRun: true }
    const wrapper = await openSweepTab()
    await button(wrapper, 'admin.crypto.loadPlan').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="crypto-sweep-records"]').exists()).toBe(false)
  })
})
