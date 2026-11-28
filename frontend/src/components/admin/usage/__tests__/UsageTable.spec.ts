const appStoreMocks = vi.hoisted(() => ({
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => appStoreMocks }))

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import UsageTable from '../UsageTable.vue'

const messages: Record<string, string> = {
  'admin.usage.userDeletedBadge': 'Deleted',
  'usage.costDetails': 'Cost Breakdown',
  'usage.costDetailsStandard': 'Cost Breakdown (Standard price)',
  'usage.failedMetered': 'Related error (metered)',
  'usage.failedMeteredHint': 'Reported usage remains billed.',
  'usage.requestFailed': 'Related error',
  'usage.failureCode': 'Error code',
  'usage.toolBilling': 'Tool billing',
  'usage.fixedPerCall': 'Fixed per call',
  'admin.usage.inputCost': 'Input Cost',
  'admin.usage.outputCost': 'Output Cost',
  'admin.usage.cacheCreationCost': 'Cache Creation Cost',
  'admin.usage.cacheReadCost': 'Cache Read Cost',
  'usage.inputTokenPrice': 'Input price',
  'usage.outputTokenPrice': 'Output price',
  'usage.perMillionTokens': '/ 1M tokens',
  'usage.serviceTier': 'Service tier',
  'usage.serviceTierPriority': 'Fast',
  'usage.serviceTierUltrafast': 'Ultrafast',
  'usage.serviceTierFlex': 'Flex',
  'usage.serviceTierStandard': 'Standard',
  'usage.serviceTierMultiplier': 'Tier multiplier',
  'usage.groupRate': 'Group rate',
  'usage.combinedRate': 'Combined rate',
  'usage.rate': 'Rate',
  'usage.accountMultiplier': 'Account rate',
  'usage.original': 'Original',
  'usage.originalStandard': 'Original (Standard price)',
  'usage.upstreamCost': 'Upstream cost',
  'usage.upstreamCostHint': 'Upstream tier price actually charged; reconciliation only',
  'usage.userBilled': 'User billed',
  'usage.accountBilled': 'Account billed',
  'usage.imageUnit': ' images',
  'usage.imageCount': 'Image count',
  'usage.imageBillingSize': 'Billing size',
  'usage.imageInputSize': 'Input size',
  'usage.imageOutputSize': 'Output size',
  'usage.imageSizeSource': 'Size source',
  'usage.imageSizeBreakdown': 'Size breakdown',
  'usage.imageSizeSourceOutput': 'Upstream output',
  'usage.imageSizeSourceInput': 'Request input',
  'usage.imageSizeSourceDefault': 'Default billing tier',
  'usage.imageSizeSourceLegacy': 'Legacy record',
  'usage.imageSizeSourceMissing': 'Not recorded',
  'usage.imageSizeNotRecorded': 'not recorded',
  'usage.imageSizeLegacyUnstandardized': 'legacy unstandardized',
  'usage.imageSizeUnknown': 'unknown',
  'usage.imageUnitPrice': 'Per-image price',
  'usage.imageTotalPrice': 'Image total price',
  'usage.stream': 'Stream',
  'usage.sync': 'Sync',
  'usage.claudeRefusal': 'Claude policy refusal',
  'usage.nativeCompactionV2': 'Compaction',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per request',
  'admin.usage.billingModeImage': 'Image',
	'admin.usage.requestId': 'Request ID',
	'admin.usage.requestIdCopied': 'Request ID copied',
	'admin.usage.responseMsgId': 'Message ID',
	'admin.usage.responseMsgIdCopied': 'Message ID copied',
	'admin.usage.upstreamRequestIdCopied': 'Upstream ID copied',
	'keys.copied': 'Copied',
	'keys.copyToClipboard': 'Copy to clipboard',
	'common.copyFailed': 'Copy failed',
	'usage.requestedModel': 'Requested',
	'usage.sentUpstreamModel': 'Sent upstream',
	'usage.upstreamResponseModel': 'Upstream response',
	'usage.modelVariant': 'Possible version variant',
  'usage.modelMismatch': 'Different model',
  'usage.latencyFirstToken': 'First',
  'usage.latencyDuration': 'Total',
  'usage.outputRate': 'Output rate',
  'usage.outputRateHint': 'Average output tokens per second',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.request_id">
        <slot name="cell-model" :row="row" :value="row.model" />
        <slot name="cell-reasoning_effort" :row="row" :value="row.reasoning_effort" />
        <slot name="cell-billing_mode" :row="row" />
        <slot name="cell-tokens" :row="row" />
        <slot name="cell-cost" :row="row" />
        <slot name="cell-latency" :row="row" />
        <slot name="cell-request_id" :row="row" />
        <slot name="cell-response_msg_id" :row="row" />
        <slot name="cell-upstream_request_id" :row="row" />
      </div>
    </div>
  `,
}

const baseImageRow = {
  request_id: 'req-admin-image',
  model: 'gpt-image-2',
  actual_cost: 0.4,
  total_cost: 0.4,
  account_rate_multiplier: 1,
  rate_multiplier: 1,
  service_tier: null,
  input_cost: 0,
  output_cost: 0,
  cache_creation_cost: 0,
  cache_read_cost: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_creation_tokens: 0,
  cache_read_tokens: 0,
  cache_creation_5m_tokens: 0,
  cache_creation_1h_tokens: 0,
  cache_ttl_overridden: false,
  billing_mode: 'image',
  image_count: 2,
  image_size: '2K',
  image_input_size: null,
  image_output_size: null,
  image_size_source: null,
  image_size_breakdown: null,
}

describe('admin UsageTable tooltip', () => {
  it('marks metered failures while leaving unknown outcomes unlabeled and charges intact', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          { ...baseImageRow, request_id: 'failed-metered', failure_status_code: 502 },
          { ...baseImageRow, request_id: 'unknown-outcome' },
          { ...baseImageRow, request_id: 'failed-unbilled', failure_status_code: 500, actual_cost: 0 },
          { ...baseImageRow, request_id: 'inflight-not-final', failure_status_code: 502, inflight: true },
        ],
        loading: false,
        columns: [],
      },
      global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
    })
    const badges = wrapper.findAll('[data-testid="usage-failure-badge"]')
    expect(badges).toHaveLength(2)
    expect(badges[0].text()).toBe('Related error (metered) · Error code 502')
    expect(badges[0].attributes('title')).toBe('Reported usage remains billed.')
    expect(badges[1].text()).toBe('Related error · Error code 500')
    expect(wrapper.props('data')[0].actual_cost).toBe(0.4)
  })
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 0,
      y: 0,
      top: 20,
      left: 20,
      right: 120,
      bottom: 40,
      width: 100,
      height: 20,
      toJSON: () => ({}),
    } as DOMRect)
  })

  it('marks only usage rows that actually applied long-context billing', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          {
            ...baseImageRow,
            request_id: 'req-long-context-enabled',
            long_context_billing_applied: true,
          },
          {
            ...baseImageRow,
            request_id: 'req-long-context-disabled',
            long_context_billing_applied: false,
          },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.findAll('[data-testid="long-context-billing-marker"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="long-context-billing-marker"]').text()).toBe('x2')
  })

  it('keeps the request type badge and adds a separate badge only for native compaction rows', () => {
    const DataTableStreamStub = {
      props: ['data'],
      template: `
        <div>
          <div v-for="row in data" :key="row.request_id">
            <slot name="cell-stream" :row="row" />
          </div>
        </div>
      `,
    }
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          {
            ...baseImageRow,
            request_id: 'req-compaction-stream',
            request_type: 'stream',
            stream: true,
            native_compaction_v2: true,
          },
          {
            ...baseImageRow,
            request_id: 'req-historical-sync',
            request_type: 'sync',
            stream: false,
            native_compaction_v2: false,
          },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStreamStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const requestBadges = wrapper.findAll('[data-testid="request-type-badge"]')
    expect(requestBadges).toHaveLength(2)
    expect(requestBadges[0].text()).toBe('Stream')
    expect(requestBadges[1].text()).toBe('Sync')
    expect(wrapper.findAll('[data-testid="native-compaction-badge"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="native-compaction-badge"]').text()).toBe('Compaction')
  })

  it('labels Claude policy refusal rows with a distinct warning badge, stream or not', () => {
    const DataTableStreamStub = {
      props: ['data'],
      template: `
        <div>
          <div v-for="row in data" :key="row.request_id">
            <slot name="cell-stream" :row="row" />
          </div>
        </div>
      `,
    }
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          { ...baseImageRow, request_id: 'req-refusal-stream', request_type: 'claude_refusal', stream: true, actual_cost: 0, total_cost: 0 },
          { ...baseImageRow, request_id: 'req-refusal-sync', request_type: 'claude_refusal', stream: false, actual_cost: 0, total_cost: 0 },
          { ...baseImageRow, request_id: 'req-cyber', request_type: 'cyber', stream: false },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStreamStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const badges = wrapper.findAll('[data-testid="request-type-badge"]')
    expect(badges).toHaveLength(3)
    expect(badges[0].text()).toBe('Claude policy refusal')
    expect(badges[1].text()).toBe('Claude policy refusal')
    expect(badges[0].classes()).toContain('bg-orange-100')
    expect(badges[0].classes()).toContain('text-orange-800')
    // 与 cyber（红）、unknown/其它类型的颜色区分开
    expect(badges[2].classes()).toContain('bg-red-100')
    expect(badges[2].classes()).not.toContain('bg-orange-100')
  })

  it('shows service tier and billing breakdown in cost tooltip', async () => {
    const row = {
      request_id: 'req-admin-1',
      actual_cost: 0.092883,
      total_cost: 0.092883,
      account_rate_multiplier: 1,
      rate_multiplier: 1,
      service_tier: 'priority',
      input_cost: 0.020285,
      output_cost: 0.00303,
      cache_creation_cost: 0,
      cache_read_cost: 0.069568,
      input_tokens: 4057,
      output_tokens: 101,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const tooltipTriggers = wrapper.findAll('.group.relative')
    await tooltipTriggers[tooltipTriggers.length - 1].trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Service tier')
    expect(text).toContain('Fast')
    expect(text).toContain('Rate')
    expect(text).toContain('1.00x')
    expect(text).toContain('Account rate')
    expect(text).toContain('User billed')
    expect(text).toContain('Account billed')
    expect(text).toContain('$0.092883')
    expect(text).toContain('$5.0000 / 1M tokens')
    expect(text).toContain('$30.0000 / 1M tokens')
    expect(text).toContain('$0.069568')
  })

  describe('service tier multiplier rows', () => {
    // 后端口径：rate_multiplier = 分组倍率（不含档位倍率）；total_cost = 上游按档位实际收的价（已含官方档位比值）；
    // actual_cost = 标准价 × 档位倍率 × 分组倍率。下面都用同一个标准价 0.01136、分组倍率 0.16。
    // 无覆盖 Fast：官方 2×，total 就是标准价 × 2。
    const tierRow = {
      request_id: 'req-tier-multiplier',
      total_cost: 0.02272,
      rate_multiplier: 0.16,
      service_tier: 'priority',
      service_tier_multiplier: 2,
      actual_cost: 0.0036352,
      account_rate_multiplier: 1,
      input_cost: 0.01,
      output_cost: 0.01272,
      cache_creation_cost: 0,
      cache_read_cost: 0,
      input_tokens: 100,
      output_tokens: 100,
    }
    // 免费 Fast（分组覆盖 1×）：total 仍是 Fast 价，用户只付标准价 × 分组倍率。
    const freeFastRow = { ...tierRow, request_id: 'req-free-fast', service_tier_multiplier: 1, actual_cost: 0.0018176 }
    // UltraFast 官方 6×（total = 标准价 × 6），分组覆盖成 3×。
    const ultrafastOverrideRow = {
      ...tierRow, request_id: 'req-ultrafast-override', service_tier: 'ultrafast',
      total_cost: 0.06816, service_tier_multiplier: 3, actual_cost: 0.0054528,
    }

    // 气泡左列是标签、右列是数字：返回「标签 → 数字」，断言行的顺序和内容。
    // admin = 管理端（默认 props，带账号计费 / 上游成本）；用户端传 showAccountBilling=false。
    // labels / line / value = 下半部分的倍率、标准价、扣费行（gap-6）；block = 上半部分「费用明细」里的金额和单价（gap-4）。
    async function openTooltip(row: Record<string, unknown>, { admin = false } = {}) {
      const wrapper = mount(UsageTable, {
        props: { data: [row as never], loading: false, columns: [], ...(admin ? {} : { showAccountBilling: false }) },
        global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
      })
      const triggers = wrapper.findAll('.group.relative')
      await triggers[triggers.length - 1].trigger('mouseenter')
      await nextTick()
      const rows = wrapper.get('.fixed').findAll('div.flex.items-center.justify-between.gap-6')
      const line = (label: string) => rows.find((item) => item.findAll('span')[0].text() === label)
      const blockRows = wrapper.get('.fixed').findAll('div.flex.items-center.justify-between.gap-4')
      return {
        wrapper,
        labels: rows.map((item) => item.findAll('span')[0].text()),
        line,
        value: (label: string) => line(label)?.findAll('span')[1].text(),
        heading: wrapper.get('[data-testid="usage-cost-details-title"]').text(),
        block: Object.fromEntries(blockRows.map((item) => [item.findAll('span')[0].text(), item.findAll('span')[1].text()])),
      }
    }

    // 例：无覆盖 Fast 行 total 已含官方 2×，不能拿 total 当「原始」去乘综合倍率
    it('Fast without an override: shows the standard price, so standard price x combined rate = user billed', async () => {
      const { labels, value } = await openTooltip(tierRow)

      // 顺序：服务档位 → 档位倍率 → 分组倍率 → 综合倍率 → 原始（标准价）→ 用户扣费；用户端没有「上游成本」
      expect(labels).toEqual([
        'Service tier', 'Tier multiplier', 'Group rate', 'Combined rate', 'Original (Standard price)', 'User billed',
      ])
      expect(value('Service tier')).toBe('Fast')
      expect(value('Tier multiplier')).toBe('2×')
      expect(value('Group rate')).toBe('0.16×')
      expect(value('Combined rate')).toBe('0.32×')
      expect(value('Original (Standard price)')).toBe('$0.01136000')
      expect(value('User billed')).toBe('$0.00363520')
    })

    it('free Fast (override 1x): total stays the Fast price but the standard price and the charge are lower', async () => {
      const { labels, value } = await openTooltip(freeFastRow)
      expect(labels).toEqual([
        'Service tier', 'Tier multiplier', 'Group rate', 'Combined rate', 'Original (Standard price)', 'User billed',
      ])
      expect(value('Tier multiplier')).toBe('1×')
      expect(value('Group rate')).toBe('0.16×')
      expect(value('Combined rate')).toBe('0.16×')
      expect(value('Original (Standard price)')).toBe('$0.01136000')
      expect(value('User billed')).toBe('$0.00181760')
    })

    it('UltraFast with an override', async () => {
      const { value } = await openTooltip(ultrafastOverrideRow)
      expect(value('Service tier')).toBe('Ultrafast')
      expect(value('Tier multiplier')).toBe('3×')
      expect(value('Group rate')).toBe('0.16×')
      expect(value('Combined rate')).toBe('0.48×')
      expect(value('Original (Standard price)')).toBe('$0.01136000')
      expect(value('User billed')).toBe('$0.00545280')
    })

    it('matches the owner reference: Ultrafast, tier 1x, group 0.145x, charge $0.00033530', async () => {
      const { value } = await openTooltip({
        ...tierRow, service_tier: 'ultrafast', service_tier_multiplier: 1,
        rate_multiplier: 0.145, total_cost: 0.0138744, actual_cost: 0.000335298,
      })
      expect(value('Tier multiplier')).toBe('1×')
      expect(value('Group rate')).toBe('0.145×')
      expect(value('Combined rate')).toBe('0.145×')
      expect(value('Original (Standard price)')).toBe('$0.00231240')
      expect(value('User billed')).toBe('$0.00033530')
    })

    // 档位倍率 / 分组倍率 / 综合倍率：至多 4 位小数、去掉多余的 0（1 → 1×，0.145 → 0.145×，2.5 → 2.5×），不补零
    it.each([
      { service_tier: 'priority', rate_multiplier: 0.16, service_tier_multiplier: 2, tier: 'Fast', group: '0.16×', multiplier: '2×', combined: '0.32×' },
      { service_tier: 'fast', rate_multiplier: 0.16, service_tier_multiplier: 2.5, tier: 'Fast', group: '0.16×', multiplier: '2.5×', combined: '0.4×' },
      { service_tier: 'ultrafast', rate_multiplier: 0.16, service_tier_multiplier: 6, tier: 'Ultrafast', group: '0.16×', multiplier: '6×', combined: '0.96×' },
      { service_tier: 'priority', rate_multiplier: 1, service_tier_multiplier: 1, tier: 'Fast', group: '1×', multiplier: '1×', combined: '1×' },
      // 0.1 × 3 在浮点里是 0.30000000000000004，必须抹掉浮点噪声
      { service_tier: 'priority', rate_multiplier: 0.1, service_tier_multiplier: 3, tier: 'Fast', group: '0.1×', multiplier: '3×', combined: '0.3×' },
      { service_tier: 'priority', rate_multiplier: 1.2345, service_tier_multiplier: 3, tier: 'Fast', group: '1.2345×', multiplier: '3×', combined: '3.7035×' },
    ])('shows $tier tier $multiplier x group $group = $combined, standard price unchanged', async ({ tier, group, multiplier, combined, ...patch }) => {
      // 标准价固定 0.01136，实付 = 标准价 × 档位倍率 × 分组倍率
      const actual = 0.01136 * patch.service_tier_multiplier * patch.rate_multiplier
      const { value } = await openTooltip({ ...tierRow, ...patch, actual_cost: actual })
      expect(value('Service tier')).toBe(tier)
      expect(value('Tier multiplier')).toBe(multiplier)
      expect(value('Group rate')).toBe(group)
      expect(value('Combined rate')).toBe(combined)
      expect(value('Original (Standard price)')).toBe('$0.01136000')
    })

    it('admin bubble adds an upstream cost row (= total_cost) with a reconciliation note; the user bubble does not', async () => {
      const admin = await openTooltip(freeFastRow, { admin: true })
      // 上游成本在用户扣费之后、账号计费之前
      expect(admin.labels).toEqual([
        'Service tier', 'Tier multiplier', 'Group rate', 'Combined rate', 'Original (Standard price)', 'User billed',
        'Upstream cost', 'Account rate', 'Account billed',
      ])
      expect(admin.value('Upstream cost')).toBe('$0.02272000')
      expect(admin.wrapper.get('[data-testid="usage-upstream-cost"]').text()).toContain('Upstream tier price actually charged; reconciliation only')
      // 账号计费那一块照旧（仍按 total_cost × 账号倍率），只是分隔线挪到了上游成本上
      expect(admin.value('Account billed')).toBe('$0.02272000')
      expect(admin.wrapper.get('[data-testid="usage-upstream-cost"]').classes()).toContain('border-t')
      expect(admin.line('Account rate')!.classes()).not.toContain('border-t')
      admin.wrapper.unmount()

      const user = await openTooltip(freeFastRow)
      expect(user.labels).not.toContain('Upstream cost')
      expect(user.wrapper.find('[data-testid="usage-upstream-cost"]').exists()).toBe(false)
      // 用户端连数字都不能出现：total_cost 只有管理端能看到
      expect(user.wrapper.get('.fixed').text()).not.toContain('0.02272')
    })

    // 分组倍率 × 档位倍率 ≤ 0 时反推标准价会除零：整行退回老样子，不出现新行、不出现 NaN / Infinity
    it.each([
      // 老样子的「倍率」沿用 `rate_multiplier || 1`，所以 0 / 缺失都显示 1.00x
      { name: 'group rate 0', patch: { rate_multiplier: 0 }, rate: '1.00x' },
      { name: 'group rate missing', patch: { rate_multiplier: undefined }, rate: '1.00x' },
      { name: 'product underflows to 0', patch: { rate_multiplier: 1e-200, service_tier_multiplier: 1e-200 }, rate: undefined },
    ])('falls back to the old bubble when group rate x tier multiplier is not positive: $name', async ({ patch, rate }) => {
      for (const admin of [false, true]) {
        const { wrapper, labels, value } = await openTooltip({ ...tierRow, ...patch }, { admin })
        expect(labels.slice(0, 4)).toEqual(['Service tier', 'Rate', 'Original', 'User billed'])
        // 老样子：原始 = total_cost
        expect(value('Original')).toBe('$0.02272000')
        if (rate) expect(value('Rate')).toBe(rate)
        for (const testid of ['usage-tier-multiplier', 'usage-combined-multiplier', 'usage-standard-price', 'usage-upstream-cost']) {
          expect(wrapper.find(`[data-testid="${testid}"]`).exists(), testid).toBe(false)
        }
        const text = wrapper.get('.fixed').text()
        expect(text).not.toContain('NaN')
        expect(text).not.toContain('Infinity')
        wrapper.unmount()
      }
    })

    // 老数据（后端还没记录档位倍率）：气泡和以前一模一样，「原始」仍是 total_cost，管理端也没有「上游成本」
    it('keeps the old tooltip for rows without a recorded tier multiplier', async () => {
      for (const patch of [
        { service_tier_multiplier: null },
        { service_tier_multiplier: undefined },
        { service_tier_multiplier: 0 },
      ]) {
        const user = await openTooltip({ ...tierRow, ...patch })
        expect(user.labels).toEqual(['Service tier', 'Rate', 'Original', 'User billed'])
        expect(user.value('Rate')).toBe('0.16x')
        expect(user.value('Original')).toBe('$0.02272000')
        user.wrapper.unmount()

        const admin = await openTooltip({ ...tierRow, ...patch }, { admin: true })
        expect(admin.labels).toEqual(['Service tier', 'Rate', 'Original', 'User billed', 'Account rate', 'Account billed'])
        // 账号倍率这一行仍自带分隔线（和改动前一样）
        expect(admin.line('Account rate')!.classes()).toEqual(expect.arrayContaining(['border-t', 'pt-1.5']))
        for (const testid of ['usage-tier-multiplier', 'usage-combined-multiplier', 'usage-standard-price', 'usage-upstream-cost']) {
          expect(admin.wrapper.find(`[data-testid="${testid}"]`).exists(), testid).toBe(false)
        }
        admin.wrapper.unmount()
      }
    })

    it.each([null, 'standard', 'default', 'flex'])('ignores a stray tier multiplier on a %s row', async (serviceTier) => {
      const { wrapper, labels, value } = await openTooltip({ ...tierRow, service_tier: serviceTier, service_tier_multiplier: 2 })
      expect(labels).toEqual(['Service tier', 'Rate', 'Original', 'User billed'])
      expect(value('Original')).toBe('$0.02272000')
      expect(wrapper.find('[data-testid="usage-tier-multiplier"]').exists()).toBe(false)
    })

    it('does not add tier rows to fixed-price tool calls', async () => {
      const { wrapper, labels, heading, block } = await openTooltip({
        ...tierRow,
        inbound_endpoint: '/v1/tools/search',
        rate_multiplier: 1,
        input_tokens: 0,
        output_tokens: 0,
        cache_creation_tokens: 0,
        cache_read_tokens: 0,
      })
      expect(wrapper.find('[data-testid="fixed-tool-billing"]').exists()).toBe(true)
      expect(labels).not.toContain('Tier multiplier')
      expect(labels).not.toContain('Combined rate')
      expect(labels).not.toContain('Original (Standard price)')
      // 固定计费的工具调用没有服务档位：费用明细也不缩放（这行的分项之和恰好等于 total_cost，守卫只靠「工具调用没有档位」）
      expect(heading).toBe('Cost Breakdown')
      expect(block['Input Cost']).toBe('$0.01000000')
    })

    // 「费用明细」首块（输入 / 输出 / 缓存费用和单价）：Fast / UltraFast 行后端存的是上游按档位实际收的价，
    // 气泡里要换成标准价口径，才和档位倍率、原始（标准价）、用户扣费对得上。
    describe('cost breakdown block shows the Standard price on Fast / UltraFast rows', () => {
      // 线上核对过的一行：gpt-6.1-sol，UltraFast，分组把 UltraFast 档位倍率设成 1×（免费 UltraFast），分组倍率 0.16。
      // 标准价 1313 × 2e-6 + 615 × 1e-5 = 0.008776，用户实付 0.008776 × 1 × 0.16 = 0.00140416；
      // total_cost 和分项存的是上游 UltraFast 官方 6 倍价（12 / 60 美元每百万 token）。
      const freeUltrafastRow = {
        request_id: 'req-free-ultrafast',
        model: 'gpt-6.1-sol',
        service_tier: 'ultrafast',
        service_tier_multiplier: 1,
        rate_multiplier: 0.16,
        account_rate_multiplier: 1,
        input_tokens: 1313,
        output_tokens: 615,
        cache_creation_tokens: 0,
        cache_read_tokens: 0,
        input_cost: 0.015756,
        output_cost: 0.0369,
        cache_creation_cost: 0,
        cache_read_cost: 0,
        total_cost: 0.052656,
        actual_cost: 0.00140416,
      }
      const standardBlock = {
        'Input Cost': '$0.00262600',
        'Output Cost': '$0.00615000',
        'Input price': '$2.0000 / 1M tokens',
        'Output price': '$10.0000 / 1M tokens',
      }
      const storedBlock = {
        'Input Cost': '$0.01575600',
        'Output Cost': '$0.03690000',
        'Input price': '$12.0000 / 1M tokens',
        'Output price': '$60.0000 / 1M tokens',
      }
      const money = (text?: string) => Number(text?.replace(/[$×]/g, ''))

      it('free UltraFast, customer bubble: amounts and unit prices are at the Standard price, not the upstream 6x tier price', async () => {
        const { wrapper, heading, block, labels, value } = await openTooltip(freeUltrafastRow)

        expect(heading).toBe('Cost Breakdown (Standard price)')
        expect(block).toEqual(standardBlock)
        expect(labels).toEqual([
          'Service tier', 'Tier multiplier', 'Group rate', 'Combined rate', 'Original (Standard price)', 'User billed',
        ])
        expect(value('Tier multiplier')).toBe('1×')
        expect(value('Group rate')).toBe('0.16×')
        expect(value('Combined rate')).toBe('0.16×')
        expect(value('Original (Standard price)')).toBe('$0.00877600')
        expect(value('User billed')).toBe('$0.00140416')
        // 上游 6 倍价在用户端的气泡里一个数字都不能出现
        const text = wrapper.get('.fixed').text()
        for (const upstream of ['0.015756', '0.0369', '12.0000', '60.0000', '0.052656']) {
          expect(text, upstream).not.toContain(upstream)
        }
      })

      it('free UltraFast, admin bubble: same Standard-price breakdown, the upstream 6x tier price lives on the Upstream cost row', async () => {
        const { wrapper, heading, block, labels, value } = await openTooltip(freeUltrafastRow, { admin: true })

        expect(heading).toBe('Cost Breakdown (Standard price)')
        expect(block).toEqual(standardBlock)
        expect(labels).toEqual([
          'Service tier', 'Tier multiplier', 'Group rate', 'Combined rate', 'Original (Standard price)', 'User billed',
          'Upstream cost', 'Account rate', 'Account billed',
        ])
        expect(value('Original (Standard price)')).toBe('$0.00877600')
        expect(value('User billed')).toBe('$0.00140416')
        expect(value('Upstream cost')).toBe('$0.05265600')
        expect(wrapper.get('[data-testid="usage-upstream-cost"]').text()).toContain('Upstream tier price actually charged; reconciliation only')
        expect(value('Account billed')).toBe('$0.05265600')
      })

      // 同一份 token、同一个标准价 0.008776：不同档位 / 倍率组合下，气泡里的数字都要自洽
      it.each([
        {
          name: 'Fast official 2x, group 1 (no override)',
          row: { ...freeUltrafastRow, service_tier: 'priority', service_tier_multiplier: 2, rate_multiplier: 1, input_cost: 0.005252, output_cost: 0.0123, total_cost: 0.017552, actual_cost: 0.017552 },
          tier: 'Fast', tierX: '2×', group: '1×', combined: '2×', billed: '$0.01755200',
        },
        {
          name: 'Fast official 2x, group 0.16 (no override)',
          row: { ...freeUltrafastRow, service_tier: 'priority', service_tier_multiplier: 2, input_cost: 0.005252, output_cost: 0.0123, total_cost: 0.017552, actual_cost: 0.00280832 },
          tier: 'Fast', tierX: '2×', group: '0.16×', combined: '0.32×', billed: '$0.00280832',
        },
        {
          name: 'free Fast (override 1x), group 0.16',
          row: { ...freeUltrafastRow, service_tier: 'priority', input_cost: 0.005252, output_cost: 0.0123, total_cost: 0.017552, actual_cost: 0.00140416 },
          tier: 'Fast', tierX: '1×', group: '0.16×', combined: '0.16×', billed: '$0.00140416',
        },
        {
          name: 'UltraFast official 6x, group 0.16 (no override)',
          row: { ...freeUltrafastRow, service_tier_multiplier: 6, actual_cost: 0.00842496 },
          tier: 'Ultrafast', tierX: '6×', group: '0.16×', combined: '0.96×', billed: '$0.00842496',
        },
        {
          name: 'UltraFast override 3x, group 0.16',
          row: { ...freeUltrafastRow, service_tier_multiplier: 3, actual_cost: 0.00421248 },
          tier: 'Ultrafast', tierX: '3×', group: '0.16×', combined: '0.48×', billed: '$0.00421248',
        },
      ])('$name: components add up to the Standard price, Standard price x combined rate = user billed', async ({ row, tier, tierX, group, combined, billed }) => {
        const { heading, block, value } = await openTooltip(row)

        expect(heading).toBe('Cost Breakdown (Standard price)')
        expect(block).toEqual(standardBlock)
        expect(value('Service tier')).toBe(tier)
        expect(value('Tier multiplier')).toBe(tierX)
        expect(value('Group rate')).toBe(group)
        expect(value('Combined rate')).toBe(combined)
        expect(value('Original (Standard price)')).toBe('$0.00877600')
        expect(value('User billed')).toBe(billed)
        // 屏幕上的数自己也要对得上（每个数 toFixed(8)，最多差 1e-8）
        expect(Math.abs(money(block['Input Cost']) + money(block['Output Cost']) - money(value('Original (Standard price)')))).toBeLessThan(1e-8)
        expect(Math.abs(money(value('Original (Standard price)')) * money(value('Combined rate')) - money(value('User billed')))).toBeLessThan(1e-8)
      })

      it('cache creation and cache read amounts are at the Standard price too', async () => {
        // Fast 官方 2x、分组倍率 0.5。标准价：输入 1000 × 2e-6、输出 200 × 1e-5、缓存写 500 × 2.5e-6、缓存读 8000 × 1e-7
        const { heading, block, value } = await openTooltip({
          request_id: 'req-fast-cache',
          service_tier: 'priority',
          service_tier_multiplier: 2,
          rate_multiplier: 0.5,
          account_rate_multiplier: 1,
          input_tokens: 1000,
          output_tokens: 200,
          cache_creation_tokens: 500,
          cache_read_tokens: 8000,
          input_cost: 0.004,
          output_cost: 0.004,
          cache_creation_cost: 0.0025,
          cache_read_cost: 0.0016,
          total_cost: 0.0121,
          actual_cost: 0.00605,
        })

        expect(heading).toBe('Cost Breakdown (Standard price)')
        expect(block).toEqual({
          'Input Cost': '$0.00200000',
          'Output Cost': '$0.00200000',
          'Input price': '$2.0000 / 1M tokens',
          'Output price': '$10.0000 / 1M tokens',
          'Cache Creation Cost': '$0.00125000',
          'Cache Read Cost': '$0.00080000',
        })
        expect(value('Original (Standard price)')).toBe('$0.00605000')
      })

      // 不能证明分项是纯 token 档位价时，整块保持后端存的金额（和以前一样），标题也不写「标准价」
      it.each([
        { name: 'a per-call add-on sits on top of the token amounts', patch: { total_cost: 0.062656 }, input: '$0.01575600' },
        { name: 'image input is priced separately', patch: { image_input_tokens: 100, image_input_cost: 0.0012, input_cost: 0.014556 }, input: '$0.01455600' },
        { name: 'nothing was charged', patch: { actual_cost: 0 }, input: '$0.01575600' },
        { name: 'total_cost is 0', patch: { total_cost: 0 }, input: '$0.01575600' },
      ])('keeps the stored amounts and the plain title when the row is not a pure token tier price: $name', async ({ patch, input }) => {
        const { heading, block, labels } = await openTooltip({ ...freeUltrafastRow, ...patch })

        expect(heading).toBe('Cost Breakdown')
        expect(block['Input Cost']).toBe(input)
        expect(block['Output Cost']).toBe('$0.03690000')
        expect(block['Output price']).toBe('$60.0000 / 1M tokens')
        // 下面的档位倍率 / 标准价行不受影响
        expect(labels).toContain('Tier multiplier')
      })

      it.each([
        { name: 'old row without a recorded tier multiplier', patch: { service_tier_multiplier: null } },
        { name: 'standard tier row', patch: { service_tier: 'standard' } },
        { name: 'flex tier row', patch: { service_tier: 'flex' } },
        { name: 'row without a tier', patch: { service_tier: null } },
      ])('keeps the stored amounts and the plain title: $name', async ({ patch }) => {
        const { heading, block } = await openTooltip({ ...freeUltrafastRow, ...patch })

        expect(heading).toBe('Cost Breakdown')
        expect(block).toEqual(storedBlock)
      })

      it('image-billed rows keep their image breakdown even when a tier multiplier is present', async () => {
        const { heading, block } = await openTooltip({ ...baseImageRow, service_tier: 'priority', service_tier_multiplier: 2 })

        expect(heading).toBe('Cost Breakdown')
        expect(block['Image count']).toBe('2 images')
        expect(block['Image total price']).toBe('$0.40000000')
      })
    })

    it('follows the hovered row when the tooltip moves to another request', async () => {
      const wrapper = mount(UsageTable, {
        props: {
          data: [
            { ...tierRow, request_id: 'req-with-tier' },
            { ...tierRow, request_id: 'req-old-row', service_tier_multiplier: null },
          ] as never[],
          loading: false,
          columns: [],
          showAccountBilling: false,
        },
        global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
      })
      // 每行两个触发点：[tokens 1, cost 1, tokens 2, cost 2]
      const triggers = wrapper.findAll('.group.relative')
      await triggers[1].trigger('mouseenter')
      await nextTick()
      expect(wrapper.text()).toContain('Combined rate')
      await triggers[3].trigger('mouseenter')
      await nextTick()
      expect(wrapper.text()).not.toContain('Combined rate')
    })
  })

  it.each(['/v1/tools/search', '/v1/tools/fetch'])('shows fixed billing only for zero-token independent tool %s', async (endpoint) => {
    const row = { ...baseImageRow, image_count: 0, billing_mode: 'token', inbound_endpoint: endpoint, actual_cost: .005, total_cost: .005 }
    const wrapper = mount(UsageTable, {
      props: { data: [row], loading: false, columns: [] },
      global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
    })
    const triggers = wrapper.findAll('.group.relative')
    await triggers[triggers.length - 1].trigger('mouseenter')
    await nextTick()
    expect(wrapper.get('[data-testid="fixed-tool-billing"]').text()).toContain('Fixed per call')
    expect(wrapper.text()).toContain('$0.00500000')
    await wrapper.setProps({ data: [{ ...row, inbound_endpoint: '/v1/responses', input_tokens: 10 }] })
    const updated = wrapper.findAll('.group.relative')
    await updated[updated.length - 1].trigger('mouseenter')
    await nextTick()
    expect(wrapper.find('[data-testid="fixed-tool-billing"]').exists()).toBe(false)
  })

  it('shows average output tokens per second beside latency', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput',
          output_tokens: 101,
          first_token_ms: 180,
          duration_ms: 1938,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('52.1 t/s')
  })

  it('shows the average even when the first token time is missing', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput-missing-first-token',
          output_tokens: 101,
          duration_ms: 1938,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('52.1 t/s')
  })

  it('hides output rate when first-token and total durations round to the same value', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput-rounded-window',
          output_tokens: 10,
          first_token_ms: 2710,
          duration_ms: 2714,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('t/s')
  })

  it('hides output rate for tiny output samples', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput-tiny-output',
          output_tokens: 19,
          first_token_ms: 180,
          duration_ms: 1938,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('t/s')
  })

  it('hides output rate for a very short request', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput-short-window',
          output_tokens: 100,
          first_token_ms: 60,
          duration_ms: 400,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('t/s')
  })

  it('hides output rate when the total duration is unknown', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-throughput-exceeds-output',
          output_tokens: 100,
          first_token_ms: 200,
          input_tokens: 10,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('t/s')
  })

  it.each(['token', 'image', 'per_request'])('keeps eight decimal places in %s cost details', async (billingMode) => {
    const row = {
      ...baseImageRow,
      billing_mode: billingMode,
      image_count: billingMode === 'image' ? 2 : 0,
      input_cost: 0.00000001,
      image_input_cost: 0.00000002,
      output_cost: 0.00000003,
      image_output_cost: 0.00000004,
      cache_creation_cost: 0.00000005,
      cache_read_cost: 0.00000006,
      total_cost: 0.00000022,
      actual_cost: 0.00000042,
      account_stats_cost: 0.00000012,
      account_rate_multiplier: 1.5,
    }
    const wrapper = mount(UsageTable, {
      props: { data: [row], loading: false, columns: [] },
      global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
    })
    const triggers = wrapper.findAll('.group.relative')
    await triggers[triggers.length - 1].trigger('mouseenter')
    const amounts = wrapper.get('.fixed').findAll('span').map(span => span.text())
    expect(amounts).toEqual(expect.arrayContaining([
      '$0.00000001', '$0.00000002', '$0.00000003', '$0.00000004',
      '$0.00000005', '$0.00000006', '$0.00000022', '$0.00000042', '$0.00000018',
    ]))
    if (billingMode === 'image') expect(amounts).toContain('$0.00000011')
    wrapper.unmount()
  })

  it('uses eight decimal places for missing cost values', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, billing_mode: 'per_request', image_count: 0, total_cost: undefined, actual_cost: undefined }],
        loading: false,
        columns: [],
      },
      global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
    })
    const triggers = wrapper.findAll('.group.relative')
    await triggers[triggers.length - 1].trigger('mouseenter')
    const amounts = wrapper.get('.fixed').findAll('span').map(span => span.text()).filter(text => text.startsWith('$'))
    expect(amounts).toEqual(['$0.00000000', '$0.00000000', '$0.00000000', '$0.00000000'])
    wrapper.unmount()
  })

  it('shows requested and upstream models separately for admin rows', () => {
    const row = {
      request_id: 'req-admin-model-1',
      model: 'claude-sonnet-4',
      upstream_model: 'claude-sonnet-4-20250514',
      actual_cost: 0,
      total_cost: 0,
      account_rate_multiplier: 1,
      rate_multiplier: 1,
      input_cost: 0,
      output_cost: 0,
      cache_creation_cost: 0,
      cache_read_cost: 0,
      input_tokens: 0,
      output_tokens: 0,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('claude-sonnet-4')
    expect(text).toContain('claude-sonnet-4-20250514')
  })

  it('shows requested and forwarded reasoning effort separately when they differ', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-admin-effort-1',
          model: 'gpt-5.4',
          reasoning_effort: 'max',
          upstream_reasoning_effort: 'xhigh',
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Max')
    expect(text).toContain('XHigh')
    expect(text).toContain('↳')
  })

  it('shows a single reasoning effort when requested matches forwarded', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-admin-effort-2',
          model: 'gpt-5.6-sol',
          reasoning_effort: 'max',
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Max')
    expect(text).not.toContain('↳')
  })

  it('hides mapped reasoning effort for user rows that only have the requested value', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-user-effort-1',
          model: 'gpt-5.4',
          reasoning_effort: 'max',
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Max')
    expect(wrapper.text()).not.toContain('XHigh')
    expect(wrapper.text()).not.toContain('↳')
  })

	it.each([
		{
			name: 'possible version variant',
			responseModel: 'gpt-5.5-2026-08-01',
			expectedBadge: 'Possible version variant',
		},
		{
			name: 'different upstream model',
			responseModel: 'gpt-5.4',
			expectedBadge: 'Different model',
		},
	])('shows a compact upstream response audit marker for $name', ({ responseModel, expectedBadge }) => {
		const wrapper = mount(UsageTable, {
			props: {
				data: [{
					request_id: `req-${responseModel}`,
					model: 'gpt-5.6-sol',
					upstream_model: 'gpt-5.5',
					model_mapping_chain: 'gpt-5.6-sol→gpt-5.5',
					upstream_response_model: responseModel,
					upstream_model_mismatch: true,
				}],
				loading: false,
				columns: [],
			},
			global: {
				stubs: {
					DataTable: DataTableStub,
					EmptyState: true,
					Icon: true,
					Teleport: true,
				},
			},
		})

		const text = wrapper.text()
		expect(text).toContain('gpt-5.6-sol')
		expect(text).toContain('gpt-5.5')
		expect(text).toContain(responseModel)
		expect(text).toContain(expectedBadge)
	})

  it.each([
    {
      name: 'defaulted row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-default-image',
        image_size: '2K',
        image_input_size: 'auto',
        image_output_size: null,
        image_size_source: 'default',
      },
      expected: ['2K', 'Default billing tier', 'auto', 'unknown'],
    },
    {
      name: 'output-sourced row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-output-image',
        image_size: '4K',
        image_input_size: '1024x1024',
        image_output_size: '3840x2160',
        image_size_source: 'output',
        image_size_breakdown: { '4K': 1 },
      },
      expected: ['4K', 'Upstream output', '1024x1024', '3840x2160', '4K x 1'],
    },
    {
      name: 'input-sourced row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-input-image',
        image_size: '1K',
        image_input_size: '1024x1024',
        image_output_size: null,
        image_size_source: 'input',
      },
      expected: ['1K', 'Request input', '1024x1024', 'unknown'],
    },
    {
      name: 'legacy unstandardized row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-legacy-unstandardized-image',
        image_size: '512x512',
        image_input_size: null,
        image_output_size: null,
        image_size_source: null,
      },
      expected: ['legacy unstandardized: 512x512', 'Legacy record', 'unknown'],
    },
  ])('shows image usage metadata for $name', async ({ row, expected }) => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await wrapper.find('.group.relative').trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Image count')
    expect(text).toContain('Billing size')
    expect(text).toContain('Size source')
    expect(text).toContain('Input size')
    expect(text).toContain('Output size')
    expect(text).toContain('Per-image price')
    expect(text).toContain('Image total price')
    for (const value of expected) {
      expect(text).toContain(value)
    }
  })

  it('displays historical image rows with missing billing_mode as image usage without a 2K fallback', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          {
            ...baseImageRow,
            request_id: 'req-admin-legacy-missing-image',
            billing_mode: null,
            image_size: null,
            image_input_size: null,
            image_output_size: null,
            image_size_source: null,
            image_size_breakdown: null,
          },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await wrapper.find('.group.relative').trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Image')
    expect(text).toContain('Image count')
    expect(text).toContain('Per-image price')
    expect(text).toContain('not recorded')
    expect(text).not.toContain('(2K)')
  })
})

describe('admin UsageTable request ID column', () => {
  beforeEach(() => {
    appStoreMocks.showSuccess.mockReset()
    appStoreMocks.showError.mockReset()
  })

  it('renders and copies the request ID', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    // useClipboard 只在安全上下文（HTTPS/localhost）走 navigator.clipboard。
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })

    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: 'req-admin-visible-id' }],
        loading: false,
        columns: [{ key: 'request_id', label: 'Request ID' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('req-admin-visible-id')
    await wrapper.get('button[title="Copy to clipboard"]').trigger('click')

    expect(writeText).toHaveBeenCalledWith('req-admin-visible-id')
    expect(appStoreMocks.showSuccess).toHaveBeenCalledWith('Request ID copied')
  })

  it('copies over HTTP (insecure context) via the execCommand fallback', async () => {
    // 回归：IP 直连 HTTP 下 navigator.clipboard 不存在，旧实现直接抛错、点了没反应。
    const execCommand = vi.fn().mockReturnValue(true)
    vi.stubGlobal('navigator', {})
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true })

    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: 'req-http-fallback' }],
        loading: false,
        columns: [{ key: 'request_id', label: 'Request ID' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await wrapper.get('button[title="Copy to clipboard"]').trigger('click')

    expect(execCommand).toHaveBeenCalledWith('copy')
    expect(appStoreMocks.showSuccess).toHaveBeenCalledWith('Request ID copied')
  })

  it('renders and copies the upstream ID', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })

    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: '', upstream_request_id: '20260903082826779695' }],
        loading: false,
        columns: [{ key: 'upstream_request_id', label: 'Upstream ID' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('20260903082826779695')
    const copyButtons = wrapper.findAll('button[title="Copy to clipboard"]')
    expect(copyButtons).toHaveLength(1)
    await copyButtons[0].trigger('click')

    expect(writeText).toHaveBeenCalledWith('20260903082826779695')
    expect(appStoreMocks.showSuccess).toHaveBeenCalledWith('Upstream ID copied')
  })

  it('renders labeled X-Request-Id, X-Client-Request-Id, and response_msg_id with click-to-search and copy', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })

    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          ...baseImageRow,
          request_id: 'client:564966d3-8995-44d3-bc78-b5730b19a922',
          response_request_id: '4276cee5-69e9-497a-9318-f99c84625bac',
          client_request_id: '564966d3-8995-44d3-bc78-b5730b19a922',
          response_msg_id: 'msg_011ChSwsWaLgTDX3Ay9jabZw',
        }],
        loading: false,
        columns: [
          { key: 'request_id', label: 'Request ID' },
          { key: 'response_msg_id', label: 'Message ID' },
        ],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.get('[data-testid="usage-response-request-id"]').text()).toBe('4276cee5-69e9-497a-9318-f99c84625bac')
    expect(wrapper.get('[data-testid="usage-client-request-id"]').text()).toBe('564966d3-8995-44d3-bc78-b5730b19a922')
    expect(wrapper.get('[data-testid="usage-response-msg-id"]').text()).toBe('msg_011ChSwsWaLgTDX3Ay9jabZw')

    await wrapper.get('[data-testid="usage-response-msg-id"]').trigger('click')
    expect(wrapper.emitted('requestIdClick')?.[0]).toEqual(['msg_011ChSwsWaLgTDX3Ay9jabZw'])

    await wrapper.get('[data-testid="usage-response-request-id"]').trigger('click')
    expect(wrapper.emitted('requestIdClick')?.[1]).toEqual(['4276cee5-69e9-497a-9318-f99c84625bac'])

    const copyButtons = wrapper.findAll('button[title="Copy to clipboard"]')
    expect(copyButtons).toHaveLength(3)
    await copyButtons[2].trigger('click')
    expect(writeText).toHaveBeenCalledWith('msg_011ChSwsWaLgTDX3Ay9jabZw')
    expect(appStoreMocks.showSuccess).toHaveBeenCalledWith('Message ID copied')
  })
})
