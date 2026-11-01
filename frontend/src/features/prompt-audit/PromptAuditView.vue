<template>
  <AppLayout>
    <div class="mx-auto flex max-w-[1600px] flex-col gap-4 pb-8 sm:gap-6">
      <UAlert
        v-if="loadErrors.config && !draft"
        role="alert"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="loadErrors.config"
        :actions="[{ label: t('admin.promptAudit.actions.retry'), color: 'neutral', variant: 'outline', onClick: loadConfig }]"
      />

      <template v-else>
        <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-default">
          <UTabs
            :model-value="activeTab"
            :items="pageTabs"
            :content="false"
            variant="link"
            :aria-label="t('admin.promptAudit.title')"
            :ui="{ list: 'border-b-0' }"
            @update:model-value="selectTab"
          />
          <div class="flex items-center gap-2 pb-1 text-xs text-muted">
            <span v-if="draft">{{ t('admin.promptAudit.configVersion', { version: draft.config_version }) }}<template v-if="draft.updated_at"> · {{ formatDate(draft.updated_at) }}</template></span>
            <UPopover :content="{ align: 'end' }">
              <UButton icon="i-lucide-info" color="neutral" variant="ghost" size="xs" :aria-label="t('admin.promptAudit.common.about')" :title="t('admin.promptAudit.common.about')" />
              <template #content>
                <p class="max-w-sm p-3 text-sm text-toned">{{ t('admin.promptAudit.description') }}</p>
              </template>
            </UPopover>
          </div>
        </div>

        <div class="min-w-0">
          <div v-show="activeTab === 'config'" class="mx-auto flex w-full max-w-4xl flex-col gap-8 lg:gap-12" data-test="tab-panel-config">
            <section v-if="draft" aria-labelledby="prompt-switches-title">
              <UPageCard variant="naked" orientation="horizontal" class="mb-4" :ui="{ title: 'text-base' }">
                <template #title><h2 id="prompt-switches-title">{{ t('admin.promptAudit.saveBar.title') }}</h2></template>
              </UPageCard>
              <UPageCard variant="subtle" :ui="{ container: 'divide-y divide-default' }">
                <UFormField
                  v-for="row in switchRows"
                  :key="row.key"
                  :label="row.label"
                  class="flex items-center justify-between gap-2 not-last:pb-4"
                >
                  <USwitch :model-value="row.value" :disabled="row.disabled" :aria-label="row.label" :data-test="row.test" @update:model-value="toggleSwitch(row.key, $event)" />
                </UFormField>
              </UPageCard>
            </section>

            <RuntimeOverview :runtime="runtime" :loading="loading.runtime" :error="loadErrors.runtime" @refresh="loadRuntime" />

            <template v-if="draft">
              <EndpointPool
                :endpoints="draft.endpoints"
                :probe-results="probeResults"
                :probing-ids="probingIds"
                @update:endpoints="updateEndpoints"
                @probe="runProbe"
              />
              <UAlert v-if="loadErrors.groups" role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="loadErrors.groups" />
              <PolicyPanel :draft="draft" :groups="groups" @update:draft="replaceDraft" />
            </template>
          </div>

          <div v-show="activeTab === 'events'" class="flex flex-col gap-4" data-test="tab-panel-events">
            <UAlert
              v-if="draft?.enabled && !draft.store_pass_events"
              data-test="pass-events-disabled-notice"
              role="status"
              color="warning"
              variant="subtle"
              icon="i-lucide-info"
              orientation="horizontal"
              :description="t('admin.promptAudit.events.passEventsDisabled')"
              :actions="[{ label: t('admin.promptAudit.events.openConfiguration'), color: 'neutral', variant: 'outline', size: 'xs', onClick: openConfigTab }]"
            />
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
          </div>
        </div>

        <!-- Pinned to the bottom of the scroll area so the long configuration can be saved from anywhere. -->
        <div v-if="draft && activeTab === 'config'" class="sticky bottom-0 z-30 border-t border-default bg-default py-3">
          <div class="mx-auto flex w-full max-w-4xl flex-wrap items-center justify-end gap-3">
            <span class="me-auto flex items-center gap-2 text-sm" :class="dirty ? 'text-warning' : 'text-muted'">
              <UIcon :name="dirty ? 'i-lucide-circle-alert' : 'i-lucide-check'" class="size-4 shrink-0" />
              {{ dirty ? t('admin.promptAudit.saveBar.dirty') : t('admin.promptAudit.saveBar.synced') }}
            </span>
            <UButton color="neutral" variant="subtle" :label="t('common.reset')" :disabled="!dirty || loading.saving" @click="resetDraft" />
            <UButton :label="loading.saving ? t('common.saving') : t('common.save')" :loading="loading.saving" :disabled="!dirty || loading.saving" data-test="save-config" @click="saveConfig" />
          </div>
        </div>
      </template>
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
    <FilterDeleteDialog
      :show="showFilterDelete"
      :initial-filters="filters"
      :preview="deletePreview"
      :previewing="loading.previewing"
      :deleting="loading.deleting"
      @close="closeFilterDelete"
      @preview="runFilterDeletePreview"
      @confirm="confirmFilterDelete"
      @criteria-change="clearDeletePreview"
    />
    <EventDetailDialog :show="showEventDetail" :event="activeEvent" :loading="loading.detail" @close="closeEventDetail" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import RuntimeOverview from './components/RuntimeOverview.vue'
