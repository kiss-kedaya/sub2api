<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] pb-28">
      <header class="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
          <p class="text-xs font-semibold uppercase tracking-[0.16em] text-primary-600 dark:text-primary-400">{{ t('nav.securityAudit') }}</p>
          <h1 class="mt-1 text-2xl font-semibold tracking-tight text-gray-950 dark:text-white">{{ t('admin.promptAudit.title') }}</h1>
          <p class="mt-2 max-w-3xl text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.description') }}</p>
        </div>
        <div v-if="draft" class="text-right text-xs text-gray-500 dark:text-dark-400">
          <p>{{ t('admin.promptAudit.configVersion', { version: draft.config_version }) }}</p>
          <p v-if="draft.updated_at" class="mt-1">{{ formatDate(draft.updated_at) }}</p>
        </div>
      </header>

      <div v-if="loadErrors.config && !draft" role="alert" class="rounded-xl border border-red-200 bg-red-50 p-5 dark:border-red-900 dark:bg-red-950/30">
        <p class="text-sm text-red-700 dark:text-red-300">{{ loadErrors.config }}</p>
        <button type="button" class="btn btn-secondary btn-sm mt-3" @click="loadConfig">{{ t('admin.promptAudit.actions.retry') }}</button>
      </div>

      <main v-else class="rounded-2xl border border-gray-200 bg-white px-4 shadow-sm dark:border-dark-700 dark:bg-dark-850 sm:px-6 lg:px-8">
        <RuntimeOverview :runtime="runtime" :loading="loading.runtime" :error="loadErrors.runtime" @refresh="loadRuntime" />

        <template v-if="draft">
          <EndpointPool
            :endpoints="draft.endpoints"
            :probe-results="probeResults"
            :probing-ids="probingIds"
            @update:endpoints="updateEndpoints"
            @probe="runProbe"
          />
          <div v-if="loadErrors.groups" role="alert" class="mt-5 rounded-lg bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ loadErrors.groups }}</div>
          <PolicyPanel :draft="draft" :groups="groups" @update:draft="replaceDraft" />
        </template>

        <EventWorkspace
          :events="events.items"
          :total="events.total"
          :page="events.page"
          :page-size="events.page_size"
          :filters="filters"
          :selected-ids="selectedEventIds"
          :loading="loading.events"
          :error="loadErrors.events"
          @filters-change="handleFiltersChanged"
          @search="applyEventFilters"
          @selection="selectedEventIds = $event"
          @page="changePage"
          @page-size="changePageSize"
          @view="openEvent"
          @delete="requestSingleDelete"
          @batch-delete="requestBatchDelete"
          @preview-delete="requestFilterDeletePreview"
        />
      </main>
    </div>

    <div v-if="draft" class="fixed inset-x-0 bottom-0 z-30 border-t border-gray-200 bg-white/95 px-4 py-3 shadow-[0_-12px_35px_rgba(15,23,42,0.08)] backdrop-blur dark:border-dark-700 dark:bg-dark-900/95 lg:left-64">
      <div class="mx-auto flex max-w-[1600px] flex-wrap items-center justify-between gap-3">
        <div class="flex flex-wrap items-center gap-x-5 gap-y-2">
          <SaveToggle :label="t('admin.promptAudit.saveBar.enabled')" :model-value="draft.enabled" data-test="enabled-toggle" @update:model-value="setEnabled" />
          <SaveToggle :label="t('admin.promptAudit.saveBar.blocking')" :model-value="draft.blocking_enabled" :disabled="!draft.enabled" data-test="blocking-toggle" @update:model-value="setBlocking" />
          <SaveToggle :label="t('admin.promptAudit.saveBar.storePass')" :model-value="draft.store_pass_events" data-test="store-pass-toggle" @update:model-value="replaceDraft({ ...draft!, store_pass_events: $event })" />
        </div>
        <div class="flex items-center gap-3">
          <span class="text-sm" :class="dirty ? 'text-amber-700 dark:text-amber-300' : 'text-gray-500 dark:text-dark-400'">
            {{ dirty ? t('admin.promptAudit.saveBar.dirty') : t('admin.promptAudit.saveBar.synced') }}
          </span>
          <button type="button" class="btn btn-secondary" :disabled="!dirty || loading.saving" @click="resetDraft">{{ t('common.reset') }}</button>
          <button type="button" class="btn btn-primary" :disabled="!dirty || loading.saving" data-test="save-config" @click="saveConfig">
            {{ loading.saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </div>
    </div>

    <ConfirmDialog
      :show="showBlockingConfirmation"
      :title="t('admin.promptAudit.blockingConfirm.title')"
      :message="t('admin.promptAudit.blockingConfirm.message')"
      :confirm-text="t('admin.promptAudit.blockingConfirm.confirm')"
      danger
      @confirm="confirmBlocking"
      @cancel="showBlockingConfirmation = false"
    />
    <ConfirmDialog
      :show="deleteRequest.mode !== ''"
      :title="t('admin.promptAudit.events.deleteConfirmTitle')"
      :message="t('admin.promptAudit.events.deleteConfirmMessage', { count: deleteRequest.ids.length })"
      :confirm-text="t('common.delete')"
      danger
      @confirm="confirmIDDelete"
      @cancel="clearDeleteRequest"
    />
    <BaseDialog :show="Boolean(deletePreview)" :title="t('admin.promptAudit.events.filterDeleteTitle')" width="normal" @close="deletePreview = null">
      <div v-if="deletePreview" class="space-y-4 text-sm text-gray-600 dark:text-dark-300">
        <p>{{ t('admin.promptAudit.events.filterDeleteCount', { count: deletePreview.matched_count }) }}</p>
        <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2">
          <dt>{{ t('admin.promptAudit.events.snapshotMax') }}</dt><dd>{{ deletePreview.snapshot_max_id }}</dd>
          <dt>Filter SHA-256</dt><dd class="break-all font-mono text-xs">{{ deletePreview.filter_hash }}</dd>
          <dt>{{ t('admin.promptAudit.events.expiresAt') }}</dt><dd>{{ formatDate(deletePreview.expires_at) }}</dd>
        </dl>
        <p class="rounded-lg bg-amber-50 px-3 py-2 text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{{ t('admin.promptAudit.events.filterDeleteWarning') }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="deletePreview = null">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-danger" :disabled="loading.deleting" data-test="confirm-filter-delete" @click="confirmFilterDelete">{{ t('admin.promptAudit.events.confirmFilterDelete') }}</button>
        </div>
      </template>
    </BaseDialog>
    <EventDetailDialog :show="showEventDetail" :event="activeEvent" :loading="loading.detail" @close="closeEventDetail" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import RuntimeOverview from './components/RuntimeOverview.vue'
import EndpointPool from './components/EndpointPool.vue'
import PolicyPanel from './components/PolicyPanel.vue'
import EventWorkspace from './components/EventWorkspace.vue'
import EventDetailDialog from './components/EventDetailDialog.vue'
import promptAuditAPI from './api'
import type {
  PromptAuditDraft,
  PromptAuditEndpointDraft,
  PromptAuditEvent,
  PromptAuditGroup,
  PromptAuditRuntime,
  PromptDeletePreview,
  PromptEventFilters,
  PromptEventPage,
  PromptLoadErrors,
  PromptProbeResult,
} from './types'
import { buildUpdateRequest, cloneData, configToDraft, draftFingerprint, emptyEventFilters } from './viewModel'

const { t, locale } = useI18n()
const appStore = useAppStore()
const serverConfig = ref<PromptAuditDraft | null>(null)
const draft = ref<PromptAuditDraft | null>(null)
const runtime = ref<PromptAuditRuntime | null>(null)
const groups = ref<PromptAuditGroup[]>([])
const events = reactive<PromptEventPage>({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
const filters = ref<PromptEventFilters>(emptyEventFilters())
const appliedFilters = ref<PromptEventFilters>(emptyEventFilters())
const selectedEventIds = ref<number[]>([])
const activeEvent = ref<PromptAuditEvent | null>(null)
const showEventDetail = ref(false)
const probeResults = reactive<Record<string, PromptProbeResult>>({})
const probingIds = ref<string[]>([])
const deletePreview = ref<PromptDeletePreview | null>(null)
const showBlockingConfirmation = ref(false)
const deleteRequest = reactive<{ mode: '' | 'single' | 'batch'; ids: number[] }>({ mode: '', ids: [] })
const loading = reactive({ config: false, runtime: false, groups: false, events: false, saving: false, detail: false, deleting: false })
const loadErrors = reactive<PromptLoadErrors>({ config: '', runtime: '', groups: '', events: '' })
const dirty = computed(() => draftFingerprint(draft.value) !== draftFingerprint(serverConfig.value))

const SaveToggle = defineComponent({
  inheritAttrs: false,
  props: { label: { type: String, required: true }, modelValue: { type: Boolean, required: true }, disabled: { type: Boolean, default: false } },
  emits: ['update:modelValue'],
  setup(props, { emit, attrs }) {
    return () => h('label', { class: ['flex items-center gap-2 text-sm', props.disabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer'] }, [
      h('button', {
        ...attrs, type: 'button', role: 'switch', 'aria-checked': props.modelValue, 'aria-label': props.label, disabled: props.disabled,
        class: ['relative h-6 w-11 rounded-full transition-colors', props.modelValue ? 'bg-primary-600' : 'bg-gray-300 dark:bg-dark-600'],
        onClick: () => !props.disabled && emit('update:modelValue', !props.modelValue),
      }, [h('span', { class: ['absolute top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform', props.modelValue ? 'translate-x-5' : 'translate-x-0.5'] })]),
      h('span', { class: 'text-gray-700 dark:text-dark-200' }, props.label),
    ])
  },
})

function errorMessage(error: unknown, fallbackKey: string): string {
  const code = extractApiErrorCode(error)
  if (code) {
    const key = `admin.promptAudit.errors.${code}`
    const translated = t(key)
    if (translated !== key) return translated
  }
  return extractApiErrorMessage(error, t(fallbackKey))
}

async function loadConfig() {
  loading.config = true
  loadErrors.config = ''
  try {
    const config = await promptAuditAPI.getConfig()
    serverConfig.value = configToDraft(config)
    draft.value = configToDraft(config)
  } catch (error) {
    loadErrors.config = errorMessage(error, 'admin.promptAudit.errors.loadConfig')
  } finally {
    loading.config = false
  }
}
async function loadRuntime() {
  loading.runtime = true
  loadErrors.runtime = ''
  try { runtime.value = await promptAuditAPI.getRuntime() }
  catch (error) { loadErrors.runtime = errorMessage(error, 'admin.promptAudit.errors.loadRuntime') }
  finally { loading.runtime = false }
}
async function loadGroups() {
  loading.groups = true
  loadErrors.groups = ''
  try { groups.value = await promptAuditAPI.listGroups() }
  catch (error) { loadErrors.groups = errorMessage(error, 'admin.promptAudit.errors.loadGroups') }
  finally { loading.groups = false }
}
async function loadEvents() {
  loading.events = true
  loadErrors.events = ''
  try {
    const result = await promptAuditAPI.listEvents(appliedFilters.value, events.page, events.page_size)
    Object.assign(events, result)
    selectedEventIds.value = []
  } catch (error) {
    loadErrors.events = errorMessage(error, 'admin.promptAudit.errors.loadEvents')
  } finally {
    loading.events = false
  }
}
async function loadInitial() {
  await Promise.allSettled([loadConfig(), loadRuntime(), loadGroups(), loadEvents()])
}

function replaceDraft(value: PromptAuditDraft) { draft.value = cloneData(value) }
function updateEndpoints(value: PromptAuditEndpointDraft[]) {
  if (!draft.value) return
  replaceDraft({ ...draft.value, endpoints: value })
}
function setEnabled(value: boolean) {
  if (!draft.value) return
  replaceDraft({ ...draft.value, enabled: value, blocking_enabled: value ? draft.value.blocking_enabled : false })
}
function setBlocking(value: boolean) {
  if (!draft.value || !draft.value.enabled) return
  if (value && !draft.value.blocking_enabled) { showBlockingConfirmation.value = true; return }
  replaceDraft({ ...draft.value, blocking_enabled: value })
}
function confirmBlocking() {
  showBlockingConfirmation.value = false
  if (draft.value) replaceDraft({ ...draft.value, blocking_enabled: true })
}
function resetDraft() {
  if (serverConfig.value) draft.value = cloneData(serverConfig.value)
}
async function saveConfig() {
  if (!draft.value || !dirty.value) return
  loading.saving = true
  try {
    const saved = await promptAuditAPI.updateConfig(buildUpdateRequest(draft.value))
    serverConfig.value = configToDraft(saved)
    draft.value = configToDraft(saved)
    appStore.showSuccess(t('admin.promptAudit.messages.saved'))
    await loadRuntime()
  } catch (error) {
    const code = extractApiErrorCode(error)
    appStore.showError(errorMessage(error, code === 'prompt_audit_config_conflict' ? 'admin.promptAudit.errors.prompt_audit_config_conflict' : 'admin.promptAudit.errors.saveConfig'))
  } finally {
    loading.saving = false
  }
}
async function runProbe(endpoint: PromptAuditEndpointDraft) {
  if (probingIds.value.includes(endpoint.id)) return
  probingIds.value = [...probingIds.value, endpoint.id]
  try {
    const result = await promptAuditAPI.probeEndpoint(endpoint)
    probeResults[endpoint.id] = result
    if (result.ok) appStore.showSuccess(t('admin.promptAudit.messages.probeSucceeded'))
    else appStore.showError(`${result.error_code || result.status}: ${result.message}`)
  } catch (error) {
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.probe'))
  } finally {
    probingIds.value = probingIds.value.filter((id) => id !== endpoint.id)
  }
}

function handleFiltersChanged(value: PromptEventFilters) {
  filters.value = cloneData(value)
  deletePreview.value = null
}
function applyEventFilters(value: PromptEventFilters) {
  filters.value = cloneData(value)
  appliedFilters.value = cloneData(value)
  events.page = 1
  deletePreview.value = null
  void loadEvents()
}
function changePage(value: number) { events.page = value; void loadEvents() }
function changePageSize(value: number) { events.page_size = value; events.page = 1; void loadEvents() }
async function openEvent(id: number) {
  showEventDetail.value = true
  loading.detail = true
  activeEvent.value = null
  try { activeEvent.value = await promptAuditAPI.getEvent(id) }
  catch (error) { appStore.showError(errorMessage(error, 'admin.promptAudit.errors.loadDetail')); showEventDetail.value = false }
  finally { loading.detail = false }
}
function closeEventDetail() { showEventDetail.value = false; activeEvent.value = null }
function requestSingleDelete(id: number) { deleteRequest.mode = 'single'; deleteRequest.ids = [id] }
function requestBatchDelete() { if (selectedEventIds.value.length) { deleteRequest.mode = 'batch'; deleteRequest.ids = [...selectedEventIds.value] } }
function clearDeleteRequest() { deleteRequest.mode = ''; deleteRequest.ids = [] }
async function confirmIDDelete() {
  const mode = deleteRequest.mode
  const ids = [...deleteRequest.ids]
  clearDeleteRequest()
  if (!mode || ids.length === 0) return
  loading.deleting = true
  try {
    const result = mode === 'single' ? await promptAuditAPI.deleteEvent(ids[0]) : await promptAuditAPI.batchDeleteEvents(ids)
    appStore.showSuccess(t('admin.promptAudit.messages.deleted', { count: result.deleted_events }))
    await Promise.allSettled([loadEvents(), loadRuntime()])
  } catch (error) { appStore.showError(errorMessage(error, 'admin.promptAudit.errors.delete')) }
  finally { loading.deleting = false }
}
async function requestFilterDeletePreview() {
  loading.deleting = true
  try { deletePreview.value = await promptAuditAPI.previewDelete(filters.value) }
  catch (error) { appStore.showError(errorMessage(error, 'admin.promptAudit.errors.previewDelete')) }
  finally { loading.deleting = false }
}
async function confirmFilterDelete() {
  if (!deletePreview.value) return
  const preview = deletePreview.value
  loading.deleting = true
  try {
    const result = await promptAuditAPI.deleteEventsByFilter(filters.value, preview)
    deletePreview.value = null
    appStore.showSuccess(t('admin.promptAudit.messages.deleted', { count: result.deleted_events }))
    await Promise.allSettled([loadEvents(), loadRuntime()])
  } catch (error) {
    deletePreview.value = null
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.deleteConfirmation'))
  } finally { loading.deleting = false }
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

onMounted(loadInitial)
</script>
