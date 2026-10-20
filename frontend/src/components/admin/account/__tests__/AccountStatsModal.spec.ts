import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import AccountStatsModal from '../AccountStatsModal.vue'

const { getStats } = vi.hoisted(() => ({ getStats: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getStats } } }))
vi.mock('vue-i18n', async (importOriginal) => ({ ...await importOriginal<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-chartjs', () => ({ Line: { template: '<div />' } }))
enableAutoUnmount(afterEach)
const account = { id: 101, name: 'fixture', status: 'active' } as any
const stats = {
  history: [], models: [], endpoints: [], upstream_endpoints: [],
  summary: { days: 30, actual_days_used: 1, total_cost: 1, total_user_cost: 2,
    total_standard_cost: 3, total_requests: 17, total_tokens: 42,
    avg_daily_cost: 1, avg_daily_user_cost: 2, avg_daily_requests: 17,
    avg_daily_tokens: 42, avg_duration_ms: 50, today: null,
    highest_cost_day: null, highest_request_day: null }
}
const global = { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
  ModelDistributionChart: true, EndpointDistributionChart: true } }
beforeEach(() => { getStats.mockReset().mockResolvedValue(stats) })
describe('AccountStatsModal conditional mounting', () => {
  it('loads usage on its first mount already visible', async () => {
    const wrapper = mount(AccountStatsModal, { props: { show: true, account }, global })
    await flushPromises()
    expect(getStats).toHaveBeenCalledTimes(1)
    expect(getStats).toHaveBeenCalledWith(101, 30)
    expect(wrapper.text()).toContain('17')
    expect(wrapper.text()).not.toContain('admin.accounts.stats.noData')
  })
  it('ignores an older account response after switching accounts', async () => {
    let resolveOld!: (value: typeof stats) => void
    getStats.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    const wrapper = mount(AccountStatsModal, { props: { show: true, account }, global })
    await wrapper.setProps({ account: { ...account, id: 102 } }); await flushPromises()
    resolveOld({ ...stats, summary: { ...stats.summary, total_requests: 999 } }); await flushPromises()
    expect(wrapper.text()).toContain('17')
    expect(wrapper.text()).not.toContain('999')
  })
  it('waits while hidden, reloads another account, and loads when reopened', async () => {
    const wrapper = mount(AccountStatsModal, { props: { show: false, account }, global })
    await flushPromises()
    expect(getStats).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true }); await flushPromises()
    expect(getStats).toHaveBeenLastCalledWith(101, 30)
    await wrapper.setProps({ account: { ...account, id: 102 } }); await flushPromises()
    expect(getStats).toHaveBeenLastCalledWith(102, 30)
    await wrapper.setProps({ show: false }); await wrapper.setProps({ show: true }); await flushPromises()
    expect(getStats).toHaveBeenCalledTimes(3)
  })
})
