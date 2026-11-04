<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <AdminPageHeader
        :title="text('上游监控', 'Upstream monitor')"
        :description="text(
          '余额、上游倍率与变动记录集中在一页。数据只来自已有的上游探测快照，页面不会额外请求上游，也不扫 usage_logs。',
          'Balances, upstream rates and change history in one place. Everything reads the existing probe snapshots; the page adds no upstream calls and never scans usage_logs.'
        )"
      >
        <template #actions>
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="text('刷新', 'Refresh')" :loading="busy" @click="refresh" />
          <UButton icon="i-lucide-download" :label="text('记录当前快照', 'Record current snapshots')" :loading="sampling" @click="pullSamples" />
        </template>
      </AdminPageHeader>

      <UAlert v-if="error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />

      <section class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4" :aria-label="text('上游概览', 'Upstream overview')">
        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('上游总余额', 'Total upstream balance') }}</p>
          <div class="mt-2 space-y-1">
            <p v-for="currency in overview?.currencies ?? []" :key="currency.unit" class="text-2xl font-semibold tabular-nums">
              {{ formatMoney(currency.total, currency.unit) }}
            </p>
            <p v-if="!overview?.currencies?.length" class="text-2xl font-semibold text-muted">—</p>
          </div>
          <p class="mt-2 text-xs text-muted">{{ overview?.with_balance ?? 0 }} / {{ overview?.total_accounts ?? 0 }} {{ text('个账号有余额', 'accounts with a balance') }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('数据健康', 'Data health') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums">
            {{ overview?.healthy ?? 0 }}
            <span class="text-sm font-normal text-muted">/ {{ overview?.total_accounts ?? 0 }}</span>
          </p>
          <p class="mt-2 text-xs text-muted">{{ text('过期', 'Stale') }} {{ overview?.stale ?? 0 }} · {{ text('失败', 'Failed') }} {{ overview?.failed ?? 0 }} · {{ text('不支持', 'Unsupported') }} {{ overview?.unsupported ?? 0 }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('倍率覆盖', 'Rate coverage') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums">{{ overview?.rate_known ?? 0 }}</p>
          <p class="mt-2 text-xs text-muted">{{ text('个账号已读到上游倍率', 'accounts have an upstream rate') }}</p>
        </article>

        <article class="card p-4">
          <p class="text-xs font-medium text-muted">{{ text('24 小时倍率变动', 'Rate changes in 24h') }}</p>
          <p class="mt-2 text-2xl font-semibold tabular-nums" :class="(overview?.rate_changes_24h ?? 0) > 0 ? 'text-amber-600 dark:text-amber-400' : ''">{{ overview?.rate_changes_24h ?? 0 }}</p>
          <p class="mt-2 text-xs text-muted">{{ text('最近采样', 'Last sample') }} {{ overview?.last_sample_at ? new Date(overview.last_sample_at).toLocaleString() : '—' }}</p>
        </article>
      </section>

      <section v-if="overview?.attention?.length" class="card p-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-semibold">{{ text('优先处理', 'Needs attention') }}</h3>
          <span class="text-xs text-muted">{{ text('按失败、不支持、过期排序', 'Ordered by failed, unsupported, stale') }}</span>
        </div>
        <div class="mt-3 flex flex-wrap gap-2">
          <button
            v-for="item in overview.attention"
            :key="item.account_id"
            type="button"
            class="inline-flex max-w-full items-center gap-2 rounded-full border border-default px-3 py-1 text-xs hover:bg-elevated"
            @click="openDetail(item)"
          >
            <span class="truncate font-medium">{{ item.name }}</span>
            <UBadge :color="statusColor(item.balance_status)" variant="subtle" size="sm">{{ statusLabel(item.balance_status) }}</UBadge>
            <span v-if="item.unlimited" class="text-muted">{{ text('不限', 'Unlimited') }}</span>
            <span v-else-if="item.balance !== null" class="tabular-nums text-muted">{{ formatMoney(item.balance, item.unit) }}</span>
          </button>
        </div>
      </section>

      <PageToolbar>
        <template #left>
          <UInput v-model="search" icon="i-lucide-search" class="w-64 max-w-full" :placeholder="text('搜索账号名称', 'Search account name')" :aria-label="text('搜索账号', 'Search accounts')" />
        </template>
        <template #right>
          <USelect v-model="statusFilter" :items="statusItems" class="min-w-36" :aria-label="text('余额状态', 'Balance status')" />
          <USelect v-model="platformFilter" :items="platformItems" class="min-w-36" :aria-label="text('平台', 'Platform')" />
          <USelect v-model="sort" :items="sortItems" class="min-w-36" :aria-label="text('排序', 'Sort')" />
        </template>
      </PageToolbar>

      <UTable :data="rows" :columns="columns" :loading="busy" :ui="tableUi">
        <template #account-cell="{ row }">
          <p class="font-medium text-highlighted">{{ row.original.name }}</p>
          <p class="text-xs text-muted">#{{ row.original.account_id }} · {{ row.original.platform }} · {{ row.original.type }}</p>
        </template>
        <template #balance-cell="{ row }">
          <p class="font-medium tabular-nums">{{ row.original.unlimited ? text('不限', 'Unlimited') : row.original.balance === null ? '—' : formatMoney(row.original.balance, row.original.unit) }}</p>
          <p class="text-xs text-muted">{{ row.original.balance_read_at ? new Date(row.original.balance_read_at).toLocaleString() : text('尚未采集', 'Not sampled yet') }}</p>
        </template>
        <template #status-cell="{ row }">
          <UBadge :color="statusColor(row.original.balance_status)" variant="subtle">{{ statusLabel(row.original.balance_status) }}</UBadge>
          <p v-if="row.original.balance_error" class="mt-1 max-w-xs whitespace-normal text-xs text-amber-600 dark:text-amber-400">{{ row.original.balance_error }}</p>
        </template>
        <template #rate-cell="{ row }">
          <p class="tabular-nums">{{ row.original.rate === null ? '—' : `× ${row.original.rate.toFixed(4)}` }}</p>
          <p class="text-xs text-muted">
            {{ row.original.rate_sync_enabled ? text('同步已开启', 'Sync on') : text('仅观察', 'Observe only') }}
            <span v-if="row.original.rate_synced != null"> · {{ text('已写回', 'Applied') }} × {{ row.original.rate_synced.toFixed(4) }}</span>
          </p>
        </template>
        <template #sampled-cell="{ row }">{{ row.original.last_sampled_at ? new Date(row.original.last_sampled_at).toLocaleString() : '—' }}</template>
        <template #actions-cell="{ row }">
          <div class="flex items-center justify-end gap-1">
            <UButton icon="i-lucide-chart-line" color="neutral" variant="ghost" :aria-label="text('详情', 'Details')" :title="text('详情', 'Details')" @click="openDetail(row.original)" />
            <UButton
              icon="i-lucide-scan-search"
              color="neutral"
              variant="ghost"
              :aria-label="text('立即读取', 'Read now')"
              :title="text('立即读取', 'Read now')"
              :loading="probingID === row.original.account_id"
              :disabled="probingID !== null"
              @click="probeAccount(row.original)"
            />
          </div>
        </template>
        <template #empty>
          <div v-if="!busy" class="space-y-1">
            <p class="font-medium text-highlighted">{{ text('没有匹配的上游账号', 'No matching upstream accounts') }}</p>
            <p class="text-sm text-muted">{{ text('先在账号管理里为 API Key 账号开启上游余额探测；这里只读取已采集的结果。', 'Enable the upstream balance probe for API-key accounts first; this page only reads stored results.') }}</p>
          </div>
        </template>
      </UTable>

      <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-3 text-sm text-muted">
        <span>{{ text(`共 ${total} 个账号`, `${total} accounts`) }}</span>
        <div class="flex items-center gap-2">
          <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" :disabled="busy || page <= 1" :aria-label="text('上一页', 'Previous page')" @click="goPage(page - 1)" />
          <span class="tabular-nums">{{ page }} / {{ pages || 1 }}</span>
          <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" :disabled="busy || page >= pages" :aria-label="text('下一页', 'Next page')" @click="goPage(page + 1)" />
        </div>
      </div>

      <section class="card p-4">
        <button type="button" class="flex w-full items-center justify-between text-left" @click="rateOpen = !rateOpen">
          <span class="font-semibold">{{ text('倍率变动记录', 'Rate change history') }}</span>
          <span class="text-xs text-muted">{{ text('最近 7 天 · 共', 'Last 7 days ·') }} {{ rateTotal }} {{ text('条', 'entries') }}</span>
        </button>
        <div v-if="rateOpen" class="mt-3 overflow-x-auto">
          <table class="w-full min-w-[640px] text-sm">
            <thead class="text-left text-xs text-muted">
              <tr>
                <th class="py-2 pr-4 font-medium">{{ text('账号', 'Account') }}</th>
                <th class="py-2 pr-4 font-medium">{{ text('原倍率', 'Previous') }}</th>
                <th class="py-2 pr-4 font-medium">{{ text('新倍率', 'New') }}</th>
                <th class="py-2 font-medium">{{ text('时间', 'When') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="change in rateChanges" :key="change.id" class="border-t border-default">
                <td class="py-2 pr-4">{{ change.name || `#${change.account_id}` }} <span class="text-xs text-muted">#{{ change.account_id }}</span></td>
                <td class="py-2 pr-4 tabular-nums">× {{ change.old_rate.toFixed(4) }}</td>
                <td class="py-2 pr-4 tabular-nums" :class="change.new_rate > change.old_rate ? 'text-amber-600 dark:text-amber-400' : 'text-emerald-600 dark:text-emerald-400'">× {{ change.new_rate.toFixed(4) }}</td>
                <td class="py-2">{{ new Date(change.changed_at).toLocaleString() }}</td>
              </tr>
              <tr v-if="!rateChanges.length">
                <td colspan="4" class="py-3 text-center text-muted">{{ text('最近 7 天没有倍率变动', 'No rate changes in the last 7 days') }}</td>
              </tr>
            </tbody>
          </table>
          <div v-if="ratePages > 1" class="mt-3 flex items-center justify-end gap-2 text-xs text-muted">
            <UButton icon="i-lucide-chevron-left" color="neutral" variant="ghost" size="xs" :disabled="ratePage <= 1" @click="loadRateChanges(ratePage - 1)" />
            <span>{{ ratePage }} / {{ ratePages }}</span>
            <UButton icon="i-lucide-chevron-right" color="neutral" variant="ghost" size="xs" :disabled="ratePage >= ratePages" @click="loadRateChanges(ratePage + 1)" />
          </div>
        </div>
      </section>
    </div>

    <BaseDialog :show="detailOpen" :title="selected ? `${selected.name} · #${selected.account_id}` : ''" @close="closeDetail">
      <div v-if="selected" class="space-y-4">
        <div class="grid grid-cols-2 gap-3 text-sm">
          <div class="rounded-lg border border-default p-3">
            <p class="text-xs text-muted">{{ text('当前余额', 'Current balance') }}</p>
            <p class="mt-1 text-xl font-semibold tabular-nums">{{ selected.unlimited ? text('不限', 'Unlimited') : selected.balance === null ? '—' : formatMoney(selected.balance, selected.unit) }}</p>
            <UBadge class="mt-2" :color="statusColor(selected.balance_status)" variant="subtle">{{ statusLabel(selected.balance_status) }}</UBadge>
          </div>
          <div class="rounded-lg border border-default p-3">
            <p class="text-xs text-muted">{{ text('上游倍率', 'Upstream rate') }}</p>
            <p class="mt-1 text-xl font-semibold tabular-nums">{{ selected.rate === null ? '—' : `× ${selected.rate.toFixed(4)}` }}</p>
            <p class="mt-2 text-xs text-muted">{{ selected.rate_sync_enabled ? text('已开启倍率同步', 'Rate sync enabled') : text('仅观察，不写回账号', 'Observe only') }}</p>
          </div>
        </div>

        <div class="rounded-lg border border-default p-3">
          <div class="flex items-center justify-between">
            <p class="text-sm font-medium">{{ text('余额趋势', 'Balance trend') }}</p>
            <div class="flex gap-1">
              <UButton v-for="days in [7, 14, 30]" :key="days" size="xs" :color="historyDays === days ? 'primary' : 'neutral'" :variant="historyDays === days ? 'solid' : 'ghost'" :label="`${days}d`" @click="loadHistory(days)" />
            </div>
          </div>
          <div v-if="chart" class="mt-3">
            <svg viewBox="0 0 600 120" class="h-32 w-full" role="img" :aria-label="text('余额趋势图', 'Balance trend chart')">
              <polyline :points="chart.polyline" fill="none" stroke="currentColor" stroke-width="2" class="text-primary-500" />
            </svg>
            <div class="flex justify-between text-xs text-muted">
              <span>{{ formatMoney(chart.min, selected.unit) }}</span>
              <span>{{ formatMoney(chart.max, selected.unit) }}</span>
            </div>
          </div>
          <p v-else class="mt-3 text-sm text-muted">{{ text('样本不足，等下一轮采样后再看。', 'Not enough samples yet; check again after the next cycle.') }}</p>
        </div>

        <dl class="grid grid-cols-2 gap-2 text-sm">
          <div><dt class="text-xs text-muted">{{ text('最近读取', 'Last read') }}</dt><dd>{{ selected.balance_read_at ? new Date(selected.balance_read_at).toLocaleString() : '—' }}</dd></div>
          <div><dt class="text-xs text-muted">{{ text('有效期至', 'Fresh until') }}</dt><dd>{{ selected.balance_fresh_until ? new Date(selected.balance_fresh_until).toLocaleString() : '—' }}</dd></div>
          <div><dt class="text-xs text-muted">{{ text('最近采样', 'Last sample') }}</dt><dd>{{ selected.last_sampled_at ? new Date(selected.last_sampled_at).toLocaleString() : '—' }}</dd></div>
          <div><dt class="text-xs text-muted">{{ text('账号状态', 'Account status') }}</dt><dd>{{ selected.status }}</dd></div>
        </dl>

        <p v-if="selected.balance_error" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ selected.balance_error }}</p>

        <div>
          <p class="mb-2 text-sm font-medium">{{ text('这个账号的倍率变动', 'Rate changes for this account') }}</p>
          <div class="max-h-48 overflow-y-auto rounded-lg border border-default">
            <table class="w-full text-sm">
              <tbody>
                <tr v-for="change in detailRateChanges" :key="change.id" class="border-b border-default last:border-b-0">
                  <td class="p-2 tabular-nums text-muted">× {{ change.old_rate.toFixed(4) }}</td>
                  <td class="p-2 tabular-nums">→ × {{ change.new_rate.toFixed(4) }}</td>
                  <td class="p-2 text-right text-xs text-muted">{{ new Date(change.changed_at).toLocaleString() }}</td>
                </tr>
                <tr v-if="!detailRateChanges.length"><td class="p-3 text-center text-muted">{{ text('暂无记录', 'No records') }}</td></tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" :label="text('关闭', 'Close')" @click="closeDetail" />
          <UButton icon="i-lucide-scan-search" :label="text('立即读取', 'Read now')" :loading="probingID === selected?.account_id" :disabled="!selected || probingID !== null" @click="selected && probeAccount(selected)" />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { probeUpstreamBilling } from '@/api/admin/accounts'
import {
  getHistory,
  getOverview,
  listAccounts,
  listRateChanges,
  sampleNow,
  type UpstreamMonitorAccount,
  type UpstreamMonitorOverview,
  type UpstreamMonitorPoint,
  type UpstreamRateChange,
} from '@/api/admin/upstreamMonitor'

const { locale } = useI18n()
const text = (zh: string, en: string) => (locale.value.startsWith('zh') ? zh : en)

const overview = ref<UpstreamMonitorOverview | null>(null)
const rows = ref<UpstreamMonitorAccount[]>([])
const total = ref(0)
const page = ref(1)
const pages = ref(1)
const pageSize = 20
const search = ref('')
const statusFilter = ref('all')
const platformFilter = ref('all')
const sort = ref('name')
const busy = ref(false)
const sampling = ref(false)
const error = ref('')
const probingID = ref<number | null>(null)

const rateChanges = ref<UpstreamRateChange[]>([])
const rateTotal = ref(0)
const ratePage = ref(1)
const ratePages = ref(1)
const rateOpen = ref(false)

const detailOpen = ref(false)
const selected = ref<UpstreamMonitorAccount | null>(null)
const history = ref<UpstreamMonitorPoint[]>([])
const historyDays = ref(7)
const detailRateChanges = ref<UpstreamRateChange[]>([])

const statusItems = computed(() => [
  { label: text('全部状态', 'All statuses'), value: 'all' },
  { label: text('正常', 'OK'), value: 'ok' },
  { label: text('数据过期', 'Stale'), value: 'stale' },
  { label: text('读取失败', 'Failed'), value: 'failed' },
  { label: text('不支持', 'Unsupported'), value: 'unsupported' },
  { label: text('未采集', 'Not sampled'), value: 'unknown' },
])
const platformItems = computed(() => [
  { label: text('全部平台', 'All platforms'), value: 'all' },
  ...['anthropic', 'openai', 'gemini', 'grok', 'deepseek', 'kimi', 'zhipu'].map(value => ({ label: value, value })),
])
const sortItems = computed(() => [
  { label: text('按名称', 'By name'), value: 'name' },
  { label: text('余额从低到高', 'Balance low to high'), value: 'balance_asc' },
  { label: text('余额从高到低', 'Balance high to low'), value: 'balance_desc' },
])

const columns = computed<TableColumn<UpstreamMonitorAccount>[]>(() => [
  { id: 'account', header: text('账号', 'Account') },
  { id: 'balance', header: text('余额', 'Balance') },
  { id: 'status', header: text('状态', 'Status') },
  { id: 'rate', header: text('上游倍率', 'Upstream rate') },
  { id: 'sampled', header: text('最近采样', 'Last sample'), meta: { class: { th: 'w-40', td: 'w-40' } } },
  { id: 'actions', header: () => text('操作', 'Actions'), meta: { class: { th: 'w-24 text-right', td: 'w-24' } } },
])
const tableUi = {
  base: 'table-fixed border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0',
}

const moneySymbols: Record<string, string> = { USD: '$', CNY: '¥', EUR: '€', GBP: '£' }
function formatMoney(value: number, unit?: string): string {
  if (!Number.isFinite(value)) return '—'
  const normalized = String(unit || '').trim().toUpperCase()
  const amount = Math.abs(value) >= 1000
    ? value.toLocaleString(undefined, { maximumFractionDigits: 2 })
    : value.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 4 })
  return moneySymbols[normalized] ? `${moneySymbols[normalized]}${amount}` : `${amount} ${normalized}`.trim()
}

