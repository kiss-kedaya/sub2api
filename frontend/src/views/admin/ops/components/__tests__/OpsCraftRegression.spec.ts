import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import OpsConcurrencyCard from '../OpsConcurrencyCard.vue'
import OpsOpenAITokenStatsCard from '../OpsOpenAITokenStatsCard.vue'
import OpsSystemLogTable from '../OpsSystemLogTable.vue'
import OpsThroughputTrendChart from '../OpsThroughputTrendChart.vue'
import OpsLatencyChart from '../OpsLatencyChart.vue'
import OpsErrorTrendChart from '../OpsErrorTrendChart.vue'
import OpsErrorDistributionChart from '../OpsErrorDistributionChart.vue'
import OpsSwitchRateTrendChart from '../OpsSwitchRateTrendChart.vue'
import OpsDashboardHeader from '../OpsDashboardHeader.vue'

const mocks = vi.hoisted(() => ({
  getConcurrencyStats: vi.fn(),
  getAccountAvailabilityStats: vi.fn(),
  getUserConcurrencyStats: vi.fn(),
  getOpenAITokenStats: vi.fn(),
  listSystemLogs: vi.fn(),
  getSystemLogSinkHealth: vi.fn(),
  getRuntimeLogConfig: vi.fn(),
  getAllGroups: vi.fn(),
  getRealtimeTrafficSummary: vi.fn(),
}))

vi.mock('@/api/admin/ops', () => ({
  opsAPI: {
    getConcurrencyStats: (...args: any[]) => mocks.getConcurrencyStats(...args),
    getAccountAvailabilityStats: (...args: any[]) => mocks.getAccountAvailabilityStats(...args),
    getUserConcurrencyStats: (...args: any[]) => mocks.getUserConcurrencyStats(...args),
    getOpenAITokenStats: (...args: any[]) => mocks.getOpenAITokenStats(...args),
    listSystemLogs: (...args: any[]) => mocks.listSystemLogs(...args),
    getSystemLogSinkHealth: (...args: any[]) => mocks.getSystemLogSinkHealth(...args),
    getRuntimeLogConfig: (...args: any[]) => mocks.getRuntimeLogConfig(...args),
    getRealtimeTrafficSummary: (...args: any[]) => mocks.getRealtimeTrafficSummary(...args),
  },
}))

