<template>
  <AppLayout>
    <div class="flex flex-col gap-4">
      <AdminPageHeader :title="t('admin.requestCapture.title')" :description="t('admin.requestCapture.description')">
        <template #actions>
          <UButton icon="i-lucide-refresh-cw" color="neutral" variant="outline" :label="t('admin.requestCapture.refresh')" :disabled="loading || !configLoaded || captureConfig.storage_ready === false" @click="refresh" />
          <UButton icon="i-lucide-plus" :label="t('admin.requestCapture.create')" @click="openCreate" />
        </template>
      </AdminPageHeader>
      <PageToolbar>
        <template #left><UTabs v-model="tab" :items="tabs" :content="false" variant="link" class="-mx-1" :ui="{ list: 'border-b-0' }" /></template>
        <template v-if="tab === 'tasks'" #right>
          <UDropdownMenu :items="columnMenu" :content="{ align: 'end' }">
            <UButton :label="t('admin.requestCapture.columns')" color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" />
          </UDropdownMenu>
        </template>
      </PageToolbar>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="error" />
      <p class="text-sm text-muted">{{ t('admin.requestCapture.instanceScope') }} <code v-if="captureConfig.instance_id" class="text-default">{{ captureConfig.instance_id }}</code><span v-else class="text-warning">{{ t('admin.requestCapture.storageNotConfigured') }}</span></p>

      <div v-show="tab === 'tasks'" class="flex flex-col gap-4">
        <template v-if="stats">
          <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
            <UPageCard v-for="card in statCards" :key="card.key" :icon="card.icon" :title="card.label" variant="subtle" :ui="statCardUi" class="lg:rounded-none first:rounded-l-lg last:rounded-r-lg">
              <span class="truncate text-2xl font-semibold text-highlighted">{{ card.value }}</span>
            </UPageCard>
          </UPageGrid>
          <div v-if="stats.admission_skipped || stats.expired_records || stats.storage_error || dropReasons.length" class="flex flex-wrap items-center gap-x-5 gap-y-2 text-sm text-muted">
            <span v-if="stats.admission_skipped">{{ t('admin.requestCapture.admissionSkipped') }}: <span class="text-highlighted">{{ stats.admission_skipped }}</span></span>
            <span v-if="stats.expired_records">{{ t('admin.requestCapture.expiredRecords') }}: <span class="text-highlighted">{{ stats.expired_records }}</span></span>
            <UBadge v-if="stats.storage_error" color="error" variant="subtle">{{ t('admin.requestCapture.storageError') }}</UBadge>
            <span v-for="[reason, count] in dropReasons" :key="reason">{{ dropReason(reason) }}: <span class="text-highlighted">{{ count }}</span></span>
            <UPopover v-if="dropReasons.length">
              <UButton icon="i-lucide-info" color="neutral" variant="ghost" size="xs" :aria-label="t('admin.requestCapture.droppedHint')" />
              <template #content><p class="max-w-xs p-3 text-sm text-muted">{{ t('admin.requestCapture.droppedHint') }}</p></template>
            </UPopover>
          </div>
        </template>
        <UAlert color="warning" variant="subtle" icon="i-lucide-shield-alert" :description="t('admin.requestCapture.privacy')" />
        <UTable v-model:column-visibility="columnVisibility" :data="tasks" :columns="taskColumns" :meta="taskMeta" :ui="tableUi">
          <template #target-cell="{ row: { original: task } }">
            <UButton variant="link" class="p-0 text-left font-medium" :data-testid="'select-capture-' + task.id" @click="selectTask(task)">{{ t('admin.requestCapture.' + task.target_type) }} #{{ task.target_id }} {{ task.target_name }}</UButton>
            <div v-if="isDiagnostic(task)" class="text-xs font-medium text-primary">{{ t('admin.requestCapture.diagnosticTask') }}</div>
            <div class="text-xs text-muted">{{ task.save_media ? t('admin.requestCapture.mediaIncluded') : t('admin.requestCapture.metadataOnly') }}</div>
          </template>
          <template #status-cell="{ row: { original: task } }">
            <UBadge :color="taskStatusColor(task.status)" variant="subtle">{{ task.status }}</UBadge>
            <div v-if="task.reason" class="mt-1 text-xs text-warning">{{ task.reason }}</div>
          </template>
          <template #remaining-cell="{ row: { original: task } }">{{ remaining(task) }}</template>
          <template #incomplete-cell="{ row: { original: task } }">{{ task.partial }} / {{ task.skipped }}</template>
          <template #bytes-cell="{ row: { original: task } }">{{ bytes(task.bytes) }}</template>
          <template #actions-cell="{ row: { original: task } }">
            <div class="flex items-center justify-end gap-1">
              <UButton v-if="task.status === 'running' && !isDiagnostic(task)" icon="i-lucide-square" color="neutral" variant="ghost" size="sm" :label="t('admin.requestCapture.stop')" :disabled="busy" @click="stop(task)" />
              <UButton v-if="task.status !== 'running'" icon="i-lucide-download" color="neutral" variant="ghost" size="sm" :label="t('admin.requestCapture.export')" :disabled="busy || !streamExport" @click="download(task.id)" />
              <UButton icon="i-lucide-trash-2" color="error" variant="ghost" size="sm" :aria-label="t('admin.requestCapture.delete')" :title="t('admin.requestCapture.delete')" data-testid="delete-capture" :disabled="busy || (isDiagnostic(task) && (!!captureConfig.id1_diagnostic_enabled || task.status === 'running'))" @click="remove(task)" />
            </div>
            <p v-if="isDiagnostic(task) && task.status === 'running'" class="mt-1 max-w-xs whitespace-normal text-right text-xs text-muted">{{ t('admin.requestCapture.diagnosticStopHint') }}</p>
          </template>
          <template #empty>{{ t('admin.requestCapture.empty') }}</template>
        </UTable>
        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
          <span class="text-sm text-muted">{{ streamExport ? '' : t('admin.requestCapture.exportBrowser') }}</span>
          <div class="flex items-center gap-1.5">
            <UButton icon="i-lucide-chevron-left" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.previous')" :disabled="taskPage <= 1" @click="changeTaskPage(-1)" />
            <span class="min-w-8 text-center text-sm text-muted">{{ taskPage }}</span>
            <UButton icon="i-lucide-chevron-right" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.next')" :disabled="!taskMore" @click="changeTaskPage(1)" />
          </div>
        </div>

        <section v-if="selectedTask" class="flex flex-col gap-4 pt-2">
          <UPageCard :title="`${t('admin.requestCapture.records')} - ${selectedTask.target_name}`" :description="t(isDiagnostic(selectedTask) ? 'admin.requestCapture.allOutcomes' : 'admin.requestCapture.errorsOnly')" variant="naked" />
          <form class="flex flex-wrap items-center gap-1.5" @submit.prevent="recordPage = 1; loadRecords()">
            <UInput v-model.trim="requestID" icon="i-lucide-search" class="w-full max-w-sm" placeholder="Request ID" aria-label="Request ID" />
            <UButton type="submit" color="neutral" variant="outline" :label="t('admin.requestCapture.filter')" />
          </form>
          <UTable :data="records" :columns="recordColumns" :ui="tableUi">
            <template #request-cell="{ row: { original: record } }">
              <UButton variant="link" class="break-all p-0 text-left" @click="viewRecord(record)">{{ record.request_id || record.id }}</UButton>
              <div class="text-xs text-muted">{{ record.created_at }} / {{ record.protocol }}<span v-if="record.turn"> / {{ t('admin.requestCapture.turn') }} {{ record.turn }}</span></div>
            </template>
            <template #model-cell="{ row: { original: record } }">{{ record.model }}</template>
            <template #status-cell="{ row: { original: record } }">
              <div class="flex flex-wrap items-center gap-1.5">
                <UBadge :color="record.status >= 400 ? 'error' : 'neutral'" variant="subtle">HTTP {{ record.status }}</UBadge>
                <span v-if="record.is_error" class="text-error" data-testid="capture-outcome">{{ captureOutcome(record) }}</span>
                <span v-if="record.partial" class="text-warning">{{ record.reason || t('admin.requestCapture.partial') }}</span>
              </div>
              <div v-if="record.error_code" class="mt-1 break-all text-xs text-error">{{ record.error_code }}</div>
            </template>
            <template #bytes-cell="{ row: { original: record } }">{{ bytes(record.bytes) }}</template>
          </UTable>
          <div class="flex items-center justify-end gap-1.5 border-t border-default pt-4">
            <UButton icon="i-lucide-chevron-left" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.previous')" :disabled="recordPage <= 1" @click="recordPage--; loadRecords()" />
            <span class="min-w-8 text-center text-sm text-muted">{{ recordPage }}</span>
            <UButton icon="i-lucide-chevron-right" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.next')" :disabled="!recordMore" @click="recordPage++; loadRecords()" />
          </div>
        </section>
        <section v-if="detail" class="flex flex-col gap-4 pt-2">
          <UPageCard :title="`${t('admin.requestCapture.detail')} - ${detail.request_id}`" variant="naked" orientation="horizontal" :ui="{ title: 'break-all' }">
            <UButton icon="i-lucide-download" color="neutral" variant="outline" class="w-fit lg:ms-auto" :label="t('admin.requestCapture.export')" data-testid="export-capture-record" :disabled="busy || !streamExport || (selectedTask?.status === 'running' && !isDiagnostic(selectedTask))" @click="download(detail.task_id, detail.id)" />
          </UPageCard>
          <div class="flex flex-col gap-1 text-sm">
            <p v-if="detail.expires_at" class="text-xs text-muted">{{ t('admin.requestCapture.expiresAt') }}: {{ detail.expires_at }}</p>
            <p v-if="detail.is_error" class="text-error">HTTP {{ detail.status }} · {{ captureOutcome(detail) }}<span v-if="detail.error_code"> · {{ detail.error_code }}</span></p>
            <p v-if="detail.partial" class="text-warning">{{ t('admin.requestCapture.partial') }}: {{ detail.reason }}</p>
          </div>
          <UCollapsible v-model:open="metadataOpen" class="flex flex-col gap-2">
            <UButton color="neutral" variant="ghost" class="-mx-2.5 w-fit" :label="t('admin.requestCapture.metadata')" :trailing-icon="metadataOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" />
            <template #content><pre class="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-elevated p-3 text-xs text-default">{{ metadata }}</pre></template>
          </UCollapsible>
          <UTabs :model-value="stage" :items="stageItems" :content="false" variant="pill" size="sm" class="w-fit max-w-full" @update:model-value="selectStage(String($event))" />
          <USelect v-if="stageParts.length" :model-value="part?.name" :items="partItems" class="w-full max-w-xl" :aria-label="t('admin.requestCapture.segment')" @update:model-value="selectPart(String($event))" />
          <p v-else class="text-sm text-muted">{{ t('admin.requestCapture.noPart') }}</p>
          <p v-if="part?.omitted" class="text-sm text-warning">{{ t('admin.requestCapture.omitted') }}: {{ part.omitted }}</p>
          <pre v-if="part" class="max-h-144 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-elevated p-4 text-xs leading-5 text-default ring ring-inset ring-default">{{ content }}</pre>
          <div v-if="part" class="flex gap-1.5">
            <UButton color="neutral" variant="outline" :label="t('admin.requestCapture.firstSegment')" :disabled="contentOffset === 0 || contentLoading" @click="readContent(0)" />
            <UButton color="neutral" variant="outline" trailing-icon="i-lucide-chevron-right" :label="t('admin.requestCapture.nextSegment')" :disabled="!contentMore || contentLoading" :loading="contentLoading" @click="readContent(nextOffset)" />
          </div>
        </section>
      </div>

      <div v-show="tab === 'settings'" class="mx-auto w-full lg:max-w-2xl">
        <form id="capture-config" data-testid="capture-config" class="flex flex-col gap-4 sm:gap-6" @submit.prevent="saveConfig">
          <div>
            <UPageCard :title="t('admin.requestCapture.targetedSettings')" :description="t('admin.requestCapture.limitsHint')" variant="naked" orientation="horizontal" class="mb-4" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }">
              <UButton type="submit" form="capture-config" data-testid="capture-config-save" :label="t('common.save')" :disabled="busy || !configLoaded || captureConfig.storage_ready === false" :loading="busy" class="w-fit lg:ms-auto" />
            </UPageCard>
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.requestCapture.enabled')" class="flex items-center justify-between gap-4"><USwitch v-model="captureConfig.enabled" data-testid="capture-enabled" :disabled="!configLoaded || captureConfig.storage_ready === false" /></UFormField>
              <USeparator />
              <UFormField :label="t('admin.requestCapture.quota')" required class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model.number="captureConfig.quota_mib" type="number" min="1" required class="w-full sm:w-40" /></UFormField>
              <USeparator />
              <UFormField :label="t('admin.requestCapture.retentionDays')" required class="flex max-sm:flex-col justify-between items-start gap-4"><UInput v-model.number="captureConfig.retention_days" type="number" min="1" max="30" required class="w-full sm:w-40" /></UFormField>
            </UPageCard>
          </div>
          <div>
            <UPageCard :title="t('admin.requestCapture.diagnosticSettings')" :description="t('admin.requestCapture.diagnosticHint', { hours: captureConfig.diagnostic_retention_hours || 8 })" variant="naked" class="mb-4" />
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.requestCapture.diagnosticEnabled')" class="flex items-center justify-between gap-4"><USwitch v-model="captureConfig.id1_diagnostic_enabled" data-testid="id1-diagnostic-enabled" :disabled="!configLoaded || captureConfig.storage_ready === false" /></UFormField>
              <USeparator />
              <UFormField :label="t('admin.requestCapture.diagnosticUsers')" :description="t('admin.requestCapture.diagnosticLimits', { mib: captureConfig.record_limit_mib || 128, records: captureConfig.diagnostic_record_limit || 100000 })" class="flex max-sm:flex-col justify-between items-start gap-4">
                <UInput v-model="diagnosticUserIDsText" data-testid="diagnostic-user-ids" inputmode="numeric" placeholder="1, 5279" class="w-full sm:w-56" :disabled="!configLoaded || captureConfig.storage_ready === false" />
              </UFormField>
            </UPageCard>
          </div>
        </form>
      </div>
    </div>
    <BaseDialog :show="createOpen" :title="t('admin.requestCapture.create')" @close="createOpen = false">
      <form id="capture-create" class="space-y-4" @submit.prevent="create">
        <UAlert v-if="configLoaded && !captureConfig.enabled" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('admin.requestCapture.captureDisabled')" />
        <div class="grid gap-4 sm:grid-cols-2">
          <UFormField :label="t('admin.requestCapture.targetType')"><USelect v-model="targetType" :items="kindItems" class="w-full" /></UFormField>
          <UFormField :label="t('admin.requestCapture.duration')" required><UInput v-model.number="duration" type="number" min="1" max="1440" step="1" required class="w-full" /></UFormField>
        </div>
        <UFormField :label="t('admin.requestCapture.search')"><UInput v-model="search" type="search" icon="i-lucide-search" class="w-full" :placeholder="t('admin.requestCapture.searchHint')" /></UFormField>
        <UFormField :label="t('admin.requestCapture.target')" :help="t('admin.requestCapture.targetHint')" required>
          <div class="flex items-center gap-1.5">
            <USelect v-model="targetID" :items="targetItems" :placeholder="t('admin.requestCapture.selectTarget')" :loading="searching" class="min-w-0 flex-1" :aria-label="t('admin.requestCapture.target')" />
            <UButton type="button" icon="i-lucide-chevron-left" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.previous')" :disabled="targetPage <= 1 || searching" @click="loadTargets(targetPage - 1)" />
            <UButton type="button" icon="i-lucide-chevron-right" color="neutral" variant="outline" :aria-label="t('admin.requestCapture.next')" :disabled="!targetMore || searching" @click="loadTargets(targetPage + 1)" />
          </div>
        </UFormField>
        <USwitch v-model="saveMedia" :label="t('admin.requestCapture.saveMedia')" />
        <UAlert v-if="error" role="alert" color="error" variant="subtle" :title="error" />
      </form>
      <template #footer>
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="createOpen = false" />
        <UButton type="submit" form="capture-create" icon="i-lucide-radio" :label="t('admin.requestCapture.start')" :loading="busy" :disabled="busy || !configLoaded || !captureConfig.enabled || !targetID || !validDuration" />
      </template>
    </BaseDialog>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { adminAPI } from '@/api'