function statusLabel(status: string): string {
  const labels: Record<string, [string, string]> = {
    ok: ['正常', 'OK'],
    stale: ['数据过期', 'Stale'],
    failed: ['读取失败', 'Failed'],
    unsupported: ['不支持', 'Unsupported'],
    unknown: ['未采集', 'Not sampled'],
  }
  const label = labels[status]
  return label ? text(label[0], label[1]) : status
}
function statusColor(status: string) {
  if (status === 'ok') return 'success' as const
  if (status === 'failed') return 'error' as const
  if (status === 'stale') return 'warning' as const
  return 'neutral' as const
}

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    const [nextOverview, nextPage] = await Promise.all([
      getOverview(),
      listAccounts({
        page: page.value,
        pageSize,
        search: search.value.trim(),
        balanceStatus: statusFilter.value === 'all' ? '' : statusFilter.value,
        platform: platformFilter.value === 'all' ? '' : platformFilter.value,
        sort: sort.value,
      }),
    ])
    overview.value = nextOverview
    rows.value = nextPage.items
    total.value = nextPage.total
    pages.value = nextPage.pages || 1
    if (page.value > pages.value) {
      page.value = pages.value
      await refresh()
    }
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('加载失败', 'Failed to load')
  } finally {
    busy.value = false
  }
}

