<template>
  <AppLayout>
    <div class="ops-center flex flex-col gap-6">
      <AdminPageHeader
        :description="text('账号资产、渠道健康与告警集中在一个入口，按账号 / 渠道 / 诊断三层分组。', 'Account assets, channel health and alerts in one place, grouped into account, channel and diagnostics tiers.')"
      >
        <template #title>
          <span class="inline-flex flex-wrap items-center gap-2">
            {{ text('运营中心', 'Operations center') }}
            <UBadge color="neutral" variant="subtle" size="sm">{{ text('L0 总览', 'L0 overview') }}</UBadge>
          </span>
        </template>
        <template #actions>
          <UButton to="/admin/account-ops" color="neutral" variant="outline" icon="i-lucide-bell-ring" :label="text('账号通知设置', 'Notification settings')" />
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="loading" :label="text('刷新', 'Refresh')" @click="refresh" />
        </template>
      </AdminPageHeader>

      <UAlert v-if="error" role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="error" />
      <UAlert
        v-if="observer"
        color="neutral"
        variant="subtle"
        icon="i-lucide-info"
        :description="text('账号资产与告警需要主管理员权限，当前只显示系统统计。', 'Account assets and alerts require the primary administrator; only system totals are shown.')"
      />

      <UPageGrid as="section" class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px" :aria-label="text('运营概览', 'Operations overview')">
        <UPageCard icon="i-lucide-wallet" :title="text('上游总余额', 'Total upstream balance')" variant="subtle" :ui="statUi" :class="statClass">
          <div class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
            <span v-for="total in balanceTotals.slice(0, 2)" :key="total.unit" class="text-2xl font-semibold tabular-nums text-highlighted">
              {{ formatMoney(total.total, total.unit) }}
            </span>
            <span v-if="!balanceTotals.length" class="text-2xl font-semibold text-dimmed">—</span>
          </div>
          <p class="text-xs text-muted">
            {{ accountsWithBalance }} {{ text('个账号有余额快照', 'accounts with a balance snapshot') }}<template v-if="balanceTotals.length > 2"> · +{{ balanceTotals.length - 2 }} {{ text('种币种', 'more currencies') }}</template>
          </p>
        </UPageCard>

        <UPageCard icon="i-lucide-chart-no-axes-column" :title="text('今日消耗', 'Spend today')" variant="subtle" :ui="statUi" :class="statClass">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">${{ formatCost(stats?.today_actual_cost) }}</span>
          <p class="text-xs text-muted">{{ text('今日请求', 'Requests today') }} {{ stats?.today_requests ?? 0 }}</p>
        </UPageCard>

        <UPageCard icon="i-lucide-users" :title="text('账号健康', 'Account health')" variant="subtle" :ui="statUi" :class="statClass">
          <span class="text-2xl font-semibold tabular-nums text-highlighted">
            {{ stats?.normal_accounts ?? 0 }}
            <span class="text-sm font-normal text-dimmed">/ {{ stats?.total_accounts ?? 0 }}</span>
          </span>
          <p class="text-xs text-muted">
            {{ text('异常', 'Error') }} {{ stats?.error_accounts ?? 0 }} · {{ text('限流', 'Rate limited') }} {{ stats?.ratelimit_accounts ?? 0 }} · {{ text('过载', 'Overloaded') }} {{ stats?.overload_accounts ?? 0 }}
          </p>
        </UPageCard>

        <UPageCard icon="i-lucide-bell" :title="text('待处理', 'Needs attention')" variant="subtle" :ui="statUi" :class="statClass">
          <div class="flex items-center gap-2">
            <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ attentionAll.length }}</span>
            <UBadge v-if="attentionAll.length" color="warning" variant="subtle" size="sm">{{ text('需处理', 'Open') }}</UBadge>
          </div>
          <p class="text-xs text-muted">
            {{ text('告警', 'Alerts') }} {{ pendingEvents }} · {{ text('资产风险', 'Asset risks') }} {{ riskAccounts.length }} · {{ text('通知', 'Notifications') }} {{ settings ? (settings.config.enabled ? text('已开启', 'on') : text('未开启', 'off')) : '—' }}
          </p>
        </UPageCard>
      </UPageGrid>

      <section class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-base font-semibold text-highlighted">{{ text('需要处理', 'Needs attention') }}</h2>
            <UTooltip :text="text('通知失败、余额读取失败或额度即将用尽。', 'Failed notifications, unreadable balances or nearly exhausted quotas.')">
              <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
            </UTooltip>
          </div>
          <UButton to="/admin/account-ops" color="neutral" variant="ghost" size="sm" trailing-icon="i-lucide-arrow-right" :label="text('打开账号通知', 'Open account notifications')" />
        </div>
        <UTable
          :data="attention"
          :columns="attentionColumns"
          :empty="loading ? text('加载中…', 'Loading…') : text('当前没有需要处理的账号。', 'Nothing needs attention right now.')"
          :ui="tableUi"
        >
          <template #item-cell="{ row }">
            <div class="flex min-w-0 items-start gap-2.5">
              <UBadge :color="row.original.severity === 'critical' ? 'error' : 'warning'" variant="subtle" size="sm" class="mt-0.5 shrink-0">
                {{ row.original.severity === 'critical' ? text('严重', 'Critical') : text('提醒', 'Warning') }}
              </UBadge>
              <div class="min-w-0">
                <p class="text-sm font-medium text-highlighted">{{ row.original.title }}</p>
                <p class="mt-0.5 truncate text-xs text-muted">{{ row.original.detail }}</p>
              </div>
            </div>
          </template>
          <template #account-cell="{ row }">
            <span class="text-xs tabular-nums text-muted">#{{ row.original.accountId }}</span>
          </template>
          <template #action-cell="{ row }">
            <div class="flex justify-end">
              <UButton :to="row.original.href" color="neutral" variant="ghost" size="xs" :label="text('处理', 'Resolve')" />
            </div>
          </template>
        </UTable>
      </section>

      <section class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-base font-semibold text-highlighted">{{ text('账号资产', 'Account assets') }}</h2>
            <UTooltip :text="text('余额、5 小时与 7 天额度，按风险优先排序。', 'Balances, 5-hour and 7-day quotas, ordered by risk.')">
              <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
            </UTooltip>
          </div>
          <div class="flex flex-wrap items-center gap-1.5">
            <UInput v-model="search" icon="i-lucide-search" class="w-full sm:w-56" :placeholder="text('搜索账号 / 分组', 'Search account or group')" :aria-label="text('搜索账号', 'Search accounts')" />
            <USelect
              v-model="riskFilter"
              class="w-32"
              :items="[
                { value: 'all', label: text('全部', 'All') },
                { value: 'risk', label: text('需关注', 'Needs attention') },
                { value: 'ok', label: text('正常', 'Healthy') }
              ]"
              :aria-label="text('筛选状态', 'Filter status')"
            />
            <USelect
              v-model="sortMode"
              class="w-32"
              :items="[
                { value: 'risk', label: text('按风险', 'By risk') },
                { value: 'balance', label: text('按余额', 'By balance') },
                { value: 'name', label: text('按名称', 'By name') }
              ]"
              :aria-label="text('排序方式', 'Sort order')"
            />
          </div>
        </div>

        <UAlert
          v-if="observer"
          color="neutral"
          variant="subtle"
          icon="i-lucide-lock"
          :description="text('需要主管理员权限才能查看账号资产。', 'Primary administrator access is required to view account assets.')"
        />
        <template v-else>
          <UTable
            :data="pageRows"
            :columns="assetColumns"
            :get-row-id="(row: AccountRow) => String(row.account.account_id)"
            :empty="loading ? text('加载中…', 'Loading…') : text('没有匹配的账号。', 'No accounts match the filters.')"
            :ui="tableUi"
          >
            <template #account-cell="{ row }">
              <p class="font-medium text-highlighted">{{ row.original.account.account_name }}</p>
              <p class="mt-0.5 text-xs text-muted">
                #{{ row.original.account.account_id }}<template v-if="row.original.account.group_name"> · {{ row.original.account.group_name }}</template>
              </p>
            </template>
            <template #platform-cell="{ row }">
              <p class="text-default">{{ row.original.account.platform }}</p>
              <p class="mt-0.5 text-xs text-muted">{{ row.original.account.type === 'oauth' ? 'OAuth' : 'API Key' }}</p>
            </template>
            <template #balance-cell="{ row }">
              <div class="flex flex-wrap items-center gap-2">
                <span class="tabular-nums text-default">{{ row.original.balance }}</span>
                <UBadge :color="row.original.statusColor" variant="subtle" size="sm">{{ row.original.statusLabel }}</UBadge>
              </div>
            </template>
            <template #w5h-cell="{ row }">
              <div v-if="row.original.windows[0]" class="flex items-center gap-2">
                <UProgress :model-value="row.original.windows[0].percent" :color="row.original.windows[0].color" size="sm" class="w-16" />
                <span class="text-xs tabular-nums text-toned">{{ row.original.windows[0].label }}</span>
              </div>
              <span v-else class="text-xs text-dimmed">—</span>
            </template>
            <template #w7d-cell="{ row }">
              <div v-if="row.original.windows[1]" class="flex items-center gap-2">
                <UProgress :model-value="row.original.windows[1].percent" :color="row.original.windows[1].color" size="sm" class="w-16" />
                <span class="text-xs tabular-nums text-toned">{{ row.original.windows[1].label }}</span>
              </div>
              <span v-else class="text-xs text-dimmed">—</span>
            </template>
            <template #observed-cell="{ row }">
              <span class="text-xs text-muted">{{ date(row.original.account.received_at) }}</span>
            </template>
          </UTable>
          <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
            <span class="text-sm text-muted">{{ text('显示', 'Showing') }} {{ rangeStart }}–{{ rangeEnd }} / {{ filteredAccounts.length }}</span>
            <UPagination v-model:page="page" :items-per-page="pageSize" :total="filteredAccounts.length" />
          </div>
        </template>
      </section>

      <section class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-1.5">
            <h2 class="text-base font-semibold text-highlighted">{{ text('功能分级', 'Feature tiers') }}</h2>
            <UTooltip :text="text('从账号到渠道再到诊断，逐层下钻。', 'Drill down from accounts to channels, then diagnostics.')">
              <UIcon name="i-lucide-info" class="size-4 text-dimmed" />
            </UTooltip>
          </div>
          <span class="text-xs text-muted">{{ text('侧边栏同名分组可直接展开', 'The sidebar group expands to the same links') }}</span>
        </div>
        <UPageGrid class="gap-4 lg:grid-cols-3">
          <UPageCard
            v-for="tier in tiers"
            :key="tier.level"
            :description="tier.description"
            variant="subtle"
            :ui="{ container: 'gap-y-3 sm:p-5', description: 'text-xs' }"
          >
            <template #title>
              <span class="flex items-center gap-2">
                <UBadge color="neutral" variant="subtle" size="sm">{{ tier.level }}</UBadge>
                <span class="text-base font-semibold text-highlighted">{{ tier.title }}</span>
              </span>
            </template>
            <ul class="-mx-2 flex flex-col gap-0.5">
              <li v-for="item in tier.items" :key="item.to">
                <RouterLink :to="item.to" class="group flex items-center gap-3 rounded-md px-2 py-1.5 transition-colors hover:bg-elevated/60">
                  <UIcon :name="item.icon" class="size-4 shrink-0 text-muted" />
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium text-default">{{ item.label }}</span>
                    <span class="block truncate text-xs text-muted">{{ item.note }}</span>
                  </span>
                  <UIcon name="i-lucide-chevron-right" class="size-4 shrink-0 text-dimmed transition-transform group-hover:translate-x-0.5" />
                </RouterLink>
              </li>
            </ul>
          </UPageCard>
        </UPageGrid>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import { statCardClass, statCardUi, templateTableUi } from '@/views/admin/ops/utils/templateUi'
