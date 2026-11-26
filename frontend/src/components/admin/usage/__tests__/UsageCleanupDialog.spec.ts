import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'

import UsageCleanupDialog from '../UsageCleanupDialog.vue'
import type { AdminUsageQueryParams, UsageCleanupTask } from '@/api/admin/usage'

const messages: Record<string, string> = {
  'common.cancel': 'Cancel',
  'common.refresh': 'Refresh',
  'common.no': 'No',
  'admin.usage.userFilter': 'User',
  'usage.apiKeyFilter': 'API Key',
  'usage.model': 'Model',
  'admin.usage.requestId': 'Request ID',
  'usage.type': 'Type',
  'usage.ws': 'WS',
  'usage.live': 'Live',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'usage.cyber': 'Cyber',
  'usage.claudeRefusal': 'Claude policy refusal',
  'usage.compactionFilter': 'Request Kind',
  'usage.compactionOnly': 'Compaction Only',
  'admin.usage.billingType': 'Billing Type',
  'admin.usage.billingTypeBalance': 'Balance',
  'admin.usage.billingTypeSubscription': 'Subscription',
  'admin.usage.billingMode': 'Billing Mode',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per Request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.billingModeVideo': 'Video',
  'admin.usage.upstreamModelAudit': 'Upstream model audit',
  'admin.usage.upstreamModelMismatchOnly': 'Mismatched only',
  'admin.usage.upstreamModelMatchedOnly': 'Matched only',
  'admin.usage.account': 'Account',
  'admin.usage.group': 'Group',
  'admin.usage.cleanup.title': 'Cleanup Usage Records',
  'admin.usage.cleanup.subtitle': 'Create an async cleanup task.',
  'admin.usage.cleanup.warning': 'Cleanup is irreversible.',
  'admin.usage.cleanup.clearFilters': 'Clear filters',
  'admin.usage.cleanup.previewTitle': 'Cleanup Scope Preview',
  'admin.usage.cleanup.previewRefresh': 'Recount',
  'admin.usage.cleanup.previewLoading': 'Estimating...',
  'admin.usage.cleanup.previewMatched': '{count} matching records',
  'admin.usage.cleanup.previewMatchedTruncated': '{count}+ matching records',
  'admin.usage.cleanup.previewEmpty': 'No usage records match the current filters',
  'admin.usage.cleanup.previewFailed': 'Failed to estimate matched records',
  'admin.usage.cleanup.maxRangeHint': 'Max {days} days per task',
  'admin.usage.cleanup.scopeAll': 'Full range',
  'admin.usage.cleanup.scopeFilters': 'Active filters',
  'admin.usage.cleanup.removeFilter': 'Remove filter: {label}',
  'admin.usage.cleanup.confirmRange': 'Time range',
  'admin.usage.cleanup.confirmEstimated': 'Estimated rows',
  'admin.usage.cleanup.confirmScope': 'Filters',
  'admin.usage.cleanup.activeTasksBadge': '{count} active',
  'admin.usage.cleanup.duration': 'Duration',
  'admin.usage.cleanup.canceledAt': 'Canceled at',
  'admin.usage.cleanup.taskScopeAll': 'All in range',
  'admin.usage.cleanup.submit': 'Submit Cleanup',
  'admin.usage.cleanup.submitting': 'Submitting...',
  'admin.usage.cleanup.confirmTitle': 'Confirm Cleanup',
  'admin.usage.cleanup.confirmMessage': 'Are you sure?',
  'admin.usage.cleanup.confirmSubmit': 'Confirm Cleanup',
  'admin.usage.cleanup.cancel': 'Cancel Task',
  'admin.usage.cleanup.cancelConfirmTitle': 'Confirm Cancel',
  'admin.usage.cleanup.cancelConfirmMessage': 'Cancel task?',
  'admin.usage.cleanup.cancelConfirmMessageWithId': 'Cancel task #{id}?',
  'admin.usage.cleanup.cancelConfirm': 'Confirm Cancel',
  'admin.usage.cleanup.cancelSuccess': 'Task canceled',
  'admin.usage.cleanup.cancelFailed': 'Cancel failed',
  'admin.usage.cleanup.recentTasks': 'Recent Cleanup Tasks',
  'admin.usage.cleanup.loadingTasks': 'Loading tasks...',
  'admin.usage.cleanup.noTasks': 'No cleanup tasks yet',
  'admin.usage.cleanup.range': 'Range',
  'admin.usage.cleanup.deletedRows': 'Deleted',
  'admin.usage.cleanup.missingRange': 'Please select a date range',
  'admin.usage.cleanup.submitSuccess': 'Cleanup task created',
  'admin.usage.cleanup.submitFailed': 'Failed to create cleanup task',
  'admin.usage.cleanup.loadFailed': 'Failed to load cleanup tasks',
  'admin.usage.cleanup.errors.disabled': 'Usage cleanup is disabled',
  'admin.usage.cleanup.errors.invalidRange': 'End time must be after start time',
  'admin.usage.cleanup.errors.rangeTooLarge': 'Date range cannot exceed {days} days',
  'admin.usage.cleanup.errors.invalidRequestId': 'Request ID cannot be empty',
  'admin.usage.cleanup.errors.requestIdRangeTooLarge': 'Short request ID requires <=24h range',
  'admin.usage.cleanup.errors.invalidBillingMode': 'Invalid billing mode',
  'admin.usage.cleanup.errors.cancelConflict': 'Task can no longer be canceled',
  'admin.usage.cleanup.errors.previewTimeout': 'Preview timed out. Narrow filters or submit directly.',
  'admin.usage.cleanup.status.pending': 'Pending',
  'admin.usage.cleanup.status.running': 'Running',
  'admin.usage.cleanup.status.succeeded': 'Succeeded',
  'admin.usage.cleanup.status.failed': 'Failed',
  'admin.usage.cleanup.status.canceled': 'Canceled'
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => {
      const raw = messages[key] ?? key
      return params
        ? raw.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? ''))
        : raw
    },
    locale: { value: 'en' }
  })
}))

