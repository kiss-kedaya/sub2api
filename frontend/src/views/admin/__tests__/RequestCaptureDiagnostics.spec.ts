import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import RequestCaptureView from '../RequestCaptureView.vue'
import { adminAPI } from '@/api'
import * as captures from '@/api/admin/requestCaptures'
import type { CaptureConfig, CaptureRecord, CaptureTask } from '@/api/admin/requestCaptures'

const state = vi.hoisted(() => ({ stepUp: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/auth/TotpStepUpDialog.vue', () => ({ default: { template: '<div />' } }))
vi.mock('@/composables/useStepUp', () => ({ isStepUpCancelled: () => false, useStepUp: () => ({ visible: ref(false), run: state.stepUp, onCancel: vi.fn() }) }))
vi.mock('@/api', () => ({ adminAPI: { users: { list: vi.fn(async () => ({ items: [], total: 0 })) } } }))
vi.mock('@/api/admin/requestCaptures', () => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), listTasks: vi.fn(), listRecords: vi.fn(), getRecord: vi.fn(),
  getContent: vi.fn(), createTask: vi.fn(), stopTask: vi.fn(), deleteTask: vi.fn(),
  canStreamExport: () => true, exportCapture: vi.fn(), authorizeSensitiveRead: vi.fn()
}))

const config: CaptureConfig = { enabled: false, id1_diagnostic_enabled: false, quota_mib: 1024, retention_days: 7, diagnostic_retention_hours: 8, record_limit_mib: 128, instance_id: 'test-slot', storage_ready: true }
const task: CaptureTask = { id: 'diagnostic', mode: 'id1_diagnostic', instance_id: 'test-slot', target_type: 'user', target_id: 1, target_name: 'Administrator', save_media: true, created_at: '2026-10-28T00:00:00Z', status: 'running', requests: 2, partial: 0, skipped: 0, bytes: 100 }
const record: CaptureRecord = { id: 'record', task_id: task.id, request_id: 'test-request', instance_id: 'test-slot', user_id: 1, group_id: 1, path: '/v1/responses', protocol: 'openai', status: 200, is_error: false, partial: false, created_at: '2026-10-28T00:00:00Z', expires_at: '2026-10-28T08:00:00Z', bytes: 100, attempts: [], parts: [] }
beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(adminAPI.users.list).mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
  state.stepUp.mockImplementation(async (action: () => Promise<unknown>) => action())
  vi.mocked(captures.getConfig).mockResolvedValue({ ...config })
  vi.mocked(captures.saveConfig).mockImplementation(async value => ({ ...value }))
  vi.mocked(captures.listTasks).mockResolvedValue({ items: [{ ...task }], has_more: false, stats: { instance_id: 'test-slot', used_bytes: 100, buffer_bytes: 0, peak_buffer_bytes: 0, active_requests: 0, admission_skipped: 1, storage_error: false, skipped_by_reason: { quota_exceeded: 1 }, expired_records: 3 } })
  vi.mocked(captures.listRecords).mockResolvedValue({ items: [{ ...record }], has_more: false })
  vi.mocked(captures.getRecord).mockResolvedValue({ ...record })
  vi.mocked(captures.exportCapture).mockImplementation(async (_task, _record, authorize) => { await authorize?.() })
})