import {
  getAccountOpsEvents,
  getAccountOpsSettings,
  getAccountOpsThresholdAccounts,
  type AccountOpsEvent,
  type AccountOpsSettings,
  type AccountOpsThresholdAccount,
} from '@/api/admin/accountOps'
import { getStats } from '@/api/admin/dashboard'
import type { DashboardStats } from '@/types'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { buildAttentionItems, maxUsedPercent, summarizeBalances } from '@/utils/opsCenter'

type BadgeColor = 'success' | 'error' | 'warning' | 'neutral'

interface DirectoryItem {
  to: string
  label: string
  note: string
  /** Lucide icon name (literal strings so the icon bundle scan picks them up). */
  icon: string
}

interface DirectoryTier {
  level: string
  title: string
  description: string
  items: DirectoryItem[]
}

interface WindowCell {
  color: 'error' | 'warning' | 'success'
  percent: number
  label: string
}

interface AccountRow {
  account: AccountOpsThresholdAccount
  balance: string
  statusLabel: string
  statusColor: BadgeColor
  windows: Array<WindowCell | null>
}

// Template stat cards (dashboard-vue HomeStats) and table chrome (HomeSales / customers).
const statUi = statCardUi
const statClass = statCardClass
const tableUi = templateTableUi

const { locale } = useI18n()
const auth = useAuthStore()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const loading = ref(false)
const error = ref('')
const stats = ref<DashboardStats | null>(null)
const accounts = ref<AccountOpsThresholdAccount[]>([])
const events = ref<AccountOpsEvent[]>([])
const settings = ref<AccountOpsSettings | null>(null)
const search = ref('')
const riskFilter = ref<'all' | 'risk' | 'ok'>('all')
const sortMode = ref<'risk' | 'balance' | 'name'>('risk')
const page = ref(1)
const pageSize = 10
let alive = true
let timer: number | undefined
let sequence = 0

