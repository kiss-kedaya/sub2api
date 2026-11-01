<template>
  <div class="flex flex-col gap-4 p-4 sm:p-6" data-testid="usage-connections">
    <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="reload()">
      <USelect v-model="state" :items="stateItems" class="min-w-36" :aria-label="t('connections.state')" @update:model-value="reload()" />
      <USelect v-model="source" :items="sourceItems" class="min-w-32" :aria-label="t('connections.source')" @update:model-value="reload()" />
      <UInput v-model="host" icon="i-lucide-globe" class="w-full sm:w-48" maxlength="255" placeholder="api.example.com" :aria-label="t('connections.host')" />
      <UInput v-model="requestId" icon="i-lucide-search" class="w-full sm:w-56" maxlength="128" :placeholder="t('connections.requestId')" :aria-label="t('connections.requestId')" />
      <UInput v-model="clientIp" class="w-full sm:w-36" maxlength="64" placeholder="IP" aria-label="IP" />
      <UButton type="submit" color="neutral" variant="outline" icon="i-lucide-search" :label="t('common.search')" />
      <UButton type="button" color="neutral" variant="ghost" :label="t('common.reset')" @click="resetLocal" />
      <UPopover :content="{ align: 'end' }" class="ml-auto">
        <UButton type="button" color="neutral" variant="ghost" icon="i-lucide-info" :label="t('connections.aboutData')" />
        <template #content>
          <div class="flex max-w-sm flex-col gap-2 p-3 text-xs text-muted">
            <p>{{ t('connections.scope') }}</p>
            <p>{{ t('connections.identityScope') }}</p>
          </div>
        </template>
      </UPopover>
    </form>
    <UAlert
      v-if="error"
      role="alert"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      :description="error"
      :actions="[{ label: t('requestTiming.retry'), color: 'error', variant: 'outline', size: 'xs', onClick: load }]"
    />
    <DataTable :columns="columns" :data="rows" :loading="loading" row-key="trace_id">
      <template #cell-started_at="{ row }"><span class="whitespace-nowrap">{{ formatDateTime(row.started_at) }}</span></template>
      <template #cell-url="{ row }"><span class="block text-xs text-muted">{{ row.method }} · {{ row.http_version || '—' }} · {{ row.tls_version || '—' }}</span><span class="break-all font-mono text-xs">{{ url(row) }}</span></template>
      <template #cell-source="{ row }">{{ t(`connections.sources.${row.source}`) }}<span v-if="row.legacy" class="block text-xs text-muted">{{ t('connections.legacy') }}</span></template>
      <template #cell-user="{ row }"><span>{{ row.user_name || row.user_email || (row.user_id ? `#${row.user_id}` : t('connections.unidentified')) }}</span><span v-if="row.detail.identity_source === 'recognized_credential'" class="block text-xs text-warning">{{ t('connections.recognizedCredential') }}</span><span v-if="row.api_key_id" class="block text-xs text-muted">{{ row.api_key_name || `#${row.api_key_id}` }}</span></template>
      <template #cell-state="{ row }"><UBadge :color="stateColor(row.state)" variant="subtle" size="sm">{{ t(`connections.states.${row.state}`) }}</UBadge><span class="mt-1 block text-xs text-muted">HTTP {{ row.legacy && !row.detail.version ? '—' : row.detail.status || '—' }}</span><span v-if="row.detail.auth_rejection || row.error_type" class="mt-1 block text-xs text-muted">{{ row.detail.auth_rejection || row.error_type }} · {{ row.error_status || row.detail.status }}</span></template>
      <template #cell-model="{ row }">{{ row.model || '—' }}<span v-if="row.account_id" class="block text-xs text-muted">{{ row.account_name || `#${row.account_id}` }}</span></template>
      <template #cell-total="{ row }">{{ elapsed(row) }}<span class="block text-xs text-muted">{{ row.detail.downstream_bytes == null || (row.network_error === 'tls_error' && !row.detail.status) || (row.legacy && !row.detail.version) ? t('requestTiming.missing') : t('connections.bytes', { count: row.detail.downstream_bytes }) }}</span></template>
      <template #cell-actions="{ row }"><UTooltip :text="t('connections.details')"><UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-panel-right-open" :aria-label="t('connections.details')" @click="selected = row" /></UTooltip></template>
      <template #empty><p class="p-6 text-center text-sm text-muted">{{ t('connections.empty') }}</p></template>
    </DataTable>
    <div class="flex items-center justify-between gap-3 border-t border-default pt-4 text-sm">
      <span class="text-muted">{{ t('connections.page', { page }) }}</span>
      <div class="flex gap-1.5">
        <UButton color="neutral" variant="outline" icon="i-lucide-chevron-left" :label="t('connections.previous')" :disabled="loading || page <= 1" @click="changePage(-1)" />
        <UButton color="neutral" variant="outline" trailing-icon="i-lucide-chevron-right" :label="t('connections.next')" :disabled="loading || !hasMore || page >= 500" @click="changePage(1)" />
      </div>
    </div>
    <OpsErrorDetailModal v-model:show="showErrorDetail" :error-id="errorDetailId" error-type="request" />
    <BaseDialog :show="!!selected" :title="t('connections.details')" width="extra-wide" close-on-click-outside @close="selected = null">
      <div v-if="selected" class="flex flex-col gap-5">
        <UAlert color="neutral" variant="subtle" icon="i-lucide-info" :description="t('connections.scope')" />
        <dl class="grid gap-4 text-sm sm:grid-cols-2">
          <div v-for="item in summary(selected)" :key="item.label"><dt class="text-muted">{{ item.label }}</dt><dd class="mt-1 break-all font-mono text-xs text-default">{{ item.value }}</dd></div>
        </dl>
        <div v-if="selected.error_log_id"><UButton color="neutral" variant="outline" icon="i-lucide-file-warning" :label="t('connections.errorDetail')" @click="openError(selected)" /></div>
        <UAlert v-if="selected.detail.truncated" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('requestTiming.truncated')" />
        <USeparator />
        <section><h4 class="mb-3 text-sm font-semibold text-highlighted">{{ t('requestTiming.timeline') }}</h4><p v-if="!events.length" class="text-sm text-muted">{{ t('connections.noTimeline') }}</p><ol v-else class="flex flex-col gap-2 border-l border-default pl-4"><li v-for="[name, value] in events" :key="name" class="flex justify-between gap-4 text-sm"><span class="text-default">{{ field(name) }}</span><span class="tabular-nums text-muted">{{ ms(value) }}</span></li></ol></section>
        <section v-if="selected.detail.attempts?.length"><h4 class="mb-3 text-sm font-semibold text-highlighted">{{ t('requestTiming.attempt') }}</h4><ul class="divide-y divide-default"><li v-for="a in selected.detail.attempts" :key="a.number" class="py-2.5 text-sm first:pt-0"><p class="text-default">#{{ a.number }} · {{ a.kind }} · {{ t('requestTiming.account') }} #{{ a.account_id }} · HTTP {{ a.status || '—' }}</p><p class="mt-1 text-xs text-muted">{{ a.cleanup_canceled ? t('requestTiming.health.normalClose') : a.error ? field(a.error) : t('requestTiming.health.noTransportError') }} · {{ a.terminal || '—' }}</p></li></ul></section>
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
const stateColor = (s: ConnectionState) => s === 'completed' || s === 'edge_completed' ? 'info' as const : s === 'unknown' ? 'neutral' as const : 'warning' as const
const stateItems = computed(() => states.map(s => ({ value: s, label: t(`connections.states.${s}`) })))
const sourceItems = computed(() => sources.map(s => ({ value: s, label: t(`connections.sources.${s}`) })))
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
