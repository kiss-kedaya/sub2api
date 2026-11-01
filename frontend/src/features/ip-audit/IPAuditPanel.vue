<template>
  <section class="flex min-w-0 flex-col gap-4" data-testid="ip-audit-panel">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="max-w-3xl text-sm text-muted">{{ t('ipAudit.scopeHint') }}</p>
      <UButton :label="t('ipAudit.settings')" icon="i-lucide-settings-2" color="neutral" variant="link" class="px-0" data-testid="ip-open-settings" @click="$emit('configure')" />
    </div>
    <UAlert color="warning" variant="subtle" icon="i-lucide-server-off" :description="t('ipAudit.externalHint')" data-testid="host-disconnected" />
    <UCollapsible :unmount-on-hide="false" data-testid="ip-source-inventory">
      <template #default="{ open }">
        <UButton :label="t('ipAudit.inventory')" icon="i-lucide-shield-check" color="neutral" variant="ghost" class="px-0 font-medium text-default hover:bg-transparent" trailing-icon="i-lucide-chevron-down" :ui="{ trailingIcon: ['transition-transform duration-200', open ? 'rotate-180' : ''] }" />
      </template>
      <template #content>
        <ul class="mt-2 flex flex-col gap-2 text-sm">
          <li v-for="item in inventory" :key="item.id" class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span class="font-medium text-default">{{ sourceName(item.id) }}</span>
            <UBadge color="neutral" variant="subtle" size="sm">{{ label(item.scope) }}</UBadge>
            <UBadge color="neutral" variant="subtle" size="sm">{{ label(item.storage) }}</UBadge>
            <UBadge color="neutral" variant="subtle" size="sm">{{ label(item.enumeration) }}</UBadge>
            <UBadge :color="item.can_release ? 'success' : 'neutral'" variant="outline" size="sm">{{ t(item.can_release ? 'ipAudit.sourceManaged' : 'ipAudit.readOnly') }}</UBadge>
            <UButton v-if="item.manage_url" :to="item.manage_url" :label="t('ipAudit.sourceManagement')" color="neutral" variant="link" size="sm" class="px-0" />
          </li>
        </ul>
      </template>
    </UCollapsible>

    <form @submit.prevent="apply">
      <fieldset :disabled="releasing" class="flex min-w-0 flex-col gap-2">
        <div class="flex flex-wrap items-center gap-1.5">
          <USelect :model-value="source" :items="sourceItems" class="min-w-48" :aria-label="t('ipAudit.source')" data-testid="audit-source" @update:model-value="onSourceSelect" />
          <UInput v-model="ip" icon="i-lucide-search" class="w-full sm:w-56" placeholder="IP / CIDR" aria-label="IP / CIDR" maxlength="80" :disabled="source === 'admin_history' || source === 'security_penalty'" data-testid="audit-ip" />
          <UInput v-if="source === 'security_penalty'" v-model="userId" type="number" min="1" class="w-full sm:w-56" :placeholder="t('ipAudit.explicitUser')" :aria-label="t('ipAudit.explicitUser')" data-testid="audit-user" />
          <USelect v-model="stateFilter" :items="stateItems" class="min-w-36" :aria-label="t('ipAudit.state')" data-testid="audit-state" />
          <USelect v-model="sort" :items="sortItems" class="min-w-36" :aria-label="t('ipAudit.order')" :disabled="source === 'admin_history'" />
        </div>
        <div class="flex flex-wrap items-center justify-between gap-1.5">
          <div class="flex flex-wrap items-center gap-1.5">
            <UFieldGroup>
              <UBadge color="neutral" variant="outline" size="lg" :label="t('ipAudit.start')" />
              <UInput v-model="start" type="datetime-local" :aria-label="t('ipAudit.start')" />
            </UFieldGroup>
            <UFieldGroup>
              <UBadge color="neutral" variant="outline" size="lg" :label="t('ipAudit.end')" />
              <UInput v-model="end" type="datetime-local" :aria-label="t('ipAudit.end')" />
            </UFieldGroup>
            <UButton type="submit" :label="t('common.search')" icon="i-lucide-search" :disabled="loading || releasing" data-testid="audit-filter" />
            <UButton :label="t('common.refresh')" icon="i-lucide-refresh-cw" color="neutral" variant="outline" :disabled="loading || releasing" @click="refresh" />
          </div>
          <UDropdownMenu :items="columnItems" :content="{ align: 'end' }">
            <UButton :label="t('ipAudit.columns')" color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" />
          </UDropdownMenu>
        </div>
      </fieldset>
    </form>
    <UAlert v-if="exactSource && !applied.ip" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('ipAudit.exactHint')" />
    <p v-if="source === 'security_penalty'" class="text-sm text-muted">{{ t('ipAudit.penaltyHint') }}</p>
    <p v-if="source === 'admin_history'" class="text-sm text-muted">{{ t('ipAudit.historyHint') }}</p>
    <p v-if="selectedSource && !selectedSource.can_release" class="text-sm text-muted">
      {{ t('ipAudit.readOnlyHint') }}
      <UButton v-if="selectedSource.manage_url" :to="selectedSource.manage_url" :label="t('ipAudit.sourceManagement')" color="neutral" variant="link" size="sm" class="px-0 align-baseline" />
    </p>
    <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
    <UAlert v-if="partial" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('ipAudit.partial')" />
    <UAlert v-if="outcome" role="status" :color="outcome.readback === 'released' ? 'success' : 'warning'" variant="subtle" :icon="outcome.readback === 'released' ? 'i-lucide-circle-check' : 'i-lucide-triangle-alert'" :title="t(outcome.readback === 'released' ? 'ipAudit.released' : outcome.readback === 'still_present' ? 'ipAudit.stillRestricted' : 'ipAudit.unverified')" data-testid="release-outcome">
      <template #description>
        <p>{{ t('ipAudit.notUnrestricted') }}</p>
        <p v-if="outcome.remaining.length">{{ t('ipAudit.remaining') }}: {{ outcome.remaining.map(r => `${sourceName(r.source)} (${r.network})`).join(', ') }}</p>
        <p v-if="outcome.unknown_sources.length">{{ t('ipAudit.unchecked') }}: {{ outcome.unknown_sources.map(sourceName).join(', ') }}</p>
      </template>
    </UAlert>

    <UTable v-model:column-visibility="columnVisibility" :data="rows" :columns="columns" :loading="loading" class="min-w-0" :ui="tableUi">
      <template #loading><p role="status">{{ t('common.loading') }}</p></template>
      <template #empty><p class="text-sm text-muted">{{ t('ipAudit.empty') }}</p></template>
      <template #subject-cell="{ row }">
        <code class="text-default">{{ row.original.network || (row.original.user_id ? `UID ${row.original.user_id}` : t('ipAudit.unavailable')) }}</code>
        <p v-if="row.original.actor_id" class="text-dimmed">{{ t('ipAudit.operator') }} #{{ row.original.actor_id }}</p>
      </template>
      <template #source-cell="{ row }">
        <span class="text-default">{{ sourceName(row.original.origin_source || row.original.source) }}</span>
        <p>{{ label(row.original.scope) }}</p>
        <p v-if="row.original.manual">{{ t('ipAudit.manual') }}</p>
        <p class="text-dimmed">{{ label(row.original.duration || 'unknown') }}</p>
      </template>
      <template #reason-cell="{ row }">{{ label(row.original.reason) }}</template>
      <template #state-cell="{ row }">
        <UBadge :color="stateTone(row.original.state)" variant="subtle">{{ label(row.original.state) }}</UBadge>
        <p class="mt-1">{{ label(row.original.effective) }}</p>
        <p v-if="row.original.result" class="text-dimmed">{{ label(row.original.result) }}</p>
      </template>
      <template #count-cell="{ row }">{{ row.original.count ?? t('ipAudit.unavailable') }} / {{ row.original.threshold ?? t('ipAudit.unavailable') }}</template>
      <template #time-cell="{ row }">{{ date(row.original.started_at) }}<p class="text-dimmed">{{ date(row.original.expires_at) }}</p></template>
      <template #actions-cell="{ row }">
        <div class="flex justify-end">
          <UButton v-if="row.original.can_release" :label="t('ipAudit.release')" icon="i-lucide-unlock" color="neutral" variant="outline" size="sm" :disabled="releasing" data-testid="release-source" @click="confirmEntry = row.original" />
          <UBadge v-else color="neutral" variant="outline" icon="i-lucide-lock">{{ t('ipAudit.readOnly') }}</UBadge>
        </div>
      </template>
    </UTable>
    <div class="flex items-center justify-between gap-3 border-t border-default pt-4">
      <UButton :label="t('ipAudit.previous')" icon="i-lucide-chevron-left" color="neutral" variant="outline" :disabled="loading || releasing || !previous.length" @click="previousPage" />
      <UButton :label="t('ipAudit.next')" trailing-icon="i-lucide-chevron-right" color="neutral" variant="outline" :disabled="loading || releasing || !hasMore" data-testid="audit-next" @click="nextPage" />
    </div>

    <BaseDialog :show="!!confirmEntry" :title="t('ipAudit.confirmTitle')" width="narrow" @close="!releasing && (confirmEntry = null)">
      <div v-if="confirmEntry" class="flex flex-col gap-4" data-testid="release-dialog">
        <p class="flex flex-wrap items-center gap-2 text-sm">
          <span class="font-medium text-highlighted">{{ sourceName(confirmEntry.source) }}</span>
          <code class="text-default">{{ confirmEntry.network || `UID ${confirmEntry.user_id}` }}</code>
        </p>
        <p class="text-sm text-muted">{{ t(confirmEntry.scope === 'all_user_states' ? 'ipAudit.confirmUserScope' : 'ipAudit.confirmHint') }}</p>
        <UFormField :label="t('ipAudit.reason')">
          <USelect v-model="reason" :items="reasonItems" class="w-full" />
        </UFormField>
      </div>
      <template #footer>
        <UButton :label="t('common.cancel')" color="neutral" variant="subtle" :disabled="releasing" @click="confirmEntry = null" />
        <UButton :label="t('ipAudit.confirm')" :disabled="releasing" data-testid="release-confirm" @click="release" />
      </template>
    </BaseDialog>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { listIPAudit, releaseIPAudit, type AuditEntry, type AuditQuery, type AuditSource, type ReleaseResult } from './api'