const observer = computed(() => auth.isAdminObserver)
const balanceTotals = computed(() => summarizeBalances(accounts.value))
const accountsWithBalance = computed(() => accounts.value.filter(account => typeof account.balance === 'number' && Number.isFinite(account.balance)).length)
const attentionAll = computed(() => buildAttentionItems(accounts.value, events.value, 100))
const attention = computed(() => attentionAll.value.slice(0, 8))
const pendingEvents = computed(() => events.value.filter(event => ['pending', 'sending', 'failed'].includes(event.state)).length)
const riskAccounts = computed(() => accounts.value.filter(account => riskScore(account) > 0))

const filteredAccounts = computed(() => {
  const query = search.value.trim().toLowerCase()
  const rows = accounts.value.filter(account => {
    const score = riskScore(account)
    if (riskFilter.value === 'risk' && score === 0) return false
    if (riskFilter.value === 'ok' && score > 0) return false
    if (!query) return true
    const haystack = [account.account_name, String(account.account_id), account.group_name ?? '', account.platform].join(' ').toLowerCase()
    return haystack.includes(query)
  })
  return rows.sort((a, b) => {
    if (sortMode.value === 'name') return a.account_name.localeCompare(b.account_name)
    if (sortMode.value === 'balance') return (a.balance ?? Number.MAX_SAFE_INTEGER) - (b.balance ?? Number.MAX_SAFE_INTEGER)
    return riskScore(b) - riskScore(a) || a.account_name.localeCompare(b.account_name)
  })
})
const pageCount = computed(() => Math.max(1, Math.ceil(filteredAccounts.value.length / pageSize)))
const pageRows = computed(() => filteredAccounts.value.slice((page.value - 1) * pageSize, page.value * pageSize).map(toRow))
const rangeStart = computed(() => (filteredAccounts.value.length ? (page.value - 1) * pageSize + 1 : 0))
const rangeEnd = computed(() => Math.min(page.value * pageSize, filteredAccounts.value.length))

