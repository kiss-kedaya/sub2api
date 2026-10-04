import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'

enableAutoUnmount(afterEach)

const { listByAccount, listResults, update } = vi.hoisted(() => ({
  listByAccount: vi.fn(),
  listResults: vi.fn(),
  update: vi.fn(),
}))

vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: { listByAccount, listResults, update } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('ScheduledTestsPanel quality results', () => {
  it('shows active audit progress instead of selecting old Pelican artwork after switching', async () => {
    listByAccount.mockResolvedValue([{ id: 1, account_id: 2, model_id: 'gpt-6-astra', cron_expression: '*/5 * * * *',
      enabled: true, max_results: 20, auto_recover: false, quality_check_enabled: true, quality_provider: 'chanshui',
      active_audit: { id: 'fixture-audit', status: 'running' } }])
    listResults.mockResolvedValue([{ id: 3, plan_id: 1, status: 'success', response_text: '<html>old pelican</html>', latency_ms: 2 }])
    const wrapper = mount(ScheduledTestsPanel, { props: { show: false, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } } })
    await wrapper.setProps({ show: true }); await flushPromises()
    await wrapper.findAll('div').find(el => el.classes().includes('cursor-pointer') && el.text().includes('gpt-6-astra'))!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="audit-progress"]').exists()).toBe(true)
    expect(wrapper.find('iframe').exists()).toBe(false)
    await wrapper.get('[data-testid="show-all-results"]').setValue(true)
    expect(wrapper.find('iframe').exists()).toBe(true)
  })
  it('loads and saves the provider and section selection when editing a plan', async () => {
    const plan = { id: 1, account_id: 2, model_id: 'gpt-test', cron_expression: '*/5 * * * *',
      enabled: true, max_results: 20, auto_recover: false, quality_check_enabled: true,
      quality_provider: 'chanshui', quality_config: { base_url: 'https://audit.example', protocol: 'auto', timeout: 120, sections: ['fingerprint', 'tools'] } }
    listByAccount.mockResolvedValue([plan])
    update.mockResolvedValue(plan)
    const wrapper = mount(ScheduledTestsPanel, {
      props: { show: false, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } },
    })
    await wrapper.setProps({ show: true }); await flushPromises()
    await wrapper.get('button[title="admin.scheduledTests.editPlan"]').trigger('click')
    expect((wrapper.get('[data-testid="chanshui-service-url"]').element as HTMLInputElement).value).toBe('https://audit.example')
    expect((wrapper.get('input[data-section="tools"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.findAll('button').find(button => button.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(1, expect.objectContaining({ quality_provider: 'chanshui', quality_config: expect.objectContaining(plan.quality_config) }))
    expect(JSON.stringify(update.mock.calls.at(-1))).not.toContain('api_key')
  })
  it('keeps inconclusive responses distinct from success and failure', async () => {
    listByAccount.mockResolvedValue([{
      id: 1, account_id: 2, model_id: 'quality-test-model',
      cron_expression: '*/5 * * * *', enabled: true, max_results: 20,
      auto_recover: false, quality_check_enabled: false, next_run_at: '2026-09-26T10:00:00Z'
    }])
    listResults.mockResolvedValue([{
      id: 3, plan_id: 1, status: 'unknown', latency_ms: 1000,
      response_text: '<html><body><svg></svg></body></html>',
      error_message: 'quality check inconclusive: rendered evaluation required',
      created_at: '2026-09-26T10:00:00Z'
    }])
    const wrapper = mount(ScheduledTestsPanel, {
      props: { show: false, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const header = wrapper.findAll('div').find(el => el.classes().includes('cursor-pointer') && el.text().includes('quality-test-model'))
    expect(header).toBeDefined()
    await header!.trigger('click')
    await flushPromises()
    expect(listResults).toHaveBeenCalledWith(1, 20)
    expect(wrapper.text()).toContain('admin.scheduledTests.unknown')
    expect(wrapper.text()).not.toContain('admin.scheduledTests.success')
    expect(wrapper.text()).not.toContain('admin.scheduledTests.failed')
    expect(wrapper.text()).not.toContain('admin.scheduledTests.errorMessage')
    expect(wrapper.text()).toContain('rendered evaluation required')
    expect(wrapper.text()).toContain('#3')
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
  })

  it('marks only degradation-check plans with the quality badge', async () => {
    listByAccount.mockResolvedValue([
      {
        id: 1, account_id: 2, model_id: 'plain-model',
        cron_expression: '*/5 * * * *', enabled: true, max_results: 20,
        auto_recover: false, quality_check_enabled: false, next_run_at: null
      },
      {
        id: 2, account_id: 2, model_id: 'quality-model',
        cron_expression: '*/5 * * * *', enabled: true, max_results: 20,
        auto_recover: false, quality_check_enabled: true, next_run_at: null
      }
    ])
    listResults.mockResolvedValue([])
    const wrapper = mount(ScheduledTestsPanel, {
      props: { show: false, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } }
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    const badges = wrapper.findAll('span').filter(el => el.text() === 'admin.scheduledTests.qualityCheck')
    expect(badges).toHaveLength(1)
  })
})