async function loadRateChanges(target = 1) {
  try {
    const result = await listRateChanges({ page: target, pageSize: 10, days: 7 })
    rateChanges.value = result.items
    rateTotal.value = result.total
    ratePage.value = result.page
    ratePages.value = result.pages || 1
  } catch {
    rateChanges.value = []
  }
}

function goPage(target: number) {
  if (target < 1 || target > pages.value) return
  page.value = target
  void refresh()
}

async function pullSamples() {
  sampling.value = true
  error.value = ''
  try {
    await sampleNow()
    await Promise.all([refresh(), loadRateChanges(ratePage.value)])
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('采样失败', 'Sampling failed')
  } finally {
    sampling.value = false
  }
}

async function probeAccount(row: UpstreamMonitorAccount) {
  probingID.value = row.account_id
  error.value = ''
  try {
    await probeUpstreamBilling(row.account_id)
    await sampleNow()
    await Promise.all([refresh(), loadRateChanges(ratePage.value)])
    if (selected.value?.account_id === row.account_id) {
      await loadDetail(row.account_id)
    }
  } catch (cause) {
    error.value = (cause as { message?: string })?.message || text('读取失败', 'Read failed')
  } finally {
    probingID.value = null
  }
}

async function loadDetail(accountId: number) {
  const [points, changes] = await Promise.all([
    getHistory(accountId, historyDays.value),
    listRateChanges({ page: 1, pageSize: 20, days: 30, accountId }),
  ])
  history.value = points.points
  detailRateChanges.value = changes.items
}