const attentionColumns = computed<TableColumn<(typeof attention.value)[number]>[]>(() => [
  { id: 'item', header: text('事项', 'Item') },
  { id: 'account', header: text('账号', 'Account'), meta: { class: { th: 'w-24', td: 'w-24' } } },
  { id: 'action', header: () => '', meta: { class: { th: 'w-20', td: 'w-20' } } },
])

const assetColumns = computed<TableColumn<AccountRow>[]>(() => [
  { id: 'account', header: text('账号', 'Account') },
  { id: 'platform', header: text('平台', 'Platform') },
  { id: 'balance', header: text('余额', 'Balance') },
  { id: 'w5h', header: '5h' },
  { id: 'w7d', header: '7d' },
  { id: 'observed', header: text('观测', 'Observed') },
])

watch([search, riskFilter, sortMode], () => {
  page.value = 1
})
watch(pageCount, value => {
  if (page.value > value) page.value = value
})

const tiers = computed<DirectoryTier[]>(() => [
  {
    level: 'L1',
    title: text('账号运营', 'Account operations'),
    description: text('账号池、告警、自动化与凭证恢复。', 'Account pool, alerts, automation and credential recovery.'),
    items: [
      { to: '/admin/accounts', label: text('账号池', 'Accounts'), note: text('上游账号与调度状态', 'Upstream accounts and scheduling'), icon: 'i-lucide-users' },
      { to: '/admin/account-ops', label: text('账号通知', 'Account notifications'), note: text('余额与额度告警', 'Balance and quota alerts'), icon: 'i-lucide-bell' },
      { to: '/admin/ops-automation', label: text('账号自动化', 'Account automation'), note: text('定时推理与自动停调', 'Scheduled probes and auto-pause'), icon: 'i-lucide-bot' },
      { to: '/admin/credential-guard', label: text('凭证巡检', 'Credential inspection'), note: text('失效检测与重新登录', 'Expiry checks and re-login'), icon: 'i-lucide-shield-check' },
    ],
  },
  {
    level: 'L2',
    title: text('渠道运营', 'Channel operations'),
    description: text('价格、监控、运维、分组倍率与出口。', 'Pricing, monitoring, operations, group ratios and egress.'),
    items: [
      { to: '/admin/channels/pricing', label: text('渠道定价', 'Channel pricing'), note: text('模型价格与上游成本', 'Model prices and upstream cost'), icon: 'i-lucide-tag' },
      { to: '/admin/channels/monitor', label: text('渠道监控', 'Channel monitor'), note: text('成功率、延迟与吞吐', 'Success rate, latency and throughput'), icon: 'i-lucide-activity' },
      { to: '/admin/channel-operations', label: text('渠道运维', 'Channel operations'), note: text('延迟、质量与错误下钻', 'Latency, quality and error drill-down'), icon: 'i-lucide-server' },
      { to: '/admin/model-comparison', label: text('模型比价', 'Model comparison'), note: text('跨分组实付价与价差', 'Effective cross-group prices and spread'), icon: 'i-lucide-scale' },
      { to: '/admin/groups', label: text('分组与倍率', 'Groups and ratios'), note: text('分组倍率与模型映射', 'Group ratios and model mapping'), icon: 'i-lucide-folder' },
      { to: '/admin/mihomo', label: text('代理出口', 'Managed proxies'), note: text('订阅、节点与动态代理', 'Subscriptions, nodes and dynamic proxies'), icon: 'i-lucide-globe' },
    ],
  },
  {
    level: 'L3',
    title: text('监控诊断', 'Monitoring and diagnostics'),
    description: text('状态、连接、缓存、采集与流量。', 'Status, connections, cache, capture and traffic.'),
    items: [
      { to: '/admin/ops', label: text('渠道状态', 'Channel status'), note: text('实时监控与异常', 'Realtime monitoring and errors'), icon: 'i-lucide-gauge' },
      { to: '/admin/connections', label: text('连接诊断', 'Connections'), note: text('上游连接与 DNS', 'Upstream connections and DNS'), icon: 'i-lucide-cable' },
      { to: '/admin/group-cache-tests', label: text('分组缓存测试', 'Cache tests'), note: text('提示词缓存命中验证', 'Prompt cache hit checks'), icon: 'i-lucide-database-zap' },
      { to: '/admin/request-captures', label: text('请求采集', 'Request capture'), note: text('按请求 ID 回放', 'Replay by request ID'), icon: 'i-lucide-scan-search' },
      { to: '/admin/traffic', label: text('用户流量', 'User traffic'), note: text('请求、IP 与端点', 'Requests, IPs and endpoints'), icon: 'i-lucide-waypoints' },
      { to: '/admin/usage', label: text('用量日志', 'Usage logs'), note: text('按天、模型与用户统计', 'Daily, model and user breakdowns'), icon: 'i-lucide-chart-no-axes-column' },
      { to: '/admin/excel-grants', label: text('Excel 协议授权', 'Excel authorization'), note: text('协议授权与设备', 'Protocol grants and devices'), icon: 'i-lucide-file-spreadsheet' },
      { to: '/admin/serverless', label: text('地区路由', 'Regional routing'), note: text('地区与回源策略', 'Regions and origin policy'), icon: 'i-lucide-map-pin' },
    ],
  },
])

