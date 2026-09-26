import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ScheduledTestsPanel from '../ScheduledTestsPanel.vue'

enableAutoUnmount(afterEach)

const { listByAccount, listResults } = vi.hoisted(() => ({
  listByAccount: vi.fn(),
  listResults: vi.fn()
}))

vi.mock('@/api/admin', () => ({ adminAPI: { scheduledTests: { listByAccount, listResults } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('ScheduledTestsPanel quality results', () => {
  it('keeps inconclusive responses distinct from success and failure', async () => {
    listByAccount.mockResolvedValue([{
      id: 1, account_id: 2, model_id: 'quality-test-model',
      cron_expression: '*/5 * * * *', enabled: true, max_results: 20,
      auto_recover: false, next_run_at: '2026-09-26T10:00:00Z'
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
    expect(wrapper.get('iframe').attributes('sandbox')).toBe('allow-scripts')
  })
})
