<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <section class="space-y-5 p-4 sm:p-6" :aria-label="t('traffic.title')" :aria-busy="loading">
    <p class="rounded-lg bg-sky-50 p-3 text-sm text-sky-900 dark:bg-sky-950/40 dark:text-sky-200">{{ t('traffic.scope') }}</p>
    <div v-if="admin" class="rounded-lg border p-3 text-sm" :class="healthError || health?.status === 'degraded' ? 'border-red-300 text-red-700 dark:text-red-300' : health?.status === 'delayed' ? 'border-amber-300 text-amber-700 dark:text-amber-300' : 'border-default text-muted'" data-testid="traffic-health">
      <template v-if="health">
        <p class="font-medium">{{ t('traffic.nodeHealth') }} · {{ t(`traffic.healthStatuses.${health.status}`) }}</p>
        <p class="tabular-nums">{{ t('traffic.queueSummary', { queued: health.queued_events ?? 0, capacity: health.queue_capacity ?? 0, processing: health.processing_events ?? 0, dropped: health.dropped_events ?? 0 }) }}</p>
        <p class="tabular-nums">{{ t('traffic.healthSummary', { pending: health.pending_events, age: health.oldest_pending_age_seconds, quarantined: health.quarantined_events, lost: health.persistence_failures }) }}</p>
        <p v-if="health.storage_available === false" role="alert">{{ t('traffic.storageUnavailable') }}</p>
        <p class="mt-1 text-xs">{{ t('traffic.nodeHealthScope') }} · {{ displayTime(health.checked_at) }}</p>
      </template>
      <p v-if="!health && !healthError">{{ t('common.loading') }}</p>
      <p v-if="healthError" role="alert">{{ t('traffic.healthFailed') }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyFilters">
      <div>
        <label for="traffic-period" class="input-label">{{ t('traffic.period') }}</label>
        <Select id="traffic-period" v-model="period" :options="periodOptions" :searchable="false" class="w-32" :aria-label="t('traffic.period')" />
      </div>
      <div>
        <label for="traffic-date" class="input-label">{{ t('traffic.date') }}</label>
        <input id="traffic-date" :value="date || currentDate" type="date" class="input" @change="setDate" />
      </div>
      <UsageFilters
        v-if="admin"
        :key="searchResetKey"
        v-model="searchFilters"
        :api-key-search="searchAdminKeys"
        @change="applyFilters"
      />
      <div v-else class="w-full sm:w-auto sm:min-w-[220px]">
        <label for="traffic-key" class="input-label">{{ t('usage.apiKeyFilter') }}</label>
        <Select :key="searchResetKey" id="traffic-key" v-model="searchFilters.api_key_id" :options="ownKeyOptions" searchable remote :loading="ownKeyLoading" @click="onOwnKeyOpen" @keydown.down="onOwnKeyOpen" @keydown.up="onOwnKeyOpen" @search="searchOwnKeys" @change="selectOwnKey">
          <template #selected="{ option }">{{ searchFilters.api_key_id ? selectedKeyLabel : option?.label || t('usage.allApiKeys') }}</template>
        </Select>
        <p v-if="ownKeyError" role="alert" class="mt-1 text-xs text-red-600 dark:text-red-400">{{ t('traffic.keysFailed') }} <button type="button" class="underline" @click="searchOwnKeys(ownKeyQuery)">{{ t('common.refresh') }}</button></p>
      </div>
      <button class="btn btn-primary min-h-11 lg:min-h-0" type="submit" :disabled="loading">{{ t('traffic.apply') }}</button>
      <button class="btn btn-secondary min-h-11 lg:min-h-0" type="button" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
      <button class="btn btn-secondary min-h-11 lg:min-h-0" type="button" @click="reset">{{ t('common.reset') }}</button>
    </form>
    <p class="text-xs text-muted">{{ t('traffic.keyHistoryHint') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <div class="flex gap-2">
        <button class="btn btn-secondary btn-sm min-h-11 lg:min-h-0" type="button" :disabled="loading || !report" @click="shift(-1)">{{ t('traffic.previousPeriod') }}</button>
        <button class="btn btn-secondary btn-sm min-h-11 lg:min-h-0" type="button" :disabled="loading || !report" @click="shift(1)">{{ t('traffic.nextPeriod') }}</button>
      </div>
      <span v-if="report" class="tabular-nums text-muted">{{ rangeLabel }} · {{ report.timezone }}</span>
    </div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
    <p v-if="detailsError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ detailsError }} <button type="button" class="underline" @click="loadDetails(requestedPage)">{{ t('common.refresh') }}</button></p>
    <p v-if="summaryLoading" role="status" class="py-8 text-center text-sm text-muted">{{ t('traffic.loading') }}</p>
    <template v-if="summary">
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="card in cards" :key="card.label" class="rounded-xl border border-default p-4">
          <p class="text-sm text-muted">{{ card.label }}</p>
          <p class="mt-2 text-xl font-semibold tabular-nums text-highlighted" :title="card.raw">{{ card.value }}</p>
        </div>
      </div>
      <p class="text-xs text-muted">{{ summary.recorded_since ? t('traffic.since', { time: displayTime(summary.recorded_since) }) : t('traffic.notRecorded') }} {{ t('traffic.history') }}</p>
      <div v-if="summary.requests" class="rounded-xl border border-default p-4">
        <h3 class="mb-1 text-sm font-medium">{{ t('traffic.trend') }}</h3>
        <p class="mb-3 text-xs text-muted">{{ t(`traffic.trendScopes.${trendBucket}`) }}</p>
        <div class="h-64" role="img" :aria-label="t('traffic.trend')"><Bar :data="chartData" :options="chartOptions" /></div>
      </div>
      <details v-if="summary.requests" class="overflow-x-auto rounded-xl border border-default">
        <summary class="cursor-pointer px-4 py-3 text-sm font-medium">{{ t('traffic.trendDetails') }}</summary>
        <table class="w-full text-left text-sm">
          <caption class="px-4 py-3 text-left font-medium">{{ t('traffic.trend') }} · {{ t('traffic.upload') }} / {{ t('traffic.download') }}</caption>
          <thead class="bg-elevated text-muted"><tr><th class="px-4 py-2">{{ t('traffic.bucket') }}</th><th class="min-w-40 px-4 py-2">{{ t('traffic.upload') }}</th><th class="min-w-40 px-4 py-2">{{ t('traffic.download') }}</th><th class="px-4 py-2">{{ t('traffic.total') }}</th></tr></thead>
          <tbody class="divide-y divide-default">
            <tr v-for="point in (summary.trend ?? [])" :key="point.bucket">
              <td class="whitespace-nowrap px-4 py-2 font-mono text-xs">{{ point.bucket }}</td>
              <td v-for="direction in directions" :key="direction" class="px-4 py-2 tabular-nums" :title="`${point[direction]} B`">
                {{ bytes(point[direction]) }}
                <div class="mt-1 h-1.5 overflow-hidden rounded-sm bg-elevated"><div class="h-full w-full origin-left rounded-sm" :class="direction === 'upload_bytes' ? 'bg-primary' : 'bg-emerald-500'" :style="{ transform: `scaleX(${Math.min(1, Math.max(0, point[direction] / maxBytes))})` }" /></div>
              </td>
              <td class="px-4 py-2 tabular-nums">{{ bytes(point.total_bytes) }}</td>
            </tr>
          </tbody>
        </table>
      </details>
    </template>
    <p v-if="detailsLoading" role="status" class="py-8 text-center text-sm text-muted">{{ t('traffic.loadingRecords') }}</p>
    <template v-if="details && !detailsLoading">
      <div class="overflow-x-auto rounded-xl border border-default">
        <table class="w-full text-left text-sm">
          <caption class="px-4 py-3 text-left font-medium">{{ t('traffic.records') }}</caption>
          <thead class="bg-elevated text-muted"><tr><th class="px-4 py-2">{{ t('usage.time') }}</th><th v-if="admin" class="px-4 py-2">{{ t('traffic.user') }}</th><th class="px-4 py-2">API Key</th><th class="px-4 py-2">{{ t('traffic.endpoint') }}</th><th class="px-4 py-2">{{ t('traffic.upload') }}</th><th class="px-4 py-2">{{ t('traffic.download') }}</th><th class="px-4 py-2">{{ t('traffic.result') }}</th></tr></thead>
          <tbody class="divide-y divide-default">
            <tr v-for="item in (details.items ?? [])" :key="item.event_id">
              <td class="whitespace-nowrap px-4 py-3 text-xs tabular-nums">{{ displayTime(item.started_at) }}</td>
              <td v-if="admin" class="px-4 py-3 tabular-nums">#{{ item.user_id }}</td>
              <td class="px-4 py-3 tabular-nums">#{{ item.api_key_id }}</td>
              <td class="px-4 py-3"><span class="whitespace-nowrap font-mono text-xs">{{ item.method }} {{ item.endpoint }}</span><span class="block wrap-anywhere text-xs text-muted" :title="item.event_id">{{ item.request_id || item.event_id }}</span></td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums" :title="`${item.upload_bytes} B`">{{ bytes(item.upload_bytes) }}<span v-if="!item.request_complete" class="block text-xs text-amber-700 dark:text-amber-400">{{ t('traffic.partialRead') }}</span></td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums" :title="`${item.download_bytes} B`">{{ bytes(item.download_bytes) }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs tabular-nums">HTTP {{ item.status_code }}<span class="block" :class="item.outcome === 'completed' ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">{{ t(`traffic.outcomes.${item.outcome}`) }}</span></td>
            </tr>
            <tr v-if="!(details.items ?? []).length"><td :colspan="admin ? 7 : 6" class="p-8 text-center text-muted">{{ t('traffic.empty') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="flex items-center justify-between text-sm">
        <span class="tabular-nums">{{ t('traffic.page', { page }) }}</span>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary min-h-11 sm:min-h-0" :disabled="page <= 1 || detailsLoading" @click="previousPage">{{ t('traffic.previous') }}</button><button type="button" class="btn btn-secondary min-h-11 sm:min-h-0" :disabled="!details?.next_cursor || detailsLoading" @click="nextPage">{{ t('traffic.next') }}</button></div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, Tooltip, Legend, type ChartOptions } from 'chart.js'
import { Bar } from 'vue-chartjs'
import UsageFilters from '@/components/usage/TrafficIdentityFilters.vue'
import trafficZh from '@/i18n/locales/zh/traffic'
import trafficEn from '@/i18n/locales/en/traffic'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { chartTooltipStyle, useChartTheme } from '@/components/charts/chartTheme'
import { getTraffic, getTrafficKeys, getTrafficHealth, formatTrafficBytes as bytes, type TrafficPeriod, type TrafficReport, type TrafficKeyOption, type TrafficDeliveryHealth } from '@/api/traffic'

const props = withDefaults(defineProps<{ admin?: boolean; apiKeyOptions?: SelectOption[] }>(), { admin: false, apiKeyOptions: () => [] })
const { t } = useI18n({ useScope: 'local', messages: { zh: { traffic: trafficZh }, en: { traffic: trafficEn } } })
const chartTheme = useChartTheme()
ChartJS.register(CategoryScale, LinearScale, BarElement, Tooltip, Legend)
const periods: TrafficPeriod[] = ['day', 'week', 'month', 'year']
const periodOptions = computed(() => periods.map(value => ({ value, label: t(`traffic.periods.${value}`) })))
const directions = ['upload_bytes', 'download_bytes'] as const
const period = ref<TrafficPeriod>('month'), date = ref(''), page = ref(1)
const searchFilters = ref<{ user_id?: number; api_key_id?: number | null }>({ api_key_id: null })
const searchResetKey = ref(0)
const filters = ref<{ user_id?: number; api_key_id?: number }>({})
const summary = ref<TrafficReport | null>(null), details = ref<TrafficReport | null>(null)
const report = computed(() => details.value || summary.value)
const summaryLoading = ref(false), detailsLoading = ref(false), error = ref(''), detailsError = ref('')
const loading = computed(() => summaryLoading.value || detailsLoading.value)
let cursors: Array<string | undefined> = [undefined]
let queryDate: string | undefined
const requestedPage = ref(1)
const health = ref<TrafficDeliveryHealth | null>(null), healthError = ref(false)
const ownKeyResults = ref<TrafficKeyOption[]>([]), ownKeyQuery = ref(''), ownKeyLoading = ref(false), ownKeyError = ref(false), ownKeyLabel = ref('')
let keyController: AbortController | undefined
let controller: AbortController | undefined
let detailsController: AbortController | undefined
const ownKeyOptions = computed<SelectOption[]>(() => {
  const options = new Map<string | number | boolean | null, SelectOption>()
  if (!ownKeyQuery.value) for (const option of props.apiKeyOptions) options.set(option.value, option)
  if (Array.isArray(ownKeyResults.value)) {
    for (const key of ownKeyResults.value) options.set(key.id, { value: key.id, label: `${key.name || `#${key.id}`}${key.deleted ? ` (${t('traffic.deletedKey')})` : ''}` })
  }
  options.delete(null)
  return [{ value: null, label: t('usage.allApiKeys') }, ...options.values()]
})
const selectedKeyLabel = computed(() => ownKeyLabel.value || ownKeyOptions.value.find(option => option.value === searchFilters.value.api_key_id)?.label || `#${searchFilters.value.api_key_id}`)
const searchAdminKeys = (userId: number | undefined, query: string) => getTrafficKeys(true, userId, query)
async function searchOwnKeys(query = '') {
  keyController?.abort()
  const current = new AbortController(); keyController = current
  ownKeyQuery.value = query; ownKeyLoading.value = true; ownKeyError.value = false; ownKeyResults.value = []
  try {
    const result = await getTrafficKeys(false, undefined, query, current.signal)
    if (!current.signal.aborted) ownKeyResults.value = Array.isArray(result) ? result : []
  } catch {
    if (!current.signal.aborted) ownKeyError.value = true
  } finally { if (!current.signal.aborted) ownKeyLoading.value = false }
}
function selectOwnKey(_value: unknown, option: SelectOption | null) { ownKeyLabel.value = option?.label || ''; applyFilters() }
function onOwnKeyOpen(event: Event) {
  if (!(event.target instanceof Element) || !event.target.closest('#traffic-key')) return
  if (ownKeyQuery.value) void searchOwnKeys()
}
const siteDate = (value: string | Date) => {
  if (!value) return ''
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: report.value?.timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(d)
  const get = (type: string) => parts.find(p => p.type === type)?.value || ''
  return `${get('year')}-${get('month')}-${get('day')}`
}
const currentDate = computed(() => report.value ? siteDate(new Date()) : '')
const displayTime = (value: string) => {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return new Intl.DateTimeFormat(undefined, { timeZone: report.value?.timezone || 'UTC', dateStyle: 'short', timeStyle: 'medium' }).format(d)
}
const rangeLabel = computed(() => {
  if (!report.value) return ''
  const start = siteDate(report.value.start)
  const endDate = new Date(report.value.end)
  const end = Number.isNaN(endDate.getTime()) ? '' : siteDate(new Date(endDate.getTime() - 1))
  if (!start && !end) return '—'
  return `${start || '—'} — ${end || '—'}`
})
const cards = computed(() => summary.value ? [
  { label: t('traffic.upload'), value: bytes(summary.value.upload_bytes ?? 0), raw: `${summary.value.upload_bytes ?? 0} B` },
  { label: t('traffic.download'), value: bytes(summary.value.download_bytes ?? 0), raw: `${summary.value.download_bytes ?? 0} B` },
  { label: t('traffic.total'), value: bytes(summary.value.total_bytes ?? 0), raw: `${summary.value.total_bytes ?? 0} B` },
  { label: t('traffic.requests'), value: (summary.value.requests ?? 0).toLocaleString(), raw: String(summary.value.requests ?? 0) }
] : [])
const maxBytes = computed(() => Math.max(1, ...((summary.value?.trend ?? []).flatMap(p => [p.upload_bytes, p.download_bytes]))))
const trendBucket = computed(() => summary.value?.period === 'day' ? 'hour' : summary.value?.period === 'year' ? 'month' : 'day')
const chartData = computed(() => {
  const theme = chartTheme.value
  const trend = summary.value?.trend ?? []
  return {
    labels: trend.map(point => point.bucket),
    datasets: [
      { label: t('traffic.upload'), data: trend.map(point => point.upload_bytes), backgroundColor: theme.input, borderRadius: 3 },
      { label: t('traffic.download'), data: trend.map(point => point.download_bytes), backgroundColor: theme.output, borderRadius: 3 }
    ]
  }
})
const chartOptions = computed<ChartOptions<'bar'>>(() => {
  const theme = chartTheme.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    plugins: {
      legend: { position: 'bottom', labels: { color: theme.text } },
      tooltip: {
        ...chartTooltipStyle(theme),
        callbacks: { label: context => `${context.dataset.label}: ${bytes(Number(context.raw))}` }
      }
    },
    scales: {
      x: { stacked: true, grid: { display: false }, ticks: { color: theme.text, maxRotation: 0, maxTicksLimit: 6 } },
      y: { stacked: true, beginAtZero: true, grid: { color: theme.grid }, ticks: { color: theme.text, callback: value => bytes(Number(value)) } }
    }
  }
})
function setDate(event: Event) { date.value = (event.target as HTMLInputElement).value }
function applyFilters() {
  const user = searchFilters.value.user_id, key = searchFilters.value.api_key_id
  filters.value = { ...(props.admin && user ? { user_id: user } : {}), ...(key ? { api_key_id: key } : {}) }
}
function reset() {
  searchFilters.value = { api_key_id: null }; searchResetKey.value++; date.value = ''; period.value = 'month'; filters.value = {}; ownKeyLabel.value = ''
  if (!props.admin) void searchOwnKeys()
}
function shift(direction: number) {
  if (!report.value) return
  const anchor = new Date(`${siteDate(report.value.start)}T00:00:00Z`)
  if (period.value === 'year') anchor.setUTCFullYear(anchor.getUTCFullYear() + direction)
  else if (period.value === 'month') anchor.setUTCMonth(anchor.getUTCMonth() + direction)
  else anchor.setUTCDate(anchor.getUTCDate() + direction * (period.value === 'week' ? 7 : 1))
  date.value = anchor.toISOString().slice(0, 10)
}
function nextPage() {
  if (!details.value?.next_cursor || detailsLoading.value) return
  cursors[page.value] = details.value.next_cursor
  cursors.length = page.value + 1
  void loadDetails(page.value + 1)
}
function previousPage() { if (page.value > 1 && !detailsLoading.value) void loadDetails(page.value - 1) }
async function loadDetails(targetPage: number) {
  detailsController?.abort()
  const current = new AbortController(); detailsController = current
  requestedPage.value = targetPage
  detailsLoading.value = true; detailsError.value = ''; details.value = null
  try {
    const result = await getTraffic(props.admin, { period: period.value, date: queryDate, view: 'details', cursor: cursors[targetPage - 1], page_size: 20, ...filters.value }, current.signal)
    if (!current.signal.aborted) {
      details.value = result; page.value = targetPage
      // Keep subsequent pages in the same calendar period across midnight.
      const nextDate = siteDate(result?.start)
      if (nextDate) queryDate = nextDate
      if (summary.value && summary.value.start !== result.start && controller) void loadSummary(controller)
    }
  } catch {
    if (!current.signal.aborted) detailsError.value = t('traffic.recordsFailed')
  } finally { if (!current.signal.aborted) detailsLoading.value = false }
}
async function load() {
  controller?.abort(); detailsController?.abort()
  const current = new AbortController(); controller = current
  cursors = [undefined]; page.value = 1; queryDate = date.value || undefined
  summaryLoading.value = true; error.value = ''; summary.value = null
  if (props.admin) void loadHealth(current)
  // Details remain usable while the more expensive summary is loading/failing.
  void loadDetails(1)
  void loadSummary(current)
}
async function loadSummary(current: AbortController) {
  summaryLoading.value = true; summary.value = null; error.value = ''
  try {
    let result = await getTraffic(props.admin, { period: period.value, date: queryDate, view: 'summary', page_size: 20, ...filters.value }, current.signal)
    if (current.signal.aborted) return
    // Initial requests may straddle midnight. Align the summary with the
    // details' resolved site-calendar period rather than mixing two periods.
    if (details.value && details.value.start !== result.start) {
      result = await getTraffic(props.admin, { period: period.value, date: queryDate, view: 'summary', page_size: 20, ...filters.value }, current.signal)
    }
    if (!current.signal.aborted) summary.value = result
  } catch {
    if (!current.signal.aborted) error.value = t('traffic.failed')
  } finally { if (!current.signal.aborted) summaryLoading.value = false }
}
async function loadHealth(current: AbortController) {
  try {
    const result = await getTrafficHealth(current.signal)
    if (!current.signal.aborted) { health.value = result; healthError.value = false }
  } catch {
    if (!current.signal.aborted) { health.value = null; healthError.value = true }
  }
}
watch([period, date, filters], () => { void load() }, { deep: true })
void load()
if (!props.admin) void searchOwnKeys()
onUnmounted(() => { controller?.abort(); detailsController?.abort(); keyController?.abort() })
</script>