async function openDetail(row: UpstreamMonitorAccount) {
  selected.value = row
  detailOpen.value = true
  history.value = []
  detailRateChanges.value = []
  try {
    await loadDetail(row.account_id)
  } catch {
    // The trend is optional; the snapshot summary above is still useful.
  }
}

async function loadHistory(days: number) {
  if (!selected.value) return
  historyDays.value = days
  try {
    const points = await getHistory(selected.value.account_id, days)
    history.value = points.points
  } catch {
    history.value = []
  }
}

function closeDetail() {
  detailOpen.value = false
  selected.value = null
  history.value = []
  detailRateChanges.value = []
}

const chart = computed(() => {
  const points = history.value
  if (points.length < 2) return null
  const values = points.map(point => point.balance)
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const width = 600
  const height = 120
  const polyline = points.map((point, index) => {
    const x = (index / (points.length - 1)) * width
    const y = height - ((point.balance - min) / span) * (height - 12) - 6
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
  return { polyline, min, max }
})

let searchTimer: ReturnType<typeof setTimeout> | undefined
watch([statusFilter, platformFilter, sort], () => { page.value = 1; void refresh() })
watch(search, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; void refresh() }, 300)
})
watch(rateOpen, open => { if (open && !rateChanges.value.length) void loadRateChanges(1) })

onMounted(() => {
  void refresh()
  void loadRateChanges(1)
})
onBeforeUnmount(() => { if (searchTimer) clearTimeout(searchTimer) })
</script>

