<!-- Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later. -->
<template>
  <section class="flex flex-col gap-4 sm:gap-6" :aria-label="t('traffic.title')" :aria-busy="loading">
    <form class="flex flex-wrap items-start justify-between gap-1.5" @submit.prevent="applyFilters">
      <div class="flex flex-wrap items-start gap-1.5">
        <div id="traffic-period">
          <UTabs :model-value="period" :items="periodItems" :content="false" size="sm" :aria-label="t('traffic.period')" @update:model-value="(value) => (period = value as TrafficPeriod)" />
        </div>
        <UInput
          id="traffic-date"
          :model-value="date || currentDate"
          type="date"
          class="w-40"
          :aria-label="t('traffic.date')"
          :ui="{ base: 'dark:[color-scheme:dark]' }"
          @change="setDate"
        />
        <UsageFilters
          v-if="admin"
          :key="searchResetKey"
          v-model="searchFilters"
          :api-key-search="searchAdminKeys"
          @change="applyFilters"
        />
        <div v-else class="w-full sm:w-auto sm:min-w-[220px]">
          <Select :key="searchResetKey" id="traffic-key" v-model="searchFilters.api_key_id" :options="ownKeyOptions" searchable remote :loading="ownKeyLoading" :aria-label="t('usage.apiKeyFilter')" @click="onOwnKeyOpen" @keydown.down="onOwnKeyOpen" @keydown.up="onOwnKeyOpen" @search="searchOwnKeys" @change="selectOwnKey">
            <template #selected="{ option }">{{ searchFilters.api_key_id ? selectedKeyLabel : option?.label || t('usage.allApiKeys') }}</template>
          </Select>
          <p v-if="ownKeyError" role="alert" class="mt-1 flex items-center gap-1 text-xs text-error">
            {{ t('traffic.keysFailed') }}
            <UButton color="error" variant="link" size="xs" class="p-0" @click="searchOwnKeys(ownKeyQuery)">{{ t('common.refresh') }}</UButton>
          </p>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <UButton type="submit" :disabled="loading">{{ t('traffic.apply') }}</UButton>
        <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :disabled="loading" @click="load">{{ t('common.refresh') }}</UButton>
        <UButton color="neutral" variant="outline" icon="i-lucide-filter-x" @click="reset">{{ t('common.reset') }}</UButton>
      </div>
    </form>

    <div class="flex flex-wrap items-center justify-between gap-3 text-sm">
      <div class="flex items-center gap-1.5">
        <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-chevron-left" :disabled="loading || !report" @click="shift(-1)">{{ t('traffic.previousPeriod') }}</UButton>
        <UButton color="neutral" variant="ghost" size="sm" trailing-icon="i-lucide-chevron-right" :disabled="loading || !report" @click="shift(1)">{{ t('traffic.nextPeriod') }}</UButton>
      </div>
      <span v-if="report" class="tabular-nums text-muted">{{ rangeLabel }} · {{ report.timezone }}</span>
    </div>

    <UAlert color="info" variant="subtle" icon="i-lucide-info" :description="t('traffic.scope')" />
    <p class="-mt-2 text-xs text-muted sm:-mt-4">{{ t('traffic.keyHistoryHint') }}</p>

    <UAlert
      v-if="admin"
      data-testid="traffic-health"
      :color="healthError || health?.status === 'degraded' ? 'error' : health?.status === 'delayed' ? 'warning' : 'neutral'"
      variant="subtle"
      icon="i-lucide-activity"
    >
      <template #title>
        <span v-if="health">{{ t('traffic.nodeHealth') }} · {{ t(`traffic.healthStatuses.${health.status}`) }}</span>
        <span v-else-if="!healthError">{{ t('common.loading') }}</span>
        <span v-if="healthError" role="alert">{{ t('traffic.healthFailed') }}</span>
      </template>
      <template v-if="health" #description>
        <p>{{ t('traffic.queueSummary', { queued: health.queued_events ?? 0, capacity: health.queue_capacity ?? 0, processing: health.processing_events ?? 0, dropped: health.dropped_events ?? 0 }) }}</p>
        <p>{{ t('traffic.healthSummary', { pending: health.pending_events, age: health.oldest_pending_age_seconds, quarantined: health.quarantined_events, lost: health.persistence_failures }) }}</p>
        <p v-if="health.storage_available === false" role="alert">{{ t('traffic.storageUnavailable') }}</p>
        <p class="mt-1 text-xs opacity-80">{{ t('traffic.nodeHealthScope') }} · {{ displayTime(health.checked_at) }}</p>
      </template>
    </UAlert>

    <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
    <p v-if="detailsError" role="alert" class="flex items-center gap-1 text-sm text-error">
      {{ detailsError }}
      <UButton color="error" variant="link" size="sm" class="p-0" @click="loadDetails(requestedPage)">{{ t('common.refresh') }}</UButton>
    </p>
    <p v-if="summaryLoading" role="status" class="py-8 text-center text-sm text-muted">{{ t('traffic.loading') }}</p>

    <template v-if="summary">
      <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
        <UPageCard
          v-for="card in cards"
          :key="card.label"
          :icon="card.icon"
          :title="card.label"
          variant="subtle"
          :ui="{
            container: 'gap-y-1.5',
            wrapper: 'items-start',
            leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
            title: 'font-normal text-muted text-xs uppercase'
          }"
          class="first:rounded-l-lg last:rounded-r-lg hover:z-1 lg:rounded-none"
        >
          <span class="text-2xl font-semibold tabular-nums text-highlighted" :title="card.raw">{{ card.value }}</span>
        </UPageCard>
      </UPageGrid>
      <p class="-mt-2 text-xs text-muted">{{ summary.recorded_since ? t('traffic.since', { time: displayTime(summary.recorded_since) }) : t('traffic.notRecorded') }} {{ t('traffic.history') }}</p>

      <UCard v-if="summary.requests" :ui="{ root: 'overflow-visible', body: '!px-0 !pt-0 !pb-3' }">
        <template #header>
          <p class="text-xs uppercase text-muted">{{ t('traffic.trend') }}</p>
          <p class="mt-1 text-sm text-muted">{{ t(`traffic.trendScopes.${trendBucket}`) }}</p>
        </template>
        <div class="h-64 px-4 pt-4" role="img" :aria-label="t('traffic.trend')"><Bar :data="chartData" :options="chartOptions" /></div>
      </UCard>

      <UCollapsible v-if="summary.requests">
        <UButton
          color="neutral"
          variant="ghost"
          trailing-icon="i-lucide-chevron-down"
          class="-mx-2.5"
          :ui="{ trailingIcon: 'transition-transform group-data-[state=open]:rotate-180' }"
          :label="t('traffic.trendDetails')"
        />
        <template #content>
          <UTable
            :data="summary.trend"
            :columns="trendColumns"
            :caption="`${t('traffic.trend')} · ${t('traffic.upload')} / ${t('traffic.download')}`"
            class="mt-2"
            :ui="tableUi"
          >
            <template #bucket-cell="{ row }"><span class="whitespace-nowrap font-mono text-xs">{{ row.original.bucket }}</span></template>
            <template #upload_bytes-cell="{ row }">
              <div class="min-w-40 tabular-nums" :title="`${row.original.upload_bytes} B`">
                {{ bytes(row.original.upload_bytes) }}
                <UProgress :model-value="row.original.upload_bytes / maxBytes * 100" color="info" size="2xs" class="mt-1" />
              </div>
            </template>
            <template #download_bytes-cell="{ row }">
              <div class="min-w-40 tabular-nums" :title="`${row.original.download_bytes} B`">
                {{ bytes(row.original.download_bytes) }}
                <UProgress :model-value="row.original.download_bytes / maxBytes * 100" color="success" size="2xs" class="mt-1" />
              </div>
            </template>
            <template #total_bytes-cell="{ row }"><span class="tabular-nums">{{ bytes(row.original.total_bytes) }}</span></template>
          </UTable>
        </template>
      </UCollapsible>
    </template>

    <p v-if="detailsLoading" role="status" class="py-8 text-center text-sm text-muted">{{ t('traffic.loadingRecords') }}</p>
    <template v-if="details && !detailsLoading">
      <UTable :data="details.items" :columns="recordColumns" :caption="t('traffic.records')" :ui="tableUi">
        <template #started_at-cell="{ row }"><span class="whitespace-nowrap text-xs">{{ displayTime(row.original.started_at) }}</span></template>
        <template #user_id-cell="{ row }">#{{ row.original.user_id }}</template>
        <template #api_key_id-cell="{ row }">#{{ row.original.api_key_id }}</template>
        <template #endpoint-cell="{ row }">
          <span class="whitespace-nowrap font-mono text-xs">{{ row.original.method }} {{ row.original.endpoint }}</span>
          <span class="block text-xs text-muted" :title="row.original.event_id">{{ row.original.request_id || row.original.event_id }}</span>
        </template>
        <template #upload_bytes-cell="{ row }">
          <span class="whitespace-nowrap tabular-nums" :title="`${row.original.upload_bytes} B`">{{ bytes(row.original.upload_bytes) }}</span>
          <span v-if="!row.original.request_complete" class="block text-xs text-warning">{{ t('traffic.partialRead') }}</span>
        </template>
        <template #download_bytes-cell="{ row }">
          <span class="whitespace-nowrap tabular-nums" :title="`${row.original.download_bytes} B`">{{ bytes(row.original.download_bytes) }}</span>
        </template>
        <template #outcome-cell="{ row }">
          <span class="whitespace-nowrap text-xs">HTTP {{ row.original.status_code }}</span>
          <UBadge :color="row.original.outcome === 'completed' ? 'success' : 'warning'" variant="subtle" size="sm" class="mt-0.5 block w-fit">{{ t(`traffic.outcomes.${row.original.outcome}`) }}</UBadge>
        </template>
        <template #empty><p class="py-6 text-center text-muted">{{ t('traffic.empty') }}</p></template>
      </UTable>
      <div class="flex items-center justify-between gap-3 border-t border-default pt-4 text-sm">
        <span class="text-muted">{{ t('traffic.page', { page }) }}</span>
        <div class="flex gap-1.5">
          <UButton color="neutral" variant="outline" :disabled="page <= 1 || detailsLoading" @click="previousPage">{{ t('traffic.previous') }}</UButton>
          <UButton color="neutral" variant="outline" :disabled="!details?.next_cursor || detailsLoading" @click="nextPage">{{ t('traffic.next') }}</UButton>
        </div>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn, TabsItem } from '@nuxt/ui'
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, Tooltip, Legend, type ChartOptions } from 'chart.js'
import { Bar } from 'vue-chartjs'
import UsageFilters from '@/components/usage/TrafficIdentityFilters.vue'
import trafficZh from '@/i18n/locales/zh/traffic'
import trafficEn from '@/i18n/locales/en/traffic'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { getTraffic, getTrafficKeys, getTrafficHealth, formatTrafficBytes as bytes, type TrafficPeriod, type TrafficReport, type TrafficKeyOption, type TrafficDeliveryHealth, type TrafficEvent, type TrafficTotals } from '@/api/traffic'