const showSuccess = vi.fn()
const showError = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

const mockListCleanupTasks = vi.fn()
const mockPreviewCleanupTask = vi.fn()
const mockCreateCleanupTask = vi.fn()
const mockCancelCleanupTask = vi.fn()
const mockSearchApiKeys = vi.fn()
const mockGetUserById = vi.fn()
const mockGetAccountById = vi.fn()
const mockGetModelStats = vi.fn()

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    listCleanupTasks: (...args: unknown[]) => mockListCleanupTasks(...args),
    previewCleanupTask: (...args: unknown[]) => mockPreviewCleanupTask(...args),
    createCleanupTask: (...args: unknown[]) => mockCreateCleanupTask(...args),
    cancelCleanupTask: (...args: unknown[]) => mockCancelCleanupTask(...args),
    searchApiKeys: (...args: unknown[]) => mockSearchApiKeys(...args)
  }
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: { getById: (...args: unknown[]) => mockGetUserById(...args) },
    accounts: { getById: (...args: unknown[]) => mockGetAccountById(...args) },
    dashboard: { getModelStats: (...args: unknown[]) => mockGetModelStats(...args) }
  }
}))

const setUserKeywordSpy = vi.fn()
const setApiKeyKeywordSpy = vi.fn()
const setAccountKeywordSpy = vi.fn()
const setAdvancedOpenSpy = vi.fn()

