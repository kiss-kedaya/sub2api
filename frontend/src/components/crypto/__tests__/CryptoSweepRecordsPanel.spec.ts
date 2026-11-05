import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import USelect from '@nuxt/ui/components/Select.vue'
import CryptoSweepRecordsPanel from '../CryptoSweepRecordsPanel.vue'
import { clearAddressOwnerCache } from '@/composables/useAddressOwners'
import type { SweepRecord } from '@/api/admin/crypto'

const mocks = vi.hoisted(() => ({ getAddressOwners: vi.fn(), copy: vi.fn() }))
vi.mock('@/api/admin/crypto', () => ({ getAddressOwners: mocks.getAddressOwners }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    locale: ref('en-US'),
    t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '')
  })
}))
enableAutoUnmount(afterEach)

// Fixture values only.
const addr = (n: number) => '0x' + n.toString(16).padStart(40, '0')
const hash = (n: number) => '0x' + n.toString(16).padStart(64, '0')
const recent = new Date(Date.now() - 3 * 86400000).toISOString()
const longAgo = new Date(Date.now() - 800 * 86400000).toISOString()

function record(id: number, over: Partial<SweepRecord> = {}): SweepRecord {
  return {
    id, chain: 'bsc', from_address: addr(id), to_address: addr(9999), token: '0xtoken', token_symbol: 'USDT', amount: '123.45',
    gas_tx_hash: '', sweep_tx_hash: hash(id), status: 'swept', error: '', created_at: recent, updated_at: recent, ...over
  }
}

const owners: Record<string, unknown> = {}
beforeEach(() => {
  clearAddressOwnerCache()
  mocks.copy.mockReset().mockResolvedValue(true)
  for (const key of Object.keys(owners)) delete owners[key]
  mocks.getAddressOwners.mockReset().mockImplementation(async (batch: string[]) =>
    Object.fromEntries(batch.filter(key => key in owners).map(key => [key, owners[key]])))
})

// ULink/UButton read the router; an empty in-memory one keeps them quiet.
const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div />' } }] })

async function render(props: Record<string, unknown> = {}) {
  const wrapper = mount(CryptoSweepRecordsPanel, {
    props: { records: [record(3), record(2), record(1)], ...props },
    global: { plugins: [router] }
  })
  await flushPromises()
  return wrapper
}
const byId = (wrapper: Awaited<ReturnType<typeof render>>, id: string) => wrapper.findAll(`[data-testid="${id}"]`)
const rows = (wrapper: Awaited<ReturnType<typeof render>>) => wrapper.findAll('tbody tr')