vi.mock('@/api', () => ({
  adminAPI: {
    groups: {
      getAll: (...args: any[]) => mocks.getAllGroups(...args),
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
  useAdminSettingsStore: () => ({ opsRealtimeMonitoringEnabled: false }),
}))

vi.mock('@/composables/useConfirmation', () => ({ confirm: vi.fn().mockResolvedValue(true) }))

vi.mock('@vueuse/core', () => ({
  useMediaQuery: () => ref(true),
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  ArcElement: {},
  BarElement: {},
  CategoryScale: {},
  Filler: {},
  Legend: {},
  LineElement: {},
  LinearScale: {},
  PointElement: {},
  Title: {},
  Tooltip: {},
}))

vi.mock('vue-chartjs', async () => {
  const { defineComponent: dc } = await import('vue')
  const makeStub = (name: string) =>
    dc({
      name,
      props: {
        data: { type: Object, required: true },
        options: { type: Object, default: () => ({}) },
      },
      template: `<div class="chart-stub-${name.toLowerCase()}" />`,
    })
  return {
    Line: makeStub('Line'),
    Bar: makeStub('Bar'),
    Doughnut: makeStub('Doughnut'),
  }
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: { modelValue: { type: [String, Number, null], default: '' } },
  emits: ['update:modelValue'],
  template: '<div class="select-stub" />',
})

const HelpTooltipStub = defineComponent({
  name: 'HelpTooltip',
  props: { content: { type: String, default: '' }, trigger: { type: String, default: 'hover' } },
  template: '<span class="help-tooltip-stub" :data-trigger="trigger" />',
})

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('M6 Ops Craft Regressions', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.getAllGroups.mockResolvedValue([])
    mocks.getRealtimeTrafficSummary.mockResolvedValue({ enabled: false, summary: null })
  })

  it('OpsConcurrencyCard uses GPU scaleX bars and keeps refresh button enabled during silent poll', async () => {
    mocks.getConcurrencyStats.mockResolvedValue({
      enabled: true,
      platform: {
        openai: { current_in_use: 3, max_capacity: 10, waiting_in_queue: 0 },
      },
      group: {},
      account: {},
    })
    mocks.getAccountAvailabilityStats.mockResolvedValue({
      enabled: true,
      platform: {
        openai: { total_accounts: 5, available_count: 4, rate_limit_count: 1, error_count: 0 },
      },
      group: {},
      account: {},
    })

    const wrapper = mount(OpsConcurrencyCard, {
      props: { refreshToken: 0 },
    })
    await flushPromises()

    const bar = wrapper.find('[style*="scaleX"]')
    expect(bar.exists()).toBe(true)
    expect(bar.attributes('style')).toContain('scaleX(0.3)')
    expect(bar.attributes('style')).not.toContain('width:')

    // Trigger silent poll via refreshToken and hold promise pending
    const pendingConc = deferred<any>()
    const pendingAvail = deferred<any>()
    mocks.getConcurrencyStats.mockReturnValueOnce(pendingConc.promise)
    mocks.getAccountAvailabilityStats.mockReturnValueOnce(pendingAvail.promise)

    await wrapper.setProps({ refreshToken: 1 })
    const refreshBtn = wrapper.findAll('button')[1]
    expect(refreshBtn.attributes('disabled')).toBeUndefined()

    pendingConc.resolve({
      enabled: true,
      platform: { openai: { current_in_use: 5, max_capacity: 10, waiting_in_queue: 0 } },
      group: {},
      account: {},
    })
    pendingAvail.resolve({
      enabled: true,
      platform: { openai: { total_accounts: 5, available_count: 5, rate_limit_count: 0, error_count: 0 } },
      group: {},
      account: {},
    })
    await flushPromises()
    expect(wrapper.find('[style*="scaleX"]').attributes('style')).toContain('scaleX(0.5)')
  })

  it('OpsOpenAITokenStatsCard keeps table mounted during silent refreshToken polls', async () => {
    mocks.getOpenAITokenStats.mockResolvedValue({
      time_range: '30d',
      items: [
        {
          model: 'gpt-5',
          request_count: 10,
          avg_tokens_per_sec: 40.5,
          avg_first_token_ms: 120,
          total_output_tokens: 1000,
          avg_duration_ms: 400,
          requests_with_first_token: 10,
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      top_n: 20,
    })

    const wrapper = mount(OpsOpenAITokenStatsCard, {
      props: { refreshToken: 0 },
      global: { stubs: { Select: SelectStub, EmptyState: true } },
    })
    await flushPromises()
    expect(wrapper.find('.max-h-\\[420px\\]').exists()).toBe(true)

    const pending = deferred<any>()
    mocks.getOpenAITokenStats.mockReturnValueOnce(pending.promise)
    await wrapper.setProps({ refreshToken: 1 })

    // Table container stays mounted (no loading skeleton flicker)
    expect(wrapper.find('.max-h-\\[420px\\]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.ops.loadingText')

    pending.resolve({
      time_range: '30d',
      items: [
        {
          model: 'gpt-5',
          request_count: 10,
          avg_tokens_per_sec: 40.5,
          avg_first_token_ms: 120,
          total_output_tokens: 1000,
          avg_duration_ms: 400,
          requests_with_first_token: 10,
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      top_n: 20,
    })
    await flushPromises()
  })

  it('OpsSystemLogTable keeps log rows mounted during silent refreshToken polls', async () => {
    mocks.listSystemLogs.mockResolvedValue({
      items: [{ id: 1, created_at: '2026-07-14T00:10:01Z', host: 'node-1', level: 'info', component: 'app', message: 'ok' }],
      total: 1,
      page: 1,
      page_size: 20,
    })
    mocks.getSystemLogSinkHealth.mockResolvedValue({
      queue_depth: 0,
      queue_capacity: 5000,
      dropped_count: 0,
      write_failed_count: 0,
      written_count: 1,
      avg_write_delay_ms: 0,
    })
    mocks.getRuntimeLogConfig.mockResolvedValue({
      level: 'info',
      persist_access_logs: false,
      enable_sampling: false,
      sampling_initial: 100,
      sampling_thereafter: 100,
      caller: true,
      stacktrace_level: 'error',
      retention_days: 30,
    })

    const wrapper = mount(OpsSystemLogTable, {
      props: { refreshToken: 0 },
      global: { stubs: { Select: SelectStub, Pagination: true } },
    })
    await flushPromises()
    expect(wrapper.find('table').exists()).toBe(true)

    const pending = deferred<any>()
    mocks.listSystemLogs.mockReturnValueOnce(pending.promise)
    await wrapper.setProps({ refreshToken: 1 })

    expect(wrapper.find('table').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('common.loading')

    pending.resolve({
      items: [{ id: 1, created_at: '2026-07-14T00:10:01Z', host: 'node-1', level: 'info', component: 'app', message: 'ok' }],
      total: 1,
      page: 1,
      page_size: 20,
    })
    await flushPromises()
  })

  it('OpsSystemLogTable preserves empty state without loading flicker during silent refreshToken polls', async () => {
    mocks.listSystemLogs.mockResolvedValue({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
    })
    mocks.getSystemLogSinkHealth.mockResolvedValue({
      queue_depth: 0,
      queue_capacity: 5000,
      dropped_count: 0,
      write_failed_count: 0,
      written_count: 0,
      avg_write_delay_ms: 0,
    })
    mocks.getRuntimeLogConfig.mockResolvedValue({
      level: 'info',
      persist_access_logs: false,
      enable_sampling: false,
      sampling_initial: 100,
      sampling_thereafter: 100,
      caller: true,
      stacktrace_level: 'error',
      retention_days: 30,
    })

    const wrapper = mount(OpsSystemLogTable, {
      props: { refreshToken: 0 },
      global: { stubs: { Select: SelectStub, Pagination: true } },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.ops.systemLogs.empty')
    expect(wrapper.text()).not.toContain('common.loading')

    const pending = deferred<any>()
    mocks.listSystemLogs.mockReturnValueOnce(pending.promise)
    await wrapper.setProps({ refreshToken: 1 })

    expect(wrapper.text()).toContain('admin.ops.systemLogs.empty')
    expect(wrapper.text()).not.toContain('common.loading')

    pending.resolve({
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
    })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.ops.systemLogs.empty')
  })

  it('OpsConcurrencyCard keeps silent refreshToken polls non-loading when mode data is empty', async () => {
    mocks.getConcurrencyStats.mockResolvedValue({
      enabled: true,
      platform: {},
      group: {},
      account: {},
    })
    mocks.getAccountAvailabilityStats.mockResolvedValue({
      enabled: true,
      platform: {},
      group: {},
      account: {},
    })

    const wrapper = mount(OpsConcurrencyCard, {
      props: { refreshToken: 0 },
    })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.ops.concurrency.empty')

    const pendingConc = deferred<any>()
    const pendingAvail = deferred<any>()
    mocks.getConcurrencyStats.mockReturnValueOnce(pendingConc.promise)
    mocks.getAccountAvailabilityStats.mockReturnValueOnce(pendingAvail.promise)

    await wrapper.setProps({ refreshToken: 1 })
    const refreshBtn = wrapper.findAll('button')[1]
    expect(refreshBtn.attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('admin.ops.concurrency.empty')

    pendingConc.resolve({ enabled: true, platform: {}, group: {}, account: {} })
    pendingAvail.resolve({ enabled: true, platform: {}, group: {}, account: {} })
    await flushPromises()
  })

  it('all 5 Ops Chart.js components disable poll-refresh and hover animations', () => {
    const stubs = { EmptyState: true, HelpTooltip: HelpTooltipStub }

    const throughput = mount(OpsThroughputTrendChart, {
      props: {
        loading: false,
        timeRange: '1h',
        points: [{ bucket_start: '2026-01-01T00:00:00Z', request_count: 5, qps: 1, tps: 100, switch_count: 0 }],
      },
      global: { stubs },
    })
    const latency = mount(OpsLatencyChart, {
      props: { loading: false, latencyData: { total_requests: 5, buckets: [{ range: '0-100ms', count: 5 }] } as any },
      global: { stubs },
    })
    const errorTrend = mount(OpsErrorTrendChart, {
      props: {
        loading: false,
        timeRange: '1h',
        points: [{
          bucket_start: '2026-01-01T00:00:00Z',
          error_count_total: 2,
          error_count_sla: 2,
          upstream_error_count_excl_429_529: 0,
          upstream_429_count: 0,
          upstream_529_count: 0,
          business_limited_count: 0,
        }],
      },
      global: { stubs },
    })
    const errorDist = mount(OpsErrorDistributionChart, {
      props: { loading: false, data: { total: 2, items: [{ status_code: 500, total: 2, sla: 2, business_limited: 0 }] } },
      global: { stubs },
    })
    const switchRate = mount(OpsSwitchRateTrendChart, {
      props: {
        loading: false,
        timeRange: '1h',
        points: [{ bucket_start: '2026-01-01T00:00:00Z', request_count: 5, switch_count: 1 }],
      },
      global: { stubs },
    })

    for (const [chartWrapper, stubName] of [
      [throughput, 'Line'],
      [latency, 'Bar'],
      [errorTrend, 'Line'],
      [errorDist, 'Doughnut'],
      [switchRate, 'Line'],
    ] as const) {
      const comp = chartWrapper.findComponent({ name: stubName })
      expect(comp.exists()).toBe(true)
      const opts = comp.props('options')
      expect(opts.animation).toBe(false)
      expect(opts.transitions?.active?.animation?.duration).toBe(0)
    }
    expect(errorDist.findComponent({ name: 'Doughnut' }).props('options').cutout).toBe('65%')
  })

  it('OpsDashboardHeader uses accessible diagnosis button trigger without nested HelpTooltip and sets trigger="auto"', async () => {
    const wrapper = mount(OpsDashboardHeader, {
      props: {
        overview: { health_score: 96, qps: { current: 2 }, error_rate: 0 } as any,
        platform: '',
        groupId: null,
        timeRange: '1h',
        queryMode: 'auto',
        loading: false,
        lastUpdated: null,
      },
      global: {
        stubs: {
          Select: SelectStub,
          BaseDialog: true,
          Icon: true,
          HelpTooltip: HelpTooltipStub,
        },
      },
    })
    await flushPromises()

    const triggerButton = wrapper.find('button[aria-label="admin.ops.diagnosis.title"]')
    expect(triggerButton.exists()).toBe(true)
    expect(triggerButton.find('.help-tooltip-stub').exists()).toBe(false)

    const tooltips = wrapper.findAll('.help-tooltip-stub')
    expect(tooltips.length).toBeGreaterThan(0)
    for (const tip of tooltips) {
      expect(tip.attributes('data-trigger')).toBe('auto')
    }

    const realtimeWindowButtons = wrapper.findAll('button').filter((btn) =>
      ['1min', '5min', '30min', '1h'].includes(btn.text().trim())
    )
    expect(realtimeWindowButtons).toHaveLength(4)
    for (const btn of realtimeWindowButtons) {
      expect(btn.classes()).toContain('min-h-11')
      expect(btn.classes()).not.toContain('min-h-8')
    }

    const detailsButtons = wrapper.findAll('button').filter((btn) =>
      btn.text().trim() === 'admin.ops.requestDetails.details'
    )
    expect(detailsButtons.length).toBeGreaterThanOrEqual(7)
    for (const btn of detailsButtons) {
      expect(btn.classes()).toContain('min-h-11')
    }
  })
})
