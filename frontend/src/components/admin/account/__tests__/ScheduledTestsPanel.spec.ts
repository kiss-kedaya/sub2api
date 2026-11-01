import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
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
  it('loads and polls on each v-if mount without a watcher error or an orphan timer', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    const visible = ref(false)
    const accountId = ref(2)
    const onError = vi.fn()
    const plan = { id: 1, account_id: 2, model_id: 'first-mount-plan', enabled: true, max_results: 20 }
    listByAccount.mockReset().mockResolvedValue([plan])
    const parent = defineComponent({
      setup: () => () => visible.value
        ? h(ScheduledTestsPanel, { show: true, accountId: accountId.value, modelOptions: [] })
        : null,
    })
    const wrapper = mount(parent, { global: {
      stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true },
      config: { errorHandler: onError },
    } })
    try {
      await flushPromises()
      expect(listByAccount).not.toHaveBeenCalled()
      visible.value = true
      await flushPromises()
      expect(listByAccount).toHaveBeenCalledTimes(1)
      expect(listByAccount).toHaveBeenLastCalledWith(2)
      expect(wrapper.text()).toContain('first-mount-plan')
      await vi.advanceTimersByTimeAsync(15000)
      expect(listByAccount).toHaveBeenCalledTimes(2)

      visible.value = false
      await nextTick()
      expect(wrapper.findComponent(ScheduledTestsPanel).exists()).toBe(false)
      await vi.advanceTimersByTimeAsync(30000)
      expect(listByAccount).toHaveBeenCalledTimes(2)

      accountId.value = 3
      listByAccount.mockResolvedValue([{ ...plan, id: 2, account_id: 3, model_id: 'reopened-plan' }])
      visible.value = true
      await flushPromises()
      expect(listByAccount).toHaveBeenCalledTimes(3)
      expect(listByAccount).toHaveBeenLastCalledWith(3)
      expect(wrapper.text()).toContain('reopened-plan')
      expect(wrapper.text()).not.toContain('first-mount-plan')
      await vi.advanceTimersByTimeAsync(15000)
      expect(listByAccount).toHaveBeenCalledTimes(4)
      expect(onError).not.toHaveBeenCalled()

      wrapper.unmount()
      expect(vi.getTimerCount()).toBe(0)
      await vi.advanceTimersByTimeAsync(30000)
      expect(listByAccount).toHaveBeenCalledTimes(4)
    } finally {
      wrapper.unmount()
      visibility.mockRestore()
      vi.useRealTimers()
    }
  })
  it('loads existing plans on its first mount already visible', async () => {
    listByAccount.mockReset().mockResolvedValue([{ id: 1, account_id: 2, model_id: 'first-open-model', enabled: true, max_results: 20 }])
    const wrapper = mount(ScheduledTestsPanel, { props: { show: true, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } } })
    await flushPromises()
    expect(listByAccount).toHaveBeenCalledTimes(1)
    expect(listByAccount).toHaveBeenCalledWith(2)
    expect(wrapper.text()).toContain('first-open-model')
    await wrapper.setProps({ accountId: 3 }); await flushPromises()
    expect(listByAccount).toHaveBeenLastCalledWith(3)
  })
  it('does not start polling when unmounted before the initial request finishes', async () => {
    vi.useFakeTimers()
    let finish!: (value: never[]) => void
    listByAccount.mockReset().mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mount(ScheduledTestsPanel, { props: { show: true, accountId: 2, modelOptions: [] },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } } })
    try {
      wrapper.unmount()
      finish([]); await flushPromises()
      expect(vi.getTimerCount()).toBe(0)
      await vi.advanceTimersByTimeAsync(30000)
      expect(listByAccount).toHaveBeenCalledTimes(1)
    } finally { vi.useRealTimers() }
  })
  it('does not reload unchanged reports or poll while the page is hidden', async () => {
    vi.useFakeTimers()
    const visibility = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    const plan = { id: 1, account_id: 2, model_id: 'perf-model', enabled: true, max_results: 20, quality_provider: 'pelican', last_run_at: '2026-10-04T00:00:00Z' }
    listByAccount.mockReset().mockResolvedValue([plan])
    listResults.mockReset().mockResolvedValue([{ id: 3, plan_id: 1, status: 'success', response_text: '<html>fixture</html>' }])
    const wrapper = mount(ScheduledTestsPanel, { props: { show: false, accountId: 2, modelOptions: [] }, global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, ConfirmDialog: true } } })
    try {
      await wrapper.setProps({ show: true }); await flushPromises()
      await wrapper.findAll('div').find(el => el.classes().includes('cursor-pointer') && el.text().includes('perf-model'))!.trigger('click'); await flushPromises()
      expect(listResults).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(30000); await flushPromises()
      expect(listResults).toHaveBeenCalledTimes(1)
      listByAccount.mockResolvedValue([{ ...plan, last_run_at: '2026-10-04T00:05:00Z' }])
      listResults.mockResolvedValue([{ id: 4, plan_id: 1, status: 'success', response_text: '<html>new</html>' }])
      await vi.advanceTimersByTimeAsync(15000); await flushPromises()
      expect(listResults).toHaveBeenLastCalledWith(1, 20, 3)
      visibility.mockReturnValue('hidden')
      const before = listByAccount.mock.calls.length
      await vi.advanceTimersByTimeAsync(30000); await flushPromises()
      expect(listByAccount).toHaveBeenCalledTimes(before)
    } finally { wrapper.unmount(); visibility.mockRestore(); vi.useRealTimers() }
  })
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
    expect(wrapper.find('[data-testid="show-all-results"]').exists()).toBe(false)
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
    expect(wrapper.get('[role="checkbox"][data-section="tools"]').attributes('aria-checked')).toBe('true')
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