describe('records table', () => {
  it('has the seven columns in order, with the user column right after the time and chain', async () => {
    const wrapper = await render()
    const headers = wrapper.findAll('thead th').map(th => th.text())
    expect(headers).toEqual([
      'admin.crypto.colTime', 'admin.crypto.chain', 'admin.crypto.sweepView.colUser',
      'admin.crypto.sweepView.colDepositAddress', 'admin.crypto.amount', 'common.status', 'TX'
    ])
    expect(rows(wrapper)).toHaveLength(3)
  })

  it('shows who owns each deposit address, from the shared lookup', async () => {
    owners[addr(3)] = { user_id: 30, username: 'fixture-name', email: 'fixture-30@example.test', deleted: false }
    owners[addr(2)] = { user_id: 20, username: '', email: 'only-email@example.test', deleted: false }
    owners[addr(1)] = { user_id: 10, username: 'gone', email: 'gone@example.test', deleted: true }
    const wrapper = await render()
    const primaries = byId(wrapper, 'crypto-owner-primary').map(node => node.text())
    expect(primaries).toEqual(['fixture-name', 'only-email@example.test', 'gone'])
    expect(byId(wrapper, 'crypto-owner-deleted')).toHaveLength(1)
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
  })

  it('shows a dash for an address nobody owns and when the lookup fails', async () => {
    mocks.getAddressOwners.mockRejectedValue({ status: 404 })
    const wrapper = await render()
    expect(byId(wrapper, 'crypto-owner-unknown')).toHaveLength(3)
    expect(wrapper.text()).not.toContain('example.test')
    expect(rows(wrapper)).toHaveLength(3) // the table is never held back by it
  })

  it('shows the amount with its symbol, or the bare amount when the symbol is empty', async () => {
    const wrapper = await render({ records: [record(2), record(1, { token_symbol: '' }), record(3, { token_symbol: undefined })] })
    expect(byId(wrapper, 'crypto-sweep-amount').map(node => node.text())).toEqual(['123.45 USDT', '123.45', '123.45'])
  })

  it('shortens the deposit address, keeps the full one on hover, and copies it', async () => {
    const wrapper = await render({ records: [record(255)] })
    const shown = byId(wrapper, 'crypto-sweep-address')[0]
    expect(shown.text()).toBe('0x000000…0000ff')
    expect(shown.attributes('title')).toBe(addr(255))
    await byId(wrapper, 'crypto-sweep-copy')[0].trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(addr(255))
  })

  it('links the deposit address and the sweep hash to the right explorer, safely', async () => {
    const wrapper = await render({ records: [record(255, { gas_tx_hash: hash(77) })] })
    const address = byId(wrapper, 'crypto-sweep-address-link')[0]
    expect(address.attributes('href')).toBe('https://bscscan.com/address/' + addr(255))
    expect(address.attributes('target')).toBe('_blank')
    expect(address.attributes('rel')).toContain('noopener')
    const tx = byId(wrapper, 'crypto-sweep-tx')[0]
    expect(tx.attributes('href')).toBe('https://bscscan.com/tx/' + hash(255))
    expect(tx.attributes('target')).toBe('_blank')
    expect(tx.attributes('rel')).toContain('noopener')
    const gas = byId(wrapper, 'crypto-sweep-gas-link')[0]
    expect(gas.attributes('href')).toBe('https://bscscan.com/tx/' + hash(77))
    expect(gas.text()).toBe('admin.crypto.sweepView.gasTx')
  })

  it('does not link a hash or address it cannot vouch for, nor a chain it does not know', async () => {
    const wrapper = await render({
      records: [record(1, { sweep_tx_hash: '0xshort', gas_tx_hash: '0xshort', from_address: 'not-an-address' }), record(2, { chain: 'mystery' })]
    })
    expect(byId(wrapper, 'crypto-sweep-address-link')).toHaveLength(0)
    expect(byId(wrapper, 'crypto-sweep-gas-link')).toHaveLength(0)
    expect(wrapper.findAll('a')).toHaveLength(0)
    expect(byId(wrapper, 'crypto-sweep-tx')[0].text()).toBe('0xshort')
  })

  it('shows only a small gas link when there is a gas hash but no sweep hash yet', async () => {
    const wrapper = await render({ records: [record(1, { sweep_tx_hash: '', gas_tx_hash: hash(5), status: 'gas_pending' })] })
    expect(byId(wrapper, 'crypto-sweep-tx')[0].text()).toBe('—')
    expect(byId(wrapper, 'crypto-sweep-gas-link')).toHaveLength(1)
  })

  it('does not show a gas link when there is no gas transaction', async () => {
    const wrapper = await render({ records: [record(1)] })
    expect(byId(wrapper, 'crypto-sweep-gas-link')).toHaveLength(0)
  })

  it('uses updated_at for the time, falls back to created_at, and keeps the creation time and id on hover', async () => {
    const created = new Date(Date.now() - 10 * 86400000).toISOString()
    const wrapper = await render({
      records: [record(7, { created_at: created, updated_at: recent }), record(6, { created_at: created, updated_at: undefined })]
    })
    const times = byId(wrapper, 'crypto-sweep-time')
    expect(times[0].text()).not.toBe('—')
    expect(times[0].text()).not.toBe(times[1].text())
    expect(times[0].attributes('title')).toContain('"id":7')
    expect(times[0].attributes('title')).toContain(new Date(created).toLocaleString())
  })

  it('omits the year inside a year and shows it beyond', async () => {
    const wrapper = await render({ records: [record(2, { updated_at: recent }), record(1, { updated_at: longAgo })] })
    const [near, far] = byId(wrapper, 'crypto-sweep-time').map(node => node.text())
    expect(near).not.toMatch(/\d{4}/)
    expect(far).toMatch(new RegExp(String(new Date(longAgo).getFullYear())))
  })
})