import EndpointPool from './components/EndpointPool.vue'
import PolicyPanel from './components/PolicyPanel.vue'
import EventWorkspace from './components/EventWorkspace.vue'
import EventDetailDialog from './components/EventDetailDialog.vue'
import FilterDeleteDialog from './components/FilterDeleteDialog.vue'
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
type PromptAuditPageTab = 'config' | 'events'
const activeTab = ref<PromptAuditPageTab>('events')
const pageTabs = computed(() => [
  { value: 'events', label: t('admin.promptAudit.tabs.events'), icon: 'i-lucide-list-filter' },
  { value: 'config', label: t('admin.promptAudit.tabs.config'), icon: 'i-lucide-settings-2' },
])
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
const showFilterDelete = ref(false)
const deletePreview = ref<PromptDeletePreview | null>(null)
const deletePreviewFilters = ref<PromptEventFilters | null>(null)
const showBlockingConfirmation = ref(false)
const deleteRequest = reactive<{ mode: '' | 'single' | 'batch'; ids: number[] }>({ mode: '', ids: [] })
const loading = reactive({ config: false, runtime: false, groups: false, events: false, saving: false, detail: false, deleting: false, previewing: false })
const loadErrors = reactive<PromptLoadErrors>({ config: '', runtime: '', groups: '', events: '' })
const dirty = computed(() => draftFingerprint(draft.value) !== draftFingerprint(serverConfig.value))

type SwitchKey = 'enabled' | 'blocking' | 'latestTurnOnly' | 'storePass'
const switchRows = computed(() => {
  const value = draft.value
  if (!value) return []
  return [
    { key: 'enabled' as const, label: t('admin.promptAudit.saveBar.enabled'), value: value.enabled, disabled: false, test: 'enabled-toggle' },
    { key: 'blocking' as const, label: t('admin.promptAudit.saveBar.blocking'), value: value.blocking_enabled, disabled: !value.enabled, test: 'blocking-toggle' },
    { key: 'latestTurnOnly' as const, label: t('admin.promptAudit.saveBar.blockingLatestTurnOnly'), value: value.blocking_latest_turn_only, disabled: !value.enabled || !value.blocking_enabled, test: 'blocking-latest-turn-only-toggle' },
    { key: 'storePass' as const, label: t('admin.promptAudit.saveBar.storePass'), value: value.store_pass_events, disabled: false, test: 'store-pass-toggle' },
  ]
})
function toggleSwitch(key: SwitchKey, value: boolean) {
  if (!draft.value) return
  if (key === 'enabled') setEnabled(value)
  else if (key === 'blocking') setBlocking(value)
  else if (key === 'latestTurnOnly') replaceDraft({ ...draft.value, blocking_latest_turn_only: value })
  else replaceDraft({ ...draft.value, store_pass_events: value })
}
function selectTab(value: string | number) {
  activeTab.value = value === 'config' ? 'config' : 'events'
}
function openConfigTab() { activeTab.value = 'config' }

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
  clearDeletePreview()
}
function applyEventFilters(value: PromptEventFilters) {
  filters.value = cloneData(value)
  appliedFilters.value = cloneData(value)
  events.page = 1
  clearDeletePreview()
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
function clearDeletePreview() {
  deletePreview.value = null
  deletePreviewFilters.value = null
}
function requestFilterDeletePreview() {
  clearDeletePreview()
  showFilterDelete.value = true
}
function closeFilterDelete() {
  showFilterDelete.value = false
  clearDeletePreview()
}
async function runFilterDeletePreview(value: PromptEventFilters) {
  loading.previewing = true
  try {
    deletePreview.value = await promptAuditAPI.previewDelete(value)
    deletePreviewFilters.value = cloneData(value)
  } catch (error) {
    clearDeletePreview()
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.previewDelete'))
  } finally { loading.previewing = false }
}
async function confirmFilterDelete(filters?: PromptEventFilters) {
  if (loading.deleting) return
  loading.deleting = true
  try {
    let preview = deletePreview.value
    let previewFilters = deletePreviewFilters.value ? cloneData(deletePreviewFilters.value) : null
    // One-click path: no fresh preview (never requested, or cleared by a
    // criteria change) — mint the confirmation token on the fly from the
    // criteria the dialog just emitted, then delete in the same action.
    if ((!preview || !previewFilters) && filters) {
      preview = await promptAuditAPI.previewDelete(filters)
      previewFilters = cloneData(filters)
    }
    if (!preview || !previewFilters) return
    const result = await promptAuditAPI.deleteEventsByFilter(previewFilters, preview)
    closeFilterDelete()
    appStore.showSuccess(t('admin.promptAudit.messages.deleted', { count: result.deleted_events }))
    await Promise.allSettled([loadEvents(), loadRuntime()])
  } catch (error) {
    clearDeletePreview()
    appStore.showError(errorMessage(error, 'admin.promptAudit.errors.deleteConfirmation'))
  } finally { loading.deleting = false }
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}

onMounted(loadInitial)
</script>
