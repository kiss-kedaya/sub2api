import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import UpstreamMonitorView from '../UpstreamMonitorView.vue'
import * as api from '@/api/admin/upstreamMonitor'
import * as accountsApi from '@/api/admin/accounts'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/layout/PageToolbar.vue', () => ({ default: { template: '<div><slot name="left" /><slot name="right" /></div>' } }))
vi.mock('@/components/admin/page/AdminPageHeader.vue', () => ({ default: { template: '<header><slot name="actions" /></header>' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({
  default: { props: ['show', 'title'], template: '<div v-if="show" data-test="dialog"><slot /><slot name="footer" /></div>' },
}))
vi.mock('@/api/admin/accounts', () => ({ probeUpstreamBilling: vi.fn() }))
vi.mock('@/api/admin/upstreamMonitor', () => ({
  getOverview: vi.fn(),
  listAccounts: vi.fn(),
  getHistory: vi.fn(),
  listRateChanges: vi.fn(),
  sampleNow: vi.fn(),
}))

const router = createRouter({
  history: createMemoryHistory(),
  routes: [{ path: '/', component: { template: '<div />' } }],
})

const account = (overrides: Partial<api.UpstreamMonitorAccount> = {}): api.UpstreamMonitorAccount => ({
  account_id: 7,
  name: 'main-upstream',
  platform: 'anthropic',
  type: 'apikey',
  status: 'active',
  balance: 13.5,
  unlimited: false,
  unit: 'USD',
  balance_status: 'ok',
  balance_read_at: '2026-11-05T04:00:00Z',
  balance_fresh_until: '2026-11-05T05:00:00Z',
  rate: 0.8,
  rate_status: 'ok',
  rate_sync_enabled: false,
  rate_read_at: '2026-11-05T04:00:00Z',
  last_sampled_at: '2026-11-05T04:05:00Z',
  ...overrides,
})

const overview = (): api.UpstreamMonitorOverview => ({
  generated_at: '2026-11-05T05:00:00Z',
  total_accounts: 2,
  with_balance: 2,
  healthy: 1,
  stale: 1,
  failed: 0,
  unsupported: 0,
  rate_known: 2,
  rate_changes_24h: 1,
  currencies: [{ unit: 'USD', total: 13.5, accounts: 2 }],
  attention: [account({ account_id: 8, name: 'backup-upstream', balance_status: 'stale', balance: 4 })],
  last_sample_at: '2026-11-05T04:05:00Z',
})

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(api.getOverview).mockResolvedValue(overview())
  vi.mocked(api.listAccounts).mockResolvedValue({
    items: [account(), account({ account_id: 8, name: 'backup-upstream', balance_status: 'stale', balance: 4 })],
    total: 2,
    page: 1,
    page_size: 20,
    pages: 1,
  })
  vi.mocked(api.listRateChanges).mockResolvedValue({
    items: [{ id: 1, account_id: 7, name: 'main-upstream', old_rate: 1, new_rate: 0.8, changed_at: '2026-11-05T03:00:00Z' }],
    total: 1,
    page: 1,
    page_size: 10,
    pages: 1,
  })
  vi.mocked(api.getHistory).mockResolvedValue({
    account_id: 7,
    days: 7,
    points: [
      { balance: 10, unit: 'USD', sampled_at: '2026-11-04T00:00:00Z' },
      { balance: 13.5, unit: 'USD', sampled_at: '2026-11-05T00:00:00Z' },
    ],
  })
})

describe('upstream monitor view', () => {
  it('renders the stored balance summary and account rows', async () => {
    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('$13.50')
    expect(wrapper.text()).toContain('main-upstream')
    expect(wrapper.text()).toContain('backup-upstream')
    expect(wrapper.text()).toContain('数据过期')
    expect(api.getOverview).toHaveBeenCalled()
    expect(api.listAccounts).toHaveBeenCalledWith(expect.objectContaining({ page: 1, pageSize: 20, sort: 'name' }))
    wrapper.unmount()
  })

  it('opens the detail dialog and loads the sampled history', async () => {
    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()

    const detailButtons = wrapper.findAll('button[aria-label="详情"]')
    expect(detailButtons.length).toBe(2)
    await detailButtons[0].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="dialog"]').exists()).toBe(true)
    expect(wrapper.find('svg').exists()).toBe(true)
    expect(api.getHistory).toHaveBeenCalledWith(7, 7)
    wrapper.unmount()
  })

  it('probes one account and samples the snapshot again', async () => {
    vi.mocked(accountsApi.probeUpstreamBilling).mockResolvedValue({ account_id: 7, snapshot: null } as never)
    vi.mocked(api.sampleNow).mockResolvedValue({ scanned: 2, sampled: 1, rate_changes: 0 })

    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()
    await wrapper.findAll('button[aria-label="立即读取"]')[0].trigger('click')
    await flushPromises()

    expect(accountsApi.probeUpstreamBilling).toHaveBeenCalledWith(7)
    expect(api.sampleNow).toHaveBeenCalled()
    wrapper.unmount()
  })
})