describe('status cell', () => {
  it('labels each status with the same wording as the other tables', async () => {
    const statuses = ['swept', 'failed', 'planned', 'gas_pending', 'gas_confirmed', 'sweep_pending', 'token_pending', 'manual_review']
    const wrapper = await render({ records: statuses.map((status, index) => record(index + 1, { status })) })
    expect(byId(wrapper, 'crypto-sweep-status').map(node => node.text())).toEqual([
      'admin.crypto.statusSwept', 'admin.crypto.failed', 'admin.crypto.planned', 'admin.crypto.statusGasPending',
      'admin.crypto.statusGasConfirmed', 'admin.crypto.statusTokenPending', 'admin.crypto.statusTokenPending', 'admin.crypto.statusManualReview'
    ])
  })

  it('prints an unknown status as it is', async () => {
    const wrapper = await render({ records: [record(1, { status: 'brand_new' })] })
    expect(byId(wrapper, 'crypto-sweep-status')[0].text()).toBe('brand_new')
  })

  it('shows the error under a failed badge, masked, clamped to two lines, full text on hover', async () => {
    const message = 'rpc https://node.example.test/fixture-path?key=fixture-secret refused'
    const wrapper = await render({ records: [record(1, { status: 'failed', error: message }), record(2)] })
    const [error] = byId(wrapper, 'crypto-sweep-error')
    expect(byId(wrapper, 'crypto-sweep-error')).toHaveLength(1) // the clean row has none
    expect(error.text()).not.toContain('https://')
    expect(error.text()).not.toContain('fixture-secret')
    expect(error.attributes('title')).toBe(error.text())
    expect(error.classes()).toContain('line-clamp-2')
  })

  it('shows an error line whatever the status, because a pending job can carry one too', async () => {
    const wrapper = await render({ records: [record(1, { status: 'sweep_pending', error: 'awaiting canonical receipt and required confirmations' })] })
    expect(byId(wrapper, 'crypto-sweep-error')[0].text()).toBe('awaiting canonical receipt and required confirmations')
  })
})

describe('filter bar', () => {
  const mixed = () => [
    record(8, { status: 'swept', chain: 'bsc' }),
    record(7, { status: 'sweep_pending', chain: 'ethereum' }),
    record(6, { status: 'gas_pending', chain: 'bsc' }),
    record(5, { status: 'failed', chain: 'bsc', error: 'RPC request failed' }),
    record(4, { status: 'planned', chain: 'base' }),
    record(3, { status: 'manual_review', chain: 'bsc' })
  ]
  const select = (wrapper: Awaited<ReturnType<typeof render>>, index: number) => wrapper.findAllComponents(USelect)[index]
  const ids = (wrapper: Awaited<ReturnType<typeof render>>) => byId(wrapper, 'crypto-sweep-address').map(node => Number.parseInt(node.attributes('title')!.slice(2), 16))

  it.each([
    ['swept', [8]],
    ['running', [7, 6]],
    ['failed', [5]],
    ['pending', [4, 3]],
    ['__all__', [8, 7, 6, 5, 4, 3]]
  ])('status filter %s keeps the matching rows', async (value, expected) => {
    const wrapper = await render({ records: mixed() })
    select(wrapper, 0).vm.$emit('update:modelValue', value)
    await flushPromises()
    expect(ids(wrapper)).toEqual(expected)
  })

  it('offers the four status groups plus "all"', async () => {
    const wrapper = await render({ records: mixed() })
    const items = select(wrapper, 0).props('items') as Array<{ label: string; value: string }>
    expect(items.map(item => item.value)).toEqual(['__all__', 'swept', 'running', 'failed', 'pending'])
    expect(items.map(item => item.label)).toEqual([
      'admin.crypto.allStatuses', 'admin.crypto.statusSwept', 'admin.crypto.sweepView.statusRunning', 'admin.crypto.failed', 'admin.crypto.sweepView.statusQueued'
    ])
  })

  it('chain filter lists the chains actually present and narrows the table', async () => {
    const wrapper = await render({ records: mixed() })
    const items = select(wrapper, 1).props('items') as Array<{ label: string; value: string }>
    expect(items.map(item => item.value)).toEqual(['__all__', 'base', 'bsc', 'ethereum'])
    select(wrapper, 1).vm.$emit('update:modelValue', 'ethereum')
    await flushPromises()
    expect(ids(wrapper)).toEqual([7])
  })

  it('status and chain filters combine', async () => {
    const wrapper = await render({ records: mixed() })
    select(wrapper, 0).vm.$emit('update:modelValue', 'running')
    select(wrapper, 1).vm.$emit('update:modelValue', 'bsc')
    await flushPromises()
    expect(ids(wrapper)).toEqual([6])
  })

  it('searches user name, email, address, either hash and the record id', async () => {
    owners[addr(8)] = { user_id: 80, username: 'fixture-name', email: 'fixture-80@example.test', deleted: false }
    owners[addr(7)] = { user_id: 70, username: '', email: 'second@example.test', deleted: false }
    const records = mixed().map(r => (r.id === 6 ? { ...r, gas_tx_hash: hash(0xabcdef) } : r))
    const wrapper = await render({ records })
    const search = wrapper.get('[data-testid="crypto-sweep-search"]')
    const type = async (value: string) => { await search.setValue(value); await flushPromises() }

    await type('FIXTURE-NAME')
    expect(ids(wrapper)).toEqual([8])
    await type('second@example')
    expect(ids(wrapper)).toEqual([7])
    await type(addr(5).slice(0, 42))
    expect(ids(wrapper)).toEqual([5])
    await type(hash(4).slice(-10))
    expect(ids(wrapper)).toEqual([4])
    await type('abcdef') // the gas hash
    expect(ids(wrapper)).toEqual([6])
    await type('#3')
    expect(ids(wrapper)).toEqual([3])
    await type('')
    expect(ids(wrapper)).toHaveLength(6)
  })

  it('shows a different message when a filter hides everything than when there is nothing at all', async () => {
    const wrapper = await render({ records: mixed() })
    await wrapper.get('[data-testid="crypto-sweep-search"]').setValue('zzz-no-such-thing')
    await flushPromises()
    expect(byId(wrapper, 'crypto-sweep-no-match')[0].text()).toBe('admin.crypto.sweepView.noMatch')
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.find('[data-testid="crypto-sweep-filters"]').exists()).toBe(true) // can still be cleared
    expect(byId(wrapper, 'crypto-sweep-empty')).toHaveLength(0)

    const empty = await render({ records: [] })
    expect(byId(empty, 'crypto-sweep-empty')[0].text()).toBe('common.noData')
    expect(byId(empty, 'crypto-sweep-no-match')).toHaveLength(0)
    expect(empty.find('[data-testid="crypto-sweep-filters"]').exists()).toBe(false)
  })

  it('suggests loading more when a filter finds nothing but older rows exist', async () => {
    const wrapper = await render({ records: mixed(), hasMore: true })
    await wrapper.get('[data-testid="crypto-sweep-search"]').setValue('zzz-no-such-thing')
    await flushPromises()
    expect(byId(wrapper, 'crypto-sweep-no-match')[0].text()).toBe('admin.crypto.sweepView.noMatchMore')
    expect(byId(wrapper, 'crypto-sweep-load-more')).toHaveLength(1)
  })

  it('says loading, not "no data", while the first page is on its way', async () => {
    const wrapper = await render({ records: [], loading: true })
    expect(byId(wrapper, 'crypto-sweep-empty')[0].text()).toBe('common.loading')
  })
})