describe('ID1 rolling request diagnostics', () => {
  it('does not overwrite configuration or poll an unpinned instance before configuration loads', async () => {
    vi.mocked(captures.getConfig).mockRejectedValueOnce(new Error('Configuration unavailable'))
    const wrapper = mount(RequestCaptureView); await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="id1-diagnostic-enabled"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLButtonElement>('[data-testid="capture-config-save"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="capture-config"]').trigger('submit'); await flushPromises()
    expect(captures.saveConfig).not.toHaveBeenCalled()
    expect(captures.listTasks).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps both capture switches off by default and can read retained history while disabled', async () => {
    const wrapper = mount(RequestCaptureView); await flushPromises()
    // The capture switches are USwitch (<button role="switch">).
    expect(wrapper.get('[data-testid="id1-diagnostic-enabled"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="capture-enabled"]').attributes('aria-checked')).toBe('false')
    expect(captures.listTasks).toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.requestCapture.expiredRecords')
    expect(wrapper.text()).toContain('admin.requestCapture.dropReasons.quota_exceeded')
    await wrapper.get('[data-testid="capture-config"]').trigger('submit'); await flushPromises()
    expect(captures.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, id1_diagnostic_enabled: false }))
    expect(state.stepUp).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('loads successful and failed diagnostic records and routes stopping through the independent switch', async () => {
    vi.mocked(captures.getConfig).mockResolvedValue({ ...config, id1_diagnostic_enabled: true })
    const wrapper = mount(RequestCaptureView); await flushPromises()
    await wrapper.get('[data-testid="select-capture-diagnostic"]').trigger('click'); await flushPromises()
    expect(captures.listRecords).toHaveBeenLastCalledWith('diagnostic', 1, '', false)
    expect(wrapper.text()).toContain('admin.requestCapture.continuous')
    expect(wrapper.text()).toContain('admin.requestCapture.allOutcomes')
    const row = wrapper.get('[data-testid="select-capture-diagnostic"]').element.closest('tr')!
    expect(row.textContent).not.toContain('admin.requestCapture.stop')
    expect(row.querySelector<HTMLButtonElement>('[data-testid="delete-capture"]')?.disabled).toBe(true)
    const diagnosticSwitch = wrapper.get('[data-testid="id1-diagnostic-enabled"]')
    expect(diagnosticSwitch.attributes('aria-checked')).toBe('true')
    await diagnosticSwitch.trigger('click')
    expect(diagnosticSwitch.attributes('aria-checked')).toBe('false')
    // Unsaved switch changes must not permit deleting an active diagnostic task.
    expect(row.querySelector<HTMLButtonElement>('[data-testid="delete-capture"]')?.disabled).toBe(true)
    await wrapper.get('[data-testid="capture-config"]').trigger('submit'); await flushPromises()
    expect(captures.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, id1_diagnostic_enabled: false }))
    expect(captures.stopTask).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('allows finalized individual diagnostic exports while running and preserves 2FA authorization', async () => {
    const wrapper = mount(RequestCaptureView); await flushPromises()
    await wrapper.get('[data-testid="select-capture-diagnostic"]').trigger('click'); await flushPromises()
    const link = wrapper.findAll('button').find(button => button.text() === record.request_id)!
    await link.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain(record.expires_at)
    const exportButton = wrapper.get<HTMLButtonElement>('[data-testid="export-capture-record"]')
    expect(exportButton.element.disabled).toBe(false)
    await exportButton.trigger('click'); await flushPromises()
    expect(captures.exportCapture).toHaveBeenCalledWith('diagnostic', 'record', expect.any(Function))
    expect(state.stepUp).toHaveBeenCalled()
    expect(captures.authorizeSensitiveRead).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps targeted tasks filtered to errors and blocks their exports while running', async () => {
    vi.mocked(captures.listTasks).mockResolvedValue({ items: [{ ...task, mode: 'targeted' }], has_more: false, stats: { instance_id: 'test-slot', used_bytes: 0, buffer_bytes: 0, peak_buffer_bytes: 0, active_requests: 0, admission_skipped: 0, storage_error: false } })
    const wrapper = mount(RequestCaptureView); await flushPromises()
    await wrapper.get('[data-testid="select-capture-diagnostic"]').trigger('click'); await flushPromises()
    expect(captures.listRecords).toHaveBeenLastCalledWith('diagnostic', 1, '', true)
    await wrapper.findAll('button').find(button => button.text() === record.request_id)!.trigger('click'); await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="export-capture-record"]').element.disabled).toBe(true)
    wrapper.unmount()
  })
})