function riskScore(account: AccountOpsThresholdAccount): number {
  let score = 0
  if (account.balance_status === 'failed') score += 100
  else if (account.balance_status === 'stale') score += 60
  const used = maxUsedPercent(account)
  if (used !== null) {
    if (used >= 95) score += 40
    else if (used >= 90) score += 30
    else if (used >= 70) score += 10
  }
  return score
}

function usagePercent(account: AccountOpsThresholdAccount, key: '5h' | '7d'): number | null {
  const window = account.usage_windows?.find(item => item.window === key)
  const value = window?.used_percent
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function windowCell(account: AccountOpsThresholdAccount, key: '5h' | '7d'): WindowCell | null {
  const percent = usagePercent(account, key)
  if (percent === null) return null
  return {
    color: percent >= 90 ? 'error' : percent >= 70 ? 'warning' : 'success',
    percent: Math.min(100, Math.max(0, percent)),
    label: `${Math.round(percent)}%`,
  }
}

function toRow(account: AccountOpsThresholdAccount): AccountRow {
  return {
    account,
    balance: formatMoney(account.balance, account.unit),
    statusLabel: balanceStatusLabel(account.balance_status),
    statusColor: balanceStatusColor(account.balance_status),
    windows: [windowCell(account, '5h'), windowCell(account, '7d')],
  }
}

function formatMoney(value: number | null | undefined, unit?: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '—'
  const symbols: Record<string, string> = { USD: '$', CNY: '¥', EUR: '€', GBP: '£' }
  const normalized = String(unit || '').trim().toUpperCase()
  const amount = value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return symbols[normalized] ? `${symbols[normalized]}${amount}` : `${amount} ${normalized}`.trim()
}

function formatCost(value: number | null | undefined): string {
  const amount = Number(value)
  if (!Number.isFinite(amount)) return '0.0000'
  if (amount >= 1000) return `${(amount / 1000).toFixed(2)}K`
  if (amount >= 1) return amount.toFixed(2)
  if (amount >= 0.01) return amount.toFixed(3)
  return amount.toFixed(4)
}

function balanceStatusLabel(status: string): string {
  const labels: Record<string, [string, string]> = {
    ok: ['正常', 'OK'],
    stale: ['数据过期', 'Stale'],
    failed: ['读取失败', 'Failed'],
    unsupported: ['不支持', 'Unsupported'],
    unknown: ['未知', 'Unknown'],
  }
  const label = labels[status]
  return label ? text(label[0], label[1]) : status
}

function balanceStatusColor(status: string): BadgeColor {
  if (status === 'ok') return 'success'
  if (status === 'failed') return 'error'
  if (status === 'stale') return 'warning'
  return 'neutral'
}

function date(value?: string | null): string {
  if (!value) return '—'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? '—' : parsed.toLocaleString()
}

async function load() {
  if (loading.value) return
  const current = ++sequence
  loading.value = true
  const problems: string[] = []
  const jobs: Promise<void>[] = []

  jobs.push(getStats()
    .then(value => {
      if (alive && current === sequence) stats.value = value
    })
    .catch(err => {
      problems.push(extractApiErrorMessage(err, text('概览统计加载失败', 'Failed to load overview')))
    }))

  if (!observer.value) {
    jobs.push(getAccountOpsThresholdAccounts()
      .then(value => {
        if (alive && current === sequence) accounts.value = value
      })
      .catch(err => {
        problems.push(extractApiErrorMessage(err, text('账号资产加载失败', 'Failed to load account assets')))
      }))
    jobs.push(getAccountOpsEvents()
      .then(value => {
        if (alive && current === sequence) events.value = value.items
      })
      .catch(err => {
        problems.push(extractApiErrorMessage(err, text('告警加载失败', 'Failed to load alerts')))
      }))
    jobs.push(getAccountOpsSettings()
      .then(value => {
        if (alive && current === sequence) settings.value = value
      })
      .catch(err => {
        problems.push(extractApiErrorMessage(err, text('通知配置加载失败', 'Failed to load notification settings')))
      }))
  }

  await Promise.allSettled(jobs)
  if (!alive || current !== sequence) return
  error.value = problems.join(' · ')
  loading.value = false
}

function refresh() {
  void load()
}

onMounted(() => {
  void load()
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible' && !loading.value) void load()
  }, 60_000)
})

onBeforeUnmount(() => {
  alive = false
  sequence++
  if (timer) window.clearInterval(timer)
})
</script>