const UsageFiltersStub = defineComponent({
  name: 'UsageFilters',
  props: ['modelValue', 'startDate', 'endDate', 'modelOptions', 'compact', 'flat', 'showActions', 'mode', 'exporting'],
  emits: ['update:modelValue', 'update:startDate', 'update:endDate', 'groups-loaded', 'entity-label'],
  setup(_, { expose }) {
    expose({
      setUserKeyword: setUserKeywordSpy,
      setApiKeyKeyword: setApiKeyKeywordSpy,
      setAccountKeyword: setAccountKeywordSpy,
      setAdvancedOpen: setAdvancedOpenSpy
    })
    return {}
  },
  template: '<div data-testid="usage-filters-stub"><slot name="header-actions" /></div>'
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: ['show', 'title', 'description', 'width'],
  emits: ['close'],
  template: '<div v-if="show" data-testid="base-dialog"><slot /><div data-testid="dialog-footer"><slot name="footer" /></div></div>'
})

const ConfirmDialogStub = defineComponent({
  name: 'ConfirmDialog',
  props: ['show', 'title', 'message', 'confirmText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: `
    <div v-if="show" :data-testid="'confirm-dialog-' + title">
      <p data-testid="confirm-message">{{ message }}</p>
      <slot />
      <button type="button" data-testid="confirm-accept" @click="$emit('confirm')">{{ confirmText }}</button>
      <button type="button" data-testid="confirm-cancel" @click="$emit('cancel')">Cancel</button>
    </div>
  `
})

const defaultPreview = () => ({
  matched_rows: 42,
  truncated: false,
  max_range_days: 31,
  start_time: '2026-05-01T00:00:00Z',
  end_time: '2026-05-02T23:59:59Z'
})

function mountDialog(props: {
  show?: boolean
  filters?: AdminUsageQueryParams
  startDate?: string
  endDate?: string
  modelOptions?: string[]
} = {}) {
  return mount(UsageCleanupDialog, {
    props: {
      show: props.show ?? true,
      filters: props.filters ?? {},
      startDate: props.startDate ?? '2026-05-01T00:00:00',
      endDate: props.endDate ?? '2026-05-02T23:59:59',
      modelOptions: props.modelOptions
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        UsageFilters: UsageFiltersStub,
        Pagination: true
      }
    }
  })
}

describe('UsageCleanupDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPreviewCleanupTask.mockResolvedValue(defaultPreview())
    mockListCleanupTasks.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 })
    mockCreateCleanupTask.mockResolvedValue({ id: 101, status: 'pending' })
    mockCancelCleanupTask.mockResolvedValue({ id: 101, status: 'canceled' })
    mockGetModelStats.mockResolvedValue({ models: [{ model: 'gpt-5' }, { model: 'claude-4' }] })
    mockGetUserById.mockResolvedValue({ id: 7, email: 'alice@example.com' })
    mockGetAccountById.mockResolvedValue({ id: 9, name: 'acc-prod' })
    mockSearchApiKeys.mockResolvedValue([{ id: 11, name: 'prod-key', user_id: 7 }])
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('sends all 12 filters to preview and create, hydrates entity labels, and loads model options', async () => {
    const wrapper = mountDialog({
      filters: {
        user_id: 7,
        api_key_id: 11,
        account_id: 9,
        group_id: 3,
        model: 'claude-4',
        request_id: '  =req_abc  ',
        request_type: 'stream',
        native_compaction_v2: true,
        billing_type: 1,
        billing_mode: 'image',
        upstream_model_mismatch: true
      }
    })
    await flushPromises()

    // Compact layout & auto-loaded model options (sorted) passed to UsageFilters
    const filtersComp = wrapper.findComponent(UsageFiltersStub)
    expect(filtersComp.props('compact')).toBe(true)
    expect(filtersComp.props('flat')).toBe(true)
    expect(filtersComp.props('modelOptions')).toEqual(['claude-4', 'gpt-5'])
    expect(setAdvancedOpenSpy).toHaveBeenCalledWith(true)

    // Hydrated labels from APIs (including exact apiKeyId lookup)
    expect(mockGetUserById).toHaveBeenCalledWith(7, true)
    expect(mockGetAccountById).toHaveBeenCalledWith(9)
    expect(mockSearchApiKeys).toHaveBeenCalledWith(7, '', 11)
    expect(setUserKeywordSpy).toHaveBeenCalledWith('alice@example.com')
    expect(setApiKeyKeywordSpy).toHaveBeenCalledWith('prod-key')
    expect(setAccountKeywordSpy).toHaveBeenCalledWith('acc-prod')

    // Preview called with FULL filter parity including the 4 previously-dropped filters
    expect(mockPreviewCleanupTask).toHaveBeenCalledTimes(1)
    const previewPayload = mockPreviewCleanupTask.mock.calls[0][0]
    expect(previewPayload).toMatchObject({
      start_date: '2026-05-01T00:00:00',
      end_date: '2026-05-02T23:59:59',
      user_id: 7,
      api_key_id: 11,
      account_id: 9,
      group_id: 3,
      model: 'claude-4',
      request_id: '=req_abc',
      request_type: 'stream',
      stream: true,
      native_compaction_v2: true,
      billing_type: 1,
      billing_mode: 'image',
      upstream_model_mismatch: true
    })

    // Preview count shown
    expect(wrapper.get('[data-testid="cleanup-preview-count"]').text()).toBe('42 matching records')

    // Open confirm dialog and verify scope summary
    await wrapper.get('[data-testid="cleanup-dialog-submit"]').trigger('click')
    const confirmBox = wrapper.get('[data-testid="cleanup-confirm-summary"]')
    expect(confirmBox.text()).toContain('42 matching records')
    expect(confirmBox.text()).toContain('alice@example.com')
    expect(confirmBox.text()).toContain('=req_abc')
    expect(confirmBox.text()).toContain('Image')
    expect(confirmBox.text()).toContain('Mismatched only')

    // Confirm submits full payload
    await wrapper.get('[data-testid="confirm-dialog-Confirm Cleanup"] [data-testid="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mockCreateCleanupTask).toHaveBeenCalledWith(previewPayload)
    expect(showSuccess).toHaveBeenCalledWith('Cleanup task created')
    wrapper.unmount()
  })

  it('displays truncated counts and disables submit on 0 matching records or 400/503 preview errors', async () => {
    // Case 1: truncated 100,000+
    mockPreviewCleanupTask.mockResolvedValueOnce({
      ...defaultPreview(),
      matched_rows: 100000,
      truncated: true
    })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-count"]').text()).toBe('100,000+ matching records')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeUndefined()

    // Case 2: 0 matches disables submit
    mockPreviewCleanupTask.mockResolvedValueOnce({
      ...defaultPreview(),
      matched_rows: 0,
      truncated: false
    })
    await wrapper.get('[data-testid="cleanup-preview-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-count"]').text()).toBe('No usage records match the current filters')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeDefined()

    // Case 3: 400 USAGE_CLEANUP_RANGE_TOO_LARGE shows localized alert and disables submit
    mockPreviewCleanupTask.mockRejectedValueOnce({
      status: 400,
      reason: 'USAGE_CLEANUP_RANGE_TOO_LARGE',
      metadata: { max_range_days: '31' },
      message: 'date range exceeds 31 days'
    })
    await wrapper.get('[data-testid="cleanup-preview-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-error"]').text()).toContain('Date range cannot exceed 31 days')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeDefined()

    // Case 4: 500 transient error shows alert without hard-blocking submit
    mockPreviewCleanupTask.mockRejectedValueOnce({
      status: 500,
      message: 'temporary timeout'
    })
    await wrapper.get('[data-testid="cleanup-preview-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-error"]').text()).toContain('temporary timeout')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('clears optional filters while preserving date range and highlights full-range scope', async () => {
    vi.useFakeTimers()
    const wrapper = mountDialog({
      filters: { model: 'gpt-5', billing_mode: 'video' }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="cleanup-clear-filters"]').exists()).toBe(true)

    await wrapper.get('[data-testid="cleanup-clear-filters"]').trigger('click')
    expect(wrapper.get('[data-testid="cleanup-scope-all-badge"]').text()).toBe('Full range')

    vi.advanceTimersByTime(350)
    await flushPromises()
    const latestPayload = mockPreviewCleanupTask.mock.calls.at(-1)?.[0]
    expect(latestPayload).toEqual({
      start_date: '2026-05-01T00:00:00',
      end_date: '2026-05-02T23:59:59',
      timezone: expect.any(String)
    })
    wrapper.unmount()
  })

  it('renders rich task filter chips, duration, error message, and polls ONLY while active tasks exist', async () => {
    vi.useFakeTimers()
    const runningTask: UsageCleanupTask = {
      id: 88,
      status: 'running',
      filters: {
        start_time: '2026-05-01T00:00:00Z',
        end_time: '2026-05-02T00:00:00Z',
        request_id: '=req_1',
        billing_mode: 'image',
        native_compaction_v2: true,
        upstream_model_mismatch: true
      },
      created_by: 1,
      deleted_rows: 1200,
      started_at: '2026-05-02T00:00:00Z',
      created_at: '2026-05-02T00:00:00Z',
      updated_at: '2026-05-02T00:00:05Z'
    }
    const succeededTask: UsageCleanupTask = {
      ...runningTask,
      status: 'succeeded',
      deleted_rows: 2500,
      finished_at: '2026-05-02T00:00:12Z'
    }

    mockListCleanupTasks
      .mockResolvedValueOnce({ items: [runningTask], total: 1, page: 1, page_size: 5 })
      .mockResolvedValueOnce({ items: [succeededTask], total: 1, page: 1, page_size: 5 })

    const wrapper = mountDialog()
    await flushPromises()

    expect(wrapper.get('[data-testid="cleanup-active-tasks-badge"]').text()).toBe('1 active')
    const taskFiltersText = wrapper.get('[data-testid="cleanup-task-filters"]').text()
    expect(taskFiltersText).toContain('=req_1')
    expect(taskFiltersText).toContain('Image')
    expect(taskFiltersText).toContain('Compaction Only')
    expect(taskFiltersText).toContain('Mismatched only')

    // Advances 3s -> silent poll fires without wiping out task list
    vi.advanceTimersByTime(3000)
    await flushPromises()
    expect(mockListCleanupTasks).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="cleanup-active-tasks-badge"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="cleanup-task-item"]').text()).toContain('12.0s')

    // No active tasks left -> advancing 30s does NOT trigger extra polls
    vi.advanceTimersByTime(30000)
    await flushPromises()
    expect(mockListCleanupTasks).toHaveBeenCalledTimes(2)
    // Transitioning running -> succeeded automatically refreshes preview count
    expect(mockPreviewCleanupTask).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('surfaces backend error reasons on submit and cancel failures', async () => {
    const pendingTask: UsageCleanupTask = {
      id: 55,
      status: 'pending',
      filters: {
        start_time: '2026-05-01T00:00:00Z',
        end_time: '2026-05-02T00:00:00Z'
      },
      created_by: 1,
      deleted_rows: 0,
      created_at: '2026-05-02T00:00:00Z',
      updated_at: '2026-05-02T00:00:00Z'
    }
    mockListCleanupTasks.mockResolvedValue({ items: [pendingTask], total: 1, page: 1, page_size: 5 })
    mockCreateCleanupTask.mockRejectedValueOnce({
      status: 400,
      reason: 'USAGE_CLEANUP_REQUEST_ID_RANGE_TOO_LARGE',
      message: 'short request_id pattern requires <=24h'
    })
    mockCancelCleanupTask.mockRejectedValueOnce({
      status: 409,
      reason: 'USAGE_CLEANUP_CANCEL_CONFLICT',
      message: 'cannot cancel'
    })

    const wrapper = mountDialog()
    await flushPromises()

    // Submit error shows localized reason
    await wrapper.get('[data-testid="cleanup-dialog-submit"]').trigger('click')
    await wrapper.get('[data-testid="confirm-dialog-Confirm Cleanup"] [data-testid="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(showError).toHaveBeenCalledWith('Short request ID requires <=24h range')

    // Cancel task includes task #55 in confirm message and surfaces 409 reason
    await wrapper.get('[data-testid="cleanup-task-cancel"]').trigger('click')
    expect(wrapper.get('[data-testid="confirm-dialog-Confirm Cancel"] [data-testid="confirm-message"]').text()).toBe(
      'Cancel task #55?'
    )
    await wrapper.get('[data-testid="confirm-dialog-Confirm Cancel"] [data-testid="confirm-accept"]').trigger('click')
    await flushPromises()
    expect(mockCancelCleanupTask).toHaveBeenCalledWith(55)
    expect(showError).toHaveBeenCalledWith('Task can no longer be canceled')
    wrapper.unmount()
  })

  it('invalidates stale preview immediately on filter change and blocks submit until new preview resolves', async () => {
    vi.useFakeTimers()
    let resolveSecondPreview!: (value: ReturnType<typeof defaultPreview>) => void
    mockPreviewCleanupTask
      .mockResolvedValueOnce({ ...defaultPreview(), matched_rows: 5 })
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveSecondPreview = resolve
          })
      )

    const wrapper = mountDialog({
      filters: { model: 'gpt-5' }
    })
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-count"]').text()).toBe('5 matching records')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeUndefined()

    // Clearing optional filters immediately wipes stale "5 matching records" and disables submit
    await wrapper.get('[data-testid="cleanup-clear-filters"]').trigger('click')
    expect(wrapper.find('[data-testid="cleanup-preview-count"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="cleanup-preview-loading"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeDefined()

    // Even after debounce fires and request is in flight, submit remains disabled
    vi.advanceTimersByTime(350)
    await flushPromises()
    expect(mockPreviewCleanupTask).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeDefined()

    // Once the updated full-range preview resolves, the new count is shown and submit unlocks
    resolveSecondPreview({ ...defaultPreview(), matched_rows: 100000, truncated: true })
    await flushPromises()
    expect(wrapper.get('[data-testid="cleanup-preview-count"]').text()).toBe('100,000+ matching records')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('syncs filtersRef when BaseDialog mounts its body asynchronously on show false -> true', async () => {
    // Emulates real BaseDialog where `rendered` flips true only after watch + mounted ticks
    const AsyncBaseDialogStub = defineComponent({
      name: 'BaseDialog',
      props: ['show', 'title', 'description', 'width'],
      emits: ['close'],
      data() {
        return { delayedRendered: false }
      },
      watch: {
        show: {
          immediate: true,
          handler(val: boolean) {
            if (val) {
              Promise.resolve()
                .then(() => Promise.resolve())
                .then(() => {
                  this.delayedRendered = true
                })
            } else {
              this.delayedRendered = false
            }
          }
        }
      },
      template:
        '<div v-if="delayedRendered" data-testid="async-base-dialog"><slot /><div data-testid="dialog-footer"><slot name="footer" /></div></div>'
    })

    const wrapper = mount(UsageCleanupDialog, {
      props: {
        show: false,
        filters: { user_id: 7, group_id: 3 },
        startDate: '2026-05-01T00:00:00',
        endDate: '2026-05-02T23:59:59'
      },
      global: {
        stubs: {
          BaseDialog: AsyncBaseDialogStub,
          ConfirmDialog: ConfirmDialogStub,
          UsageFilters: UsageFiltersStub,
          Pagination: true
        }
      }
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(setAdvancedOpenSpy).toHaveBeenCalledWith(true)
    expect(setUserKeywordSpy).toHaveBeenCalledWith('alice@example.com')

    // Reopening a second time with cached label must STILL hydrate filtersRef after async mount
    setAdvancedOpenSpy.mockClear()
    setUserKeywordSpy.mockClear()
    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(setAdvancedOpenSpy).toHaveBeenCalledWith(true)
    expect(setUserKeywordSpy).toHaveBeenCalledWith('alice@example.com')
    wrapper.unmount()
  })

  it('clears tasksLoading even when a silent poll supersedes a manual refresh', async () => {
    vi.useFakeTimers()
    const runningTask: UsageCleanupTask = {
      id: 71,
      status: 'running',
      filters: { start_time: '2026-05-01T00:00:00Z', end_time: '2026-05-02T00:00:00Z' },
      created_by: 1,
      deleted_rows: 10,
      created_at: '2026-05-02T00:00:00Z',
      updated_at: '2026-05-02T00:00:01Z'
    }

    let rejectManual!: (err: unknown) => void
    mockListCleanupTasks
      .mockResolvedValueOnce({ items: [runningTask], total: 1, page: 1, page_size: 5 })
      .mockImplementationOnce(
        () =>
          new Promise((_, reject) => {
            rejectManual = reject
          })
      )
      .mockResolvedValueOnce({ items: [runningTask], total: 1, page: 1, page_size: 5 })

    const wrapper = mountDialog()
    await flushPromises()

    // Trigger manual refresh (sets tasksLoading = true -> button disabled/loading)
    await wrapper.get('[data-testid="cleanup-tasks-refresh"]').trigger('click')
    expect(wrapper.get('[data-testid="cleanup-tasks-refresh"]').attributes('disabled')).toBeDefined()

    // Before manual refresh resolves, 3s silent poll fires and aborts manual request
    vi.advanceTimersByTime(3000)
    rejectManual({ code: 'ERR_CANCELED', name: 'CanceledError' })
    await flushPromises()

    // Refresh button must NOT stay stuck in loading/disabled state
    expect(wrapper.get('[data-testid="cleanup-tasks-refresh"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('removes individual scope chips, clears stale stream when request_type is cleared, and maps 504 preview timeout', async () => {
    vi.useFakeTimers()
    mockPreviewCleanupTask
      .mockResolvedValueOnce(defaultPreview())
      .mockRejectedValueOnce({ status: 504, reason: 'USAGE_CLEANUP_PREVIEW_TIMEOUT', message: 'timeout' })
      .mockResolvedValueOnce(defaultPreview())

    const wrapper = mountDialog({
      filters: {
        user_id: 7,
        request_type: 'stream',
        stream: true,
        billing_mode: 'image'
      }
    })
    await flushPromises()

    // Remove user_id chip individually
    await wrapper.get('[data-testid="cleanup-scope-chip-remove-user_id"]').trigger('click')
    expect(setUserKeywordSpy).toHaveBeenLastCalledWith('')
    expect(wrapper.find('[data-testid="cleanup-scope-chip-user_id"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="cleanup-scope-chip-billing_mode"]').exists()).toBe(true)

    vi.advanceTimersByTime(350)
    await flushPromises()
    // 504 preview timeout shows localized error while keeping Submit enabled
    expect(wrapper.get('[data-testid="cleanup-preview-error"]').text()).toContain('Preview timed out')
    expect(wrapper.get('[data-testid="cleanup-dialog-submit"]').attributes('disabled')).toBeUndefined()

    // Clearing request_type chip must ALSO clear stream so legacy stream=true does not leak back
    await wrapper.get('[data-testid="cleanup-scope-chip-remove-request_type"]').trigger('click')
    vi.advanceTimersByTime(350)
    await flushPromises()
    const latestPayload = mockPreviewCleanupTask.mock.calls.at(-1)?.[0]
    expect(latestPayload.request_type).toBeUndefined()
    expect(latestPayload.stream).toBeUndefined()
    expect(latestPayload.user_id).toBeUndefined()
    expect(latestPayload.billing_mode).toBe('image')
    wrapper.unmount()
  })
})
