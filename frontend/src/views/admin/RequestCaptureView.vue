<template>
  <AppLayout>
    <div class="space-y-5">
      <form class="card space-y-4 p-4" data-testid="capture-config" @submit.prevent="saveConfig">
        <div class="flex flex-wrap items-end gap-4">
          <label class="flex items-center gap-2"><input v-model="captureConfig.enabled" type="checkbox" :disabled="!configLoaded || captureConfig.storage_ready === false" />{{ t('admin.requestCapture.enabled') }}</label>
          <label>{{ t('admin.requestCapture.quota') }} <input v-model.number="captureConfig.quota_mib" class="input w-28" type="number" min="1" required /></label>
          <label>{{ t('admin.requestCapture.retentionDays') }} <input v-model.number="captureConfig.retention_days" class="input w-24" type="number" min="1" max="30" required /></label>
        </div>
        <div class="space-y-2 rounded-lg border border-blue-100 bg-blue-50/60 p-3 dark:border-blue-900 dark:bg-blue-950/30">
          <label class="flex items-center gap-2 font-medium"><input v-model="captureConfig.id1_diagnostic_enabled" type="checkbox" data-testid="id1-diagnostic-enabled" :disabled="!configLoaded || captureConfig.storage_ready === false" />{{ t('admin.requestCapture.diagnosticEnabled') }}</label>
          <label class="block max-w-xl text-sm"><span class="input-label">{{ t('admin.requestCapture.diagnosticUsers') }}</span><input v-model="diagnosticUserIDsText" data-testid="diagnostic-user-ids" class="input mt-1 w-full" inputmode="numeric" placeholder="1, 5279" :disabled="!configLoaded || captureConfig.storage_ready === false" /></label>
          <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.requestCapture.diagnosticHint', { hours: captureConfig.diagnostic_retention_hours || 8 }) }}</p>
          <p class="text-xs text-gray-500">{{ t('admin.requestCapture.diagnosticLimits', { mib: captureConfig.record_limit_mib || 128, records: captureConfig.diagnostic_record_limit || 100000 }) }}</p>
        </div>
        <button class="btn btn-primary" :disabled="busy || !configLoaded || captureConfig.storage_ready === false">{{ t('common.save') }}</button>
      </form>
      <p class="text-sm text-gray-500">{{ t('admin.requestCapture.instanceScope') }} <code v-if="captureConfig.instance_id">{{ captureConfig.instance_id }}</code><span v-else>{{ t('admin.requestCapture.storageNotConfigured') }}</span></p>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('admin.requestCapture.title') }}</h1><p class="mt-1 text-sm text-gray-500">{{ t('admin.requestCapture.description') }}</p></div>
        <button class="btn btn-secondary" :disabled="loading || !configLoaded || captureConfig.storage_ready === false" @click="refresh">{{ t('admin.requestCapture.refresh') }}</button>
      </div>
      <p class="rounded-lg bg-amber-50 p-4 text-sm text-amber-900 dark:bg-amber-950 dark:text-amber-200">{{ t('admin.requestCapture.privacy') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950 dark:text-red-200">{{ error }}</p>
      <div v-if="stats" class="card flex flex-wrap gap-5 p-4 text-sm">
        <span>{{ t('admin.requestCapture.instance') }}: <code>{{ stats.instance_id }}</code></span>
        <span>{{ t('admin.requestCapture.used') }}: {{ bytes(stats.used_bytes) }}</span>
        <span>{{ t('admin.requestCapture.active') }}: {{ stats.active_requests }}</span>
        <span>{{ t('admin.requestCapture.buffer') }}: {{ bytes(stats.buffer_bytes) }}</span>
        <span v-if="stats.admission_skipped">{{ t('admin.requestCapture.admissionSkipped') }}: {{ stats.admission_skipped }}</span>
        <span v-if="stats.expired_records">{{ t('admin.requestCapture.expiredRecords') }}: {{ stats.expired_records }}</span>
        <strong v-if="stats.storage_error" class="text-red-600">{{ t('admin.requestCapture.storageError') }}</strong>
        <div v-if="dropReasons.length" class="w-full border-t border-gray-100 pt-3 dark:border-dark-700"><p class="mb-2 text-xs text-gray-500">{{ t('admin.requestCapture.droppedHint') }}</p><div class="flex flex-wrap gap-x-5 gap-y-2"><span v-for="[reason, count] in dropReasons" :key="reason">{{ dropReason(reason) }}: {{ count }}</span></div></div>
      </div>
      <form class="card space-y-4 p-5" @submit.prevent="create">
        <h2 class="text-lg font-semibold">{{ t('admin.requestCapture.create') }}</h2>
        <div class="grid gap-4 md:grid-cols-4">
          <label class="space-y-1"><span class="input-label">{{ t('admin.requestCapture.targetType') }}</span><select v-model="targetType" class="input"><option v-for="kind in kinds" :key="kind" :value="kind">{{ t('admin.requestCapture.' + kind) }}</option></select></label>
          <label class="space-y-1 md:col-span-2"><span class="input-label">{{ t('admin.requestCapture.search') }}</span><input v-model="search" class="input" type="search" :placeholder="t('admin.requestCapture.searchHint')" /></label>
          <label class="space-y-1"><span class="input-label">{{ t('admin.requestCapture.duration') }}</span><input v-model.number="duration" class="input" type="number" min="1" max="1440" step="1" required /></label>
        </div>
        <div class="flex flex-wrap items-center gap-3">
          <select v-model.number="targetID" class="input min-w-0 flex-1" required :aria-label="t('admin.requestCapture.target')"><option :value="0" disabled>{{ t('admin.requestCapture.selectTarget') }}</option><option v-for="option in targets" :key="option.id" :value="option.id">#{{ option.id }} {{ option.label }}</option></select>
          <button type="button" class="btn btn-secondary" :disabled="targetPage <= 1 || searching" @click="loadTargets(targetPage - 1)">{{ t('admin.requestCapture.previous') }}</button>
          <button type="button" class="btn btn-secondary" :disabled="!targetMore || searching" @click="loadTargets(targetPage + 1)">{{ t('admin.requestCapture.next') }}</button>
        </div>
        <label class="flex items-center gap-2 text-sm"><input v-model="saveMedia" type="checkbox" class="checkbox" />{{ t('admin.requestCapture.saveMedia') }}</label>
        <p class="text-xs text-gray-500">{{ t('admin.requestCapture.targetHint') }}</p>
        <button class="btn btn-primary" :disabled="busy || !configLoaded || !captureConfig.enabled || !targetID || !validDuration">{{ t('admin.requestCapture.start') }}</button>
      </form>
      <div class="card overflow-hidden">
        <div class="overflow-x-auto"><table class="w-full text-left text-sm">
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800"><tr><th class="p-3">{{ t('admin.requestCapture.target') }}</th><th class="p-3">{{ t('admin.requestCapture.status') }}</th><th class="p-3">{{ t('admin.requestCapture.remaining') }}</th><th class="p-3">{{ t('admin.requestCapture.records') }}</th><th class="p-3">{{ t('admin.requestCapture.incomplete') }}</th><th class="p-3">{{ t('admin.requestCapture.size') }}</th><th class="p-3">{{ t('admin.requestCapture.actions') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700"><tr v-for="task in tasks" :key="task.id" :class="{ 'bg-blue-50 dark:bg-blue-950': selectedTask?.id === task.id }">
            <td class="p-3"><button class="text-primary-600 underline" :data-testid="'select-capture-' + task.id" @click="selectTask(task)">{{ t('admin.requestCapture.' + task.target_type) }} #{{ task.target_id }} {{ task.target_name }}</button><div v-if="isDiagnostic(task)" class="text-xs font-medium text-blue-600 dark:text-blue-300">{{ t('admin.requestCapture.diagnosticTask') }}</div><div class="text-xs text-gray-500">{{ task.save_media ? t('admin.requestCapture.mediaIncluded') : t('admin.requestCapture.metadataOnly') }}</div></td>
            <td class="p-3">{{ task.status }}<div v-if="task.reason" class="text-xs text-amber-700">{{ task.reason }}</div></td><td class="p-3">{{ remaining(task) }}</td><td class="p-3">{{ task.requests }}</td><td class="p-3">{{ task.partial }} / {{ task.skipped }}</td><td class="p-3">{{ bytes(task.bytes) }}</td>
            <td class="p-3"><div class="flex gap-2"><button v-if="task.status === 'running' && !isDiagnostic(task)" class="btn btn-secondary" :disabled="busy" @click="stop(task)">{{ t('admin.requestCapture.stop') }}</button><button v-if="task.status !== 'running'" class="btn btn-secondary" :disabled="busy || !streamExport" @click="download(task.id)">{{ t('admin.requestCapture.export') }}</button><button class="btn btn-secondary text-red-600" :disabled="busy || (isDiagnostic(task) && (!!captureConfig.id1_diagnostic_enabled || task.status === 'running'))" @click="remove(task)">{{ t('admin.requestCapture.delete') }}</button></div><p v-if="isDiagnostic(task) && task.status === 'running'" class="mt-2 max-w-xs text-xs text-gray-500">{{ t('admin.requestCapture.diagnosticStopHint') }}</p></td>
          </tr><tr v-if="!tasks.length"><td colspan="7" class="p-6 text-center text-gray-500">{{ t('admin.requestCapture.empty') }}</td></tr></tbody>
        </table></div>
        <div class="flex justify-end gap-3 p-3"><button class="btn btn-secondary" :disabled="taskPage <= 1" @click="changeTaskPage(-1)">{{ t('admin.requestCapture.previous') }}</button><span class="self-center">{{ taskPage }}</span><button class="btn btn-secondary" :disabled="!taskMore" @click="changeTaskPage(1)">{{ t('admin.requestCapture.next') }}</button></div>
      </div>
      <p v-if="!streamExport" class="text-sm text-gray-500">{{ t('admin.requestCapture.exportBrowser') }}</p>
      <section v-if="selectedTask" class="card space-y-4 p-5">
        <h2 class="font-semibold">{{ t('admin.requestCapture.records') }} - {{ selectedTask.target_name }}</h2>
        <form class="flex flex-wrap items-center gap-3" @submit.prevent="recordPage = 1; loadRecords()"><input v-model.trim="requestID" class="input max-w-md" placeholder="Request ID" /><span class="text-sm text-gray-500">{{ t(isDiagnostic(selectedTask) ? 'admin.requestCapture.allOutcomes' : 'admin.requestCapture.errorsOnly') }}</span><button class="btn btn-secondary">{{ t('admin.requestCapture.filter') }}</button></form>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr><th class="p-2">Request ID</th><th class="p-2">{{ t('admin.requestCapture.model') }}</th><th class="p-2">{{ t('admin.requestCapture.status') }}</th><th class="p-2">{{ t('admin.requestCapture.size') }}</th></tr></thead><tbody><tr v-for="record in records" :key="record.id" class="border-t border-gray-100 dark:border-dark-700"><td class="p-2"><button class="break-all text-primary-600 underline" @click="viewRecord(record)">{{ record.request_id || record.id }}</button><div class="text-xs text-gray-500">{{ record.created_at }} / {{ record.protocol }}<span v-if="record.turn"> / {{ t('admin.requestCapture.turn') }} {{ record.turn }}</span></div></td><td class="p-2">{{ record.model }}</td><td class="p-2"><span>HTTP {{ record.status }}</span><span v-if="record.is_error" class="ml-2 text-red-600" data-testid="capture-outcome">{{ captureOutcome(record) }}</span><div v-if="record.error_code" class="break-all text-xs text-red-600">{{ record.error_code }}</div><span v-if="record.partial" class="ml-2 text-amber-700">{{ record.reason || t('admin.requestCapture.partial') }}</span></td><td class="p-2">{{ bytes(record.bytes) }}</td></tr></tbody></table></div>
        <div class="flex justify-end gap-3"><button class="btn btn-secondary" :disabled="recordPage <= 1" @click="recordPage--; loadRecords()">{{ t('admin.requestCapture.previous') }}</button><span class="self-center">{{ recordPage }}</span><button class="btn btn-secondary" :disabled="!recordMore" @click="recordPage++; loadRecords()">{{ t('admin.requestCapture.next') }}</button></div>
      </section>
      <section v-if="detail" class="card space-y-4 p-5">
        <div class="flex flex-wrap justify-between gap-3"><h2 class="min-w-0 break-all font-semibold">{{ t('admin.requestCapture.detail') }} - {{ detail.request_id }}</h2><button class="btn btn-secondary" data-testid="export-capture-record" :disabled="busy || !streamExport || (selectedTask?.status === 'running' && !isDiagnostic(selectedTask))" @click="download(detail.task_id, detail.id)">{{ t('admin.requestCapture.export') }}</button></div>
        <p v-if="detail.expires_at" class="text-xs text-gray-500">{{ t('admin.requestCapture.expiresAt') }}: {{ detail.expires_at }}</p>
        <p v-if="detail.is_error" class="text-sm text-red-600">HTTP {{ detail.status }} · {{ captureOutcome(detail) }}<span v-if="detail.error_code"> · {{ detail.error_code }}</span></p>
        <p v-if="detail.partial" class="text-sm text-amber-700">{{ t('admin.requestCapture.partial') }}: {{ detail.reason }}</p>
        <details><summary class="cursor-pointer text-sm">{{ t('admin.requestCapture.metadata') }}</summary><pre class="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-sm bg-gray-50 p-3 text-xs dark:bg-dark-900">{{ metadata }}</pre></details>
        <div class="flex flex-wrap gap-2"><button v-for="item in stages" :key="item" class="btn" :class="stage === item ? 'btn-primary' : 'btn-secondary'" @click="selectStage(item)">{{ t('admin.requestCapture.' + item) }}</button></div>
        <select v-if="stageParts.length" :value="part?.name" class="input" :aria-label="t('admin.requestCapture.segment')" @change="selectPart(($event.target as HTMLSelectElement).value)"><option v-for="item in stageParts" :key="item.name" :value="item.name">{{ item.name }} / {{ t('admin.requestCapture.attempt') }} {{ item.attempt }} / {{ t('admin.requestCapture.turn') }} {{ item.turn }} / {{ bytes(item.bytes) }}</option></select>
        <p v-else class="text-sm text-gray-500">{{ t('admin.requestCapture.noPart') }}</p>
        <p v-if="part?.omitted" class="text-sm text-amber-700">{{ t('admin.requestCapture.omitted') }}: {{ part.omitted }}</p>
        <pre v-if="part" class="max-h-144 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-gray-950 p-4 text-xs leading-5 text-gray-100">{{ content }}</pre>
        <div v-if="part" class="flex gap-3"><button class="btn btn-secondary" :disabled="contentOffset === 0 || contentLoading" @click="readContent(0)">{{ t('admin.requestCapture.firstSegment') }}</button><button class="btn btn-secondary" :disabled="!contentMore || contentLoading" @click="readContent(nextOffset)">{{ t('admin.requestCapture.nextSegment') }}</button></div>
      </section>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
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
const streamExport = api.canStreamExport()
const validDuration = computed(() => Number.isInteger(duration.value) && duration.value >= 1 && duration.value <= 1440)
const stageParts = computed(() => (detail.value?.parts || []).filter(item => item.stage === stage.value).sort((a, b) => a.name.localeCompare(b.name)))
const metadata = computed(() => { if (!detail.value) return ''; const { parts: _parts, ...meta } = detail.value; return JSON.stringify(meta, null, 2) })
const dropReasons = computed(() => Object.entries(stats.value?.skipped_by_reason || {}).filter(([, count]) => count > 0).sort(([a], [b]) => a.localeCompare(b)))
const knownDropReasons = new Set(['quota_exceeded', 'record_limit', 'buffer_limit', 'queue_full', 'active_limit', 'storage_error', 'admission_busy', 'session_or_buffer_limit', 'record_capacity'])
function dropReason(reason: string) { return knownDropReasons.has(reason) ? t('admin.requestCapture.dropReasons.' + reason) : reason }
function isDiagnostic(task?: CaptureTask) { return task?.mode === 'id1_diagnostic' }
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
async function create() { if (!targetID.value || !validDuration.value) return; await action(async () => { const task = await api.createTask({ target_type: targetType.value, target_id: targetID.value, duration_minutes: duration.value, save_media: saveMedia.value }); taskPage.value = 1; selectTask(task) }) }
async function stop(task: CaptureTask) { await action(() => api.stopTask(task.id)) }
async function remove(task: CaptureTask) { if (!window.confirm(t('admin.requestCapture.deleteConfirm'))) return; await action(async () => { await api.deleteTask(task.id); if (selectedTask.value?.id === task.id) { selectedTask.value = undefined; detail.value = undefined; records.value = []; detailVersion++; contentVersion++ } }) }
async function download(task: string, record?: string) { await action(() => api.exportCapture(task, record, () => stepUp.run(api.authorizeSensitiveRead))) }
function changeTaskPage(delta: number) { taskPage.value += delta; void refresh() }
function selectTask(task: CaptureTask) { selectedTask.value = task; recordPage.value = 1; records.value = []; detail.value = undefined; detailVersion++; contentVersion++; void loadRecords() }
async function viewRecord(record: CaptureRecord) { const version = ++detailVersion; try { const result = await api.getRecord(record.task_id, record.id); if (version !== detailVersion || disposed) return; detail.value = result; selectStage(stages[0]) } catch (err) { report(err) } }
function selectStage(value: string) { stage.value = value; part.value = stageParts.value[0]; content.value = ''; contentMore.value = false; contentVersion++; if (part.value && part.value.bytes > 0) void readContent(0) }
function selectPart(name: string) { part.value = stageParts.value.find(item => item.name === name); content.value = ''; contentVersion++; if (part.value && part.value.bytes > 0) void readContent(0) }
async function readContent(offset: number) { if (!detail.value || !part.value || stepUp.visible.value) return; const version = ++contentVersion; const task = detail.value.task_id, record = detail.value.id, name = part.value.name; contentLoading.value = true; try { const result = await stepUp.run(() => api.getContent(task, record, name, offset)); if (version !== contentVersion || disposed) return; content.value = result.text; contentOffset.value = offset; nextOffset.value = result.next_offset; contentMore.value = result.has_more } catch (err) { report(err) } finally { if (version === contentVersion) contentLoading.value = false } }
watch([targetType, search], () => { targetID.value = 0; clearTimeout(debounce); debounce = setTimeout(() => { void loadTargets(1) }, 250) })
onMounted(() => { void api.getConfig().then(value=>{if(disposed)return;captureConfig.value=value;diagnosticUserIDsText.value=(value.diagnostic_user_ids || []).join(', ');configLoaded.value=true;if(value.storage_ready !== false)void refresh()}).catch(report); void loadTargets(); poll = setInterval(() => { now.value = Date.now(); if(configLoaded.value && captureConfig.value.storage_ready !== false)void refresh() }, 3000) })
onUnmounted(() => { disposed = true; stepUp.onCancel(); clearInterval(poll); clearTimeout(debounce); searchAbort?.abort(); detailVersion++; contentVersion++ })
</script>
