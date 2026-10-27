<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <section class="space-y-5 p-4 sm:p-6" :aria-label="t('traffic.title')" :aria-busy="loading">
    <p class="rounded-lg bg-sky-50 p-3 text-sm text-sky-900 dark:bg-sky-950/40 dark:text-sky-200">{{ t('traffic.scope') }}</p>
    <div v-if="admin" class="rounded-lg border p-3 text-sm" :class="healthError || health?.status === 'degraded' ? 'border-red-300 text-red-700 dark:text-red-300' : health?.status === 'delayed' ? 'border-amber-300 text-amber-700 dark:text-amber-300' : 'border-gray-200 text-gray-600 dark:border-dark-700 dark:text-gray-300'" data-testid="traffic-health">
      <template v-if="health">
        <p class="font-medium">{{ t('traffic.nodeHealth') }} · {{ t(`traffic.healthStatuses.${health.status}`) }}</p>
        <p>{{ t('traffic.queueSummary', { queued: health.queued_events ?? 0, capacity: health.queue_capacity ?? 0, processing: health.processing_events ?? 0, dropped: health.dropped_events ?? 0 }) }}</p>
        <p>{{ t('traffic.healthSummary', { pending: health.pending_events, age: health.oldest_pending_age_seconds, quarantined: health.quarantined_events, lost: health.persistence_failures }) }}</p>
        <p v-if="health.storage_available === false" role="alert">{{ t('traffic.storageUnavailable') }}</p>
        <p class="mt-1 text-xs">{{ t('traffic.nodeHealthScope') }} · {{ displayTime(health.checked_at) }}</p>
      </template>
      <p v-if="!health && !healthError">{{ t('common.loading') }}</p>
      <p v-if="healthError" role="alert">{{ t('traffic.healthFailed') }}</p>
    </div>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyFilters">
      <div>
        <label for="traffic-period" class="input-label">{{ t('traffic.period') }}</label>
        <select id="traffic-period" v-model="period" class="input">
          <option v-for="value in periods" :key="value" :value="value">{{ t(`traffic.periods.${value}`) }}</option>
        </select>
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
      <button class="btn btn-primary" type="submit" :disabled="loading">{{ t('traffic.apply') }}</button>
      <button class="btn btn-secondary" type="button" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
      <button class="btn btn-secondary" type="button" @click="reset">{{ t('common.reset') }}</button>
    </form>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('traffic.keyHistoryHint') }}</p>
    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <div class="flex gap-2">
        <button class="btn btn-secondary btn-sm" type="button" :disabled="loading || !report" @click="shift(-1)">{{ t('traffic.previousPeriod') }}</button>
        <button class="btn btn-secondary btn-sm" type="button" :disabled="loading || !report" @click="shift(1)">{{ t('traffic.nextPeriod') }}</button>
      </div>
      <span v-if="report" class="text-gray-500 dark:text-gray-400">{{ rangeLabel }} · {{ report.timezone }}</span>
    </div>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/40 dark:text-red-300">{{ error }}</p>
    <p v-if="detailsError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ detailsError }} <button type="button" class="underline" @click="loadDetails(requestedPage)">{{ t('common.refresh') }}</button></p>
    <p v-if="summaryLoading" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('traffic.loading') }}</p>
    <template v-if="summary">
      <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <div v-for="card in cards" :key="card.label" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ card.label }}</p>
          <p class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-white" :title="card.raw">{{ card.value }}</p>
        </div>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ summary.recorded_since ? t('traffic.since', { time: displayTime(summary.recorded_since) }) : t('traffic.notRecorded') }} {{ t('traffic.history') }}</p>
      <div v-if="summary.requests" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <h3 class="mb-1 text-sm font-medium">{{ t('traffic.trend') }}</h3>
        <p class="mb-3 text-xs text-gray-500 dark:text-gray-400">{{ t(`traffic.trendScopes.${trendBucket}`) }}</p>
        <div class="h-64" role="img" :aria-label="t('traffic.trend')"><Bar :data="chartData" :options="chartOptions" /></div>
      </div>
      <details v-if="summary.requests" class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <summary class="cursor-pointer px-4 py-3 text-sm font-medium">{{ t('traffic.trendDetails') }}</summary>
        <table class="w-full text-left text-sm">
          <caption class="px-4 py-3 text-left font-medium">{{ t('traffic.trend') }} · {{ t('traffic.upload') }} / {{ t('traffic.download') }}</caption>
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="px-4 py-2">{{ t('traffic.bucket') }}</th><th class="min-w-40 px-4 py-2">{{ t('traffic.upload') }}</th><th class="min-w-40 px-4 py-2">{{ t('traffic.download') }}</th><th class="px-4 py-2">{{ t('traffic.total') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="point in summary.trend" :key="point.bucket">
              <td class="whitespace-nowrap px-4 py-2 font-mono text-xs">{{ point.bucket }}</td>
              <td v-for="direction in directions" :key="direction" class="px-4 py-2 tabular-nums" :title="`${point[direction]} B`">
                {{ bytes(point[direction]) }}
                <div class="mt-1 h-1.5 rounded bg-gray-100 dark:bg-dark-700"><div class="h-full rounded" :class="direction === 'upload_bytes' ? 'bg-sky-500' : 'bg-emerald-500'" :style="{ width: `${point[direction] / maxBytes * 100}%` }" /></div>
              </td>
              <td class="px-4 py-2 tabular-nums">{{ bytes(point.total_bytes) }}</td>
            </tr>
          </tbody>
        </table>
      </details>
    </template>
    <p v-if="detailsLoading" role="status" class="py-8 text-center text-sm text-gray-500">{{ t('traffic.loadingRecords') }}</p>
    <template v-if="details && !detailsLoading">
      <div class="overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-700">
        <table class="w-full text-left text-sm">
          <caption class="px-4 py-3 text-left font-medium">{{ t('traffic.records') }}</caption>
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="px-4 py-2">{{ t('usage.time') }}</th><th v-if="admin" class="px-4 py-2">{{ t('traffic.user') }}</th><th class="px-4 py-2">API Key</th><th class="px-4 py-2">{{ t('traffic.endpoint') }}</th><th class="px-4 py-2">{{ t('traffic.upload') }}</th><th class="px-4 py-2">{{ t('traffic.download') }}</th><th class="px-4 py-2">{{ t('traffic.result') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="item in details.items" :key="item.event_id">
              <td class="whitespace-nowrap px-4 py-3 text-xs">{{ displayTime(item.started_at) }}</td>
              <td v-if="admin" class="px-4 py-3">#{{ item.user_id }}</td>
              <td class="px-4 py-3">#{{ item.api_key_id }}</td>
              <td class="px-4 py-3"><span class="whitespace-nowrap font-mono text-xs">{{ item.method }} {{ item.endpoint }}</span><span class="block text-xs text-gray-500" :title="item.event_id">{{ item.request_id || item.event_id }}</span></td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums" :title="`${item.upload_bytes} B`">{{ bytes(item.upload_bytes) }}<span v-if="!item.request_complete" class="block text-xs text-amber-700 dark:text-amber-400">{{ t('traffic.partialRead') }}</span></td>
              <td class="whitespace-nowrap px-4 py-3 tabular-nums" :title="`${item.download_bytes} B`">{{ bytes(item.download_bytes) }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-xs">HTTP {{ item.status_code }}<span class="block" :class="item.outcome === 'completed' ? 'text-emerald-700 dark:text-emerald-400' : 'text-amber-700 dark:text-amber-400'">{{ t(`traffic.outcomes.${item.outcome}`) }}</span></td>
            </tr>
            <tr v-if="!details.items.length"><td :colspan="admin ? 7 : 6" class="p-8 text-center text-gray-500">{{ t('traffic.empty') }}</td></tr>
          </tbody>
        </table>
      </div>
      <div class="flex items-center justify-between text-sm">
        <span>{{ t('traffic.page', { page }) }}</span>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary" :disabled="page <= 1 || detailsLoading" @click="previousPage">{{ t('traffic.previous') }}</button><button type="button" class="btn btn-secondary" :disabled="!details?.next_cursor || detailsLoading" @click="nextPage">{{ t('traffic.next') }}</button></div>
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
import { getTraffic, getTrafficKeys, getTrafficHealth, formatTrafficBytes as bytes, type TrafficPeriod, type TrafficReport, type TrafficKeyOption, type TrafficDeliveryHealth } from '@/api/traffic'

const props = withDefaults(defineProps<{ admin?: boolean; apiKeyOptions?: SelectOption[] }>(), { admin: false, apiKeyOptions: () => [] })
const { t } = useI18n({ useScope: 'local', messages: { zh: { traffic: trafficZh }, en: { traffic: trafficEn } } })
ChartJS.register(CategoryScale, LinearScale, BarElement, Tooltip, Legend)
const periods: TrafficPeriod[] = ['day', 'week', 'month', 'year']
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
  for (const key of ownKeyResults.value) options.set(key.id, { value: key.id, label: `${key.name || `#${key.id}`}${key.deleted ? ` (${t('traffic.deletedKey')})` : ''}` })
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
    if (!current.signal.aborted) ownKeyResults.value = result
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
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: report.value?.timezone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date(value))
  const get = (type: string) => parts.find(p => p.type === type)?.value || ''
  return `${get('year')}-${get('month')}-${get('day')}`
}
const currentDate = computed(() => report.value ? siteDate(new Date()) : '')
const displayTime = (value: string) => new Intl.DateTimeFormat(undefined, { timeZone: report.value?.timezone || 'UTC', dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
const rangeLabel = computed(() => report.value ? `${siteDate(report.value.start)} — ${siteDate(new Date(new Date(report.value.end).getTime() - 1))}` : '')
const cards = computed(() => summary.value ? [
  { label: t('traffic.upload'), value: bytes(summary.value.upload_bytes), raw: `${summary.value.upload_bytes} B` },
  { label: t('traffic.download'), value: bytes(summary.value.download_bytes), raw: `${summary.value.download_bytes} B` },
  { label: t('traffic.total'), value: bytes(summary.value.total_bytes), raw: `${summary.value.total_bytes} B` },
  { label: t('traffic.requests'), value: summary.value.requests.toLocaleString(), raw: String(summary.value.requests) }
] : [])
const maxBytes = computed(() => Math.max(1, ...(summary.value?.trend.flatMap(p => [p.upload_bytes, p.download_bytes]) || [])))
const trendBucket = computed(() => summary.value?.period === 'day' ? 'hour' : summary.value?.period === 'year' ? 'month' : 'day')
const chartData = computed(() => ({
  labels: summary.value?.trend.map(point => point.bucket) || [],
  datasets: [
    { label: t('traffic.upload'), data: summary.value?.trend.map(point => point.upload_bytes) || [], backgroundColor: '#0ea5e9', borderRadius: 3 },
    { label: t('traffic.download'), data: summary.value?.trend.map(point => point.download_bytes) || [], backgroundColor: '#10b981', borderRadius: 3 }
  ]
}))
const chartOptions = computed<ChartOptions<'bar'>>(() => ({
  responsive: true, maintainAspectRatio: false,
  plugins: { legend: { position: 'bottom' }, tooltip: { callbacks: { label: context => `${context.dataset.label}: ${bytes(Number(context.raw))}` } } },
  scales: { x: { stacked: true, grid: { display: false }, ticks: { maxRotation: 0, maxTicksLimit: 6 } }, y: { stacked: true, beginAtZero: true, ticks: { callback: value => bytes(Number(value)) } } }
}))
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
      queryDate = siteDate(result.start)
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
