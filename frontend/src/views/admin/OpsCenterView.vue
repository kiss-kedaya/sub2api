<template>
  <AppLayout>
    <div class="ops-center space-y-5">
      <AdminPageHeader
        :description="text('账号资产、渠道健康与告警集中在一个入口，按账号 / 渠道 / 诊断三层分组。', 'Account assets, channel health and alerts in one place, grouped into account, channel and diagnostics tiers.')"
      >
        <template #title>
          <span class="inline-flex flex-wrap items-center gap-2">
            {{ text('运营中心', 'Operations center') }}
            <span class="badge badge-primary">{{ text('L0 总览', 'L0 overview') }}</span>
          </span>
        </template>
        <template #actions>
          <RouterLink to="/admin/account-ops" class="btn btn-secondary">{{ text('账号通知设置', 'Notification settings') }}</RouterLink>
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="refresh">
            <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
            {{ text('刷新', 'Refresh') }}
          </button>
        </template>
      </AdminPageHeader>

      <p v-if="error" role="alert" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ error }}</p>
      <p v-if="observer" class="rounded-lg bg-primary-50 p-3 text-sm text-primary-800 dark:bg-dark-800 dark:text-primary-300">
        {{ text('账号资产与告警需要主管理员权限，当前只显示系统统计。', 'Account assets and alerts require the primary administrator; only system totals are shown.') }}
      </p>

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" :aria-label="text('运营概览', 'Operations overview')">
        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('上游总余额', 'Total upstream balance') }}</p>
            <span class="admin-stat-icon rounded-lg bg-primary-50 p-2 text-primary-600 dark:bg-primary-950/40"><Icon name="wallet" size="sm" /></span>
          </div>
          <div class="mt-3 space-y-0.5">
            <p v-for="total in balanceTotals.slice(0, 2)" :key="total.unit" class="text-2xl font-semibold tabular-nums">
              {{ formatMoney(total.total, total.unit) }}
            </p>
            <p v-if="!balanceTotals.length" class="text-2xl font-semibold text-gray-400">—</p>
            <p v-if="balanceTotals.length > 2" class="text-xs text-gray-500">+{{ balanceTotals.length - 2 }} {{ text('种币种', 'more currencies') }}</p>
          </div>
          <p class="mt-2 text-xs text-gray-500">{{ accountsWithBalance }} {{ text('个账号有余额快照', 'accounts with a balance snapshot') }}</p>
        </article>

        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('今日消耗', 'Spend today') }}</p>
            <span class="admin-stat-icon rounded-lg bg-emerald-50 p-2 text-emerald-600 dark:bg-emerald-950/40"><Icon name="chart" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums">${{ formatCost(stats?.today_actual_cost) }}</p>
          <p class="mt-2 text-xs text-gray-500">{{ text('今日请求', 'Requests today') }} {{ stats?.today_requests ?? 0 }}</p>
        </article>

        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('账号健康', 'Account health') }}</p>
            <span class="admin-stat-icon rounded-lg bg-sky-50 p-2 text-sky-600 dark:bg-sky-950/40"><Icon name="users" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums">
            {{ stats?.normal_accounts ?? 0 }}
            <span class="text-sm font-normal text-gray-400">/ {{ stats?.total_accounts ?? 0 }}</span>
          </p>
          <p class="mt-2 text-xs text-gray-500">
            {{ text('异常', 'Error') }} {{ stats?.error_accounts ?? 0 }} · {{ text('限流', 'Rate limited') }} {{ stats?.ratelimit_accounts ?? 0 }} · {{ text('过载', 'Overloaded') }} {{ stats?.overload_accounts ?? 0 }}
          </p>
        </article>

        <article class="card p-5">
          <div class="flex items-start justify-between gap-2">
            <p class="text-xs font-medium text-gray-500">{{ text('待处理', 'Needs attention') }}</p>
            <span class="admin-stat-icon rounded-lg bg-amber-50 p-2 text-amber-600 dark:bg-amber-950/40"><Icon name="bell" size="sm" /></span>
          </div>
          <p class="mt-3 text-2xl font-semibold tabular-nums" :class="attentionAll.length ? 'text-amber-600 dark:text-amber-400' : ''">{{ attentionAll.length }}</p>
          <p class="mt-2 text-xs text-gray-500">
            {{ text('告警', 'Alerts') }} {{ pendingEvents }} · {{ text('资产风险', 'Asset risks') }} {{ riskAccounts.length }} · {{ text('通知', 'Notifications') }} {{ settings ? (settings.config.enabled ? text('已开启', 'on') : text('未开启', 'off')) : '—' }}
          </p>
        </article>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="text-base font-semibold">{{ text('需要处理', 'Needs attention') }}</h2>
            <p class="mt-1 text-xs text-gray-500">{{ text('通知失败、余额读取失败或额度即将用尽。', 'Failed notifications, unreadable balances or nearly exhausted quotas.') }}</p>
          </div>
          <RouterLink to="/admin/account-ops" class="text-sm text-primary-600">{{ text('打开账号通知', 'Open account notifications') }}</RouterLink>
        </div>
        <ul v-if="attention.length" class="divide-y divide-gray-100 dark:divide-dark-700">
          <li v-for="item in attention" :key="item.key" class="flex flex-wrap items-center gap-3 px-5 py-3">
            <span class="h-2 w-2 flex-shrink-0 rounded-full" :class="item.severity === 'critical' ? 'bg-red-500' : 'bg-amber-500'" />
            <div class="min-w-0 flex-1">
              <p class="text-sm font-medium">{{ item.title }}</p>
              <p class="mt-0.5 truncate text-xs text-gray-500">{{ item.detail }}</p>
            </div>
            <span class="text-xs text-gray-400">#{{ item.accountId }}</span>
            <RouterLink :to="item.href" class="text-xs text-primary-600">{{ text('处理', 'Resolve') }}</RouterLink>
          </li>
        </ul>
        <div v-else class="px-5 py-10 text-center text-sm text-gray-500">
          {{ loading ? text('加载中…', 'Loading…') : text('当前没有需要处理的账号。', 'Nothing needs attention right now.') }}
        </div>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
          <div>
            <h2 class="text-base font-semibold">{{ text('账号资产', 'Account assets') }}</h2>
            <p class="mt-1 text-xs text-gray-500">{{ text('余额、5 小时与 7 天额度，按风险优先排序。', 'Balances, 5-hour and 7-day quotas, ordered by risk.') }}</p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <UInput v-model="search" size="lg" class="w-44" :placeholder="text('搜索账号 / 分组', 'Search account or group')" :aria-label="text('搜索账号', 'Search accounts')" />
            <USelect
              v-model="riskFilter"
              size="lg"
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
              size="lg"
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

        <p v-if="observer" class="p-5 text-sm text-gray-500">{{ text('需要主管理员权限才能查看账号资产。', 'Primary administrator access is required to view account assets.') }}</p>
        <template v-else>
          <div class="overflow-x-auto">
            <table v-if="pageRows.length" class="w-full min-w-[860px] text-left text-sm">
              <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800">
                <tr>
                  <th class="px-5 py-3">{{ text('账号', 'Account') }}</th>
                  <th class="px-4 py-3">{{ text('平台', 'Platform') }}</th>
                  <th class="px-4 py-3">{{ text('余额', 'Balance') }}</th>
                  <th class="px-4 py-3">5h</th>
                  <th class="px-4 py-3">7d</th>
                  <th class="px-5 py-3">{{ text('观测', 'Observed') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in pageRows" :key="row.account.account_id" class="border-t border-gray-100 align-middle dark:border-dark-700">
                  <td class="px-5 py-3">
                    <p class="font-medium">{{ row.account.account_name }}</p>
                    <p class="mt-0.5 text-xs text-gray-500">
                      #{{ row.account.account_id }}<template v-if="row.account.group_name"> · {{ row.account.group_name }}</template>
                    </p>
                  </td>
                  <td class="px-4 py-3">
                    <p>{{ row.account.platform }}</p>
                    <p class="mt-0.5 text-xs text-gray-500">{{ row.account.type === 'oauth' ? 'OAuth' : 'API Key' }}</p>
                  </td>
                  <td class="px-4 py-3">
                    <p class="tabular-nums">{{ row.balance }}</p>
                    <span class="badge mt-1" :class="row.statusClass">{{ row.statusLabel }}</span>
                  </td>
                  <td v-for="(cell, index) in row.windows" :key="index" class="px-4 py-3">
                    <div v-if="cell" class="flex items-center gap-2">
                      <div class="h-1.5 w-16 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                        <div class="h-full rounded-full" :class="cell.tone" :style="{ width: cell.width }" />
                      </div>
                      <span class="text-xs tabular-nums text-gray-600 dark:text-gray-300">{{ cell.label }}</span>
                    </div>
                    <span v-else class="text-xs text-gray-400">—</span>
                  </td>
                  <td class="px-5 py-3 text-xs text-gray-500">{{ date(row.account.received_at) }}</td>
                </tr>
              </tbody>
            </table>
            <div v-else class="p-10 text-center text-sm text-gray-500">
              {{ loading ? text('加载中…', 'Loading…') : text('没有匹配的账号。', 'No accounts match the filters.') }}
            </div>
          </div>
          <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-5 py-3 text-xs text-gray-500 dark:border-dark-700">
            <span>{{ text('显示', 'Showing') }} {{ rangeStart }}–{{ rangeEnd }} / {{ filteredAccounts.length }}</span>
            <div class="flex items-center gap-2">
              <button type="button" class="btn btn-secondary" :disabled="page <= 1" @click="page--">{{ text('上一页', 'Previous') }}</button>
              <span>{{ page }} / {{ pageCount }}</span>
              <button type="button" class="btn btn-secondary" :disabled="page >= pageCount" @click="page++">{{ text('下一页', 'Next') }}</button>
            </div>
          </div>
        </template>
      </section>

      <section class="space-y-3">
        <div class="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 class="text-lg font-semibold">{{ text('功能分级', 'Feature tiers') }}</h2>
            <p class="mt-1 text-sm text-gray-500">{{ text('从账号到渠道再到诊断，逐层下钻。', 'Drill down from accounts to channels, then diagnostics.') }}</p>
          </div>
          <span class="text-xs text-gray-500">{{ text('侧边栏同名分组可直接展开', 'The sidebar group expands to the same links') }}</span>
        </div>
        <div class="grid gap-4 lg:grid-cols-3">
          <article v-for="tier in tiers" :key="tier.level" class="card flex flex-col p-5">
            <div class="flex items-center gap-2">
              <span class="badge badge-primary">{{ tier.level }}</span>
              <h3 class="text-base font-semibold">{{ tier.title }}</h3>
            </div>
            <p class="mt-2 text-xs leading-5 text-gray-500">{{ tier.description }}</p>
            <ul class="mt-4 space-y-1">
              <li v-for="item in tier.items" :key="item.to">
                <RouterLink :to="item.to" class="group flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-gray-50 dark:hover:bg-dark-800">
                  <span class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300">
                    <Icon :name="item.icon" size="sm" />
                  </span>
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-sm font-medium">{{ item.label }}</span>
                    <span class="block truncate text-xs text-gray-500">{{ item.note }}</span>
                  </span>
                  <Icon name="chevronRight" size="sm" class="text-gray-400 transition-transform group-hover:translate-x-0.5" />
                </RouterLink>
              </li>
            </ul>
          </article>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
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

type IconName = 'users' | 'bell' | 'chart' | 'shield' | 'globe' | 'key' | 'wallet' | 'server' | 'chartBar'

interface DirectoryItem {
  to: string
  label: string
  note: string
  icon: IconName
}

interface DirectoryTier {
  level: string
  title: string
  description: string
  items: DirectoryItem[]
}

interface WindowCell {
  tone: string
  width: string
  label: string
}

interface AccountRow {
  account: AccountOpsThresholdAccount
  balance: string
  statusLabel: string
  statusClass: string
  windows: Array<WindowCell | null>
}

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
      { to: '/admin/accounts', label: text('账号池', 'Accounts'), note: text('上游账号与调度状态', 'Upstream accounts and scheduling'), icon: 'users' },
      { to: '/admin/account-ops', label: text('账号通知', 'Account notifications'), note: text('余额与额度告警', 'Balance and quota alerts'), icon: 'bell' },
      { to: '/admin/ops-automation', label: text('账号自动化', 'Account automation'), note: text('定时推理与自动停调', 'Scheduled probes and auto-pause'), icon: 'chart' },
      { to: '/admin/credential-guard', label: text('凭证巡检', 'Credential inspection'), note: text('失效检测与重新登录', 'Expiry checks and re-login'), icon: 'shield' },
    ],
  },
  {
    level: 'L2',
    title: text('渠道运营', 'Channel operations'),
    description: text('价格、监控、运维、分组倍率与出口。', 'Pricing, monitoring, operations, group ratios and egress.'),
    items: [
      { to: '/admin/channels/pricing', label: text('渠道定价', 'Channel pricing'), note: text('模型价格与上游成本', 'Model prices and upstream cost'), icon: 'key' },
      { to: '/admin/channels/monitor', label: text('渠道监控', 'Channel monitor'), note: text('成功率、延迟与吞吐', 'Success rate, latency and throughput'), icon: 'chartBar' },
      { to: '/admin/channel-operations', label: text('渠道运维', 'Channel operations'), note: text('延迟、质量与错误下钻', 'Latency, quality and error drill-down'), icon: 'server' },
      { to: '/admin/model-comparison', label: text('模型比价', 'Model comparison'), note: text('跨分组实付价与价差', 'Effective cross-group prices and spread'), icon: 'chartBar' },
      { to: '/admin/groups', label: text('分组与倍率', 'Groups and ratios'), note: text('分组倍率与模型映射', 'Group ratios and model mapping'), icon: 'users' },
      { to: '/admin/mihomo', label: text('代理出口', 'Managed proxies'), note: text('订阅、节点与动态代理', 'Subscriptions, nodes and dynamic proxies'), icon: 'globe' },
    ],
  },
  {
    level: 'L3',
    title: text('监控诊断', 'Monitoring and diagnostics'),
    description: text('状态、连接、缓存、采集与流量。', 'Status, connections, cache, capture and traffic.'),
    items: [
      { to: '/admin/ops', label: text('渠道状态', 'Channel status'), note: text('实时监控与异常', 'Realtime monitoring and errors'), icon: 'chart' },
      { to: '/admin/connections', label: text('连接诊断', 'Connections'), note: text('上游连接与 DNS', 'Upstream connections and DNS'), icon: 'globe' },
      { to: '/admin/group-cache-tests', label: text('分组缓存测试', 'Cache tests'), note: text('提示词缓存命中验证', 'Prompt cache hit checks'), icon: 'chartBar' },
      { to: '/admin/request-captures', label: text('请求采集', 'Request capture'), note: text('按请求 ID 回放', 'Replay by request ID'), icon: 'server' },
      { to: '/admin/traffic', label: text('用户流量', 'User traffic'), note: text('请求、IP 与端点', 'Requests, IPs and endpoints'), icon: 'users' },
      { to: '/admin/usage', label: text('用量日志', 'Usage logs'), note: text('按天、模型与用户统计', 'Daily, model and user breakdowns'), icon: 'chart' },
      { to: '/admin/excel-grants', label: text('Excel 协议授权', 'Excel authorization'), note: text('协议授权与设备', 'Protocol grants and devices'), icon: 'shield' },
      { to: '/admin/serverless', label: text('地区路由', 'Regional routing'), note: text('地区与回源策略', 'Regions and origin policy'), icon: 'globe' },
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
  const tone = percent >= 90 ? 'bg-red-500' : percent >= 70 ? 'bg-amber-500' : 'bg-emerald-500'
  return {
    tone,
    width: `${Math.min(100, Math.max(0, percent))}%`,
    label: `${Math.round(percent)}%`,
  }
}

function toRow(account: AccountOpsThresholdAccount): AccountRow {
  return {
    account,
    balance: formatMoney(account.balance, account.unit),
    statusLabel: balanceStatusLabel(account.balance_status),
    statusClass: balanceStatusClass(account.balance_status),
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

function balanceStatusClass(status: string): string {
  if (status === 'ok') return 'badge-success'
  if (status === 'failed') return 'badge-danger'
  if (status === 'stale') return 'badge-warning'
  return 'badge-gray'
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