import * as api from '@/api/admin/requestCaptures'
import type { CaptureTask, CaptureRecord, CapturePart, CaptureStats, CaptureTarget } from '@/api/admin/requestCaptures'

import zh from '@/i18n/locales/zh/admin/requestCapture'
import en from '@/i18n/locales/en/admin/requestCapture'
const { t } = useI18n({useScope:'local',messages:{zh:{admin:zh},en:{admin:en}},inheritLocale:true})
const stepUp = useStepUp()
const captureConfig=ref<api.CaptureConfig>({enabled:false,id1_diagnostic_enabled:false,diagnostic_user_ids:[],quota_mib:1024,retention_days:7})
const diagnosticUserIDsText = ref('')
const configLoaded = ref(false)
function parseDiagnosticUserIDs() {
  const values = diagnosticUserIDsText.value.split(/[\s,，]+/).map(value => value.trim()).filter(Boolean).map(Number)
  if (values.some(value => !Number.isInteger(value) || value <= 0) || new Set(values).size !== values.length || values.length > 64) throw new Error(t('admin.requestCapture.invalidDiagnosticUsers'))
  return values
}
async function saveConfig(){ if (!configLoaded.value) return; await action(async()=>{captureConfig.value=await api.saveConfig({ ...captureConfig.value, diagnostic_user_ids: parseDiagnosticUserIDs() }); diagnosticUserIDsText.value=(captureConfig.value.diagnostic_user_ids || []).join(', ')}) }
const kinds: CaptureTarget[] = ['user', 'account', 'group']
const stages = ['client_request', 'upstream_request', 'upstream_response', 'client_response']
const targetType = ref<CaptureTarget>('user'), targetID = ref(0), duration = ref(10), saveMedia = ref(false), search = ref('')
const targets = ref<Array<{ id: number; label: string }>>([]), targetPage = ref(1), targetMore = ref(false), searching = ref(false)
const tasks = ref<CaptureTask[]>([]), stats = ref<CaptureStats>(), taskPage = ref(1), taskMore = ref(false)
const selectedTask = ref<CaptureTask>(), records = ref<CaptureRecord[]>([]), recordPage = ref(1), recordMore = ref(false)
const requestID = ref(''), detail = ref<CaptureRecord>(), stage = ref(stages[0]), part = ref<CapturePart>()
const content = ref(''), contentOffset = ref(0), nextOffset = ref(0), contentMore = ref(false), contentLoading = ref(false)
const loading = ref(false), busy = ref(false), error = ref(''), now = ref(Date.now())
const tab = ref<'tasks' | 'settings'>('tasks'), createOpen = ref(false), metadataOpen = ref(false)
const streamExport = api.canStreamExport()
const validDuration = computed(() => Number.isInteger(duration.value) && duration.value >= 1 && duration.value <= 1440)
const stageParts = computed(() => (detail.value?.parts || []).filter(item => item.stage === stage.value).sort((a, b) => a.name.localeCompare(b.name)))
const metadata = computed(() => { if (!detail.value) return ''; const { parts: _parts, ...meta } = detail.value; return JSON.stringify(meta, null, 2) })
const dropReasons = computed(() => Object.entries(stats.value?.skipped_by_reason || {}).filter(([, count]) => count > 0).sort(([a], [b]) => a.localeCompare(b)))
const knownDropReasons = new Set(['quota_exceeded', 'record_limit', 'buffer_limit', 'queue_full', 'active_limit', 'storage_error', 'admission_busy', 'session_or_buffer_limit', 'record_capacity'])
function dropReason(reason: string) { return knownDropReasons.has(reason) ? t('admin.requestCapture.dropReasons.' + reason) : reason }
function isDiagnostic(task?: CaptureTask) { return task?.mode === 'id1_diagnostic' }
const tabs = computed(() => [
  { label: t('admin.requestCapture.tabs.tasks'), value: 'tasks', icon: 'i-lucide-radio' },
  { label: t('admin.requestCapture.tabs.settings'), value: 'settings', icon: 'i-lucide-settings-2' },
])
const statCardUi = { container: 'gap-y-1.5', wrapper: 'items-start', leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25', title: 'font-normal text-muted text-xs uppercase' }
const statCards = computed(() => stats.value ? [
  { key: 'instance', icon: 'i-lucide-server', label: t('admin.requestCapture.instance'), value: stats.value.instance_id || '—' },
  { key: 'used', icon: 'i-lucide-hard-drive', label: t('admin.requestCapture.used'), value: bytes(stats.value.used_bytes) },
  { key: 'active', icon: 'i-lucide-activity', label: t('admin.requestCapture.active'), value: stats.value.active_requests },
  { key: 'buffer', icon: 'i-lucide-database', label: t('admin.requestCapture.buffer'), value: bytes(stats.value.buffer_bytes) },
] : [])
const kindItems = computed(() => kinds.map(kind => ({ label: t('admin.requestCapture.' + kind), value: kind })))
const targetItems = computed(() => targets.value.map(option => ({ label: `#${option.id} ${option.label}`, value: option.id })))
const stageItems = computed(() => stages.map(item => ({ label: t('admin.requestCapture.' + item), value: item })))
const partItems = computed(() => stageParts.value.map(item => ({ label: `${item.name} / ${t('admin.requestCapture.attempt')} ${item.attempt} / ${t('admin.requestCapture.turn')} ${item.turn} / ${bytes(item.bytes)}`, value: item.name })))
const tableUi = { base: 'border-separate border-spacing-0', thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none', tbody: '[&>tr]:last:[&>td]:border-b-0', th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r', td: 'border-b border-default align-top', separator: 'h-0' }
const taskColumns = computed<TableColumn<CaptureTask>[]>(() => [
  { id: 'target', header: t('admin.requestCapture.target'), enableHiding: false },
  { id: 'status', header: t('admin.requestCapture.status') },
  { id: 'remaining', header: t('admin.requestCapture.remaining') },
  { accessorKey: 'requests', header: t('admin.requestCapture.records') },
  { id: 'incomplete', header: t('admin.requestCapture.incomplete') },
  { id: 'bytes', header: t('admin.requestCapture.size') },
  { id: 'actions', header: t('admin.requestCapture.actions'), enableHiding: false, meta: { class: { th: 'text-right' } } },
])
const recordColumns = computed<TableColumn<CaptureRecord>[]>(() => [
  { id: 'request', header: 'Request ID' }, { id: 'model', header: t('admin.requestCapture.model') },
  { id: 'status', header: t('admin.requestCapture.status') }, { id: 'bytes', header: t('admin.requestCapture.size') },
])
const taskMeta = computed(() => ({ class: { tr: (row: { original: CaptureTask }) => selectedTask.value?.id === row.original.id ? 'bg-elevated/50' : '' } }))
// "Columns" menu: the least-used column starts hidden; the choice persists per browser.
const columnStorageKey = 'admin.requestCapture.taskColumns'
const columnVisibility = ref<Record<string, boolean>>(readColumnVisibility())
function readColumnVisibility(): Record<string, boolean> { try { const saved = JSON.parse(localStorage.getItem(columnStorageKey) || 'null'); if (saved && typeof saved === 'object') return saved } catch { /* ignore a malformed value */ } return { incomplete: false } }
watch(columnVisibility, value => { try { localStorage.setItem(columnStorageKey, JSON.stringify(value)) } catch { /* storage unavailable */ } }, { deep: true })
const columnMenu = computed<DropdownMenuItem[]>(() => taskColumns.value.filter(column => column.enableHiding !== false).map(column => {
  const id = column.id || String((column as { accessorKey?: string }).accessorKey)
  return { label: String(column.header), type: 'checkbox' as const, checked: columnVisibility.value[id] !== false, onUpdateChecked: (checked: boolean) => { columnVisibility.value = { ...columnVisibility.value, [id]: checked } }, onSelect: (event?: Event) => event?.preventDefault() }
}))
function taskStatusColor(status: string) { return status === 'running' ? 'success' as const : /fail|error/.test(status) ? 'error' as const : 'neutral' as const }
function openCreate() { error.value = ''; createOpen.value = true }
let poll: ReturnType<typeof setInterval> | undefined, debounce: ReturnType<typeof setTimeout> | undefined
let searchAbort: AbortController | undefined, disposed = false, detailVersion = 0, contentVersion = 0
function captureOutcome(record: CaptureRecord) {
  if (record.status >= 400) return t('admin.requestCapture.requestFailed')
  if (record.client_outcome === 'failed' || (!record.client_outcome && record.error_code === 'basispoints_stream_incomplete')) return t('admin.requestCapture.streamFailed')
  if (record.client_outcome === 'completed') return t('admin.requestCapture.completedWithErrors')
  // Older exports do not distinguish final failure from a recovered attempt.
  return t('admin.requestCapture.containsErrors')
}
function bytes(value: number) { return (value / 1048576).toFixed(2) + ' MiB' }
function remaining(task: CaptureTask) { if (task.status !== 'running') return '-'; if (isDiagnostic(task)) return t('admin.requestCapture.continuous'); if (!task.expires_at) return '-'; const seconds = Math.max(0, Math.ceil((Date.parse(task.expires_at) - now.value) / 1000)); return Math.floor(seconds / 60) + 'm ' + seconds % 60 + 's' }
function report(err: unknown) { if (isStepUpCancelled(err)) return; const e = err as { message?: string; status?: number; name?: string; code?: string }; if (e.name === 'AbortError' || e.code === 'ERR_CANCELED') return; error.value = e.message || t('admin.requestCapture.failed');  }
async function loadTargets(page = 1) {
  searchAbort?.abort(); const controller = new AbortController(); searchAbort = controller; searching.value = true
  try {
    const kind = targetType.value, options = { signal: controller.signal }, filter = { search: search.value }
    const result = kind === 'user' ? await adminAPI.users.list(page, 20, filter, options) : kind === 'account' ? await adminAPI.accounts.list(page, 20, { ...filter, lite: 'true' }, options) : await adminAPI.groups.list(page, 20, filter, options)
    if (controller.signal.aborted || disposed) return
    targets.value = result.items.map(item => ({ id: item.id, label: 'email' in item ? item.email : item.name }))
    targetPage.value = page; targetMore.value = page * 20 < result.total
  } catch (err) { if (!controller.signal.aborted) report(err) } finally { if (searchAbort === controller) searching.value = false }
}
async function loadRecords() { if (!selectedTask.value) return; const id = selectedTask.value.id; try { const result = await api.listRecords(id, recordPage.value, requestID.value, !isDiagnostic(selectedTask.value)); if (!disposed && selectedTask.value?.id === id) { records.value = result.items; recordMore.value = result.has_more } } catch (err) { report(err) } }
async function refresh() { if (loading.value || disposed || !configLoaded.value) return; loading.value = true; try { const result = await api.listTasks(taskPage.value); if (disposed) return; tasks.value = result.items; stats.value = result.stats; taskMore.value = result.has_more; if (selectedTask.value) { selectedTask.value = result.items.find(item => item.id === selectedTask.value?.id) || selectedTask.value; await loadRecords() } } catch (err) { report(err) } finally { loading.value = false } }
async function action(fn: () => Promise<void>) { busy.value = true; error.value = ''; try { await fn(); await refresh() } catch (err) { report(err) } finally { busy.value = false } }
async function create() { if (!targetID.value || !validDuration.value) return; await action(async () => { const task = await api.createTask({ target_type: targetType.value, target_id: targetID.value, duration_minutes: duration.value, save_media: saveMedia.value }); taskPage.value = 1; createOpen.value = false; tab.value = 'tasks'; selectTask(task) }) }
async function stop(task: CaptureTask) { await action(() => api.stopTask(task.id)) }
async function remove(task: CaptureTask) { if (!window.confirm(t('admin.requestCapture.deleteConfirm'))) return; await action(async () => { await api.deleteTask(task.id); if (selectedTask.value?.id === task.id) { selectedTask.value = undefined; detail.value = undefined; records.value = []; detailVersion++; contentVersion++ } }) }
async function download(task: string, record?: string) { await action(() => api.exportCapture(task, record, () => stepUp.run(api.authorizeSensitiveRead))) }
function changeTaskPage(delta: number) { taskPage.value += delta; void refresh() }
function selectTask(task: CaptureTask) { selectedTask.value = task; recordPage.value = 1; records.value = []; detail.value = undefined; detailVersion++; contentVersion++; void loadRecords() }
async function viewRecord(record: CaptureRecord) { const version = ++detailVersion; try { const result = await api.getRecord(record.task_id, record.id); if (version !== detailVersion || disposed) return; detail.value = result; metadataOpen.value = false; selectStage(stages[0]) } catch (err) { report(err) } }
function selectStage(value: string) { stage.value = value; part.value = stageParts.value[0]; content.value = ''; contentMore.value = false; contentVersion++; if (part.value && part.value.bytes > 0) void readContent(0) }
function selectPart(name: string) { part.value = stageParts.value.find(item => item.name === name); content.value = ''; contentVersion++; if (part.value && part.value.bytes > 0) void readContent(0) }
async function readContent(offset: number) { if (!detail.value || !part.value || stepUp.visible.value) return; const version = ++contentVersion; const task = detail.value.task_id, record = detail.value.id, name = part.value.name; contentLoading.value = true; try { const result = await stepUp.run(() => api.getContent(task, record, name, offset)); if (version !== contentVersion || disposed) return; content.value = result.text; contentOffset.value = offset; nextOffset.value = result.next_offset; contentMore.value = result.has_more } catch (err) { report(err) } finally { if (version === contentVersion) contentLoading.value = false } }
watch([targetType, search], () => { targetID.value = 0; clearTimeout(debounce); debounce = setTimeout(() => { void loadTargets(1) }, 250) })
onMounted(() => { void api.getConfig().then(value=>{if(disposed)return;captureConfig.value=value;diagnosticUserIDsText.value=(value.diagnostic_user_ids || []).join(', ');configLoaded.value=true;if(value.storage_ready !== false)void refresh()}).catch(report); void loadTargets(); poll = setInterval(() => { now.value = Date.now(); if(configLoaded.value && captureConfig.value.storage_ready !== false)void refresh() }, 3000) })
onUnmounted(() => { disposed = true; stepUp.onCancel(); clearInterval(poll); clearTimeout(debounce); searchAbort?.abort(); detailVersion++; contentVersion++ })
</script>