defineEmits<{ configure: [] }>()
import zh from './locale.zh'
import en from './locale.en'
const {t,te}=useI18n({useScope:'local',messages:{zh:{ipAudit:zh},en:{ipAudit:en}},inheritLocale:true})
const sourceName = (id: string) => t(`ipAudit.sources.${id}`)
const label = (value: string) => te(`ipAudit.labels.${value}`) ? t(`ipAudit.labels.${value}`) : value || t('ipAudit.unavailable')
const source = ref('anonymous_api'), ip = ref(''), state = ref(''), sort = ref('expires_desc'), start = ref(''), end = ref(''), userId = ref('')
const inventory = ref<AuditSource[]>([]), rows = ref<AuditEntry[]>([]), hasMore = ref(false), partial = ref(false)
const loading = ref(false), releasing = ref(false), error = ref(''), confirmEntry = ref<AuditEntry | null>(null), outcome = ref<ReleaseResult | null>(null)
const reason = ref('operator_review'), reasons = ['operator_review', 'false_positive', 'configuration_fix', 'support_request']
const states = ['active', 'disabled', 'expired', 'observed', 'historical', 'allowlisted']
const applied = ref<AuditQuery>({ source: 'anonymous_api', cursor: 0, limit: 20 }), previous = ref<number[]>([]), nextCursor = ref(0)
const selectedSource = computed(() => inventory.value.find(s => s.id === source.value))
const exactSource = computed(() => source.value === 'login_failures' || source.value === 'login_limiter')
// USelect items cannot carry '' (reka-ui): "all states" is a sentinel mapped back to ''.
const ALL = '__all__'
const stateFilter = computed({ get: () => state.value || ALL, set: (value: string) => { state.value = value === ALL ? '' : value } })
const sourceItems = computed(() => inventory.value.map(item => ({ label: sourceName(item.id), value: item.id })))
const stateItems = computed(() => [{ label: t('ipAudit.allStates'), value: ALL }, ...states.map(value => ({ label: label(value), value }))])
const sortItems = computed(() => [{ label: t('ipAudit.expiryDesc'), value: 'expires_desc' }, { label: t('ipAudit.expiryAsc'), value: 'expires_asc' }])
const reasonItems = computed(() => reasons.map(value => ({ label: label(value), value })))
const tableUi = {
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default align-top',
  separator: 'h-0'
}
const columns = computed<TableColumn<AuditEntry>[]>(() => [
  { id: 'subject', header: `IP / ${t('ipAudit.subject')}`, enableHiding: false },
  { id: 'source', header: t('ipAudit.source') },
  { id: 'reason', header: t('ipAudit.reason') },
  { id: 'state', header: t('ipAudit.state') },
  { id: 'count', header: t('ipAudit.count') },
  { id: 'time', header: `${t('ipAudit.start')} / ${t('ipAudit.expiry')}` },
  { id: 'actions', header: t('common.actions'), enableHiding: false, meta: { class: { th: 'text-right' } } }
])
// Column choice persists across visits; every column is shown until the admin hides one.
const COLUMNS_KEY = 'ip-audit-columns'
function storedVisibility(): Record<string, boolean> {
  try { const value = JSON.parse(localStorage.getItem(COLUMNS_KEY) || '{}'); return value && typeof value === 'object' ? value : {} } catch { return {} }
}
const columnVisibility = ref<Record<string, boolean>>(storedVisibility())
watch(columnVisibility, value => { try { localStorage.setItem(COLUMNS_KEY, JSON.stringify(value)) } catch { /* storage unavailable */ } }, { deep: true })
const columnItems = computed(() => columns.value.filter(column => column.enableHiding !== false).map(column => ({
  label: String(column.header),
  type: 'checkbox' as const,
  checked: columnVisibility.value[column.id!] !== false,
  onUpdateChecked(checked: boolean) { columnVisibility.value = { ...columnVisibility.value, [column.id!]: checked } },
  onSelect(event: Event) { event.preventDefault() }
})))
type Tone = 'error' | 'warning' | 'success' | 'neutral'
const stateTones: Record<string, Tone> = { active: 'error', observed: 'warning', allowlisted: 'success' }
const stateTone = (value: string): Tone => stateTones[value] ?? 'neutral'
let controller: AbortController | undefined, disposed = false
const date = (value: number | null) => value == null ? t('ipAudit.unavailable') : new Date(value * 1000).toLocaleString()
const timestamp = (value: string) => value ? Math.floor(new Date(value).getTime() / 1000) : undefined
async function load() {
  controller?.abort(); const current = new AbortController(); controller = current
  loading.value = true; error.value = ''; rows.value = []; hasMore.value = false; confirmEntry.value = null
  try {
    const result = await listIPAudit({ ...applied.value }, current.signal)
    if (current.signal.aborted || disposed) return
    rows.value = result.items; inventory.value = result.sources; hasMore.value = result.has_more; nextCursor.value = result.next_cursor; partial.value = !!result.partial
  } catch { if (!current.signal.aborted && !disposed) error.value = t('ipAudit.loadError') }
  finally { if (controller === current && !disposed) loading.value = false }
}
function onSourceSelect(value: string) { if (value === source.value) return; source.value = value; sourceChanged() }
function sourceChanged() { state.value = ''; ip.value = ''; sort.value = 'expires_desc'; outcome.value = null; apply() }
function apply() {
  previous.value = []; outcome.value = null
  applied.value = { source: source.value, ip: ip.value.trim() || undefined, state: state.value || undefined, sort: sort.value, start: timestamp(start.value), end: timestamp(end.value), user_id: userId.value ? Number(userId.value) : undefined, cursor: 0, limit: 20 }
  void load()
}
function refresh() { previous.value = []; applied.value.cursor = 0; void load() }
function nextPage() { previous.value.push(applied.value.cursor); applied.value.cursor = nextCursor.value; void load() }
function previousPage() { applied.value.cursor = previous.value.pop() ?? 0; void load() }
async function release() {
  const entry = confirmEntry.value; if (!entry || releasing.value) return
  releasing.value = true; error.value = ''; outcome.value = null
  try {
    const result = await releaseIPAudit(entry, reason.value)
    if (disposed) return
    outcome.value = result; confirmEntry.value = null
    await load()
  } catch {
    if (!disposed) { await load(); error.value = t('ipAudit.releaseError'); confirmEntry.value = null }
  }
  finally { if (!disposed) releasing.value = false }
}
onMounted(load)
onUnmounted(() => { disposed = true; controller?.abort() })
</script>
