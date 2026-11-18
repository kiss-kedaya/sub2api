import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import OpsCenterView from '../OpsCenterView.vue'
import * as accountOps from '@/api/admin/accountOps'
import { getStats } from '@/api/admin/dashboard'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/api/admin/accountOps', () => ({ getAccountOpsSettings: vi.fn(), getAccountOpsThresholdAccounts: vi.fn(), getAccountOpsEvents: vi.fn() }))
vi.mock('@/api/admin/dashboard', async importOriginal => ({ ...await importOriginal<typeof import('@/api/admin/dashboard')>(), getStats: vi.fn() }))

beforeEach(() => {
  vi.resetAllMocks()
  localStorage.clear()
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { id: 1, role: 'admin' } as NonNullable<typeof auth.user>
  vi.mocked(getStats).mockResolvedValue({ today_actual_cost: 1.25, today_requests: 12, total_accounts: 3, normal_accounts: 2 } as Awaited<ReturnType<typeof getStats>>)
  vi.mocked(accountOps.getAccountOpsThresholdAccounts).mockResolvedValue([
    { account_id: 1, account_name: 'USD asset', type: 'apikey', platform: 'openai', unit: 'USD', balance: 12, balance_status: 'ok', received_at: null, usage_windows: [] },
    { account_id: 2, account_name: 'CNY asset', type: 'apikey', platform: 'openai', unit: 'CNY', balance: 80, balance_status: 'ok', received_at: null, usage_windows: [] },
    { account_id: 3, account_name: 'Needs inspection', type: 'oauth', platform: 'anthropic', unit: 'USD', balance: null, balance_status: 'failed', received_at: null, usage_windows: [] },
  ])
  vi.mocked(accountOps.getAccountOpsEvents).mockResolvedValue({ items: [], has_more: false })
  vi.mocked(accountOps.getAccountOpsSettings).mockResolvedValue({ config: { enabled: false }, smtp_configured: false, dropped_signals: 0, storage_failures: 0 } as Awaited<ReturnType<typeof accountOps.getAccountOpsSettings>>)
})
afterEach(() => vi.restoreAllMocks())
async function render() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push('/admin/ops-center')
  const wrapper = mount(OpsCenterView, { global: { plugins: [router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] } })
  await flushPromises()
  return wrapper
}
describe('operations overview', () => {
  it('keeps currency totals and dashboard spend separate; only shows summary, attention and the four other workspaces', async () => {
    const wrapper = await render()
    expect(wrapper.text()).toContain('$12.00')
    expect(wrapper.text()).toContain('¥80.00')
    expect(wrapper.text()).toContain('$1.25')
    expect(wrapper.findAll('[data-testid^="ops-workspace-"]')).toHaveLength(4)
    expect(wrapper.find('[data-testid="ops-workspace-overview"]').exists()).toBe(false)
    expect(wrapper.find('table').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('L0')
    expect(wrapper.text()).toContain('Needs inspection')
    expect(wrapper.find('a[href="/admin/account-ops/rules"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('does not expose disabled destinations through overview cards', async () => {
    useAdminSettingsStore().opsMonitoringEnabled = false
    useAppStore().cachedPublicSettings = { channel_monitor_enabled: false } as NonNullable<ReturnType<typeof useAppStore>['cachedPublicSettings']>
    const wrapper = await render()
    expect(wrapper.find('a[href="/admin/ops"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/admin/channels/monitor"]').exists()).toBe(false)
    expect(wrapper.find('a[href="/admin/request-captures"]').exists()).toBe(true)
    wrapper.unmount()
  })
  it('does not request primary-admin assets or show workspace links for an observer', async () => {
    const auth = useAuthStore()
    auth.user = { id: 2, role: 'admin' } as NonNullable<typeof auth.user>
    const wrapper = await render()
    expect(accountOps.getAccountOpsThresholdAccounts).not.toHaveBeenCalled()
    expect(accountOps.getAccountOpsSettings).not.toHaveBeenCalled()
    expect(wrapper.findAll('[data-testid^="ops-workspace-"]')).toHaveLength(0)
    wrapper.unmount()
  })
})
