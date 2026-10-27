import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpsAutomationView from '../OpsAutomationView.vue'
import * as api from '@/api/admin/opsAutomation'
import * as groupsApi from '@/api/admin/groups'
import * as accountsApi from '@/api/admin/accounts'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/common/BaseDialog.vue', () => ({ default: { props: ['show'], template: '<div v-if="show"><slot /></div>' } }))
vi.mock('@/components/common/GroupSelector.vue', () => ({ default: { props: ['modelValue', 'groups'], template: '<div />' } }))
vi.mock('@/components/common/Select.vue', () => ({ default: { props: ['modelValue', 'options'], template: '<div />' } }))
vi.mock('@/api/admin/groups', () => ({ getAll: vi.fn().mockResolvedValue([]) }))
vi.mock('@/api/admin/accounts', () => ({ list: vi.fn().mockResolvedValue({ items: [], total: 0 }) }))
vi.mock('@/api/admin/opsAutomation', () => ({ getAutomation: vi.fn(), saveAutomation: vi.fn(), getReasoningPlans: vi.fn(), saveReasoningPlan: vi.fn(), runReasoningPlan: vi.fn(), getReasoningOwnership: vi.fn(), updateReasoningOwnership: vi.fn() }))
const config = (): api.OpsAutomationConfig => ({ revision: '', updated_at: '', oauth_fallback: { enabled: false, group_ids: [], minimum_quality: 1, minimum_quota_percent: 10, maximum_load_percent: 90 }, admission: { enabled: false, platforms: [], model: '' }, oauth: [{ enabled: false, platform: 'openai', concurrency: 3, priority: 50, load_factor: 1, group_ids: [] }], ramp: { enabled: false, group_ids: [], successes_per_step: 20, step: 1, maximum: 100, cooldown_seconds: 60 }, ranking: { enabled: false, group_ids: [], mode: 'balanced', window_minutes: 60, minimum_samples: 10, quality_weight: 30, latency_weight: 45, cost_weight: 25 } })
beforeEach(() => { vi.resetAllMocks(); vi.mocked(api.getAutomation).mockResolvedValue(config()); vi.mocked(api.getReasoningPlans).mockResolvedValue([]); vi.mocked(groupsApi.getAll).mockResolvedValue([]); vi.mocked(accountsApi.list).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 30, pages: 0 }) })
describe('account automation', () => {
  it('keeps all policies disabled initially and requires an explicit save', async () => {
    const w = mount(OpsAutomationView); await flushPromises()
    for (const box of w.findAll('input[type="checkbox"]')) expect((box.element as HTMLInputElement).checked).toBe(false)
    expect(api.saveAutomation).not.toHaveBeenCalled()
    expect(w.text()).toContain('opsAutomation.oauthHint'); expect(w.text()).toContain('opsAutomation.rankingHint')
    w.unmount()
  })
  it('retains the loaded configuration after a failed refresh', async () => {
    const w = mount(OpsAutomationView); await flushPromises()
    vi.mocked(api.getAutomation).mockRejectedValueOnce(new Error('temporary'))
    await w.findAll('button').find(b => b.text() === 'opsAutomation.refresh')!.trigger('click'); await flushPromises()
    expect(w.text()).toContain('opsAutomation.oauth'); expect(w.find('form').exists()).toBe(true)
    w.unmount()
  })
  it('offers all three explicit judge methods while a new plan stays disabled', async () => {
    const w = mount(OpsAutomationView); await flushPromises()
    await w.findAll('button').find(b => b.text() === 'opsAutomation.newPlan')!.trigger('click')
    const judge = w.findAll('select').find(s => s.find('option[value="jev"]').exists())!
    expect(judge.findAll('option').map(o => o.attributes('value'))).toEqual(['local', 'llm', 'jev'])
    expect(api.saveReasoningPlan).not.toHaveBeenCalled(); w.unmount()
  })
})
