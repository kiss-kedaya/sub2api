<template>
  <div class="space-y-4 p-4 sm:p-6" data-testid="usage-connections">
    <p class="rounded-lg bg-sky-50 p-3 text-sm text-sky-800 dark:bg-sky-950/30 dark:text-sky-200">{{ t('connections.scope') }}</p>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="reload()">
      <label class="text-sm">{{ t('connections.state') }}<select v-model="state" class="input mt-1" @change="reload()"><option v-for="s in states" :key="s" :value="s">{{ t(`connections.states.${s}`) }}</option></select></label>
      <label class="text-sm">{{ t('connections.source') }}<select v-model="source" class="input mt-1" @change="reload()"><option v-for="s in sources" :key="s" :value="s">{{ t(`connections.sources.${s}`) }}</option></select></label>
      <label class="text-sm">{{ t('connections.host') }}<input v-model="host" class="input mt-1" maxlength="255" placeholder="api.example.com" /></label>
      <label class="text-sm">{{ t('connections.requestId') }}<input v-model="requestId" class="input mt-1" maxlength="128" /></label>
      <label class="text-sm">IP<input v-model="clientIp" class="input mt-1" maxlength="64" /></label>
      <button class="btn btn-secondary" type="submit">{{ t('common.search') }}</button>
      <button class="btn btn-secondary" type="button" @click="resetLocal">{{ t('common.reset') }}</button>
    </form>
    <p class="text-xs text-gray-500">{{ t('connections.identityScope') }}</p>
    <div v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }} <button class="underline" @click="load">{{ t('requestTiming.retry') }}</button></div>
    <DataTable :columns="columns" :data="rows" :loading="loading" row-key="trace_id">
      <template #cell-started_at="{ row }"><span class="whitespace-nowrap">{{ formatDateTime(row.started_at) }}</span></template>
      <template #cell-url="{ row }"><span class="block text-xs text-gray-500">{{ row.method }} · {{ row.http_version || '—' }} · {{ row.tls_version || '—' }}</span><span class="break-all font-mono text-xs">{{ url(row) }}</span></template>
      <template #cell-source="{ row }">{{ t(`connections.sources.${row.source}`) }}<span v-if="row.legacy" class="block text-xs text-gray-500">{{ t('connections.legacy') }}</span></template>
      <template #cell-user="{ row }"><span>{{ row.user_name || row.user_email || (row.user_id ? `#${row.user_id}` : t('connections.unidentified')) }}</span><span v-if="row.detail.identity_source === 'recognized_credential'" class="block text-xs text-amber-700">{{ t('connections.recognizedCredential') }}</span><span v-if="row.api_key_id" class="block text-xs text-gray-500">{{ row.api_key_name || `#${row.api_key_id}` }}</span></template>
      <template #cell-state="{ row }"><span class="rounded px-2 py-1 text-xs" :class="stateClass(row.state)">{{ t(`connections.states.${row.state}`) }}</span><span class="mt-1 block text-xs text-gray-500">HTTP {{ row.legacy && !row.detail.version ? '—' : row.detail.status || '—' }}</span><span v-if="row.detail.auth_rejection || row.error_type" class="mt-1 block text-xs text-gray-500">{{ row.detail.auth_rejection || row.error_type }} · {{ row.error_status || row.detail.status }}</span></template>
      <template #cell-model="{ row }">{{ row.model || '—' }}<span v-if="row.account_id" class="block text-xs text-gray-500">{{ row.account_name || `#${row.account_id}` }}</span></template>
      <template #cell-total="{ row }">{{ elapsed(row) }}<span class="block text-xs text-gray-500">{{ row.detail.downstream_bytes == null || (row.network_error === 'tls_error' && !row.detail.status) || (row.legacy && !row.detail.version) ? t('requestTiming.missing') : t('connections.bytes', { count: row.detail.downstream_bytes }) }}</span></template>
      <template #cell-actions="{ row }"><button class="btn btn-secondary btn-sm" @click="selected = row">{{ t('connections.details') }}</button></template>
      <template #empty><p class="p-6 text-center text-sm text-gray-500">{{ t('connections.empty') }}</p></template>
    </DataTable>
    <div class="flex items-center justify-between gap-3 text-sm">
      <span>{{ t('connections.page', { page }) }}</span>
      <div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading || page <= 1" @click="changePage(-1)">{{ t('connections.previous') }}</button><button class="btn btn-secondary" :disabled="loading || !hasMore || page >= 500" @click="changePage(1)">{{ t('connections.next') }}</button></div>
    </div>
    <OpsErrorDetailModal v-model:show="showErrorDetail" :error-id="errorDetailId" error-type="request" />
    <BaseDialog :show="!!selected" :title="t('connections.details')" width="extra-wide" close-on-click-outside @close="selected = null">
      <div v-if="selected" class="space-y-5">
        <p class="rounded-lg bg-sky-50 p-3 text-sm text-sky-800 dark:bg-sky-950/30 dark:text-sky-200">{{ t('connections.scope') }}</p>
        <dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div v-for="item in summary(selected)" :key="item.label"><dt class="text-gray-500">{{ item.label }}</dt><dd class="mt-1 break-all font-mono text-xs">{{ item.value }}</dd></div>
        </dl>
        <button v-if="selected.error_log_id" class="btn btn-secondary" @click="openError(selected)">{{ t('connections.errorDetail') }}</button>
        <p v-if="selected.detail.truncated" class="text-sm text-amber-600">{{ t('requestTiming.truncated') }}</p>
        <section><h4 class="mb-3 font-semibold">{{ t('requestTiming.timeline') }}</h4><p v-if="!events.length" class="text-sm text-gray-500">{{ t('connections.noTimeline') }}</p><ol v-else class="space-y-2 border-l border-primary-200 pl-4 dark:border-primary-800"><li v-for="[name, value] in events" :key="name" class="flex justify-between gap-4 text-sm"><span>{{ field(name) }}</span><span class="tabular-nums">{{ ms(value) }}</span></li></ol></section>
        <section v-if="selected.detail.attempts?.length"><h4 class="mb-3 font-semibold">{{ t('requestTiming.attempt') }}</h4><div v-for="a in selected.detail.attempts" :key="a.number" class="mb-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600"><p>#{{ a.number }} · {{ a.kind }} · {{ t('requestTiming.account') }} #{{ a.account_id }} · HTTP {{ a.status || '—' }}</p><p class="mt-1 text-xs text-gray-500">{{ a.cleanup_canceled ? t('requestTiming.health.normalClose') : a.error ? field(a.error) : t('requestTiming.health.noTransportError') }} · {{ a.terminal || '—' }}</p></div></section>
      </div>
    </BaseDialog>
  </div>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { isCancel } from 'axios'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import OpsErrorDetailModal from '@/views/admin/ops/components/OpsErrorDetailModal.vue'