const props = withDefaults(defineProps<{ admin?: boolean; apiKeyOptions?: SelectOption[] }>(), { admin: false, apiKeyOptions: () => [] })
const { t } = useI18n({ useScope: 'local', messages: { zh: { traffic: trafficZh }, en: { traffic: trafficEn } } })
ChartJS.register(CategoryScale, LinearScale, BarElement, Tooltip, Legend)
const periods: TrafficPeriod[] = ['day', 'week', 'month', 'year']
const periodItems = computed<TabsItem[]>(() => periods.map((value) => ({ label: t(`traffic.periods.${value}`), value })))
type TrendPoint = TrafficTotals & { bucket: string }
const trendColumns = computed<TableColumn<TrendPoint>[]>(() => [
  { accessorKey: 'bucket', header: t('traffic.bucket') },
  { accessorKey: 'upload_bytes', header: t('traffic.upload') },
  { accessorKey: 'download_bytes', header: t('traffic.download') },
  { accessorKey: 'total_bytes', header: t('traffic.total') }
])
const recordColumns = computed<TableColumn<TrafficEvent>[]>(() => [
  { accessorKey: 'started_at', header: t('usage.time') },
  ...(props.admin ? [{ accessorKey: 'user_id', header: t('traffic.user') } as TableColumn<TrafficEvent>] : []),
  { accessorKey: 'api_key_id', header: 'API Key' },
  { accessorKey: 'endpoint', header: t('traffic.endpoint') },
  { accessorKey: 'upload_bytes', header: t('traffic.upload') },
  { accessorKey: 'download_bytes', header: t('traffic.download') },
  { accessorKey: 'outcome', header: t('traffic.result') }
])
// Bordered list table, same chrome as the template list page.
const tableUi = {
  root: 'rounded-lg ring ring-default',
  base: 'border-separate border-spacing-0',
  caption: 'px-4 py-3 text-left text-sm font-medium text-highlighted',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 border-b border-default',
  td: 'border-b border-default',
  separator: 'h-0'
}
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
  { label: t('traffic.upload'), icon: 'i-lucide-arrow-up-from-line', value: bytes(summary.value.upload_bytes), raw: `${summary.value.upload_bytes} B` },
  { label: t('traffic.download'), icon: 'i-lucide-arrow-down-to-line', value: bytes(summary.value.download_bytes), raw: `${summary.value.download_bytes} B` },
  { label: t('traffic.total'), icon: 'i-lucide-arrow-down-up', value: bytes(summary.value.total_bytes), raw: `${summary.value.total_bytes} B` },
  { label: t('traffic.requests'), icon: 'i-lucide-activity', value: summary.value.requests.toLocaleString(), raw: String(summary.value.requests) }
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