describe('footer', () => {
  it('shows how many rows are loaded, regardless of the filter', async () => {
    const wrapper = await render({ records: [record(3), record(2), record(1)] })
    expect(byId(wrapper, 'crypto-sweep-loaded')[0].text()).toBe('admin.crypto.sweepView.loaded{"count":3}')
    await wrapper.get('[data-testid="crypto-sweep-search"]').setValue('#2')
    await flushPromises()
    expect(byId(wrapper, 'crypto-sweep-loaded')[0].text()).toBe('admin.crypto.sweepView.loaded{"count":3}')
  })

  it('shows "load more" only while there is more, and asks the page for it', async () => {
    const without = await render({ hasMore: false })
    expect(byId(without, 'crypto-sweep-load-more')).toHaveLength(0)

    const wrapper = await render({ hasMore: true })
    const button = byId(wrapper, 'crypto-sweep-load-more')[0]
    expect(button.text()).toBe('admin.crypto.sweepView.loadMore')
    await button.trigger('click')
    expect(wrapper.emitted('load-more')).toHaveLength(1)
  })

  it('does not fire a second request while one is on its way', async () => {
    const wrapper = await render({ hasMore: true, loadingMore: true })
    const button = byId(wrapper, 'crypto-sweep-load-more')[0]
    expect(button.attributes('disabled')).toBeDefined()
    await button.trigger('click')
    expect(wrapper.emitted('load-more')).toBeUndefined()
  })

  it('asks the page to refresh', async () => {
    const wrapper = await render()
    await byId(wrapper, 'crypto-sweep-refresh')[0].trigger('click')
    expect(wrapper.emitted('refresh')).toHaveLength(1)
  })

  it('does not load anything itself: no records request, only the owner lookup', async () => {
    await render({ hasMore: true })
    expect(mocks.getAddressOwners).toHaveBeenCalledTimes(1)
  })
})