import { formatDateTime } from '@/utils/format'
import { listConnections, type ConnectionQuery, type ConnectionTrace, type ConnectionState } from '@/api/admin/usageConnections'
const props = defineProps<{ filters: ConnectionQuery; refreshKey: number }>()
import zhConnections from '@/i18n/locales/zh/connections'
import enConnections from '@/i18n/locales/en/connections'
import zhTiming from '@/i18n/locales/zh/requestTiming'
import enTiming from '@/i18n/locales/en/requestTiming'
const { t, te } = useI18n({useScope:'local',messages:{zh:{connections:zhConnections,requestTiming:zhTiming},en:{connections:enConnections,requestTiming:enTiming}},inheritLocale:true})
const state = ref('all'), source = ref('all'), host = ref(''), requestId = ref(''), clientIp = ref('')
const rows = ref<ConnectionTrace[]>([]), page = ref(1), hasMore = ref(false), loading = ref(false), error = ref(''), selected = ref<ConnectionTrace | null>(null)
const showErrorDetail = ref(false), errorDetailId = ref<number | null>(null)
function openError(r: ConnectionTrace) { errorDetailId.value = r.error_log_id; selected.value = null; showErrorDetail.value = true }
const states = ['all', 'abnormal', 'client_disconnected', 'downstream_write_failed', 'incomplete', 'failed', 'canceled', 'edge_failed', 'completed', 'edge_completed', 'unknown']
const sources = ['all', 'edge', 'application', 'nginx', 'caddy']
let controller: AbortController | undefined
const columns = computed(() => [
  { key: 'started_at', label: t('usage.time') }, { key: 'source', label: t('connections.source') }, { key: 'url', label: 'URL' },
  { key: 'user', label: t('admin.usage.user') }, { key: 'model', label: t('usage.model') }, { key: 'state', label: t('connections.state') },
  { key: 'total', label: t('usage.latency') }, { key: 'client_ip', label: 'IP' }, { key: 'actions', label: t('common.actions') }
])
const url = (r: ConnectionTrace) => r.host ? `${r.scheme ? r.scheme + '://' : ''}${r.host}${r.path}` : r.path || t('requestTiming.missing')
const ms = (v: number | null | undefined) => v == null ? t('requestTiming.missing') : v < 1000 ? `${v.toFixed(0)}ms` : `${(v / 1000).toFixed(2)}s`
const elapsed = (r: ConnectionTrace) => r.network_error === 'tls_error' && !r.detail.status ? t('requestTiming.missing') : ms(r.detail.total_ms)
const field = (name: string) => te(`requestTiming.fields.${name}`) ? t(`requestTiming.fields.${name}`) : te(`requestTiming.${name}`) ? t(`requestTiming.${name}`) : name
const stateClass = (s: ConnectionState) => s === 'completed' || s === 'edge_completed' ? 'bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-200' : s === 'unknown' ? 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300' : 'bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-200'
const events = computed(() => Object.entries(selected.value?.detail.events ?? {}).sort((a, b) => a[1] - b[1]))
function summary(r: ConnectionTrace) {
  const missing = t('requestTiming.missing'), yes = (value: boolean) => t(value ? 'requestTiming.yes' : 'requestTiming.no')
  const application = r.source === 'application' && r.detail.version > 0
  return [
    { label: 'URL', value: url(r) }, { label: t('connections.state'), value: t(`connections.states.${r.state}`) },
    { label: t('connections.requestId'), value: r.request_id || missing }, { label: 'X-Client-Request-ID', value: r.client_request_id || missing },
    { label: 'Trace ID', value: r.trace_id }, { label: 'User-Agent', value: r.user_agent || missing },
    { label: t('admin.usage.group'), value: r.group_name || (r.group_id ? String(r.group_id) : missing) },
    { label: t('connections.upstreamStatus'), value: r.upstream_status || missing }, { label: t('connections.networkError'), value: r.network_error || missing },
    { label: t('connections.logicalError'), value: r.error_type ? `${r.error_type} (${r.error_status})` : missing },
    { label: field('terminal'), value: r.detail.terminal ? field(r.detail.terminal) : missing },
    ...['body_complete', 'client_disconnect', 'downstream_error', 'canceled'].map(key => ({ label: field(key), value: application && typeof r.detail[key as 'body_complete'] === 'boolean' ? yes(r.detail[key as 'body_complete']) : missing }))
  ]
}
async function load() {
  controller?.abort()
  const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; rows.value = []; hasMore.value = false
  try {
    const result = await listConnections({ ...props.filters, state: state.value, source: source.value, host: host.value.trim() || undefined, request_id: requestId.value.trim() || undefined, client_ip: clientIp.value.trim() || undefined, page: page.value, page_size: 20 }, current.signal)
    if (current.signal.aborted) return
    rows.value = result.items; hasMore.value = result.has_more
  } catch (e) {
    if (!current.signal.aborted && !isCancel(e)) error.value = t('connections.loadError')
  } finally { if (controller === current) loading.value = false }
}
function reload() { page.value = 1; selected.value = null; void load() }
function changePage(delta: number) { page.value += delta; selected.value = null; void load() }
function resetLocal() { state.value = 'all'; source.value = 'all'; host.value = ''; requestId.value = ''; clientIp.value = ''; reload() }
watch(() => [props.filters, props.refreshKey], reload, { deep: true, immediate: true })
onUnmounted(() => controller?.abort())
</script>
