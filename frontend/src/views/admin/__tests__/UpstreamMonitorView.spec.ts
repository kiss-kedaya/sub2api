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
  getTrend: vi.fn(),
  listAlerts: vi.fn(),
  ackAlert: vi.fn(),
  listAlertRules: vi.fn(),
  saveAlertRule: vi.fn(),
  deleteAlertRule: vi.fn(),
  exportMonitorCsv: vi.fn(),
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
  alert_enabled: false,
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
  spend: [{ unit: 'USD', today: 1.25, last_7_days: 6.5, last_30_days: 20 }],
  open_alerts: 1,
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
    items: [{ id: 1, account_id: 7, name: 'main-upstream', old_rate: 1, new_rate: 0.8, change_pct: -20, changed_at: '2026-11-05T03:00:00Z' }],
    total: 1,
    page: 1,
    page_size: 10,
    pages: 1,
  })
  vi.mocked(api.getTrend).mockResolvedValue({
    days: 7,
    points: [
      { day: '2026-11-04', unit: 'USD', spend: 2, balance_close: 15, accounts: 2 },
      { day: '2026-11-05', unit: 'USD', spend: 1.5, balance_close: 13.5, accounts: 2 },
    ],
  })
  vi.mocked(api.listAlerts).mockResolvedValue({
    items: [
      {
        id: 11,
        account_id: 7,
        name: 'main-upstream',
        kind: 'balance_low',
        level: 'warning',
        balance: 4,
        threshold: 5,
        unit: 'USD',
        message: '余额 4 USD 低于阈值 5 USD',
        created_at: '2026-11-05T04:10:00Z',
        acked_at: null,
      },
    ],
    total: 1,
    page: 1,
    page_size: 10,
    pages: 1,
  })
  vi.mocked(api.listAlertRules).mockResolvedValue({ items: [] })
  vi.mocked(api.ackAlert).mockResolvedValue({ id: 11, acked: true })
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



describe('upstream monitor second batch', () => {
  it('renders spend, trend and alerts', async () => {
    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()

    expect(wrapper.text()).toContain('今日消费')
    expect(wrapper.text()).toContain('$1.25')
    expect(api.getTrend).toHaveBeenCalledWith(7)
    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.text()).toContain('余额 4 USD 低于阈值 5 USD')
    wrapper.unmount()
  })

  it('acknowledges an alert', async () => {
    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()

    const ack = wrapper.findAll('button').find(button => button.text().includes('标记已处理'))
    expect(ack).toBeTruthy()
    await ack!.trigger('click')
    await flushPromises()

    expect(api.ackAlert).toHaveBeenCalledWith(11)
    wrapper.unmount()
  })

  it('saves a balance alert rule from the detail dialog', async () => {
    vi.mocked(api.saveAlertRule).mockResolvedValue({
      account_id: 7,
      threshold: 2.7,
      unit: 'USD',
      rate_min_pct: 10,
      enabled: true,
      updated_at: '2026-11-05T05:10:00Z',
    })

    const wrapper = mount(UpstreamMonitorView, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.findAll('button[aria-label="告警设置"]')[0].trigger('click')
    await flushPromises()

    const save = wrapper.findAll('button').find(button => button.text().includes('保存规则'))
    expect(save).toBeTruthy()
    await save!.trigger('click')
    await flushPromises()

    expect(api.saveAlertRule).toHaveBeenCalledWith(expect.objectContaining({ account_id: 7, unit: 'USD', enabled: false }))
    wrapper.unmount()
  })
})
